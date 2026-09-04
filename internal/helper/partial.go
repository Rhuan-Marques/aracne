package helper

import (
	"database/sql"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// sqliteMaxVariables is a conservative bound on the number of bind parameters
// per statement. SQLite's SQLITE_MAX_VARIABLE_NUMBER defaults to 999 on older
// builds and 32766 on newer ones; 900 stays safe everywhere and leaves headroom
// for any extra bound params (e.g. a trailing conn_type).
const sqliteMaxVariables = 900

// chunkStrings splits xs into consecutive slices of at most size elements.
// A non-positive size falls back to sqliteMaxVariables.
func chunkStrings(xs []string, size int) [][]string {
	if size <= 0 {
		size = sqliteMaxVariables
	}
	var chunks [][]string
	for i := 0; i < len(xs); i += size {
		end := i + size
		if end > len(xs) {
			end = len(xs)
		}
		chunks = append(chunks, xs[i:end])
	}
	return chunks
}

// placeholders returns "?, ?, ..." with n placeholders for SQL IN-lists.
func placeholders(n int) string {
	ps := make([]string, n)
	for i := range ps {
		ps[i] = "?"
	}
	return strings.Join(ps, ", ")
}

// readInfoValue returns the value of a single info key, or "" if absent.
func readInfoValue(db *sql.DB, key string) (string, error) {
	var value string
	err := db.QueryRow("SELECT value FROM info WHERE key = ?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return value, nil
}

// scanResourceRows builds domain.Resource values identically to ReadDb: same
// fields, fromJSONMap for properties, an empty Connections map, and the
// empty-language fallback to defaultLang. hasLanguage selects the column layout
// (legacy DBs lack the language column).
func scanResourceRows(rows *sql.Rows, hasLanguage bool, defaultLang string) (map[string]domain.Resource, error) {
	out := make(map[string]domain.Resource)
	for rows.Next() {
		var id, kind, name, language, desc, propsJSON, locPath string
		var startsAt, endsAt int
		var err error
		if hasLanguage {
			err = rows.Scan(&id, &kind, &name, &language, &desc, &propsJSON, &startsAt, &endsAt, &locPath)
		} else {
			err = rows.Scan(&id, &kind, &name, &desc, &propsJSON, &startsAt, &endsAt, &locPath)
		}
		if err != nil {
			return nil, err
		}
		if language == "" {
			language = defaultLang
		}
		out[id] = domain.Resource{
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
	}
	return out, rows.Err()
}

// resourceSelectColumns returns the resource column list matching ReadDb's
// query for the modern (with language) or legacy (without) schema.
func resourceSelectColumns(hasLanguage bool) string {
	if hasLanguage {
		return "id, kind, name, language, description, properties_json, starts_at, ends_at, loc_path"
	}
	return "id, kind, name, description, properties_json, starts_at, ends_at, loc_path"
}

// hydrateConnections fills the Connections map of each resource with its
// outgoing edges, preserving ReadDb's ORDER BY source_id, conn_type ordering so
// per-conn-type target slices match the full-read path. Orphan edges whose
// source is absent from the map are ignored.
func hydrateConnections(db *sql.DB, resources map[string]domain.Resource) error {
	if len(resources) == 0 {
		return nil
	}
	ids := make([]string, 0, len(resources))
	for id := range resources {
		ids = append(ids, id)
	}
	for _, chunk := range chunkStrings(ids, sqliteMaxVariables) {
		args := make([]interface{}, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		q := "SELECT source_id, conn_type, target_id FROM connections WHERE source_id IN (" +
			placeholders(len(chunk)) + ") ORDER BY source_id, conn_type"
		rows, err := db.Query(q, args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var sourceID, connType, targetID string
			if err := rows.Scan(&sourceID, &connType, &targetID); err != nil {
				rows.Close()
				return err
			}
			if res, ok := resources[sourceID]; ok {
				res.Connections[connType] = append(res.Connections[connType], targetID)
				resources[sourceID] = res
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
	}
	return nil
}

// ReadResourcesByIDs loads only the requested resources (batched WHERE id IN)
// with their outgoing connections hydrated. The structs match what ReadDb would
// produce for the same IDs. Missing IDs are simply absent from the result.
func ReadResourcesByIDs(dbPath string, ids []string) (map[string]domain.Resource, error) {
	out := make(map[string]domain.Resource)
	if len(ids) == 0 {
		return out, nil
	}
	err := withSQLiteRead(dbPath, func(db *sql.DB) error {
		hasLang, err := resourceLanguageColumnExists(db)
		if err != nil {
			return err
		}
		defaultLang, err := readInfoValue(db, "language")
		if err != nil {
			return err
		}
		cols := resourceSelectColumns(hasLang)
		for _, chunk := range chunkStrings(ids, sqliteMaxVariables) {
			args := make([]interface{}, len(chunk))
			for i, id := range chunk {
				args[i] = id
			}
			q := "SELECT " + cols + " FROM resources WHERE id IN (" + placeholders(len(chunk)) + ")"
			rows, err := db.Query(q, args...)
			if err != nil {
				return err
			}
			m, err := scanResourceRows(rows, hasLang, defaultLang)
			rows.Close()
			if err != nil {
				return err
			}
			for id, res := range m {
				out[id] = res
			}
		}
		return hydrateConnections(db, out)
	})
	return out, err
}

// ReadResourcesByKind loads every resource of the given kind (via
// idx_resources_kind) with outgoing connections hydrated.
func ReadResourcesByKind(dbPath string, kind domain.ResourceKind) (map[string]domain.Resource, error) {
	out := make(map[string]domain.Resource)
	err := withSQLiteRead(dbPath, func(db *sql.DB) error {
		hasLang, err := resourceLanguageColumnExists(db)
		if err != nil {
			return err
		}
		defaultLang, err := readInfoValue(db, "language")
		if err != nil {
			return err
		}
		cols := resourceSelectColumns(hasLang)
		rows, err := db.Query("SELECT "+cols+" FROM resources WHERE kind = ?", string(kind))
		if err != nil {
			return err
		}
		m, err := scanResourceRows(rows, hasLang, defaultLang)
		rows.Close()
		if err != nil {
			return err
		}
		for id, res := range m {
			out[id] = res
		}
		return hydrateConnections(db, out)
	})
	return out, err
}

// ReadResourcesByFile loads every resource whose loc_path equals locPath (via
// idx_resources_loc_path) with outgoing connections hydrated.
// NOTE: the file-node resource itself is stored with loc_path == "" (only its
// members carry a loc_path), so the owning file node is NOT returned by this
// helper. Callers needing the file node must fetch it by ID separately.
func ReadResourcesByFile(dbPath string, locPath string) (map[string]domain.Resource, error) {
	out := make(map[string]domain.Resource)
	err := withSQLiteRead(dbPath, func(db *sql.DB) error {
		hasLang, err := resourceLanguageColumnExists(db)
		if err != nil {
			return err
		}
		defaultLang, err := readInfoValue(db, "language")
		if err != nil {
			return err
		}
		cols := resourceSelectColumns(hasLang)
		rows, err := db.Query("SELECT "+cols+" FROM resources WHERE loc_path = ?", locPath)
		if err != nil {
			return err
		}
		m, err := scanResourceRows(rows, hasLang, defaultLang)
		rows.Close()
		if err != nil {
			return err
		}
		for id, res := range m {
			out[id] = res
		}
		return hydrateConnections(db, out)
	})
	return out, err
}

// ReadReverseConnections finds the sources pointing at each target (via
// idx_conn_target). When connType is non-empty it is filtered to that edge
// type. The result maps each target_id to its list of source_ids; targets with
// no inbound edges are simply absent.
func ReadReverseConnections(dbPath string, targetIDs []string, connType string) (map[string][]string, error) {
	out := make(map[string][]string)
	if len(targetIDs) == 0 {
		return out, nil
	}
	err := withSQLiteRead(dbPath, func(db *sql.DB) error {
		// Reserve one bind slot for connType when present.
		for _, chunk := range chunkStrings(targetIDs, sqliteMaxVariables-1) {
			args := make([]interface{}, 0, len(chunk)+1)
			for _, t := range chunk {
				args = append(args, t)
			}
			q := "SELECT source_id, target_id FROM connections WHERE target_id IN (" + placeholders(len(chunk)) + ")"
			if connType != "" {
				q += " AND conn_type = ?"
				args = append(args, connType)
			}
			rows, err := db.Query(q, args...)
			if err != nil {
				return err
			}
			for rows.Next() {
				var sourceID, targetID string
				if err := rows.Scan(&sourceID, &targetID); err != nil {
					rows.Close()
					return err
				}
				out[targetID] = append(out[targetID], sourceID)
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return err
			}
			rows.Close()
		}
		return nil
	})
	return out, err
}

// ReadAllWarnings loads the entire warnings table into a map keyed by warning
// ID. The table is small (warnings shift globally per update), so the partial
// path loads it wholesale, lets the scanner's add/clear logic run, and rewrites
// it from the returned map.
func ReadAllWarnings(dbPath string) (map[string]domain.TopologyWarning, error) {
	out := make(map[string]domain.TopologyWarning)
	err := withSQLiteRead(dbPath, func(db *sql.DB) error {
		rows, err := db.Query("SELECT id, source_id, kind, target_id, message, baseline FROM warnings")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, sourceID, kind, targetID, message, baseline string
			if err := rows.Scan(&id, &sourceID, &kind, &targetID, &message, &baseline); err != nil {
				return err
			}
			out[id] = domain.TopologyWarning{
				ID:       id,
				SourceID: sourceID,
				Kind:     domain.WarningKind(kind),
				TargetID: targetID,
				Message:  message,
				Baseline: baseline,
			}
		}
		return rows.Err()
	})
	return out, err
}

// WriteDelta applies a scoped change in a single transaction: it removes the
// resources named in deletes (and their outgoing connection rows), then upserts
// the given resources, replacing each upserted resource's outgoing connections
// (a per-source DELETE + re-insert drops stale edges). It does not touch info,
// warnings, or bugs. Incoming connection rows targeting deleted nodes are left
// in place (they drive missing-node warnings), mirroring WriteDb's behavior of
// re-deriving connections from the in-memory graph.
func WriteDelta(dbPath string, upserts []domain.Resource, deletes []string) error {
	return withSQLiteWrite(dbPath, func(db *sql.DB) error {
		if err := createSchema(db); err != nil {
			return err
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()

		// Deletes: remove resource rows and their outgoing connection rows.
		if len(deletes) > 0 {
			for _, chunk := range chunkStrings(deletes, sqliteMaxVariables) {
				args := make([]interface{}, len(chunk))
				for i, id := range chunk {
					args[i] = id
				}
				ph := placeholders(len(chunk))
				if _, err := tx.Exec("DELETE FROM connections WHERE source_id IN ("+ph+")", args...); err != nil {
					return err
				}
				if _, err := tx.Exec("DELETE FROM resources WHERE id IN ("+ph+")", args...); err != nil {
					return err
				}
			}
		}

		// Upserts: replace resource rows and re-derive their outgoing connections.
		if len(upserts) > 0 {
			resStmt, err := tx.Prepare("INSERT OR REPLACE INTO resources (id, kind, name, language, description, properties_json, starts_at, ends_at, loc_path) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)")
			if err != nil {
				return err
			}
			defer resStmt.Close()
			connStmt, err := tx.Prepare("INSERT OR REPLACE INTO connections VALUES (?, ?, ?)")
			if err != nil {
				return err
			}
			defer connStmt.Close()

			for _, res := range upserts {
				startsAt, endsAt := 0, 0
				locPath := ""
				if res.Location.Path != "" {
					startsAt = res.Location.StartsAt
					endsAt = res.Location.EndsAt
					locPath = res.Location.Path
				}
				if _, err := resStmt.Exec(res.ID, string(res.Kind), res.Name, res.Language,
					domain.DescriptionForStorage(res.Kind, res.Description), toJSON(res.Properties), startsAt, endsAt, locPath); err != nil {
					return err
				}
				// Clear stale outgoing edges for this source before re-inserting.
				if _, err := tx.Exec("DELETE FROM connections WHERE source_id = ?", res.ID); err != nil {
					return err
				}
				for kind, targets := range res.Connections {
					for _, target := range targets {
						if _, err := connStmt.Exec(res.ID, kind, target); err != nil {
							return err
						}
					}
				}
			}
		}

		return tx.Commit()
	})
}
