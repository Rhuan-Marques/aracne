package pyscanner

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func writePyTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestParseDoesNotImportProjectModules pins PY-1: with a bare `python3 -c`, sys.path[0] is
// the working directory -- the project root when a scan runs -- so the parse script's own
// `import json` / `import enum` / `import ast` / `import re` loaded the project's files of
// those names. That executed them, and a stdlib-named file failed every parse in the scan.
func TestParseDoesNotImportProjectModules(t *testing.T) {
	if !hasPython() {
		t.Skip("python not available")
	}
	dir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "MARKER")
	files := map[string]string{
		"app.py": "import json\n\n\ndef load(s):\n    return json.loads(s)\n",
	}
	for _, name := range []string{"json", "enum", "ast", "re"} {
		files[name+".py"] = fmt.Sprintf("open(%q, \"a\").write(%q)\nraise RuntimeError(%q)\n",
			marker, name+"\n", name+" was imported from the project")
	}
	writePyTree(t, dir, files)
	// The condition the scan runs under: the project root is the working directory.
	t.Chdir(dir)

	pr, err := ParseFile(filepath.Join(dir, "app.py"), ".", dir)
	if err != nil {
		t.Fatalf("app.py must parse beside stdlib-named project files: %v", err)
	}
	if len(pr.Functions) != 1 || pr.Functions[0].Function.Name != "load" {
		t.Fatalf("expected one function named load, got %+v", pr.Functions)
	}

	topo, err := NewPythonScanner().Scan(dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(topo.Errors) != 0 {
		t.Errorf("scan reported errors: %v", topo.Errors)
	}
	for rel := range files {
		if _, ok := topo.Resources[filepath.Join(dir, rel)]; !ok {
			t.Errorf("%s was not indexed", rel)
		}
	}
	if raw, err := os.ReadFile(marker); err == nil {
		t.Fatalf("parsing executed project modules; they wrote:\n%s", raw)
	}
}

// TestPythonParseArgsIsolateTheInterpreter pins the flag that makes the parse independent of
// the working directory: -I must reach the interpreter ahead of the script.
func TestPythonParseArgsIsolateTheInterpreter(t *testing.T) {
	args := pythonParseArgs("/p/app.py", "/p")
	c := slices.Index(args, "-c")
	if c < 0 || !slices.Contains(args[:c], "-I") {
		t.Fatalf("the parse script must run in isolated mode (-I before -c), got %q", args[:max(c, 0)])
	}
	if got := args[c+1:]; len(got) != 3 || got[0] != pythonParseScript || got[1] != "/p/app.py" || got[2] != "/p" {
		t.Fatalf("script, file and root must follow -c as argv, got %d trailing args", len(got))
	}
}

// TestResolveInternalImport_packageTargets pins PY-2's ID math: a package's names are keyed
// under its __init__.py, and `from pkg import X` also knows X as a submodule when one exists.
func TestResolveInternalImport_packageTargets(t *testing.T) {
	dir := t.TempDir()
	writePyTree(t, dir, map[string]string{
		"pkg/__init__.py":       "",
		"pkg/sub.py":            "",
		"pkg/inner/__init__.py": "",
		"mod.py":                "",
		"top/__init__.py":       "",
		"top/lib/__init__.py":   "",
		"top/lib/sub.py":        "",
		"top/user.py":           "",
		"app.py":                "",
	})
	app := filepath.Join(dir, "app.py")
	user := filepath.Join(dir, "top", "user.py")
	cases := []struct {
		name     string
		imp      pyImport
		importer string
		module   string
		symbol   string
		sub      string
	}{
		{"from pkg import name", pyImport{Name: "pkg.helper", Alias: "helper", Module: "pkg"}, app, "pkg/__init__", "helper", ""},
		{"from pkg import submodule", pyImport{Name: "pkg.sub", Alias: "sub", Module: "pkg"}, app, "pkg/__init__", "sub", "pkg/sub"},
		{"from pkg import subpackage", pyImport{Name: "pkg.inner", Alias: "inner", Module: "pkg"}, app, "pkg/__init__", "inner", "pkg/inner/__init__"},
		{"import pkg", pyImport{Name: "pkg", Alias: "pkg"}, app, "pkg/__init__", "", ""},
		{"import pkg.sub", pyImport{Name: "pkg.sub", Alias: "pkg"}, app, "pkg/sub", "", ""},
		{"from mod import name", pyImport{Name: "mod.f", Alias: "f", Module: "mod"}, app, "mod", "f", ""},
		{"from .lib import name", pyImport{Name: "lib.helper", Alias: "helper", Module: "lib", Level: 1}, user, "top/lib/__init__", "helper", ""},
		{"from .lib import submodule", pyImport{Name: "lib.sub", Alias: "sub", Module: "lib", Level: 1}, user, "top/lib/__init__", "sub", "top/lib/sub"},
		{"from . import subpackage", pyImport{Name: "lib", Alias: "lib", Level: 1}, user, "top/lib/__init__", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tgt, ok := resolveInternalImport(tc.imp, tc.importer, dir)
			if !ok {
				t.Fatalf("import did not resolve")
			}
			if tgt.ModulePath != tc.module || tgt.Symbol != tc.symbol || tgt.SubModulePath != tc.sub {
				t.Fatalf("got module=%q symbol=%q sub=%q, want module=%q symbol=%q sub=%q",
					tgt.ModulePath, tgt.Symbol, tgt.SubModulePath, tc.module, tc.symbol, tc.sub)
			}
		})
	}
}

// TestScan_packageInitImports pins PY-2 end to end: every form of import that reaches a
// package's __init__.py produces its calls edge, in a full scan and after UpdateFile.
func TestScan_packageInitImports(t *testing.T) {
	if !hasPython() {
		t.Skip("python not available")
	}
	dir := t.TempDir()
	writePyTree(t, dir, map[string]string{
		"pkg/__init__.py": "from .sub import subf\n\n\ndef helper():\n    return 1\n",
		"pkg/sub.py":      "def subf():\n    return 2\n",
		"app.py": "import pkg\nfrom pkg import helper\nfrom pkg import sub\nfrom pkg import subf\n\n\n" +
			"def a():\n    helper()\n\n\ndef b():\n    sub.subf()\n\n\ndef c():\n    pkg.helper()\n\n\ndef d():\n    subf()\n",
	})
	want := map[string]string{
		"app.a": "pkg/__init__.helper",
		"app.b": "pkg/sub.subf",
		"app.c": "pkg/__init__.helper",
		"app.d": "pkg/sub.subf", // re-exported by pkg/__init__.py
	}

	s := NewPythonScanner()
	topo, err := s.Scan(dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	check := func(label string) {
		t.Helper()
		for caller, callee := range want {
			calls := topo.Resources[caller].Connections["calls"]
			if !slices.Contains(calls, callee) {
				t.Errorf("%s: %s should call %s, calls=%v", label, caller, callee, calls)
			}
		}
	}
	check("full scan")

	if _, err := s.UpdateFile(topo, filepath.Join(dir, "app.py")); err != nil {
		t.Fatalf("UpdateFile: %v", err)
	}
	check("UpdateFile")
}

// TestScan_reexportDoesNotRebindAnotherKind guards the re-export walk: it follows a name
// only when the target module does not bind it itself. Here pkg/__init__.py defines the
// class Thing, so a function Thing in a module it imports must not capture the reference.
func TestScan_reexportDoesNotRebindAnotherKind(t *testing.T) {
	if !hasPython() {
		t.Skip("python not available")
	}
	dir := t.TempDir()
	writePyTree(t, dir, map[string]string{
		"pkg/__init__.py": "from . import other\n\n\nclass Thing:\n    pass\n",
		"pkg/other.py":    "def Thing():\n    return 1\n",
		"app.py":          "from pkg import Thing\n\n\ndef f():\n    Thing()\n",
	})
	topo, err := NewPythonScanner().Scan(dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	conns := topo.Resources["app.f"].Connections
	if slices.Contains(conns["calls"], "pkg/other.Thing") {
		t.Errorf("Thing is the class in pkg/__init__.py, not pkg/other.Thing: calls=%v", conns["calls"])
	}
	if !slices.Contains(conns["uses_class"], "pkg/__init__.Thing") {
		t.Errorf("expected app.f to use pkg/__init__.Thing, got %v", conns)
	}
}
