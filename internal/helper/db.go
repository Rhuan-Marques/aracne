package helper

import (
	"database/sql"
	"encoding/json"

	_ "modernc.org/sqlite"
	"llm-topology/internal/topology/domain"
)

func WriteDb(topo *domain.Topology, path string) error {
	db, err := sql.Open("sqlite", path+"?cache=shared&_journal_mode=WAL")
	if err != nil {
		return err
	}
	defer db.Close()

	if err := createSchema(db); err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	tx.Exec("DELETE FROM info")
	tx.Exec("DELETE FROM resources")
	tx.Exec("DELETE FROM connections")

	if _, err := tx.Exec("INSERT INTO info VALUES ('root', ?)", topo.Root); err != nil {
		return err
	}
	if _, err := tx.Exec("INSERT INTO info VALUES ('language', ?)", topo.Language); err != nil {
		return err
	}

	resStmt, err := tx.Prepare("INSERT INTO resources VALUES (?, ?, ?, ?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}
	defer resStmt.Close()

	connStmt, err := tx.Prepare("INSERT INTO connections VALUES (?, ?, ?)")
	if err != nil {
		return err
	}
	defer connStmt.Close()

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
		if _, err := resStmt.Exec(id, string(res.Kind), res.Name, res.Description, propsJSON, startsAt, endsAt, locPath); err != nil {
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

	for path, msg := range topo.Errors {
		if _, err := tx.Exec("INSERT INTO info VALUES (?, ?)", "error:"+path, msg); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func createSchema(db *sql.DB) error {
	ddl := `
	PRAGMA journal_mode=WAL;
	PRAGMA synchronous=OFF;

	CREATE TABLE IF NOT EXISTS info (key TEXT PRIMARY KEY, value TEXT);

	CREATE TABLE IF NOT EXISTS resources (
		id TEXT PRIMARY KEY,
		kind TEXT NOT NULL,
		name TEXT NOT NULL,
		description TEXT,
		properties_json TEXT,
		starts_at INT NOT NULL DEFAULT 0,
		ends_at INT NOT NULL DEFAULT 0,
		loc_path TEXT DEFAULT ''
	);

	CREATE TABLE IF NOT EXISTS connections (
		source_id TEXT NOT NULL,
		conn_type TEXT NOT NULL,
		target_id TEXT NOT NULL,
		PRIMARY KEY(source_id, conn_type, target_id)
	);
	CREATE INDEX IF NOT EXISTS idx_conn_target ON connections(target_id);
	`
	_, err := db.Exec(ddl)
	return err
}

func ReadDb(path string) (*domain.Topology, error) {
	db, err := sql.Open("sqlite", path+"?cache=shared&_journal_mode=WAL")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	topo := &domain.Topology{
		Resources: make(map[string]domain.Resource),
		Errors:    make(map[string]string),
	}

	rows, err := db.Query("SELECT key, value FROM info")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, val string
		err = rows.Scan(&key, &val)
		if err != nil {
			return nil, err
		}
		if key == "root" {
			topo.Root = val
		} else if key == "language" {
			topo.Language = val
		} else if len(key) > 6 && key[:6] == "error:" {
			topo.Errors[key[6:]] = val
		}
	}
	rows.Close()

	resRows, err := db.Query("SELECT id, kind, name, description, properties_json, starts_at, ends_at, loc_path FROM resources")
	if err != nil {
		return nil, err
	}
	defer resRows.Close()
	for resRows.Next() {
		var id, kind, name, desc, propsJSON, locPath string
		var startsAt, endsAt int
		err = resRows.Scan(&id, &kind, &name, &desc, &propsJSON, &startsAt, &endsAt, &locPath)
		if err != nil {
			return nil, err
		}
		res := domain.Resource{
			ID:          id,
			Kind:        domain.ResourceKind(kind),
			Name:        name,
			Description: desc,
			Location: domain.Location{
				StartsAt: startsAt,
				EndsAt:   endsAt,
				Path:     locPath,
			},
			Properties:  fromJSONMap(propsJSON),
			Connections: make(map[string][]string),
		}
		topo.Resources[id] = res
	}
	resRows.Close()

	connRows, err := db.Query("SELECT source_id, conn_type, target_id FROM connections ORDER BY source_id, conn_type")
	if err != nil {
		return nil, err
	}
	defer connRows.Close()
	for connRows.Next() {
		var sourceID, connType, targetID string
		err = connRows.Scan(&sourceID, &connType, &targetID)
		if err != nil {
			return nil, err
		}
		if res, ok := topo.Resources[sourceID]; ok {
			res.Connections[connType] = append(res.Connections[connType], targetID)
			topo.Resources[sourceID] = res
		}
	}

	return topo, nil
}

func UpdateDescription(dbPath string, kind domain.ResourceKind, id string, description string) error {
	db, err := sql.Open("sqlite", dbPath+"?cache=shared&_journal_mode=WAL")
	if err != nil {
		return err
	}
	defer db.Close()

	_, err = db.Exec("UPDATE resources SET description = ? WHERE id = ?", description, id)
	return err
}

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

func fromJSONMap(s string) map[string]any {
	var v map[string]any
	if s != "" {
		json.Unmarshal([]byte(s), &v)
	}
	if v == nil {
		v = make(map[string]any)
	}
	return v
}
