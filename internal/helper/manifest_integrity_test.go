package helper

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// TestSyncManifestRecordsThePreReadStamp pins ST-1.
//
// The manifest recorded each file's mtime as it was at the END of the scan, so a file edited
// while the scan ran was stamped current although the parse had read the older bytes: the edit
// was never indexed, and check-updates vouched for it. The stamp handed in is the one taken
// before the read, and a later edit must still read as modified.
func TestSyncManifestRecordsThePreReadStamp(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, ".aracne", "topology.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "a.go")
	if err := os.WriteFile(src, []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stamps := SnapshotManifest([]string{src})

	// The edit that races the scan: after the parse read the file, before the scan finished.
	if err := os.WriteFile(src, []byte("package p\n\nfunc Late() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(src, later, later); err != nil {
		t.Fatal(err)
	}

	topo := &domain.Topology{
		Root:     dir,
		Language: "go",
		Resources: map[string]domain.Resource{
			src: {ID: src, Kind: domain.ResourceFile, Name: "a.go", Language: "go"},
		},
	}
	SyncManifest(topo, dbPath, stamps)

	_, modified, _, err := DiffScanFiles(dir, "go", ManifestPath(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	if len(modified) != 1 || modified[0] != src {
		t.Fatalf("a file edited during the scan must stay stale, got modified=%v", modified)
	}
}

// TestSyncManifestStampsOnlyWhatWasParsed pins the incremental half of ST-1: the incremental
// path handed SyncManifest the whole graph, which restamped every file in it -- including ones
// this scan never opened -- with the mtime on disk.
func TestSyncManifestStampsOnlyWhatWasParsed(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, ".aracne", "topology.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	parsed := filepath.Join(dir, "parsed.go")
	untouched := filepath.Join(dir, "untouched.go")
	for _, p := range []string{parsed, untouched} {
		if err := os.WriteFile(p, []byte("package p\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	topo := &domain.Topology{
		Root:     dir,
		Language: "go",
		Resources: map[string]domain.Resource{
			parsed:    {ID: parsed, Kind: domain.ResourceFile, Name: "parsed.go", Language: "go"},
			untouched: {ID: untouched, Kind: domain.ResourceFile, Name: "untouched.go", Language: "go"},
		},
	}
	SyncManifest(topo, dbPath, SnapshotManifest([]string{parsed}))

	manifest := ReadManifest(ManifestPath(dbPath))
	if _, ok := manifest[parsed]; !ok {
		t.Fatalf("the parsed file must be stamped; manifest = %v", manifest)
	}
	if _, ok := manifest[untouched]; ok {
		t.Fatalf("a file this scan never parsed must not be declared current; manifest = %v", manifest)
	}
	added, _, _, err := DiffScanFiles(dir, "go", ManifestPath(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 1 || added[0] != untouched {
		t.Fatalf("the unparsed file must be picked up by the next scan, got added=%v", added)
	}
}

// TestUnreadableDirectoryDoesNotStopTheWalk pins ST-2.
//
// The walk callback returned the error, which aborted the walk that feeds DiffScanFiles for
// every language: one directory owned by another user stopped every incremental scan. It is
// skipped now, and a file the walk could not see because of it is not reported deleted.
func TestUnreadableDirectoryDoesNotStopTheWalk(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 directory anyway")
	}
	dir := t.TempDir()
	dbPath := filepath.Join(dir, ".aracne", "topology.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(dir, "pgdata")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	hidden := filepath.Join(locked, "gen.go")
	indexed := filepath.Join(dir, "a.go")
	for _, p := range []string{hidden, indexed} {
		if err := os.WriteFile(p, []byte("package p\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	topo := &domain.Topology{
		Root:     dir,
		Language: "go",
		Resources: map[string]domain.Resource{
			hidden:  {ID: hidden, Kind: domain.ResourceFile, Name: "gen.go", Language: "go"},
			indexed: {ID: indexed, Kind: domain.ResourceFile, Name: "a.go", Language: "go"},
		},
	}
	SyncManifest(topo, dbPath, SnapshotManifest([]string{hidden, indexed}))

	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	added := filepath.Join(dir, "b.go")
	if err := os.WriteFile(added, []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := CollectSourceFiles(dir, "go")
	if err != nil {
		t.Fatalf("an unreadable directory must be skipped, not abort the walk: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected the two readable files, got %v", files)
	}
	gotAdded, _, deleted, err := DiffScanFiles(dir, "go", ManifestPath(dbPath))
	if err != nil {
		t.Fatalf("DiffScanFiles: %v", err)
	}
	if len(gotAdded) != 1 || gotAdded[0] != added {
		t.Fatalf("the new file must still be found, got added=%v", gotAdded)
	}
	if len(deleted) != 0 {
		t.Fatalf("a file under an unreadable directory is unverified, not deleted; got deleted=%v", deleted)
	}
}
