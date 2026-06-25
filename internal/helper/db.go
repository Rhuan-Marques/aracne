package helper

import (
	"database/sql"
	"encoding/json"
	"fmt"
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
			if _, err := resStmt.Exec(id, string(res.Kind), res.Name, res.Language, res.Description, propsJSON, startsAt, endsAt, locPath); err != nil {
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

		for pat, msg := range topo.Errors {
			if _, err := tx.Exec("INSERT INTO info VALUES (?, ?)", "error:"+pat, msg); err != nil {
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
		resourceQuery := "SELECT id, kind, name, description, properties_json, starts_at, ends_at, loc_path FROM resources"
		if hasResourceLanguage {
			resourceQuery = "SELECT id, kind, name, language, description, properties_json, starts_at, ends_at, loc_path FROM resources"
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
func UpdateDescription(dbPath string, kind domain.ResourceKind, id string, description string) error {
	return withSQLiteWrite(dbPath, func(db *sql.DB) error {
		_, err := db.Exec("UPDATE resources SET description = ? WHERE id = ?", description, id)
		return err
	})
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
func DeleteBug(dbPath string, bugID string) error {
	return withSQLiteWrite(dbPath, func(db *sql.DB) error {
		_, err := db.Exec("DELETE FROM bugs WHERE id = ?", bugID)
		return err
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
