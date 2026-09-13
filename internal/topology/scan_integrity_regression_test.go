package topology_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner/goscanner"
)

func goOnlyRegistry(extra ...scanner.LanguageScanner) *scanner.Registry {
	reg := scanner.NewRegistry()
	reg.Register(goscanner.NewGoScanner())
	for _, s := range extra {
		reg.Register(s)
	}
	return reg
}

// writeLater writes body to path and moves its mtime past whatever it was, so the next manifest
// diff sees the change regardless of the filesystem's timestamp granularity.
func writeLater(t *testing.T, path, body string) {
	t.Helper()
	next := time.Now().Add(2 * time.Second)
	if info, err := os.Stat(path); err == nil && !info.ModTime().Before(next) {
		next = info.ModTime().Add(2 * time.Second)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, next, next); err != nil {
		t.Fatal(err)
	}
}

func mustReadAll(t *testing.T, mgr *topology.TopologyManager) *domain.Topology {
	t.Helper()
	topo, err := mgr.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	return topo
}

func referrersOf(topo *domain.Topology, target string) []string {
	var out []string
	for id, res := range topo.Resources {
		for connType, targets := range res.Connections {
			for _, tgt := range targets {
				if tgt == target {
					out = append(out, id+" -"+connType+"->")
				}
			}
		}
	}
	return out
}

// TestPartialPathDoesNotResurrectDeletedMethod pins SC-4.
//
// RestampUnchangedRows re-read the file's stored rows and re-appended every one missing from
// the upserts whose span hash had changed -- without consulting the deletes. A deleted
// declaration's old span always covers different code afterwards, so it came back: deleting
// `func (r Rect) Area()` left `(Rect).Area` in the graph, with its edges, until `--hard`.
func TestPartialPathDoesNotResurrectDeletedMethod(t *testing.T) {
	mgr, dir := scanProject(t, map[string]string{
		"go.mod":     "module example.com/g\n\ngo 1.22\n",
		"s/iface.go": "package s\n\ntype Shape interface {\n\tArea() int\n}\n",
		"s/s.go":     "package s\n\ntype Rect struct{ W, H int }\n\nfunc (r Rect) Area() int { return r.W * r.H }\n\nfunc (r Rect) Perim() int { return 2 * (r.W + r.H) }\n",
	})
	const ghost = "example.com/g/s.(Rect).Area"
	if _, ok := mustReadAll(t, mgr).Resources[ghost]; !ok {
		t.Fatalf("fixture: %s not indexed", ghost)
	}

	src := filepath.Join(dir, "s", "s.go")
	writeLater(t, src, "package s\n\ntype Rect struct{ W, H int }\n\nfunc (r Rect) Perim() int { return 2 * (r.W + r.H) }\n")
	before := topology.PartialIncrementalCount()
	if _, err := mgr.IncrementalScan(dir, goOnlyRegistry()); err != nil {
		t.Fatal(err)
	}
	if topology.PartialIncrementalCount() == before {
		t.Fatal("fixture: expected the Go partial fast path to handle a single-file edit")
	}
	topo := mustReadAll(t, mgr)
	if _, ok := topo.Resources[ghost]; ok {
		t.Fatalf("%s was deleted from source but is still in the graph", ghost)
	}
	if refs := referrersOf(topo, ghost); len(refs) != 0 {
		t.Fatalf("edges still point at the deleted %s: %v", ghost, refs)
	}

	// A later body edit on the fast path must not re-attach it either.
	writeLater(t, src, "package s\n\ntype Rect struct{ W, H int }\n\nfunc (r Rect) Perim() int { return 4 * (r.W + r.H) / 2 }\n")
	if _, err := mgr.IncrementalScan(dir, goOnlyRegistry()); err != nil {
		t.Fatal(err)
	}
	topo = mustReadAll(t, mgr)
	if _, ok := topo.Resources[ghost]; ok {
		t.Fatalf("%s came back after a later edit", ghost)
	}
	if refs := referrersOf(topo, ghost); len(refs) != 0 {
		t.Fatalf("edges re-attached to the deleted %s: %v", ghost, refs)
	}
}

// failingScanner claims every root and fails every scan.
type failingScanner struct{}

func (failingScanner) Name() string         { return "broken" }
func (failingScanner) Extensions() []string { return []string{".brk"} }
func (failingScanner) Detect(string) bool   { return true }
func (failingScanner) Scan(string) (*domain.Topology, error) {
	return nil, errors.New("boom")
}
func (failingScanner) UpdateFile(*domain.Topology, string) ([]domain.TopologyWarning, error) {
	return nil, nil
}

// TestOneScannerFailureDoesNotAbortTheScan pins SC-1(a): scanAllLanguages returned on the first
// scanner error, so one language's failure left every other language unindexed.
func TestOneScannerFailureDoesNotAbortTheScan(t *testing.T) {
	files := map[string]string{
		"go.mod": "module probe\n\ngo 1.22\n",
		"a.go":   "package probe\n\nfunc A() int { return 1 }\n",
	}
	scans := map[string]func(*topology.TopologyManager, string, *scanner.Registry) error{
		"full": func(m *topology.TopologyManager, root string, reg *scanner.Registry) error {
			return m.FullScan(root, reg)
		},
		"rescan": func(m *topology.TopologyManager, root string, reg *scanner.Registry) error {
			_, err := m.FullReScan(root, reg)
			return err
		},
		"incremental": func(m *topology.TopologyManager, root string, reg *scanner.Registry) error {
			_, err := m.IncrementalScan(root, reg)
			return err
		},
	}
	for name, scan := range scans {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			for rel, body := range files {
				writeLater(t, filepath.Join(dir, rel), body)
			}
			mgr := topology.New()
			mgr.Load(filepath.Join(dir, ".aracne", "topology.db"))
			os.MkdirAll(filepath.Join(dir, ".aracne"), 0o755)
			if err := scan(mgr, dir, goOnlyRegistry(failingScanner{})); err != nil {
				t.Fatalf("one failing language aborted the scan: %v", err)
			}
			topo := mustReadAll(t, mgr)
			if _, ok := topo.Resources["probe.A"]; !ok {
				t.Fatalf("the healthy language was not indexed; ids: %v", idsOf(topo))
			}
			if msg := topo.Errors["scan:broken"]; !strings.Contains(msg, "boom") {
				t.Fatalf("the failure must be recorded in Errors, got %v", topo.Errors)
			}
		})
	}

	// When every language fails there is nothing to write, and it is still an error.
	dir := t.TempDir()
	mgr := topology.New()
	mgr.Load(filepath.Join(dir, ".aracne", "topology.db"))
	os.MkdirAll(filepath.Join(dir, ".aracne"), 0o755)
	reg := scanner.NewRegistry()
	reg.Register(failingScanner{})
	if err := mgr.FullScan(dir, reg); err == nil {
		t.Fatal("a scan in which every language failed must report it")
	}
}

// TestGoModuleBelowTheRoot pins SC-1(b)/(c) through the manager: a module in a subdirectory and
// a stray .go file with no module at all both index, on the cold path and on an incremental edit.
func TestGoModuleBelowTheRoot(t *testing.T) {
	mgr, dir := scanProject(t, map[string]string{
		"backend/go.mod":  "module example.com/backend\n\ngo 1.22\n",
		"backend/main.go": "package main\n\nfunc main() { helper() }\n\nfunc helper() {}\n",
		"tools/gen.go":    "package main\n\nfunc Gen() {}\n",
	})
	topo := mustReadAll(t, mgr)
	for _, id := range []string{"example.com/backend.main", "example.com/backend.helper", "_/tools.Gen"} {
		if _, ok := topo.Resources[id]; !ok {
			t.Fatalf("missing %s; ids: %v", id, idsOf(topo))
		}
	}
	writeLater(t, filepath.Join(dir, "backend", "main.go"),
		"package main\n\nfunc main() { helper(); added() }\n\nfunc helper() {}\n\nfunc added() {}\n")
	if _, err := mgr.IncrementalScan(dir, goOnlyRegistry()); err != nil {
		t.Fatal(err)
	}
	topo = mustReadAll(t, mgr)
	if _, ok := topo.Resources["example.com/backend.added"]; !ok {
		t.Fatalf("an incremental edit below the root was not indexed; ids: %v", idsOf(topo))
	}
	if calls := topo.Resources["example.com/backend.main"].Connections["calls"]; len(calls) != 2 {
		t.Fatalf("expected main to call helper and added, got %v", calls)
	}
}

// editingScanner is goscanner with an edit injected right after it has read the source -- the
// window in which a concurrent write used to be stamped current without being parsed.
type editingScanner struct {
	*goscanner.GoScanner
	edit func()
}

func (e editingScanner) Scan(root string) (*domain.Topology, error) {
	topo, err := e.GoScanner.Scan(root)
	e.edit()
	return topo, err
}

func (e editingScanner) UpdateFilePartial(dbPath, root, absPath string) ([]domain.Resource, []string, map[string]domain.TopologyWarning, error) {
	upserts, deletes, warnings, err := e.GoScanner.UpdateFilePartial(dbPath, root, absPath)
	e.edit()
	return upserts, deletes, warnings, err
}

// TestFileEditedDuringAScanStaysStale pins ST-1 on both scan paths that stamp the manifest.
func TestFileEditedDuringAScanStaysStale(t *testing.T) {
	const base = "package probe\n\nfunc A() int { return 1 }\n"
	cases := map[string]func(*topology.TopologyManager, string, *scanner.Registry) error{
		// `arac scan --all` racing an editor save.
		"full rescan": func(m *topology.TopologyManager, root string, reg *scanner.Registry) error {
			_, err := m.FullReScan(root, reg)
			return err
		},
		// The guard's pre-tool scan racing a sub-agent's write, on the single-file fast path.
		"incremental": func(m *topology.TopologyManager, root string, reg *scanner.Registry) error {
			writeLater(t, filepath.Join(root, "a.go"), base+"\nfunc B() int { return 2 }\n")
			_, err := m.IncrementalScan(root, reg)
			return err
		},
	}
	for name, scan := range cases {
		t.Run(name, func(t *testing.T) {
			mgr, dir := scanProject(t, map[string]string{"go.mod": "module probe\n\ngo 1.22\n", "a.go": base})
			src := filepath.Join(dir, "a.go")
			fired := false
			racing := scanner.NewRegistry()
			racing.Register(editingScanner{GoScanner: goscanner.NewGoScanner(), edit: func() {
				if fired {
					return
				}
				fired = true
				body, _ := os.ReadFile(src)
				writeLater(t, src, string(body)+"\nfunc Late() int { return 3 }\n")
			}})
			if err := scan(mgr, dir, racing); err != nil {
				t.Fatal(err)
			}
			if !fired {
				t.Fatal("fixture: the edit was never injected")
			}
			if _, err := mgr.IncrementalScan(dir, goOnlyRegistry()); err != nil {
				t.Fatal(err)
			}
			if _, ok := mustReadAll(t, mgr).Resources["probe.Late"]; !ok {
				t.Fatal("a function added while the scan ran was stamped current and never indexed")
			}
		})
	}
}

// TestUnreadableDirectoryDoesNotBlockIncrementalScans pins ST-2 through the manager.
func TestUnreadableDirectoryDoesNotBlockIncrementalScans(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 directory anyway")
	}
	mgr, dir := scanProject(t, map[string]string{
		"go.mod":      "module example.com/p\n\ngo 1.22\n",
		"shapes/a.go": "package shapes\n\nfunc One() int { return 1 }\n",
		"pgdata/x":    "data\n",
	})
	locked := filepath.Join(dir, "pgdata")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	writeLater(t, filepath.Join(dir, "shapes", "new.go"), "package shapes\n\nfunc NewOne() int { return 2 }\n")
	if _, err := mgr.IncrementalScan(dir, goOnlyRegistry()); err != nil {
		t.Fatalf("an unreadable directory stopped the incremental scan: %v", err)
	}
	if _, ok := mustReadAll(t, mgr).Resources["example.com/p/shapes.NewOne"]; !ok {
		t.Fatal("the new file was not indexed")
	}
}

const relocSource = "package shapes\n\ntype Circle struct{ R int }\n\nfunc (c Circle) Area() int { return 3 * c.R * c.R }\n\nfunc Undocumented() int { return 4 }\n"

// scanRelocatable builds a project at parent/p1, scans it, and gives one symbol and its file a
// description no doc comment can restore -- the kind `arac update-description` writes.
func scanRelocatable(t *testing.T, parent string) string {
	t.Helper()
	p1 := filepath.Join(parent, "p1")
	writeLater(t, filepath.Join(p1, "go.mod"), "module example.com/p\n\ngo 1.22\n")
	writeLater(t, filepath.Join(p1, "shapes", "shape.go"), relocSource)
	os.MkdirAll(filepath.Join(p1, ".aracne"), 0o755)
	mgr := topology.New()
	mgr.Load(filepath.Join(p1, ".aracne", "topology.db"))
	if err := mgr.FullScan(p1, goOnlyRegistry()); err != nil {
		t.Fatal(err)
	}
	if err := mgr.UpdateDescription("example.com/p/shapes.Undocumented", domain.ResourceFunction, "MANUAL-GO"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.UpdateDescription(filepath.Join(p1, "shapes", "shape.go"), domain.ResourceFile, "MANUAL-FILE"); err != nil {
		t.Fatal(err)
	}
	return p1
}

// TestRelocatedProjectIsRebuiltUnderTheNewRoot pins SC-2: after `mv p1 p2`, every entry point
// that consults the stored root must rebuild under p2 without losing descriptions or raising
// node_removed warnings. A plain incremental scan used to diff the old manifest against the new
// tree, see every file deleted and every file new, and end with an empty graph.
func TestRelocatedProjectIsRebuiltUnderTheNewRoot(t *testing.T) {
	entryPoints := map[string]func(*topology.TopologyManager, string) error{
		// `arac scan`, the drift check and the watcher: the new root is passed in.
		"incremental scan": func(m *topology.TopologyManager, p2 string) error {
			_, err := m.IncrementalScan(p2, goOnlyRegistry())
			return err
		},
		// The guard's pre-tool scan, which roots itself at the STORED root.
		"pre-tool scan": func(m *topology.TopologyManager, _ string) error {
			return m.RunPreToolScan(goOnlyRegistry(), helper.PreToolScanDefault)
		},
		"pre-tool full scan": func(m *topology.TopologyManager, _ string) error {
			return m.RunPreToolScan(goOnlyRegistry(), helper.PreToolScanFull)
		},
		// What the read verbs call when they open the topology.
		"sync": func(m *topology.TopologyManager, _ string) error {
			moved, err := m.SyncRelocation(goOnlyRegistry())
			if err == nil && !moved {
				return errors.New("SyncRelocation did not see the move")
			}
			return err
		},
	}
	for name, run := range entryPoints {
		t.Run(name, func(t *testing.T) {
			// CANONICAL: every assertion below compares a path the SCAN stored -- the root, a
			// file id, a manifest key -- against one built here, and a scan resolves its root
			// before it walks. macOS hands out every t.TempDir() under /var, a symlink to
			// /private/var, so the two spellings would never meet.
			parent := helper.CanonicalPath(t.TempDir())
			p1 := scanRelocatable(t, parent)
			p2 := filepath.Join(parent, "p2")
			if err := os.Rename(p1, p2); err != nil {
				t.Fatal(err)
			}
			mgr := topology.New()
			mgr.Load(filepath.Join(p2, ".aracne", "topology.db"))
			if _, _, moved := mgr.Relocation(); !moved {
				t.Fatal("the move was not detected")
			}
			if err := run(mgr, p2); err != nil {
				t.Fatal(err)
			}

			topo := mustReadAll(t, mgr)
			if topo.Root != p2 {
				t.Fatalf("stored root = %s, want %s", topo.Root, p2)
			}
			for _, w := range topo.Warnings {
				if w.Kind == domain.WarnNodeRemoved {
					t.Fatalf("a move changed no code but raised %s", w.Message)
				}
			}
			if got := topo.Resources["example.com/p/shapes.Undocumented"].Description; got != "MANUAL-GO" {
				t.Fatalf("symbol description lost across the move: %q", got)
			}
			newFile := filepath.Join(p2, "shapes", "shape.go")
			if got := topo.Resources[newFile].Description; got != "MANUAL-FILE" {
				t.Fatalf("file description lost across the move: %q", got)
			}
			area := topo.Resources["example.com/p/shapes.(Circle).Area"]
			if area.Location.Path != newFile {
				t.Fatalf("Area still located at %s", area.Location.Path)
			}
			if _, err := mgr.Cut(area.Location); err != nil {
				t.Fatalf("read after the move: %v", err)
			}
			for path := range helper.ReadManifest(helper.ManifestPath(mgr.DbPath())) {
				if !strings.HasPrefix(path, p2+string(filepath.Separator)) {
					t.Fatalf("manifest still names %s", path)
				}
			}
			if _, _, moved := mgr.Relocation(); moved {
				t.Fatal("still reported as moved after the rebuild")
			}
			if health, err := mgr.IndexHealth("", goOnlyRegistry()); err != nil || health.Stale() {
				t.Fatalf("index health after the move: %+v, %v", health, err)
			}
		})
	}
}

// TestRelocationNeedsTheStandardLayout: a copy whose original still exists is a move too, but a
// database kept outside `<root>/.aracne/` was placed by a caller who chose the root, and keeps
// the old behaviour.
func TestRelocationNeedsTheStandardLayout(t *testing.T) {
	parent := helper.CanonicalPath(t.TempDir()) // see TestRelocatedProjectIsRebuiltUnderTheNewRoot
	p1 := scanRelocatable(t, parent)

	// A copy: sources and database duplicated, original left in place.
	p3 := filepath.Join(parent, "p3")
	writeLater(t, filepath.Join(p3, "go.mod"), "module example.com/p\n\ngo 1.22\n")
	writeLater(t, filepath.Join(p3, "shapes", "shape.go"), relocSource)
	os.MkdirAll(filepath.Join(p3, ".aracne"), 0o755)
	data, err := os.ReadFile(filepath.Join(p1, ".aracne", "topology.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p3, ".aracne", "topology.db"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	copied := topology.New()
	copied.Load(filepath.Join(p3, ".aracne", "topology.db"))
	if _, _, moved := copied.Relocation(); !moved {
		t.Fatal("a copied project must not keep answering from the original's paths")
	}

	// A custom database location: no layout to infer a root from.
	custom := filepath.Join(parent, "elsewhere", "topo.db")
	if err := os.MkdirAll(filepath.Dir(custom), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(custom, data, 0o644); err != nil {
		t.Fatal(err)
	}
	outside := topology.New()
	outside.Load(custom)
	if _, _, moved := outside.Relocation(); moved {
		t.Fatal("a database outside the .aracne layout must keep the current behaviour")
	}

	// And the project that never moved is not reported.
	stay := topology.New()
	stay.Load(filepath.Join(p1, ".aracne", "topology.db"))
	if _, _, moved := stay.Relocation(); moved {
		t.Fatal("an unmoved project reported as moved")
	}
}
