package helper

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
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
		resStmt, err := tx.Prepare("INSERT INTO resources (id, kind, name, language, description, properties_json, starts_at, ends_at, loc_path, exact_hash, norm_hash, norm_lines) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
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

		warnStmt, err := tx.Prepare("INSERT INTO warnings VALUES (?, ?, ?, ?, ?, ?)")
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
			if _, err := resStmt.Exec(id, string(res.Kind), res.Name, res.Language, domain.DescriptionForStorage(res.Kind, res.Description), propsJSON, startsAt, endsAt, locPath, res.ExactHash, res.NormHash, res.NormLines); err != nil {
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
			if _, err := warnStmt.Exec(id, w.SourceID, string(w.Kind), w.TargetID, w.Message, w.Baseline); err != nil {
				return err
			}
		}

		if err := writeScanErrors(tx, topo.Errors); err != nil {
			return err
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
		loc_path TEXT DEFAULT '',
		-- Fingerprints of the resource's own source span; see domain.Resource.ExactHash.
		-- Indexed because a move is matched by looking a hash up, not by scanning.
		exact_hash TEXT DEFAULT '',
		norm_hash TEXT DEFAULT '',
		norm_lines INT NOT NULL DEFAULT 0
	);
	CREATE INDEX IF NOT EXISTS idx_resources_norm_hash ON resources(norm_hash);
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
		message TEXT NOT NULL,
		-- The signature source_id had when this warning was raised; see
		-- domain.TopologyWarning.Baseline. Empty for every other kind.
		baseline TEXT DEFAULT ''
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
	-- scheme change. Populated by FullReScan's identity remap; never by a normal scan.
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
	if err := ensureWarningBaselineColumn(db); err != nil {
		return err
	}
	if err := ensureResourceBodyHashColumns(db); err != nil {
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
// v2: introduces the resource_alias table, which keeps an ID an agent already knows
// resolving after the ID grammar changes under it. This step deliberately does NOT rewrite
// any ID: recomputing IDs needs the scanners and the source tree, neither of which is
// available here. The table is filled by FullReScan's identity remap -- see remapByIdentity.
//
// v3: adds warnings.baseline. It runs HERE rather than only in createSchema because the
// read path selects the column: ensureSQLiteMigrated fires before any read or write of an
// existing database, while createSchema runs only on the write paths, so a database that
// was merely read after the upgrade would have failed on "no such column". Existing rows
// keep an empty baseline, which reads as "raised before this was recorded" and simply never
// discharges -- the old behaviour rather than a wrong one.
//
// v4: adds resources.exact_hash and resources.norm_hash. Here for the same reason as v3 --
// the read path selects them, and ensureSQLiteMigrated is the only thing that fires before a
// read of an existing database. Because it runs everywhere, the reader may assume the columns
// exist and does not need the has-this-column branch the `language` column still carries.
//
// DELIBERATELY NOT BACKFILLED. Computing a body hash needs the file, and a migration has no
// business reading the whole working tree. Existing rows keep an empty hash until the next
// scan re-stamps their file; an empty hash only makes a match tier unavailable, and can never
// produce a wrong match.
func applyMigrations(db *sql.DB) error {
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	// The additive column guards run UNGATED, before the version ladder.
	//
	// A numbered step that fails partway returns here with user_version already advanced by
	// the steps before it, and the ladder then skips the unfinished one forever -- every later
	// read failing on "no such column" with nothing able to repair it. That is not
	// hypothetical: it is what a database looks like after an interrupted upgrade, or after a
	// build that shipped half of a step. Re-asserting the columns is idempotent, costs a
	// PRAGMA on a sync.Once path that runs once per database per process, and turns a
	// permanently broken database back into a working one.
	if err := ensureResourceBodyHashColumns(db); err != nil {
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
		if _, err := db.Exec(
			fmt.Sprintf("PRAGMA user_version = %d", 2)); err != nil {
			return err
		}
	}
	if version < 3 {
		if err := ensureWarningBaselineColumn(db); err != nil {
			return err
		}
		if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", 3)); err != nil {
			return err
		}
	}
	if version < 4 {
		if err := ensureResourceBodyHashColumns(db); err != nil {
			return err
		}
		if _, err := db.Exec(
			fmt.Sprintf("PRAGMA user_version = %d", latestSchemaVersion)); err != nil {
			return err
		}
	}
	return nil
}

// writeScanErrors writes the scan-error log into `info`, bounded and deterministic about
// WHICH errors survive.
//
// `info` is a key/value table that also serves as the scan error log, and it was unbounded:
// a repo whose JS and TS scanners produce colliding ids wrote one row per collision. aracne's
// own self-scan accumulated 23,902 of them, which is why that database was 7.5 MB of mostly
// duplicate error text. Nothing reads more than a handful, so keep a sorted sample and record
// the true count.
//
// It is a function rather than a block inside WriteDb because WriteIncremental writes the
// same rows and did NOT cap them: the bloat this exists to stop simply grew back on the
// incremental path, which is the path a project is on almost all of the time.
func writeScanErrors(tx *sql.Tx, errs map[string]string) error {
	if _, err := tx.Exec("DELETE FROM info WHERE key LIKE 'error:%' OR key = 'error_count'"); err != nil {
		return err
	}
	pats := make([]string, 0, len(errs))
	for pat := range errs {
		pats = append(pats, pat)
	}
	sort.Strings(pats)
	for i, pat := range pats {
		if i >= maxStoredErrors {
			break
		}
		if _, err := tx.Exec("INSERT OR REPLACE INTO info VALUES (?, ?)", "error:"+pat, errs[pat]); err != nil {
			return err
		}
	}
	if len(pats) > maxStoredErrors {
		if _, err := tx.Exec("INSERT OR REPLACE INTO info VALUES (?, ?)", "error_count",
			strconv.Itoa(len(pats))); err != nil {
			return err
		}
	}
	return nil
}

// writeTopologyInfo upserts the three rows that describe the graph as a whole.
//
// UPSERT, and scoped to those three keys, because `info` is a key/value table with more than
// one writer: writeScanErrors keeps the capped error sample there, and whatever is added next
// will be there too. A `DELETE FROM info` on the incremental path took out everything it did
// not itself re-insert -- which is how the error sample came back uncapped, and how an
// id-scheme stamp that no longer exists used to be erased. A writer that owns three keys
// should touch three keys.
func writeTopologyInfo(tx *sql.Tx, topo *domain.Topology) error {
	languagesJSON, err := json.Marshal(topo.Languages)
	if err != nil {
		return err
	}
	for _, row := range [][2]string{
		{"root", topo.Root},
		{"language", topo.Language},
		{"languages", string(languagesJSON)},
	} {
		if _, err := tx.Exec(
			"INSERT INTO info (key, value) VALUES (?, ?) "+
				"ON CONFLICT(key) DO UPDATE SET value = excluded.value",
			row[0], row[1]); err != nil {
			return err
		}
	}
	return nil
}

// maxStoredErrors bounds the scan-error sample kept in `info`. See writeScanErrors.
const maxStoredErrors = 100

// latestSchemaVersion is the user_version applyMigrations brings a database up to. Bump it
// in the same commit as a new migration step.
const latestSchemaVersion = 4

// WHY THERE IS NO ID-SCHEME STAMP.
//
// Resource IDs are minted by the scanners, and their grammar has changed once (Python/JS/TS
// module paths became repo-relative; Rust took the literal "crate" with no Cargo [package]).
// A database written before such a change holds IDs the current binary would never generate,
// so matching by string alone silently loses whatever is attached to them.
//
// There used to be an `id_scheme` row in `info` recording which grammar a database held, plus
// ReadIDScheme/WriteIDScheme to read and write it. NOTHING EVER READ IT: no scan consulted it,
// and the fix it was meant to trigger -- FullReScan's identity remap -- runs unconditionally
// anyway, matching resources by path+kind+name+parent then by source hash, carrying
// descriptions across and writing resource_alias rows so old IDs keep resolving.
//
// So it was a row written on every full scan and read by nobody, and it has been removed. What
// it would have bought, had it been wired up, is an ESCALATION: `arac scan` defaults to an
// incremental scan, which re-parses only changed files -- so on a scheme-drifted database it
// re-mints the edited file under the new grammar, leaves every other file under the old one,
// and drops the cross-file edges between them with no warning. The remap only ever runs on the
// full path.
//
// IF THE ID GRAMMAR EVER CHANGES AGAIN, that is the thing to know: the release has to tell
// people to run `arac scan --all` once, because an ordinary `arac scan` will not notice and
// will half-migrate the graph. Re-introducing a stamp is the alternative, and is a bigger
// change than it looks -- it has to survive the incremental write path as well as the full one.

// WriteResourceAliases records old-ID -> new-ID mappings from the identity remap.
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
		count = 0 // reset per attempt; see UpdateDescriptions
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
		count = 0 // reset per attempt; see UpdateDescriptions
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

// ensureWarningBaselineColumn adds warnings.baseline to a database created
// before signature_changed recorded what it was raised against. Additive and
// idempotent, like the language column: an existing row keeps an empty
// baseline, which reads as "unknown" and simply never discharges -- the old
// behaviour, rather than a wrong one.
func ensureWarningBaselineColumn(db *sql.DB) error {
	has, err := columnExists(db, "warnings", "baseline")
	if err != nil || has {
		return err
	}
	_, err = db.Exec("ALTER TABLE warnings ADD COLUMN baseline TEXT DEFAULT ''")
	return err
}

// ensureResourceBodyHashColumns adds resources.exact_hash and resources.norm_hash to a
// database created before descriptions could follow moved code. Additive and idempotent, like
// the language and baseline columns before it.
func ensureResourceBodyHashColumns(db *sql.DB) error {
	// A migration may meet a database that has no `resources` table at all -- one written by
	// an older build that only ever populated `warnings`, or a half-initialised file. ALTER
	// TABLE on a missing table is an error, and an error here aborts the whole migration and
	// leaves user_version behind, so the next run repeats it forever. createSchema creates the
	// table with both columns already on it, so there is nothing to do.
	has, err := tableExists(db, "resources")
	if err != nil || !has {
		return err
	}
	for _, col := range []string{"exact_hash", "norm_hash"} {
		has, err := columnExists(db, "resources", col)
		if err != nil {
			return err
		}
		if has {
			continue
		}
		if _, err := db.Exec("ALTER TABLE resources ADD COLUMN " + col + " TEXT DEFAULT ''"); err != nil {
			return err
		}
	}
	has, err = columnExists(db, "resources", "norm_lines")
	if err != nil || has {
		return err
	}
	_, err = db.Exec("ALTER TABLE resources ADD COLUMN norm_lines INT NOT NULL DEFAULT 0")
	return err
}

// tableExists reports whether the named table is present.
func tableExists(db *sql.DB, table string) (bool, error) {
	var name string
	err := db.QueryRow(
		"SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&name)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// columnExists reports whether table already has the named column.
func columnExists(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
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
		hit := func(p string) (bool, error) {
			var one int
			switch err := stmt.QueryRow(p, p).Scan(&one); err {
			case nil:
				return true, nil
			case sql.ErrNoRows:
				return false, nil
			default:
				return false, err
			}
		}
		for _, p := range paths {
			found, err := hit(p)
			if err != nil {
				return err
			}
			// A miss is retried under the canonical spelling, because loc_path holds the one
			// the scan minted through CanonicalPath: a project reached through a symlinked
			// directory (macOS /var, a bind mount, a linked checkout) stores its files under
			// the real path while the guard is handed the path the agent typed. The answer
			// was "not indexed" for a file the index held, and every read of it went
			// un-intercepted. Only on the miss -- this runs on every guarded tool call, and
			// EvalSymlinks is one lstat per component.
			if !found {
				if canon := CanonicalPath(p); canon != p {
					if found, err = hit(canon); err != nil {
						return err
					}
				}
			}
			out[p] = found
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
		// exact_hash / norm_hash need no such probe: applyMigrations adds them at v4 and runs
		// before any read, so unlike `language` they are always there by the time we get here.
		// COALESCE the nullable text columns: `description` and `properties_json` are
		// declared without NOT NULL, and scanning a NULL into a Go string fails with
		// "converting NULL to string is unsupported" — which would make a database
		// written by anything other than our own writers unreadable rather than merely
		// incomplete.
		resourceQuery := "SELECT id, kind, name, COALESCE(description, ''), " +
			"COALESCE(properties_json, ''), starts_at, ends_at, COALESCE(loc_path, ''), " +
			"COALESCE(exact_hash, ''), COALESCE(norm_hash, ''), COALESCE(norm_lines, 0) FROM resources"
		if hasResourceLanguage {
			resourceQuery = "SELECT id, kind, name, COALESCE(language, ''), " +
				"COALESCE(description, ''), COALESCE(properties_json, ''), " +
				"starts_at, ends_at, COALESCE(loc_path, ''), " +
				"COALESCE(exact_hash, ''), COALESCE(norm_hash, ''), COALESCE(norm_lines, 0) FROM resources"
		}
		resRows, err := db.Query(resourceQuery)
		if err != nil {
			return err
		}
		for resRows.Next() {
			var id, kind, name, language, desc, propsJSON, locPath string
			var exactHash, normHash string
			var normLines int
			var startsAt, endsAt int
			if hasResourceLanguage {
				err = resRows.Scan(&id, &kind, &name, &language, &desc, &propsJSON, &startsAt, &endsAt, &locPath, &exactHash, &normHash, &normLines)
			} else {
				err = resRows.Scan(&id, &kind, &name, &desc, &propsJSON, &startsAt, &endsAt, &locPath, &exactHash, &normHash, &normLines)
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
				ExactHash:   exactHash,
				NormHash:    normHash,
				NormLines:   normLines,
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

		warnRows, err := db.Query("SELECT id, source_id, kind, target_id, message, baseline FROM warnings")
		if err != nil {
			return err
		}
		defer warnRows.Close()
		for warnRows.Next() {
			var id, sourceID, kind, targetID, message, baseline string
			if err := warnRows.Scan(&id, &sourceID, &kind, &targetID, &message, &baseline); err != nil {
				return err
			}
			read.Warnings[id] = domain.TopologyWarning{
				ID:       id,
				SourceID: sourceID,
				Kind:     domain.WarningKind(kind),
				TargetID: targetID,
				Message:  message,
				Baseline: baseline,
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
	// A description is one line: it is printed after "## id: " in every later CONTEXT block
	// and grep row header. Stored raw, a newline from any writer -- a model, an MCP caller --
	// broke that grammar and could forge a "# CONTEXT:" header of its own. Same collapse the
	// scanner's harvest applies (domain.DescriptionForStorage).
	description = strings.Join(strings.Fields(description), " ")
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
		// A function and a method are one question asked two ways. The scanners store a
		// function with a receiver or a declaring class as `method`, which no caller can see
		// from the id -- and every language's update_description mapped "Method" (Java's
		// "Constructor" too) to `function`, so each such write was refused. `id` is the primary
		// key, so the id alone names exactly one row and this cannot land on a same-named
		// resource of another kind; the two share one description budget, so the check above
		// holds for either. Every other kind stays enforced.
		if n == 0 && (kind == domain.ResourceFunction || kind == domain.ResourceMethod) {
			result, err = db.Exec("UPDATE resources SET description = ? WHERE id = ? AND kind IN (?, ?)",
				description, id, string(domain.ResourceFunction), string(domain.ResourceMethod))
			if err != nil {
				return err
			}
			if n, err = result.RowsAffected(); err != nil {
				return err
			}
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
		// Reset per attempt: withSQLiteRetry may call this callback again after a
		// SQLITE_BUSY, and the failed attempt's transaction rolls back while a counter
		// declared outside the closure does not -- so a contended write reported more
		// rows changed than it changed. Same rule as GetCallers and ReadBugs.
		count = 0
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
//
// EVERY DATABASE ENTRANCE GOES THROUGH withSQLiteRead / withSQLiteWrite. This one, ReadBugs and
// UpdateBugState used to call sql.Open directly and so had none of what those wrappers provide:
// no per-database mutex, no schema migration, no retry on SQLITE_BUSY, and -- because the DSN
// they carried was written in another driver's parameter names, see openSQLite -- no busy
// timeout either, so they failed on the first collision instead of waiting for it to clear.
//
// The bug pipeline is the workload that makes that reachable: bug-judge and bug-solver fan out
// as parallel sub-agents calling bug list / acknowledge / dismiss, while the guard's pre-tool
// scan writes the graph in front of every one of their tool calls.
func GetCallers(dbPath string, targetID string, connType string) ([]string, error) {
	var results []string
	err := withSQLiteRead(dbPath, func(db *sql.DB) error {
		rows, err := db.Query(
			"SELECT source_id FROM connections WHERE conn_type = ? AND target_id = ?",
			connType, targetID)
		if err != nil {
			return err
		}
		defer rows.Close()

		// Reset per attempt: withSQLiteRetry may call this callback again after a
		// SQLITE_BUSY, and appending to the previous attempt's slice would return the
		// partial first read concatenated with the complete second one.
		results = nil
		for rows.Next() {
			var sourceID string
			// A scan error is returned, not skipped. `continue` turned a truncated read
			// into a short list reported as a successful one.
			if err := rows.Scan(&sourceID); err != nil {
				return err
			}
			results = append(results, sourceID)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
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
//
// Locked and migrated like every other read; see GetCallers for what calling sql.Open here
// used to cost. This one also skipped ensureSQLiteMigrated, which made it the single reader
// that could fail with "no such table: bugs" on a database every other path would have brought
// up to schema first.
func ReadBugs(dbPath string, nodeID string, state domain.BugState) ([]domain.KnownBug, error) {
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

	var bugs []domain.KnownBug
	err := withSQLiteRead(dbPath, func(db *sql.DB) error {
		rows, err := db.Query(q, args...)
		if err != nil {
			return err
		}
		defer rows.Close()

		// Reset per attempt, for the same reason as GetCallers: a retried callback must
		// not append to what the attempt before it had already collected.
		bugs = nil
		for rows.Next() {
			var b domain.KnownBug
			var stateStr string
			if err := rows.Scan(&b.ID, &b.NodeID, &b.Description, &stateStr); err != nil {
				return err
			}
			b.State = domain.BugState(stateStr)
			bugs = append(bugs, b)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return bugs, nil
}

// Updates a bug's state in the SQLite database by ID.
//
// The write that most needed the lock, and the one that had it least: two bug-judge sub-agents
// acknowledging different bugs in the same fan-out collided on the first attempt and one of
// them lost its update, reported as SQLITE_BUSY. See GetCallers.
func UpdateBugState(dbPath string, bugID string, state domain.BugState) error {
	return withSQLiteWrite(dbPath, func(db *sql.DB) error {
		result, err := db.Exec("UPDATE bugs SET state = ? WHERE id = ?", string(state), bugID)
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rows == 0 {
			return fmt.Errorf("bug not found: %s", bugID)
		}
		return nil
	})
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
				fmt.Fprintf(os.Stderr, "Warning: failed to delete orphaned bug %s: %v\n", bug.ID, err)
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
				fmt.Fprintf(os.Stderr, "Warning: failed to delete orphaned bug %s: %v\n", bug.ID, derr)
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
		n = 0 // reset per attempt; see UpdateDescriptions
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
