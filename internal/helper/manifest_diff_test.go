package helper

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// writeDiffFixture writes files under a fresh root and stamps every one of them into a manifest
// beside it, exactly as a scan that just read them would. It returns the root and manifest path.
func writeDiffFixture(t *testing.T, files map[string]string) (string, string) {
	t.Helper()
	// CANONICAL, because that is what a manifest written by a real scan holds: every scan verb
	// resolves its root through CanonicalPath before it walks, and DiffScanFiles resolves the
	// root it is handed for the same reason. A fixture that stamps a manifest under an
	// unresolved temp dir -- macOS hands every t.TempDir() out under /var, a symlink to
	// /private/var -- is diffing two spellings of one tree against each other.
	dir := CanonicalPath(t.TempDir())
	var paths []string
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	dbPath := filepath.Join(dir, ".aracne", "topology.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := StampManifest(dbPath, SnapshotManifest(paths)); err != nil {
		t.Fatal(err)
	}
	return dir, ManifestPath(dbPath)
}

// TestDiffScanFiles_OlderMtimeIsModified pins ST-3: a file replaced by different bytes under an
// OLDER mtime -- `mv`/`cp -p` of an older copy, `rsync -a`, `tar`, a backup restore -- was never
// reported, because only a newer mtime counted. `check-updates` called it up to date while
// `--hard` indexed the new declarations.
func TestDiffScanFiles_OlderMtimeIsModified(t *testing.T) {
	dir, manifestPath := writeDiffFixture(t, map[string]string{
		"pkg/a.go": "package pkg\n\nfunc One() int { return 1 }\n",
		"pkg/b.go": "package pkg\n\nfunc Two() int { return 2 }\n",
	})
	a := filepath.Join(dir, "pkg", "a.go")
	if err := os.WriteFile(a, []byte("package pkg\n\nfunc One() int { return 1 }\n\nfunc Restored() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(a, old, old); err != nil {
		t.Fatal(err)
	}

	added, modified, deleted, err := DiffScanFiles(dir, "go", manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 0 || len(deleted) != 0 {
		t.Fatalf("only a.go changed; got added=%v deleted=%v", added, deleted)
	}
	// b.go is untouched and must not be reported: an equal stamp is still "up to date".
	if len(modified) != 1 || modified[0] != a {
		t.Fatalf("a file whose mtime moved BACKWARDS is modified, and nothing else is; got %v", modified)
	}
}

// TestMtimeChanged pins the one staleness rule DiffScanFiles and topology.StaleFiles share:
// any difference from the recorded stamp, in either direction, and nothing else.
func TestMtimeChanged(t *testing.T) {
	rec := time.Date(2026, 9, 1, 12, 0, 0, 123456789, time.UTC)
	if MtimeChanged(rec.In(time.FixedZone("x", 3600)), rec) {
		t.Error("the same instant in another zone is not a change")
	}
	if !MtimeChanged(rec.Add(time.Nanosecond), rec) || !MtimeChanged(rec.Add(-time.Hour), rec) {
		t.Error("a newer or an older mtime is a change")
	}
}

// TestDiffScanFiles_NowHiddenIsDeleted pins ST-4: DiffScanFiles dropped every manifest entry
// IsSourceFile now rejects, so a file that a new `scan.ignore` or hidden `paths` rule covered
// was never reported deleted and stayed in the graph -- readable and searchable -- until
// `--hard`.
func TestDiffScanFiles_NowHiddenIsDeleted(t *testing.T) {
	dir, manifestPath := writeDiffFixture(t, map[string]string{
		"pkg/a.go":   "package pkg\n",
		"gen/g.go":   "package gen\n",
		"gen/h.py":   "x = 1\n",
		"hid/h.go":   "package hid\n",
		"hid/v/k.go": "package v\n",
	})
	t.Cleanup(func() {
		domain.SetActiveIgnore(nil)
		domain.SetActivePathVisibility(nil)
	})
	domain.SetActiveIgnore(domain.BuildIgnoreMatcher(dir, []string{"gen/"}))
	domain.SetActivePathVisibility(domain.BuildPathVisibility(dir, []domain.PathRule{
		{Path: "hid", Hidden: true},
		{Path: "hid/v", Hidden: false},
	}))

	added, modified, deleted, err := DiffScanFiles(dir, "go", manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(deleted)
	want := []string{filepath.Join(dir, "gen", "g.go"), filepath.Join(dir, "hid", "h.go")}
	if len(added) != 0 || len(modified) != 0 || len(deleted) != 2 || deleted[0] != want[0] || deleted[1] != want[1] {
		t.Fatalf("the Go files the config now hides are deleted, and nothing else changes;\n"+
			"got added=%v modified=%v deleted=%v, want deleted=%v", added, modified, deleted, want)
	}

	// The Python file under the ignored tree is not the Go diff's to report: each language's
	// diff answers only for its own files.
	_, _, pyDeleted, err := DiffScanFiles(dir, "python", manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(pyDeleted) != 1 || pyDeleted[0] != filepath.Join(dir, "gen", "h.py") {
		t.Fatalf("the python diff reports exactly its own hidden file, got %v", pyDeleted)
	}
}

// TestDiffScanFiles_AllHiddenIsNotRefused pins that ST-4's deletions are not mistaken for a walk
// that went wrong: hiding every Go file is what the config asked for, so the mass-deletion guard
// must not refuse it.
func TestDiffScanFiles_AllHiddenIsNotRefused(t *testing.T) {
	dir, manifestPath := writeDiffFixture(t, map[string]string{
		"gen/g.go": "package gen\n",
	})
	t.Cleanup(func() { domain.SetActiveIgnore(nil) })
	domain.SetActiveIgnore(domain.BuildIgnoreMatcher(dir, []string{"gen/"}))

	_, _, deleted, err := DiffScanFiles(dir, "go", manifestPath)
	if err != nil {
		t.Fatalf("hiding every file of a language is not a failed walk: %v", err)
	}
	if len(deleted) != 1 {
		t.Fatalf("the hidden file is deleted, got %v", deleted)
	}
}
