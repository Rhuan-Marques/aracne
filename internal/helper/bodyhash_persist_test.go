package helper

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// seedPreBodyHashDB writes a resources table as a build before the body-hash columns existed
// would have left it, stamped at the schema version of that build.
func seedPreBodyHashDB(t *testing.T, path string) {
	t.Helper()
	db, err := openSQLite(path, true)
	if err != nil {
		t.Fatalf("openSQLite: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		CREATE TABLE resources (
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
		INSERT INTO resources VALUES ('p.F', 'function', 'F', 'go', 'Does a thing.', '{}', 1, 3, '/x/p.go');
		PRAGMA user_version = 3;
	`); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

// TestMigrationV4AddsBodyHashColumns pins the columns onto the READ path, for the same reason
// the baseline column is pinned there: createSchema runs only when something writes, so a
// database upgraded and then merely read would have failed on "no such column".
//
// It also pins the NO-BACKFILL decision. An existing row keeps empty hashes, which reads as
// "not fingerprinted yet" and only makes a match tier unavailable -- never a wrong match.
// Backfilling would mean a migration reading the whole working tree.
func TestMigrationV4AddsBodyHashColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "topology.db")
	seedPreBodyHashDB(t, path)

	var exact, norm string
	var version int
	if err := withSQLiteRead(path, func(db *sql.DB) error {
		if err := db.QueryRow(
			`SELECT COALESCE(exact_hash,''), COALESCE(norm_hash,'') FROM resources WHERE id = 'p.F'`,
		).Scan(&exact, &norm); err != nil {
			return err
		}
		return db.QueryRow(`PRAGMA user_version`).Scan(&version)
	}); err != nil {
		t.Fatalf("read after migration: %v", err)
	}
	if exact != "" || norm != "" {
		t.Errorf("pre-existing row was backfilled (exact=%q norm=%q), want both empty", exact, norm)
	}
	if version != latestSchemaVersion {
		t.Errorf("user_version = %d, want %d", version, latestSchemaVersion)
	}
}

// TestMigrationV4SurvivesADatabaseWithNoResourcesTable. ALTER TABLE on a missing table is an
// error, and an error in applyMigrations aborts it with user_version left behind -- so the
// next run repeats the same failure forever, and every later migration is stranded with it.
func TestMigrationV4SurvivesADatabaseWithNoResourcesTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "topology.db")
	seedPreBaselineWarningsDB(t, path) // creates `warnings` and nothing else

	var version int
	if err := withSQLiteRead(path, func(db *sql.DB) error {
		return db.QueryRow(`PRAGMA user_version`).Scan(&version)
	}); err != nil {
		t.Fatalf("read after migration: %v", err)
	}
	if version != latestSchemaVersion {
		t.Fatalf("user_version = %d, want %d -- the migration aborted partway", version, latestSchemaVersion)
	}
}

// bodyHashFixture is a body comfortably over the size floor, plus the same body reformatted
// and recommented.
const (
	hashFixtureA = "func Total(xs []int) int {\n\tsum := 0\n\tfor _, x := range xs {\n\t\tsum += x\n\t}\n\treturn sum\n}\n"
	hashFixtureB = "func Total(xs []int) int {\n\t// a fresh comment\n\tsum := 0\n\n\tfor _, x := range xs {\n\t\tsum += x\n\t}\n\treturn sum\n}\n"
)

func hashTopo(t *testing.T, dir, body string) *domain.Topology {
	t.Helper()
	path := filepath.Join(dir, "p.go")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	n := len(strings.Split(strings.TrimRight(body, "\n"), "\n"))
	return &domain.Topology{
		Root:      dir,
		Language:  "go",
		Languages: []string{"go"},
		Resources: map[string]domain.Resource{
			"p.Total": {
				ID: "p.Total", Kind: domain.ResourceFunction, Name: "Total", Language: "go",
				Location: domain.Location{Path: path, StartsAt: 1, EndsAt: n},
			},
		},
		Warnings: map[string]domain.TopologyWarning{},
		Errors:   map[string]string{},
	}
}

// TestBodyHashesRoundTripThroughTheDatabase is the property the whole feature rests on: a hash
// taken while the file existed is still there after the file is gone.
func TestBodyHashesRoundTripThroughTheDatabase(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "topology.db")
	topo := hashTopo(t, dir, hashFixtureA)

	StampBodyHashes(topo, nil)
	stamped := topo.Resources["p.Total"]
	if stamped.ExactHash == "" || stamped.NormHash == "" {
		t.Fatalf("fixture was not fingerprinted (exact=%q norm=%q)", stamped.ExactHash, stamped.NormHash)
	}
	if err := WriteDb(topo, dbPath); err != nil {
		t.Fatalf("WriteDb: %v", err)
	}

	// The file goes away, exactly as it does after a `git mv`.
	if err := os.Remove(filepath.Join(dir, "p.go")); err != nil {
		t.Fatal(err)
	}

	back, err := ReadDb(dbPath)
	if err != nil {
		t.Fatalf("ReadDb: %v", err)
	}
	got := back.Resources["p.Total"]
	if got.ExactHash != stamped.ExactHash || got.NormHash != stamped.NormHash {
		t.Errorf("hashes did not survive the round trip:\n got  exact=%q norm=%q\n want exact=%q norm=%q",
			got.ExactHash, got.NormHash, stamped.ExactHash, stamped.NormHash)
	}
}

// TestStampedHashDrivesTheDiff. The hashes are part of the stored row, so they have to be part
// of the fingerprint that decides whether the row is rewritten -- otherwise an edit that
// changed only the body would leave the previous body's hash in place, which is precisely the
// value a move matcher must not read.
func TestStampedHashDrivesTheDiff(t *testing.T) {
	dir := t.TempDir()
	topo := hashTopo(t, dir, hashFixtureA)
	StampBodyHashes(topo, nil)
	before := ResourceSignatures(topo.Resources)

	// Same span, same signature, same description: only the body text moves.
	changed := hashTopo(t, dir, "func Total(xs []int) int {\n\tsum := 1\n\tfor _, x := range xs {\n\t\tsum *= x\n\t}\n\treturn sum\n}\n")
	StampBodyHashes(changed, nil)

	upserts, deletes := DiffResources(before, changed.Resources)
	if len(deletes) != 0 {
		t.Fatalf("unexpected deletes: %v", deletes)
	}
	if len(upserts) != 1 {
		t.Fatalf("a changed body produced %d upserts, want 1 -- the stored hash would be stale", len(upserts))
	}
}

// TestUnchangedScanRewritesNothing. Adding fields to resourceSignature costs one full re-upsert
// on the first scan after the upgrade, and that is acceptable. What is NOT acceptable is it
// happening on every scan afterwards -- write amplification that scales with the repository
// rather than with the change, which is the failure resourceSignature's own comment records.
func TestUnchangedScanRewritesNothing(t *testing.T) {
	dir := t.TempDir()
	topo := hashTopo(t, dir, hashFixtureA)
	StampBodyHashes(topo, nil)
	first := ResourceSignatures(topo.Resources)

	// A second stamp over untouched source must be a no-op.
	StampBodyHashes(topo, nil)
	upserts, deletes := DiffResources(first, topo.Resources)
	if len(upserts) != 0 || len(deletes) != 0 {
		t.Errorf("a re-scan with no source change produced %d upserts and %d deletes, want 0 and 0",
			len(upserts), len(deletes))
	}
}

// TestReformattingKeepsNormHash, at the level that matters: a resource re-stamped after its
// file was recommented keeps the NormHash a tombstone would be matched on, while ExactHash
// moves. That is why both are stored.
func TestReformattingKeepsNormHash(t *testing.T) {
	dir := t.TempDir()
	a := hashTopo(t, dir, hashFixtureA)
	StampBodyHashes(a, nil)
	b := hashTopo(t, dir, hashFixtureB)
	StampBodyHashes(b, nil)

	ra, rb := a.Resources["p.Total"], b.Resources["p.Total"]
	if ra.NormHash != rb.NormHash {
		t.Error("a comment edit moved NormHash; a recommented move would stop matching")
	}
	if ra.ExactHash == rb.ExactHash {
		t.Error("a comment edit left ExactHash unmoved; then there is no reason to keep two hashes")
	}
}

// TestHalfMigratedDatabaseSelfHeals. applyMigrations advances user_version step by step, so a
// step that fails partway leaves the version at the last one that succeeded -- and every run
// after that skips the unfinished step and fails on "no such column" with nothing able to
// repair it. The additive guards therefore run ungated, before the ladder.
func TestHalfMigratedDatabaseSelfHeals(t *testing.T) {
	path := filepath.Join(t.TempDir(), "topology.db")
	db, err := openSQLite(path, true)
	if err != nil {
		t.Fatalf("openSQLite: %v", err)
	}
	// Stamped as fully migrated, but missing the columns that version claims to have added.
	if _, err := db.Exec(`
		CREATE TABLE resources (
			id TEXT PRIMARY KEY, kind TEXT NOT NULL, name TEXT NOT NULL,
			language TEXT DEFAULT '', description TEXT, properties_json TEXT,
			starts_at INT NOT NULL DEFAULT 0, ends_at INT NOT NULL DEFAULT 0,
			loc_path TEXT DEFAULT ''
		);
		INSERT INTO resources VALUES ('p.F','function','F','go','Does a thing.','{}',1,3,'/x/p.go');
		CREATE TABLE info (key TEXT PRIMARY KEY, value TEXT);
		CREATE TABLE connections (source_id TEXT NOT NULL, conn_type TEXT NOT NULL,
			target_id TEXT NOT NULL, PRIMARY KEY(source_id, conn_type, target_id));
		CREATE TABLE warnings (id TEXT PRIMARY KEY, source_id TEXT NOT NULL, kind TEXT NOT NULL,
			target_id TEXT DEFAULT '', message TEXT NOT NULL, baseline TEXT DEFAULT '');
		PRAGMA user_version = 4;
	`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	db.Close()

	topo, err := ReadDb(path)
	if err != nil {
		t.Fatalf("a half-migrated database stayed unreadable: %v", err)
	}
	if _, ok := topo.Resources["p.F"]; !ok {
		t.Error("resource missing after the repair")
	}
}
