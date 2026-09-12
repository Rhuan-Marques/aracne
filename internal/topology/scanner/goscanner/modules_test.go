package goscanner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// writeTree writes files (relative path -> body) under dir.
func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestDetectAgreesWithScan pins SC-1(e): Detect checked the root go.mod only, while the
// registry detects Go by its files and Scan indexes a module wherever it sits.
func TestDetectAgreesWithScan(t *testing.T) {
	s := NewGoScanner()
	cases := []struct {
		name  string
		files map[string]string
		want  bool
	}{
		{"module in a subdirectory", map[string]string{"backend/go.mod": "module example.com/backend\n", "backend/main.go": "package main\n"}, true},
		{"stray file, no module", map[string]string{"tools/gen.go": "package main\n", "app.py": "x = 1\n"}, true},
		{"go.work root", map[string]string{"go.work": "go 1.22\n"}, true},
		{"only a test file", map[string]string{"a_test.go": "package a\n"}, false},
		{"only under a pruned directory", map[string]string{"vendor/x/x.go": "package x\n"}, false},
	}
	for _, c := range cases {
		dir := t.TempDir()
		writeTree(t, dir, c.files)
		if got := s.Detect(dir); got != c.want {
			t.Errorf("%s: Detect = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestScanWithoutRootGoMod pins SC-1(b)/(c): a module below the root, sibling modules of a
// workspace, and a .go file with no module at all are all indexed, instead of the scan failing
// on a missing <root>/go.mod.
func TestScanWithoutRootGoMod(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"go.work":        "go 1.22\n\nuse (\n\t./a\n\t./b\n)\n",
		"a/go.mod":       "module example.com/a\n\ngo 1.22\n",
		"a/a.go":         "package a\n\nfunc Hello() string { return \"hi\" }\n",
		"a/sub/sub.go":   "package sub\n\nfunc Deep() int { return 1 }\n",
		"b/go.mod":       "module example.com/b\n\ngo 1.22\n",
		"b/b.go":         "package b\n\nimport \"example.com/a\"\n\nfunc Use() string { return a.Hello() }\n",
		"tools/gen.go":   "package main\n\nfunc Gen() {}\n",
		"stray_root.go":  "package main\n\nfunc Root() {}\n",
		"b/b_test.go":    "package b\n",
		"bad/go.mod":     "// no module line\n",
		"bad/inside.go":  "package bad\n\nfunc Inside() {}\n",
		"a/vendor/v.go":  "package v\n",
		"a/.hidden/h.go": "package h\n",
	})
	topo, err := NewGoScanner().Scan(dir)
	if err != nil {
		t.Fatalf("Scan without a root go.mod: %v", err)
	}
	for _, id := range []string{
		"example.com/a.Hello",
		"example.com/a/sub.Deep",
		"example.com/b.Use",
		"_/tools.Gen",
		"_.Root",
		// A go.mod that declares nothing is not a module; its files fall to the next one up.
		"_/bad.Inside",
	} {
		if _, ok := topo.Resources[id]; !ok {
			t.Errorf("missing %s; ids: %v", id, resourceIDs(topo))
		}
	}
	use := topo.Resources["example.com/b.Use"]
	if calls := use.Connections["calls"]; len(calls) != 1 || calls[0] != "example.com/a.Hello" {
		t.Errorf("a call into a sibling module must resolve, got calls=%v", calls)
	}
	bFile := topo.Resources[filepath.Join(dir, "b", "b.go")]
	if imps := bFile.Connections["imports_package"]; len(imps) != 1 || imps[0] != "example.com/a" {
		t.Errorf("an import of a sibling module is internal, got imports_package=%v", imps)
	}
	if deps := bFile.Connections["imports_dependency"]; len(deps) != 0 {
		t.Errorf("a sibling module is not a dependency, got imports_dependency=%v", deps)
	}
}

// TestRootGoModKeepsSingleModuleIDs pins SC-1(d): with a root go.mod and nothing nested, the
// whole tree is that module, exactly as before -- the ids the corpus suites pin.
//
// It used to assert the same for a tree with a nested go.mod, which named the nested module's
// files under the outer module (GO-10); see TestNestedGoModIsItsOwnModule.
func TestRootGoModKeepsSingleModuleIDs(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"go.mod":        "module example.com/root\n\ngo 1.22\n",
		"pkg/p.go":      "package pkg\n\nfunc P() {}\n",
		"nested/n.go":   "package nested\n\nfunc N() {}\n",
		"nested/x/x.go": "package x\n\nfunc X() {}\n",
	})
	topo, err := NewGoScanner().Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"example.com/root/pkg.P", "example.com/root/nested.N", "example.com/root/nested/x.X"} {
		if _, ok := topo.Resources[id]; !ok {
			t.Errorf("missing %s; ids: %v", id, resourceIDs(topo))
		}
	}
}

// TestNestedGoModIsItsOwnModule pins GO-10: a go.mod below a root go.mod starts a separate
// module, as it does for the go tool. Its files are named under ITS module path, and an import
// inside it is internal, so the call resolves instead of the import becoming a dependency.
func TestNestedGoModIsItsOwnModule(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"go.mod":              "module example.com/root\n\ngo 1.22\n",
		"pkg/p.go":            "package pkg\n\nfunc P() {}\n",
		"nested/go.mod":       "module other.example/nested\n\ngo 1.22\n",
		"nested/n.go":         "package nested\n\nimport \"other.example/nested/inner\"\n\nfunc N() int { return inner.F() }\n",
		"nested/inner/i.go":   "package inner\n\nfunc F() int { return 1 }\n",
		"tools/go.mod":        "module example.com/root/tools\n\ngo 1.22\n",
		"tools/gen/gen.go":    "package gen\n\nfunc Gen() {}\n",
		"nested/vendor/v.go":  "package v\n",
		"nested/.hidden/h.go": "package h\n",
	})
	topo, err := NewGoScanner().Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"example.com/root/pkg.P", "other.example/nested.N", "other.example/nested/inner.F", "example.com/root/tools/gen.Gen"} {
		if _, ok := topo.Resources[id]; !ok {
			t.Errorf("missing %s; ids: %v", id, resourceIDs(topo))
		}
	}
	for _, id := range []string{"example.com/root/nested.N", "example.com/root/nested/inner.F"} {
		if _, ok := topo.Resources[id]; ok {
			t.Errorf("%s names a nested module's file under the outer module", id)
		}
	}
	n := topo.Resources["other.example/nested.N"]
	if calls := n.Connections["calls"]; len(calls) != 1 || calls[0] != "other.example/nested/inner.F" {
		t.Errorf("a call inside the nested module must resolve, got calls=%v", calls)
	}
	if _, ok := topo.Resources["other.example/nested/inner"]; !ok || topo.Resources["other.example/nested/inner"].Kind != domain.ResourcePackage {
		t.Errorf("the nested module's own package must be a package, not a dependency")
	}
}

// TestUpdateFileWithoutRootGoMod: the single-file path used to return silently -- no update at
// all -- when <root>/go.mod was missing, so an edit to a module below the root was never
// indexed incrementally.
func TestUpdateFileWithoutRootGoMod(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"backend/go.mod":  "module example.com/backend\n\ngo 1.22\n",
		"backend/main.go": "package main\n\nfunc main() {}\n",
	})
	s := NewGoScanner()
	topo, err := s.Scan(dir)
	if err != nil {
		t.Fatal(err)
	}
	topo.Root = dir
	path := filepath.Join(dir, "backend", "main.go")
	writeTree(t, dir, map[string]string{"backend/main.go": "package main\n\nfunc main() { added() }\n\nfunc added() {}\n"})
	if _, err := s.UpdateFile(topo, path); err != nil {
		t.Fatal(err)
	}
	if _, ok := topo.Resources["example.com/backend.added"]; !ok {
		t.Fatalf("the edit was not indexed; ids: %v", resourceIDs(topo))
	}
}

func resourceIDs(topo *domain.Topology) []string {
	ids := make([]string, 0, len(topo.Resources))
	for id := range topo.Resources {
		ids = append(ids, id)
	}
	return ids
}
