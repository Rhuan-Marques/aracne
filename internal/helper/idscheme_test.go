package helper

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"aracne/internal/topology/domain"
)

// The id-scheme stamp and the alias table are what let a resource-ID format change happen
// without breaking every database and every ID an agent already knows. These tests pin the
// two guarantees that matter: an OLD database is recognised as old (not silently treated as
// current), and an old ID keeps resolving after a remap.

// seedSchemeDB creates a database the way a pre-v2 aracne would have: real resources, no
// id_scheme stamp, user_version reset so the migration has not run.
func seedSchemeDB(t *testing.T, path string, withResources bool) {
	t.Helper()
	db, err := openSQLite(path, true)
	if err != nil {
		t.Fatalf("openSQLite: %v", err)
	}
	defer db.Close()
	if err := createSchema(db); err != nil {
		t.Fatalf("createSchema: %v", err)
	}
	if withResources {
		// Deliberately NULL description/properties_json: the columns are nullable, and
		// ReadDb must COALESCE them rather than fail with "converting NULL to string".
		if _, err := db.Exec(
			`INSERT INTO resources (id, kind, name, language)
			 VALUES ('proj/src/a.Foo', 'struct', 'Foo', 'python')`,
		); err != nil {
			t.Fatalf("seed insert: %v", err)
		}
	}
	if _, err := db.Exec(`DELETE FROM info WHERE key = 'id_scheme'`); err != nil {
		t.Fatalf("clear stamp: %v", err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 0`); err != nil {
		t.Fatalf("reset user_version: %v", err)
	}
}

func userVersion(t *testing.T, path string) int {
	t.Helper()
	var v int
	db, err := openSQLite(path, false)
	if err != nil {
		t.Fatalf("openSQLite: %v", err)
	}
	defer db.Close()
	if err := db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		t.Fatalf("user_version: %v", err)
	}
	return v
}

func TestMigrationV2StampsExistingDatabaseAsLegacyScheme(t *testing.T) {
	// An existing database predates the stamp, so it MUST be reported as scheme 1. Getting
	// this wrong is the dangerous case: a legacy database read as "current" would skip the
	// remap and leave every ID in the old format with no aliases.
	path := filepath.Join(t.TempDir(), "topology.db")
	seedSchemeDB(t, path, true)

	if _, _, err := ReadIDScheme(path); err != nil { // triggers ensureSQLiteMigrated
		t.Fatalf("ReadIDScheme: %v", err)
	}
	if got := userVersion(t, path); got != 2 {
		t.Fatalf("want user_version 2 after migration, got %d", got)
	}
	scheme, stamped, err := ReadIDScheme(path)
	if err != nil {
		t.Fatalf("ReadIDScheme: %v", err)
	}
	if scheme != 1 || !stamped {
		t.Fatalf("want scheme 1 stamped, got scheme=%d stamped=%v", scheme, stamped)
	}
}

func TestMigrationV2LeavesEmptyDatabaseUnstamped(t *testing.T) {
	// A fresh, empty database has no IDs to be stale, so stamping it "1" would force a
	// pointless remap on the very first scan.
	path := filepath.Join(t.TempDir(), "topology.db")
	seedSchemeDB(t, path, false)

	scheme, stamped, err := ReadIDScheme(path)
	if err != nil {
		t.Fatalf("ReadIDScheme: %v", err)
	}
	if stamped {
		t.Fatal("empty database should not be stamped as legacy")
	}
	if scheme != IDSchemeVersion {
		t.Fatalf("want current scheme %d for an empty db, got %d", IDSchemeVersion, scheme)
	}
}

func TestMigrationV2DoesNotRewriteIDs(t *testing.T) {
	// Recomputing IDs needs the scanners and the source tree; the schema step must only
	// record which scheme is present.
	path := filepath.Join(t.TempDir(), "topology.db")
	seedSchemeDB(t, path, true)

	topo, err := ReadDb(path)
	if err != nil {
		t.Fatalf("ReadDb: %v", err)
	}
	if _, ok := topo.Resources["proj/src/a.Foo"]; !ok {
		ids := make([]string, 0, len(topo.Resources))
		for id := range topo.Resources {
			ids = append(ids, id)
		}
		t.Fatalf("migration altered a resource ID; got %v", ids)
	}
}

func TestWriteAndReadIDScheme(t *testing.T) {
	path := filepath.Join(t.TempDir(), "topology.db")
	seedSchemeDB(t, path, true)
	if err := WriteIDScheme(path, 7); err != nil {
		t.Fatalf("WriteIDScheme: %v", err)
	}
	scheme, stamped, err := ReadIDScheme(path)
	if err != nil {
		t.Fatalf("ReadIDScheme: %v", err)
	}
	if scheme != 7 || !stamped {
		t.Fatalf("want scheme 7 stamped, got %d / %v", scheme, stamped)
	}
	// Overwriting must replace, not duplicate or append.
	if err := WriteIDScheme(path, 8); err != nil {
		t.Fatalf("WriteIDScheme(8): %v", err)
	}
	if scheme, _, _ := ReadIDScheme(path); scheme != 8 {
		t.Fatalf("want scheme 8 after rewrite, got %d", scheme)
	}
}

func TestResourceAliasResolvesOldIDs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "topology.db")
	seedSchemeDB(t, path, true)

	n, err := WriteResourceAliases(path, map[string]string{
		"proj/src/a.Foo": "a.Foo",
		"":               "ignored",
		"self":           "self", // a self-alias is meaningless and must be skipped
	})
	if err != nil {
		t.Fatalf("WriteResourceAliases: %v", err)
	}
	if n != 1 {
		t.Fatalf("want 1 alias written (empty and self-aliases skipped), got %d", n)
	}
	got, ok := ResolveAlias(path, "proj/src/a.Foo")
	if !ok || got != "a.Foo" {
		t.Fatalf("want a.Foo, got %q (ok=%v)", got, ok)
	}
	if _, ok := ResolveAlias(path, "a.Foo"); ok {
		t.Fatal("a current ID must not resolve through the alias table")
	}
	if _, ok := ResolveAlias(path, ""); ok {
		t.Fatal("empty ID must not resolve")
	}
}

func TestResourceAliasFollowsChainsAcrossTwoMigrations(t *testing.T) {
	// A database migrated twice must still resolve IDs from before the first migration,
	// otherwise the oldest IDs quietly stop working on the second scheme change.
	path := filepath.Join(t.TempDir(), "topology.db")
	seedSchemeDB(t, path, true)
	if _, err := WriteResourceAliases(path, map[string]string{"v1.Foo": "v2.Foo"}); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteResourceAliases(path, map[string]string{"v2.Foo": "v3.Foo"}); err != nil {
		t.Fatal(err)
	}
	got, ok := ResolveAlias(path, "v1.Foo")
	if !ok || got != "v3.Foo" {
		t.Fatalf("want v3.Foo through the chain, got %q (ok=%v)", got, ok)
	}
}

func TestResourceAliasCycleTerminates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "topology.db")
	seedSchemeDB(t, path, true)
	if _, err := WriteResourceAliases(path, map[string]string{"a": "b", "b": "a"}); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		ResolveAlias(path, "a")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("ResolveAlias did not terminate on a cyclic alias chain")
	}
}

func TestRemapBugNodesFollowsAnIDSchemeChange(t *testing.T) {
	// Bugs reference resources by ID with no foreign key, and CleanupOrphanedBugs deletes
	// any bug whose node is missing. Across a scheme change every node id is missing under
	// its old spelling, so without the remap the migrating rescan would delete every bug.
	path := filepath.Join(t.TempDir(), "topology.db")
	seedSchemeDB(t, path, true)
	db, err := openSQLite(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO bugs (id, node_id, description, state)
		 VALUES ('b1', 'proj/src/a.Foo', 'boom', 'pending')`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	n, err := RemapBugNodes(path, map[string]string{
		"proj/src/a.Foo": "src/a.Foo",
		"untouched":      "untouched", // a self-mapping must not count as a change
	})
	if err != nil {
		t.Fatalf("RemapBugNodes: %v", err)
	}
	if n != 1 {
		t.Fatalf("want 1 bug remapped, got %d", n)
	}
	var node string
	if err := withSQLiteRead(path, func(db *sql.DB) error {
		return db.QueryRow("SELECT node_id FROM bugs WHERE id = 'b1'").Scan(&node)
	}); err != nil {
		t.Fatal(err)
	}
	if node != "src/a.Foo" {
		t.Fatalf("bug node = %q, want src/a.Foo", node)
	}
}

func TestFullWriteStampsTheCurrentIDScheme(t *testing.T) {
	// WriteDb clears `info`, so without an explicit re-stamp a freshly rescanned database
	// would read as legacy and be remapped again on every scan.
	path := filepath.Join(t.TempDir(), "topology.db")
	topo := &domain.Topology{
		Root: "/x", Language: "python", Languages: []string{"python"},
		Resources: map[string]domain.Resource{
			"a.Foo": {ID: "a.Foo", Kind: domain.ResourceStruct, Name: "Foo"},
		},
	}
	if err := WriteDb(topo, path); err != nil {
		t.Fatalf("WriteDb: %v", err)
	}
	scheme, stamped, err := ReadIDScheme(path)
	if err != nil {
		t.Fatal(err)
	}
	if !stamped || scheme != IDSchemeVersion {
		t.Fatalf("want stamped scheme %d, got %d (stamped=%v)", IDSchemeVersion, scheme, stamped)
	}
}

func TestUpdateDescriptionRejectsUnknownID(t *testing.T) {
	// Previously this silently succeeded, so a generator working from stale IDs reported
	// success while storing nothing.
	path := filepath.Join(t.TempDir(), "topology.db")
	seedSchemeDB(t, path, true)
	if err := UpdateDescription(path, "", "no.such.id", "hi"); err == nil {
		t.Fatal("want an error for an unknown id, got nil")
	}
}

func TestUpdateDescriptionEnforcesKind(t *testing.T) {
	path := filepath.Join(t.TempDir(), "topology.db")
	seedSchemeDB(t, path, true)
	if err := UpdateDescription(path, "function", "proj/src/a.Foo", "hi"); err == nil {
		t.Fatal("want an error when kind does not match, got nil")
	}
	if err := UpdateDescription(path, "struct", "proj/src/a.Foo", "hi"); err != nil {
		t.Fatalf("matching kind should succeed: %v", err)
	}
}

func TestUpdateDescriptionsBatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "topology.db")
	seedSchemeDB(t, path, true)
	n, err := UpdateDescriptions(path, map[string]string{
		"proj/src/a.Foo": "described",
		"missing.id":     "ignored", // a miss is counted, not fatal
	})
	if err != nil {
		t.Fatalf("UpdateDescriptions: %v", err)
	}
	if n != 1 {
		t.Fatalf("want 1 row changed, got %d", n)
	}
	topo, err := ReadDb(path)
	if err != nil {
		t.Fatal(err)
	}
	if topo.Resources["proj/src/a.Foo"].Description != "described" {
		t.Fatal("description was not written")
	}
}
