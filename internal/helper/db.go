package helper

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"aracne/internal/topology/domain"
	_ "modernc.org/sqlite"
)

// Writes topology data (resources, connections, warnings, metadata) to SQLite database.
func WriteDb(topo *domain.Topology, path string) error {
	return withSQLiteWrite(path, func(db *sql.DB) error {
		if err := createSchema(db); err != nil {
			return err
		}

		tx, err := db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()

		if _, err := tx.Exec("DELETE FROM info"); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM resources"); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM connections"); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM warnings"); err != nil {
			return err
		}

		if _, err := tx.Exec("INSERT INTO info VALUES ('root', ?)", topo.Root); err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO info VALUES ('language', ?)", topo.Language); err != nil {
			return err
		}
		languagesJSON, err := json.Marshal(topo.Languages)
		if err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO info VALUES ('languages', ?)", string(languagesJSON)); err != nil {
			return err
		}
		// Stamp the id-scheme. This is the ONLY write that may do so: WriteDb regenerates
		// every ID from the source, so the database provably holds the current grammar.
		// An incremental write must never stamp — it leaves untouched files' IDs alone, so
		// claiming the current scheme there would mark a half-legacy database as migrated
		// and suppress the remap that fixes it. Note the DELETE FROM info above drops any
		// previous stamp, so this insert is what keeps a rescanned database self-describing.
		if _, err := tx.Exec("INSERT INTO info VALUES (?, ?)",
			infoIDSchemeKey, strconv.Itoa(IDSchemeVersion)); err != nil {
			return err
		}

		resStmt, err := tx.Prepare("INSERT INTO resources (id, kind, name, language, description, properties_json, starts_at, ends_at, loc_path) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)")
		if err != nil {
			return err
		}
		defer resStmt.Close()

		// INSERT OR REPLACE (not a bare INSERT) so a duplicate edge — same
		// (source_id, conn_type, target_id) — collapses to a single row instead
		// of aborting the ENTIRE topology write on the connections UNIQUE
		// constraint. Same-id definitions (e.g. @overload signatures, a
		// property's getter/setter/deleter, or a name bound in sibling
		// control-flow branches) can legitimately yield duplicate containment
		// edges; the incremental writers (WriteIncremental / WriteScopedResources)
		// already use OR REPLACE for exactly this reason — this keeps the
		// full-scan path consistent with them.
		connStmt, err := tx.Prepare("INSERT OR REPLACE INTO connections VALUES (?, ?, ?)")
		if err != nil {
			return err
		}
		defer connStmt.Close()

		warnStmt, err := tx.Prepare("INSERT INTO warnings VALUES (?, ?, ?, ?, ?)")
		if err != nil {
			return err
		}
		defer warnStmt.Close()

		for id, res := range topo.Resources {
			propsJSON := toJSON(res.Properties)
			startsAt := 0
			endsAt := 0
			locPath := ""
			if res.Location.Path != "" {
				startsAt = res.Location.StartsAt
				endsAt = res.Location.EndsAt
				locPath = res.Location.Path
			}
			if _, err := resStmt.Exec(id, string(res.Kind), res.Name, res.Language, domain.DescriptionForStorage(res.Kind, res.Description), propsJSON, startsAt, endsAt, locPath); err != nil {
				return err
			}
			for kind, targets := range res.Connections {
				for _, target := range targets {
					if _, err := connStmt.Exec(id, kind, target); err != nil {
						return err
					}
				}
			}
		}

		for id, w := range topo.Warnings {
			if _, err := warnStmt.Exec(id, w.SourceID, string(w.Kind), w.TargetID, w.Message); err != nil {
				return err
			}
		}

		// Bounded, and deterministic about WHICH ones survive.
		//
		// `info` is a key/value table that also serves as the scan error log, and it was
		// unbounded: a repo whose JS and TS scanners produce colliding ids wrote one row
		// per collision. aracne's own self-scan accumulated 23,902 of them, which is why
		// that database was 7.5 MB of mostly duplicate error text. Nothing reads more than
		// a handful, so keep a sample and record the true count.
		pats := make([]string, 0, len(topo.Errors))
		for pat := range topo.Errors {
			pats = append(pats, pat)
		}
		sort.Strings(pats)
		for i, pat := range pats {
			if i >= maxStoredErrors {
				break
			}
			if _, err := tx.Exec("INSERT INTO info VALUES (?, ?)", "error:"+pat, topo.Errors[pat]); err != nil {
				return err
			}
		}
		if len(pats) > maxStoredErrors {
			if _, err := tx.Exec("INSERT INTO info VALUES (?, ?)", "error_count",
				strconv.Itoa(len(pats))); err != nil {
				return err
			}
		}

		return tx.Commit()
	})
}

// Initializes the topology database schema with tables for resources, connections, warnings, and bugs.
func createSchema(db *sql.DB) error {
	ddl := `
	CREATE TABLE IF NOT EXISTS info (key TEXT PRIMARY KEY, value TEXT);

	CREATE TABLE IF NOT EXISTS resources (
		id TEXT PRIMARY KEY,
		kind TEXT NOT NULL,
		name TEXT NOT NULL,
		language TEXT DEFAULT '',
		description TEXT,
		properties_json TEXT,
		starts_at INT NOT NULL DEFAULT 0,
		ends_at INT NOT NULL DEFAULT 0,
		loc_path TEXT DEFAULT ''
	);
	CREATE INDEX IF NOT EXISTS idx_resources_loc_path ON resources(loc_path);
	CREATE INDEX IF NOT EXISTS idx_resources_kind ON resources(kind);

	CREATE TABLE IF NOT EXISTS connections (
		source_id TEXT NOT NULL,
		conn_type TEXT NOT NULL,
		target_id TEXT NOT NULL,
		PRIMARY KEY(source_id, conn_type, target_id)
	);
	CREATE INDEX IF NOT EXISTS idx_conn_target ON connections(target_id);

	CREATE TABLE IF NOT EXISTS warnings (
		id TEXT PRIMARY KEY,
		source_id TEXT NOT NULL,
		kind TEXT NOT NULL,
		target_id TEXT DEFAULT '',
		message TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_warnings_source ON warnings(source_id);
	CREATE INDEX IF NOT EXISTS idx_warnings_target ON warnings(target_id);

	CREATE TABLE IF NOT EXISTS bugs (
		id TEXT PRIMARY KEY,
		node_id TEXT NOT NULL,
		description TEXT NOT NULL,
		state TEXT NOT NULL DEFAULT 'pending'
	);
	CREATE INDEX IF NOT EXISTS idx_bugs_node ON bugs(node_id);
	CREATE INDEX IF NOT EXISTS idx_bugs_state ON bugs(state);

	-- Maps a resource ID from a PREVIOUS id-scheme to its current one, so an agent (or a
	-- saved note, or a stale transcript) that still uses an old ID keeps resolving after a
	-- scheme change. Populated by the id-scheme remap; never by a normal scan.
	CREATE TABLE IF NOT EXISTS resource_alias (
		old_id TEXT PRIMARY KEY,
		new_id TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_alias_new ON resource_alias(new_id);
	`
	_, err := db.Exec(ddl)
	if err != nil {
		return err
	}
	if err := ensureResourceLanguageColumn(db); err != nil {
		return err
	}
	return nil
}

// Applies versioned, idempotent on-disk migrations to an existing topology
// database, gated by PRAGMA user_version so each runs at most once per database.
//
// v1: the "type" resource kind (structs/classes) was renamed to "struct"; rewrite
// any rows persisted under the old value.
//
// v2: introduces the resource_alias table and stamps the database with the id-scheme it
// was built under. This step deliberately does NOT rewrite any ID: recomputing IDs needs
// the scanners and the source tree, neither of which is available here. It only makes the
// database say which scheme it holds, so the next `arac scan` can notice it is behind and
// run the remap (see IDSchemeVersion / ReadIDScheme).
func applyMigrations(db *sql.DB) error {
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version < 1 {
		if _, err := db.Exec("UPDATE resources SET kind = 'struct' WHERE kind = 'type'"); err != nil {
			return err
		}
		if _, err := db.Exec("PRAGMA user_version = 1"); err != nil {
			return err
		}
	}
	if version < 2 {
		if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS resource_alias (
			old_id TEXT PRIMARY KEY,
			new_id TEXT NOT NULL
		)`); err != nil {
			return err
		}
		if _, err := db.Exec(
			"CREATE INDEX IF NOT EXISTS idx_alias_new ON resource_alias(new_id)"); err != nil {
			return err
		}
		// An existing database predates the scheme stamp, so by definition it holds
		// scheme 1. A brand-new database is stamped by the scanner that fills it.
		var n int
		if err := db.QueryRow("SELECT COUNT(*) FROM resources").Scan(&n); err == nil && n > 0 {
			if _, err := db.Exec(
				"INSERT OR IGNORE INTO info (key, value) VALUES (?, ?)",
				infoIDSchemeKey, "1"); err != nil {
				return err
			}
		}
		if _, err := db.Exec(
			fmt.Sprintf("PRAGMA user_version = %d", latestSchemaVersion)); err != nil {
			return err
		}
	}
	return nil
}

// maxStoredErrors bounds the scan-error sample kept in `info`. See WriteDb.
const maxStoredErrors = 100

// latestSchemaVersion is the user_version applyMigrations brings a database up to. Bump it
// in the same commit as a new migration step.
const latestSchemaVersion = 2

// IDSchemeVersion is the resource-ID grammar this binary produces. Bump it in the same
// commit as any change to how a scanner builds IDs, so existing databases are detected as
// stale and remapped instead of silently half-matching.
//
//	1 — Python/JS/TS module paths rooted at filepath.Base(projectRoot) with "/" separators;
//	    Rust rooted at the directory basename when no Cargo.toml [package] exists.
//	2 — Python/JS/TS module paths are repo-relative (the project-directory base name is
//	    gone); Rust uses the literal "crate" when there is no Cargo [package]. Go and Java
//	    are unchanged — they were already rooted in something the source states.
const IDSchemeVersion = 2

// infoIDSchemeKey is the `info` row holding the scheme a database was built under.
const infoIDSchemeKey = "id_scheme"

// ReadIDScheme reports the id-scheme a database was built under, and whether it was
// stamped at all. An unstamped database with resources predates the stamp and is scheme 1;
// an empty database reports the current scheme because the next scan will fill it.
func ReadIDScheme(dbPath string) (scheme int, stamped bool, err error) {
	err = withSQLiteRead(dbPath, func(db *sql.DB) error {
		var val string
		row := db.QueryRow("SELECT value FROM info WHERE key = ?", infoIDSchemeKey)
		switch scanErr := row.Scan(&val); {
		case scanErr == sql.ErrNoRows:
			var n int
			if e := db.QueryRow("SELECT COUNT(*) FROM resources").Scan(&n); e != nil {
				return e
			}
			scheme, stamped = IDSchemeVersion, false
			if n > 0 {
				scheme = 1
			}
			return nil
		case scanErr != nil:
			return scanErr
		}
		stamped = true
		v, convErr := strconv.Atoi(strings.TrimSpace(val))
		if convErr != nil {
			return fmt.Errorf("unreadable %s value %q: %w", infoIDSchemeKey, val, convErr)
		}
		scheme = v
		return nil
	})
	return scheme, stamped, err
}

// WriteIDScheme stamps the database with an id-scheme version.
func WriteIDScheme(dbPath string, scheme int) error {
	return withSQLiteWrite(dbPath, func(db *sql.DB) error {
		_, err := db.Exec(
			"INSERT INTO info (key, value) VALUES (?, ?) "+
				"ON CONFLICT(key) DO UPDATE SET value = excluded.value",
			infoIDSchemeKey, strconv.Itoa(scheme))
		return err
	})
}

// WriteResourceAliases records old-ID -> new-ID mappings from an id-scheme remap.
// Existing rows for the same old ID are replaced, and aliases that would point at
// themselves are skipped.
func WriteResourceAliases(dbPath string, aliases map[string]string) (int64, error) {
	if len(aliases) == 0 {
		return 0, nil
	}
	var count int64
	err := withSQLiteWrite(dbPath, func(db *sql.DB) error {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		stmt, err := tx.Prepare(
			"INSERT INTO resource_alias (old_id, new_id) VALUES (?, ?) " +
				"ON CONFLICT(old_id) DO UPDATE SET new_id = excluded.new_id")
		if err != nil {
			return err
		}
		defer stmt.Close()
		olds := make([]string, 0, len(aliases))
		for old := range aliases {
			olds = append(olds, old)
		}
		sort.Strings(olds)
		for _, old := range olds {
			if old == "" || aliases[old] == "" || old == aliases[old] {
				continue
			}
			if _, err := stmt.Exec(old, aliases[old]); err != nil {
				return err
			}
			count++
		}
		return tx.Commit()
	})
	return count, err
}

// RemapBugNodes rewrites bugs.node_id through an old -> new ID mapping.
//
// Bugs reference resources by ID with no foreign key, and CleanupOrphanedBugs deletes any
// bug whose node is absent from the topology. Across an id-scheme change EVERY node id is
// absent under its old spelling, so without this every open bug would be silently deleted
// by the very rescan that migrated the database.
func RemapBugNodes(dbPath string, aliases map[string]string) (int64, error) {
	if len(aliases) == 0 {
		return 0, nil
	}
	var count int64
	err := withSQLiteWrite(dbPath, func(db *sql.DB) error {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		stmt, err := tx.Prepare("UPDATE bugs SET node_id = ? WHERE node_id = ?")
		if err != nil {
			return err
		}
		defer stmt.Close()
		olds := make([]string, 0, len(aliases))
		for old := range aliases {
			olds = append(olds, old)
		}
		sort.Strings(olds)
		for _, old := range olds {
			if old == aliases[old] {
				continue
			}
			res, err := stmt.Exec(aliases[old], old)
			if err != nil {
				return err
			}
			n, err := res.RowsAffected()
			if err != nil {
				return err
			}
			count += n
		}
		return tx.Commit()
	})
	return count, err
}

// ResolveAlias maps a legacy resource ID to its current one. Returns ("", false) when the
// ID has no alias. Aliases are followed transitively (bounded) so a database migrated
// twice still resolves IDs from before the first migration.
func ResolveAlias(dbPath, oldID string) (string, bool) {
	if oldID == "" {
		return "", false
	}
	var out string
	err := withSQLiteRead(dbPath, func(db *sql.DB) error {
		cur := oldID
		for hops := 0; hops < 8; hops++ {
			var next string
			row := db.QueryRow("SELECT new_id FROM resource_alias WHERE old_id = ?", cur)
			if scanErr := row.Scan(&next); scanErr != nil {
				break
			}
			if next == "" || next == cur {
				break
			}
			cur = next
		}
		if cur != oldID {
			out = cur
		}
		return nil
	})
	if err != nil || out == "" {
		return "", false
	}
	return out, true
}

// Adds a language column to the resources table if it doesn't exist.
func ensureResourceLanguageColumn(db *sql.DB) error {
	hasColumn, err := resourceLanguageColumnExists(db)
	if err != nil {
		return err
	}
	if hasColumn {
		return nil
	}
	_, err = db.Exec("ALTER TABLE resources ADD COLUMN language TEXT DEFAULT ''")
	return err
}

// Checks if the language column exists in the resources database table.
func resourceLanguageColumnExists(db *sql.DB) (bool, error) {
	rows, err := db.Query("PRAGMA table_info(resources)")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name string
		var typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return false, err
		}
		if name == "language" {
			return true, nil
		}
	}
	return false, rows.Err()
}

// TrackedFiles reports, for each absolute file path, whether the topology holds any resource
// for it.
//
// Deliberately a QUERY rather than a filter over ReadDb. The guard calls this inside a
// PreToolUse hook on the agent's critical path, and materialising the whole graph to answer a
// yes/no about two paths does not scale with the repository: measured on the benchmark
// fixtures, ReadDb takes 13ms for fd (157 resources) but 3.5s for mui/material-ui (74,909) --
// past any timeout a hook can justify, and precisely the large repositories where the answer
// matters most.
//
// A file appears in the table twice over: a FILE resource is keyed by its absolute path, and
// every symbol inside it carries that path in loc_path. Either is proof the topology models
// the file.
func TrackedFiles(dbPath string, paths []string) (map[string]bool, error) {
	out := make(map[string]bool, len(paths))
	if len(paths) == 0 {
		return out, nil
	}
	err := withSQLiteRead(dbPath, func(db *sql.DB) error {
		stmt, err := db.Prepare("SELECT 1 FROM resources WHERE id = ? OR loc_path = ? LIMIT 1")
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, p := range paths {
			var one int
			switch err := stmt.QueryRow(p, p).Scan(&one); err {
			case nil:
				out[p] = true
			case sql.ErrNoRows:
				out[p] = false
			default:
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Loads the complete topology from a SQLite database, including resources, connections, and warnings.
func ReadDb(path string) (*domain.Topology, error) {
	var topo *domain.Topology
	err := withSQLiteRead(path, func(db *sql.DB) error {
		read := &domain.Topology{
			Resources: make(map[string]domain.Resource),
			Warnings:  make(map[string]domain.TopologyWarning),
			Errors:    make(map[string]string),
		}

		rows, err := db.Query("SELECT key, value FROM info")
		if err != nil {
			return err
		}
		for rows.Next() {
			var key, val string
			err = rows.Scan(&key, &val)
			if err != nil {
				rows.Close()
				return err
			}
			if key == "root" {
				read.Root = val
			} else if key == "language" {
				read.Language = val
			} else if key == "languages" {
				_ = json.Unmarshal([]byte(val), &read.Languages)
			} else if len(key) > 6 && key[:6] == "error:" {
				read.Errors[key[6:]] = val
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()

		hasResourceLanguage, err := resourceLanguageColumnExists(db)
		if err != nil {
			return err
		}
		// COALESCE the nullable text columns: `description` and `properties_json` are
		// declared without NOT NULL, and scanning a NULL into a Go string fails with
		// "converting NULL to string is unsupported" — which would make a database
		// written by anything other than our own writers unreadable rather than merely
		// incomplete.
		resourceQuery := "SELECT id, kind, name, COALESCE(description, ''), " +
			"COALESCE(properties_json, ''), starts_at, ends_at, COALESCE(loc_path, '') FROM resources"
		if hasResourceLanguage {
			resourceQuery = "SELECT id, kind, name, COALESCE(language, ''), " +
				"COALESCE(description, ''), COALESCE(properties_json, ''), " +
				"starts_at, ends_at, COALESCE(loc_path, '') FROM resources"
		}
		resRows, err := db.Query(resourceQuery)
		if err != nil {
			return err
		}
		for resRows.Next() {
			var id, kind, name, language, desc, propsJSON, locPath string
			var startsAt, endsAt int
			if hasResourceLanguage {
				err = resRows.Scan(&id, &kind, &name, &language, &desc, &propsJSON, &startsAt, &endsAt, &locPath)
			} else {
				err = resRows.Scan(&id, &kind, &name, &desc, &propsJSON, &startsAt, &endsAt, &locPath)
			}
			if err != nil {
				resRows.Close()
				return err
			}
			if language == "" {
				language = read.Language
			}
			res := domain.Resource{
				ID:          id,
				Kind:        domain.ResourceKind(kind),
				Name:        name,
				Language:    language,
				Description: desc,
				Location: domain.Location{
					StartsAt: startsAt,
					EndsAt:   endsAt,
					Path:     locPath,
				},
				Properties:  fromJSONMap(propsJSON),
				Connections: make(map[string][]string),
			}
			read.Resources[id] = res
		}
		if err := resRows.Err(); err != nil {
			resRows.Close()
			return err
		}
		resRows.Close()

		connRows, err := db.Query("SELECT source_id, conn_type, target_id FROM connections ORDER BY source_id, conn_type")
		if err != nil {
			return err
		}
		for connRows.Next() {
			var sourceID, connType, targetID string
			err = connRows.Scan(&sourceID, &connType, &targetID)
			if err != nil {
				connRows.Close()
				return err
			}
			if res, ok := read.Resources[sourceID]; ok {
				res.Connections[connType] = append(res.Connections[connType], targetID)
				read.Resources[sourceID] = res
			}
		}
		if err := connRows.Err(); err != nil {
			connRows.Close()
			return err
		}
		connRows.Close()

		warnRows, err := db.Query("SELECT id, source_id, kind, target_id, message FROM warnings")
		if err != nil {
			return err
		}
		defer warnRows.Close()
		for warnRows.Next() {
			var id, sourceID, kind, targetID, message string
			if err := warnRows.Scan(&id, &sourceID, &kind, &targetID, &message); err != nil {
				return err
			}
			read.Warnings[id] = domain.TopologyWarning{
				ID:       id,
				SourceID: sourceID,
				Kind:     domain.WarningKind(kind),
				TargetID: targetID,
				Message:  message,
			}
		}
		if err := warnRows.Err(); err != nil {
			return err
		}

		if len(read.Languages) == 0 && read.Language != "" {
			read.Languages = []string{read.Language}
		}
		seenLanguages := make(map[string]bool, len(read.Languages))
		for _, lang := range read.Languages {
			if lang != "" {
				seenLanguages[lang] = true
			}
		}
		for _, res := range read.Resources {
			if res.Language != "" && !seenLanguages[res.Language] {
				read.Languages = append(read.Languages, res.Language)
				seenLanguages[res.Language] = true
			}
		}

		topo = read
		return nil
	})
	return topo, err
}

// Updates a resource's description in the database.
//
// `kind`, when non-empty, is enforced rather than ignored: an update aimed at the wrong
// kind is a caller bug and must not silently land on a same-named resource. A write that
// matches no row returns an error too — previously this call succeeded silently against a
// nonexistent ID, so a description generator working from stale or mis-formatted IDs
// would report success while storing nothing.
//
// The description is length-checked before the write. The budget applies to descriptions
// arriving now, never to what is already stored (see domain.ValidateDescription): bulk
// restores go through UpdateDescriptions, which stays uncapped so a sidecar can put back
// long descriptions written before the cap existed.
func UpdateDescription(dbPath string, kind domain.ResourceKind, id string, description string) error {
	if err := domain.ValidateDescription(kind, description); err != nil {
		return err
	}
	return withSQLiteWrite(dbPath, func(db *sql.DB) error {
		query := "UPDATE resources SET description = ? WHERE id = ?"
		args := []interface{}{description, id}
		if kind != "" {
			query += " AND kind = ?"
			args = append(args, string(kind))
		}
		result, err := db.Exec(query, args...)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			if kind != "" {
				return fmt.Errorf("no %s resource with id %q", kind, id)
			}
			return fmt.Errorf("no resource with id %q", id)
		}
		return nil
	})
}

// UpdateDescriptions applies many description writes in ONE transaction and returns the
// number of rows changed. Restoring a sidecar can touch tens of thousands of rows, and one
// transaction per row makes that minutes rather than seconds.
//
// Unlike UpdateDescription this does not fail on a miss: the caller (a sidecar import) has
// already resolved every id against the topology and reports its own per-tier accounting.
func UpdateDescriptions(dbPath string, descriptions map[string]string) (int64, error) {
	var count int64
	if len(descriptions) == 0 {
		return 0, nil
	}
	err := withSQLiteWrite(dbPath, func(db *sql.DB) error {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		stmt, err := tx.Prepare("UPDATE resources SET description = ? WHERE id = ?")
		if err != nil {
			return err
		}
		defer stmt.Close()
		ids := make([]string, 0, len(descriptions))
		for id := range descriptions {
			ids = append(ids, id)
		}
		sort.Strings(ids) // deterministic write order
		for _, id := range ids {
			res, err := stmt.Exec(descriptions[id], id)
			if err != nil {
				return err
			}
			n, err := res.RowsAffected()
			if err != nil {
				return err
			}
			count += n
		}
		return tx.Commit()
	})
	return count, err
}

// Clears descriptions from resources in the database, optionally filtered by resource kind.
func ClearDescriptions(dbPath string, targets []domain.ResourceKind) (int64, error) {
	var count int64
	err := withSQLiteWrite(dbPath, func(db *sql.DB) error {
		query := "UPDATE resources SET description = '' WHERE COALESCE(description, '') <> ''"
		args := make([]interface{}, 0, len(targets))
		if len(targets) > 0 {
			placeholders := make([]string, 0, len(targets))
			for _, target := range targets {
				placeholders = append(placeholders, "?")
				args = append(args, string(target))
			}
			query += " AND kind IN (" + strings.Join(placeholders, ", ") + ")"
		}

		result, err := db.Exec(query, args...)
		if err != nil {
			return err
		}
		count, err = result.RowsAffected()
		return err
	})
	return count, err
}

// Queries the database for all source IDs that call a target resource via a specified connection type.
func GetCallers(dbPath string, targetID string, connType string) ([]string, error) {
	db, err := sql.Open("sqlite", dbPath+"?cache=shared&_journal_mode=WAL")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query("SELECT source_id FROM connections WHERE conn_type = ? AND target_id = ?", connType, targetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []string
	for rows.Next() {
		var sourceID string
		if err := rows.Scan(&sourceID); err != nil {
			continue
		}
		results = append(results, sourceID)
	}
	return results, nil
}

// Inserts a KnownBug record into the SQLite database.
func CreateBug(dbPath string, bug domain.KnownBug) error {
	return withSQLiteWrite(dbPath, func(db *sql.DB) error {
		if err := createSchema(db); err != nil {
			return err
		}

		_, err := db.Exec("INSERT INTO bugs (id, node_id, description, state) VALUES (?, ?, ?, ?)",
			bug.ID, bug.NodeID, bug.Description, string(bug.State))
		return err
	})
}

// Queries the SQLite database for known bugs, optionally filtered by node ID or bug state.
func ReadBugs(dbPath string, nodeID string, state domain.BugState) ([]domain.KnownBug, error) {
	db, err := sql.Open("sqlite", dbPath+"?cache=shared&_journal_mode=WAL")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	q := "SELECT id, node_id, description, state FROM bugs WHERE 1=1"
	var args []interface{}
	if nodeID != "" {
		q += " AND node_id = ?"
		args = append(args, nodeID)
	}
	if state != "" {
		q += " AND state = ?"
		args = append(args, string(state))
	}
	q += " ORDER BY id"

	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var bugs []domain.KnownBug
	for rows.Next() {
		var b domain.KnownBug
		var stateStr string
		if err := rows.Scan(&b.ID, &b.NodeID, &b.Description, &stateStr); err != nil {
			continue
		}
		b.State = domain.BugState(stateStr)
		bugs = append(bugs, b)
	}
	return bugs, nil
}

// Updates a bug's state in the SQLite database by ID.
func UpdateBugState(dbPath string, bugID string, state domain.BugState) error {
	db, err := sql.Open("sqlite", dbPath+"?cache=shared&_journal_mode=WAL")
	if err != nil {
		return err
	}
	defer db.Close()

	result, err := db.Exec("UPDATE bugs SET state = ? WHERE id = ?", string(state), bugID)
	if err != nil {
		return err
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("bug not found: %s", bugID)
	}
	return nil
}

// Deletes a single bug record from the SQLite database by ID.
// A delete that matched nothing is an ERROR, not a success -- same contract as
// UpdateBugState above. Reporting success for a bug that was not there made the bug-judge
// fan-out's mutual-delete race invisible: two judges resolving the same duplicate pair both
// reported "deleted" while one of them had removed a record the other had already taken.
func DeleteBug(dbPath string, bugID string) error {
	return withSQLiteWrite(dbPath, func(db *sql.DB) error {
		result, err := db.Exec("DELETE FROM bugs WHERE id = ?", bugID)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			return fmt.Errorf("bug not found: %s", bugID)
		}
		return nil
	})
}

// Deletes all bug records from the SQLite database.
func DeleteAllBugs(dbPath string) error {
	return withSQLiteWrite(dbPath, func(db *sql.DB) error {
		_, err := db.Exec("DELETE FROM bugs")
		return err
	})
}

// Marshals a value to JSON string, returning empty object on nil or marshal error.
func toJSON(v interface{}) string {
	if v == nil {
		return "{}"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// Unmarshals a JSON string into a map, returning an empty map on parse error.
func fromJSONMap(s string) map[string]any {
	var v map[string]any
	if s != "" {
		if err := json.Unmarshal([]byte(s), &v); err != nil {
			v = make(map[string]any)
		}
	}
	if v == nil {
		v = make(map[string]any)
	}
	return v
}

// Deletes bug records whose associated resources no longer exist in the topology.
func CleanupOrphanedBugs(dbPath string, topo *domain.Topology) error {
	bugs, err := ReadBugs(dbPath, "", "")
	if err != nil {
		return err
	}
	for _, bug := range bugs {
		if _, ok := topo.Resources[bug.NodeID]; !ok {
			if err := DeleteBug(dbPath, bug.ID); err != nil {
				fmt.Printf("Warning: failed to delete orphaned bug %s: %v\n", bug.ID, err)
			}
		}
	}
	return nil
}

// CleanupOrphanedBugsScoped is the partial-path equivalent of CleanupOrphanedBugs:
// it deletes bugs whose NodeID no longer exists, using a targeted per-bug
// existence check instead of an in-memory resource set. When there are no bugs
// it does nothing (the common case), so it costs one cheap query.
func CleanupOrphanedBugsScoped(dbPath string) error {
	bugs, err := ReadBugs(dbPath, "", "")
	if err != nil {
		return err
	}
	if len(bugs) == 0 {
		return nil
	}
	nodeIDs := make([]string, 0, len(bugs))
	seen := make(map[string]bool, len(bugs))
	for _, bug := range bugs {
		if !seen[bug.NodeID] {
			seen[bug.NodeID] = true
			nodeIDs = append(nodeIDs, bug.NodeID)
		}
	}
	existing := make(map[string]bool, len(nodeIDs))
	err = withSQLiteRead(dbPath, func(db *sql.DB) error {
		for _, chunk := range chunkStrings(nodeIDs, sqliteMaxVariables) {
			args := make([]interface{}, len(chunk))
			for i, id := range chunk {
				args[i] = id
			}
			rows, qerr := db.Query("SELECT id FROM resources WHERE id IN ("+placeholders(len(chunk))+")", args...)
			if qerr != nil {
				return qerr
			}
			for rows.Next() {
				var id string
				if serr := rows.Scan(&id); serr != nil {
					rows.Close()
					return serr
				}
				existing[id] = true
			}
			if rerr := rows.Err(); rerr != nil {
				rows.Close()
				return rerr
			}
			rows.Close()
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, bug := range bugs {
		if !existing[bug.NodeID] {
			if derr := DeleteBug(dbPath, bug.ID); derr != nil {
				fmt.Printf("Warning: failed to delete orphaned bug %s: %v\n", bug.ID, derr)
			}
		}
	}
	return nil
}

// ClearOversizedDescriptions deletes every stored description that overruns its kind's budget,
// optionally restricted to `targets`. Returns how many were removed.
//
// WHY THIS EXISTS AS A COMMAND. domain.DescriptionForStorage keeps over-budget text out of the
// database from now on, but it cannot fix what is already there: descriptions written before it
// existed are grandfathered, and domain.ValidateDescription deliberately never sweeps stored
// rows. This is that sweep, run explicitly. On grafana/k6 it is 34% of harvested doc comments.
//
// It CLEARS rather than truncates, for the same reason the storage path now refuses: the first
// 120 characters of a paragraph is a severed clause that reads like a description without being
// one. Cleared resources become visible to `descriptions generate` and
// `node_list_no_description`, which is the state that gets them a real one.
func ClearOversizedDescriptions(dbPath string, targets []domain.ResourceKind) (int64, error) {
	type row struct {
		id   string
		kind domain.ResourceKind
		desc string
	}
	var over []row
	if err := withSQLiteRead(dbPath, func(db *sql.DB) error {
		rows, err := db.Query("SELECT id, kind, description FROM resources " +
			"WHERE description IS NOT NULL AND TRIM(description) <> ''")
		if err != nil {
			return err
		}
		defer rows.Close()
		want := map[domain.ResourceKind]bool{}
		for _, t := range targets {
			want[t] = true
		}
		for rows.Next() {
			var r row
			var kind string
			if err := rows.Scan(&r.id, &kind, &r.desc); err != nil {
				return err
			}
			r.kind = domain.ResourceKind(kind)
			if len(want) > 0 && !want[r.kind] {
				continue
			}
			if domain.ValidateDescription(r.kind, r.desc) != nil {
				over = append(over, r)
			}
		}
		return rows.Err()
	}); err != nil {
		return 0, err
	}
	if len(over) == 0 {
		return 0, nil
	}
	var n int64
	err := withSQLiteWrite(dbPath, func(db *sql.DB) error {
		stmt, err := db.Prepare("UPDATE resources SET description = '' WHERE id = ?")
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, r := range over {
			res, err := stmt.Exec(r.id)
			if err != nil {
				return err
			}
			if c, _ := res.RowsAffected(); c > 0 {
				n += c
			}
		}
		return nil
	})
	return n, err
}
