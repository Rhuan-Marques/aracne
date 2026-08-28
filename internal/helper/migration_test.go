package helper

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// Seeds a database the way an older aracne would have: a resource stored under
// the legacy "type" kind with user_version still at 0, bypassing the public
// wrappers so the migration does not run during seeding.
func seedLegacyTypeDB(t *testing.T, path string) {
	t.Helper()
	db, err := openSQLite(path, true)
	if err != nil {
		t.Fatalf("openSQLite: %v", err)
	}
	defer db.Close()
	if err := createSchema(db); err != nil {
		t.Fatalf("createSchema: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO resources (id, kind, name) VALUES ('p.S', 'type', 'S')`); err != nil {
		t.Fatalf("seed insert: %v", err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 0`); err != nil {
		t.Fatalf("reset user_version: %v", err)
	}
}

func TestApplyMigrationsRenamesTypeToStruct(t *testing.T) {
	path := filepath.Join(t.TempDir(), "topology.db")
	seedLegacyTypeDB(t, path)

	// Opening through the public read path must migrate the legacy "type" row to
	// "struct" and bump user_version.
	var kind string
	var version int
	if err := withSQLiteRead(path, func(db *sql.DB) error {
		if err := db.QueryRow(`SELECT kind FROM resources WHERE id = 'p.S'`).Scan(&kind); err != nil {
			return err
		}
		return db.QueryRow(`PRAGMA user_version`).Scan(&version)
	}); err != nil {
		t.Fatalf("read: %v", err)
	}
	if kind != "struct" {
		t.Fatalf("kind = %q, want %q", kind, "struct")
	}
	// Assert the chain reached the LATEST version rather than a hardcoded one, so adding
	// a migration does not require editing this test.
	if version != latestSchemaVersion {
		t.Fatalf("user_version = %d, want %d", version, latestSchemaVersion)
	}
}

func TestApplyMigrationsIsVersionGated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "topology.db")
	seedLegacyTypeDB(t, path)

	db, err := openSQLite(path, true)
	if err != nil {
		t.Fatalf("openSQLite: %v", err)
	}
	defer db.Close()

	// First run migrates and gates future runs.
	if err := applyMigrations(db); err != nil {
		t.Fatalf("applyMigrations (1): %v", err)
	}
	// A row inserted under the legacy kind AFTER the gate is set must be left
	// untouched: the migration is a one-time, version-gated step, not a trigger.
	if _, err := db.Exec(`INSERT INTO resources (id, kind, name) VALUES ('p.T', 'type', 'T')`); err != nil {
		t.Fatalf("post-migration insert: %v", err)
	}
	if err := applyMigrations(db); err != nil {
		t.Fatalf("applyMigrations (2): %v", err)
	}
	var kind string
	if err := db.QueryRow(`SELECT kind FROM resources WHERE id = 'p.T'`).Scan(&kind); err != nil {
		t.Fatalf("query: %v", err)
	}
	if kind != "type" {
		t.Fatalf("kind = %q, want %q (version gate should skip the second run)", kind, "type")
	}
}
