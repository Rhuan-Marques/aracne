package topology_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/topology"
)

// A Cargo package name or a Go module path roots every id, so a change to it is a change to
// every file -- and an incremental scan diffs source files only. Renaming the package from `q`
// to `renamed` and touching one file used to leave that file under `renamed::` and every other
// under `q::`, with the call between them lost.
func TestProjectManifestChangeRerootsEveryID(t *testing.T) {
	for _, c := range []struct {
		name     string
		files    map[string]string
		manifest string // the manifest file to edit
		from, to string
		touch    string // one source file the edit also touches, as an agent would
		want     string // an id only the re-rooted graph has
	}{
		{
			name: "cargo package renamed",
			files: map[string]string{
				"Cargo.toml": additionCargo,
				"src/lib.rs": "pub mod a;\npub mod b;\n",
				"src/a.rs":   "pub fn fa() {}\n",
				"src/b.rs":   "use crate::a::fa;\npub fn fb() { fa(); }\n",
			},
			manifest: "Cargo.toml", from: `name = "q"`, to: `name = "renamed"`,
			touch: "src/b.rs",
			want:  "renamed::a::fa",
		},
		{
			name: "go module renamed",
			files: map[string]string{
				"go.mod":   "module example.com/old\n\ngo 1.21\n",
				"a/a.go":   "package a\n\nfunc Fa() {}\n",
				"b/b.go":   "package b\n\nimport \"example.com/old/a\"\n\nfunc Fb() { a.Fa() }\n",
				"c/c.go":   "package c\n\nfunc Fc() {}\n",
				"d/doc.go": "package d\n",
			},
			manifest: "go.mod", from: "example.com/old", to: "example.com/new",
			touch: "c/c.go",
			want:  "example.com/new/a.Fa",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			additionWrite(t, dir, c.files, time.Time{})
			reg := contractRegistry()
			mgr := topology.New()
			mgr.Load(filepath.Join(dir, "topology.db"))
			if err := mgr.FullScan(dir, reg); err != nil {
				t.Fatalf("FullScan: %v", err)
			}

			manifest := filepath.Join(dir, c.manifest)
			data, err := os.ReadFile(manifest)
			if err != nil {
				t.Fatal(err)
			}
			additionWrite(t, dir, map[string]string{
				c.manifest: strings.ReplaceAll(string(data), c.from, c.to),
				c.touch:    c.files[c.touch] + "\n",
			}, time.Now().Add(3*time.Second))
			if _, err := mgr.IncrementalScan(dir, reg); err != nil {
				t.Fatalf("IncrementalScan: %v", err)
			}
			incremental, err := mgr.ReadAll()
			if err != nil {
				t.Fatal(err)
			}

			cold := topology.New()
			cold.Load(filepath.Join(t.TempDir(), "cold.db"))
			if err := cold.FullScan(dir, reg); err != nil {
				t.Fatalf("cold FullScan: %v", err)
			}
			fresh, err := cold.ReadAll()
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := fresh.Resources[c.want]; !ok {
				t.Fatalf("the cold scan has no %s, so this case proves nothing", c.want)
			}
			if diff := additionDiff(incremental, fresh); len(diff) > 0 {
				t.Errorf("incremental scan differs from a cold scan (- incremental only, + cold only):\n  %s",
					strings.Join(diff, "\n  "))
			}
		})
	}
}

// The check runs before every tool call, so it must never turn into a full rescan per call. A
// full rescan is detected here by what only it can see: a source file rewritten with its old
// mtime restored, which an incremental scan's diff cannot notice.
func TestUnchangedProjectManifestNeverForcesAFullRescan(t *testing.T) {
	dir := t.TempDir()
	additionWrite(t, dir, map[string]string{
		"go.mod": "module example.com/g\n\ngo 1.21\n",
		"a/a.go": "package a\n\nfunc Fa() {}\n",
	}, time.Time{})
	reg := contractRegistry()
	mgr := topology.New()
	mgr.Load(filepath.Join(dir, "topology.db"))
	if err := mgr.FullScan(dir, reg); err != nil {
		t.Fatalf("FullScan: %v", err)
	}

	src := filepath.Join(dir, "a", "a.go")
	info, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("package a\n\nfunc Fa() {}\n\nfunc Hidden() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(src, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	const hidden = "example.com/g/a.Hidden"
	rescanned := func() bool {
		t.Helper()
		if _, err := mgr.IncrementalScan(dir, reg); err != nil {
			t.Fatalf("IncrementalScan: %v", err)
		}
		topo, err := mgr.ReadAll()
		if err != nil {
			t.Fatal(err)
		}
		_, ok := topo.Resources[hidden]
		return ok
	}
	goMod := filepath.Join(dir, "go.mod")
	later := time.Now().Add(5 * time.Second)

	// Nothing changed.
	if rescanned() {
		t.Fatal("a scan with no manifest change ran a full rescan")
	}
	// Touched, same bytes: a checkout, an editor save.
	if err := os.Chtimes(goMod, later, later); err != nil {
		t.Fatal(err)
	}
	if rescanned() {
		t.Fatal("touching go.mod ran a full rescan")
	}
	// A requirement added: goscanner reads only the module line, so no id changed.
	if err := os.WriteFile(goMod, []byte("module example.com/g\n\ngo 1.21\n\nrequire example.com/dep v1.0.0\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(goMod, later.Add(time.Second), later.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if rescanned() {
		t.Fatal("a go.mod requirement change ran a full rescan")
	}
	// A database written before any of this was recorded gets a record, not a rescan.
	if err := os.Remove(filepath.Join(dir, "project_manifests.json")); err != nil {
		t.Fatal(err)
	}
	if rescanned() {
		t.Fatal("a database with no manifest record ran a full rescan")
	}
	if _, err := os.Stat(filepath.Join(dir, "project_manifests.json")); err != nil {
		t.Fatalf("the missing record was not written: %v", err)
	}

	// And the case the check exists for: the module line itself.
	if err := os.WriteFile(goMod, []byte("module example.com/h\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(goMod, later.Add(2*time.Second), later.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.IncrementalScan(dir, reg); err != nil {
		t.Fatalf("IncrementalScan: %v", err)
	}
	topo, err := mgr.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := topo.Resources["example.com/h/a.Hidden"]; !ok {
		t.Fatal("renaming the module did not rescan the project")
	}
	// Once: the next scan is incremental again.
	if err := os.WriteFile(src, []byte("package a\n\nfunc Fa() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(src, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.IncrementalScan(dir, reg); err != nil {
		t.Fatalf("IncrementalScan: %v", err)
	}
	if topo, err = mgr.ReadAll(); err != nil {
		t.Fatal(err)
	}
	if _, ok := topo.Resources["example.com/h/a.Hidden"]; !ok {
		t.Fatal("the scan after the rescan rescanned again")
	}
}
