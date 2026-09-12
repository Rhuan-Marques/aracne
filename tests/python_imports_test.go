package tests_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// TestPythonScanNeverImportsProjectModules pins PY-1.
//
// The Python scanner runs its embedded parse script with `python3 -c`, and with a bare -c
// sys.path[0] is the working directory -- the project root under `arac scan`, and under the
// guard's pre-tool scan that runs before every agent tool call. The script's own
// `import json` / `import enum` / `import ast` therefore picked up the PROJECT's json.py,
// executing it: arbitrary code execution just by opening a session in a cloned repo. A
// stdlib-named file that did not execute anything hostile still broke every parse, so the
// scan indexed 0 files.
func TestPythonScanNeverImportsProjectModules(t *testing.T) {
	if !hasPython() {
		t.Skip("Python not available")
	}
	dir := t.TempDir()
	// Outside the project, so the scan cannot see it and no project file depends on it.
	marker := filepath.Join(t.TempDir(), "MARKER")
	shadows := []string{"json", "enum", "ast", "re", "site"}
	for _, name := range shadows {
		writeFile(t, filepath.Join(dir, name+".py"), fmt.Sprintf(
			"open(%q, \"a\").write(%q)\nraise RuntimeError(%q)\n",
			marker, name+"\n", name+" was imported from the project"))
	}
	writeFile(t, filepath.Join(dir, "app.py"), "import json\n\n\ndef load(s):\n    return json.loads(s)\n")

	dbPath := filepath.Join(dir, "topo.db")
	mustRun(t, dir, "scan", "-root", dir, "-output", dbPath)

	if raw, err := os.ReadFile(marker); err == nil {
		t.Fatalf("scanning executed project modules; they wrote:\n%s", raw)
	}
	topo, err := helper.ReadDb(dbPath)
	if err != nil {
		t.Fatalf("read db: %v", err)
	}
	for _, name := range append(shadows, "app") {
		if _, ok := topo.Resources[filepath.Join(dir, name+".py")]; !ok {
			t.Errorf("%s.py was not indexed: a project module named like the stdlib must not break its own parse", name)
		}
	}
	if _, ok := topo.Resources["app.load"]; !ok {
		t.Errorf("app.load missing: app.py did not parse")
	}
}

// pyPackageImportFiles reaches names in a package every way Python allows: through the
// package's __init__.py, as a submodule, via a re-export in __init__.py, and in the relative
// forms. A package keys its resources under __init__.py (pkg/__init__.helper), which the
// import resolution used to miss by building `pkg.helper`.
var pyPackageImportFiles = map[string]string{
	"pkg/__init__.py":       "from .sub import subf\n\n\ndef helper():\n    return 1\n\n\nclass Widget:\n    def run(self):\n        return 0\n",
	"pkg/sub.py":            "def subf():\n    return 2\n",
	"pkg/inner/__init__.py": "def deep():\n    return 3\n",
	"pkg/rel.py": "from . import sub\nfrom . import helper\nfrom . import subf\n\n\n" +
		"def r_sub():\n    sub.subf()\n\n\ndef r_helper():\n    helper()\n\n\ndef r_reexport():\n    subf()\n",
	"top/__init__.py":     "",
	"top/lib/__init__.py": "def helper():\n    return 4\n",
	"top/lib/sub.py":      "def subf():\n    return 5\n",
	"top/user.py": "from .lib import helper\nfrom .lib import sub\n\n\n" +
		"def u_helper():\n    helper()\n\n\ndef u_sub():\n    sub.subf()\n",
	"app.py": "import pkg\nfrom pkg import helper\nfrom pkg import sub\nfrom pkg import subf\nfrom pkg import inner\nfrom pkg import Widget\n\n\n" +
		"def a():\n    helper()\n\n\ndef b():\n    sub.subf()\n\n\ndef c():\n    pkg.helper()\n\n\n" +
		"def d():\n    subf()\n\n\ndef e():\n    inner.deep()\n\n\ndef f():\n    w = Widget()\n    w.run()\n",
}

// pyPackageImportCalls is the calls edge each caller in pyPackageImportFiles must carry.
var pyPackageImportCalls = map[string]string{
	"app.a":              "pkg/__init__.helper",     // from pkg import helper
	"app.b":              "pkg/sub.subf",            // from pkg import sub; sub.subf()
	"app.c":              "pkg/__init__.helper",     // import pkg; pkg.helper()
	"app.d":              "pkg/sub.subf",            // from pkg import subf (re-exported by __init__)
	"app.e":              "pkg/inner/__init__.deep", // from pkg import inner (a subpackage)
	"app.f":              "pkg/__init__.Widget.run", // a class defined in __init__.py
	"pkg/rel.r_sub":      "pkg/sub.subf",            // from . import sub
	"pkg/rel.r_helper":   "pkg/__init__.helper",     // from . import helper
	"pkg/rel.r_reexport": "pkg/sub.subf",            // from . import subf (re-exported)
	"top/user.u_helper":  "top/lib/__init__.helper", // from .lib import helper
	"top/user.u_sub":     "top/lib/sub.subf",        // from .lib import sub
}

func assertPyCalls(t *testing.T, label string, topo *domain.Topology, want map[string]string) {
	t.Helper()
	for caller, callee := range want {
		res, ok := topo.Resources[caller]
		if !ok {
			t.Errorf("%s: caller %q missing", label, caller)
			continue
		}
		if !slices.Contains(res.Connections["calls"], callee) {
			t.Errorf("%s: %s should call %s, calls=%v", label, caller, callee, res.Connections["calls"])
		}
	}
}

// TestPythonPackageImportsResolve pins PY-2 across a full scan and an incremental one: every
// import that goes through a package's __init__.py resolves to the real resource, and editing
// the importer incrementally yields the same graph as a cold scan.
func TestPythonPackageImportsResolve(t *testing.T) {
	if !hasPython() {
		t.Skip("Python not available")
	}
	dir := t.TempDir()
	for rel, content := range pyPackageImportFiles {
		writeFileMk(t, dir, rel, content)
	}

	incrDB := filepath.Join(dir, "incr.db")
	mustRun(t, dir, "scan", "-root", dir, "-output", incrDB)
	first, err := helper.ReadDb(incrDB)
	if err != nil {
		t.Fatalf("read db: %v", err)
	}
	assertPyCalls(t, "full scan", first, pyPackageImportCalls)

	// Edit the importer, then take the incremental path over it.
	writeFileMk(t, dir, "app.py", pyPackageImportFiles["app.py"]+"\n\n# edited\n")
	mustRun(t, dir, "scan", "-root", dir, "-output", incrDB)
	incr, err := helper.ReadDb(incrDB)
	if err != nil {
		t.Fatalf("read incr db: %v", err)
	}
	assertPyCalls(t, "incremental scan", incr, pyPackageImportCalls)

	fullDB := filepath.Join(dir, "full.db")
	mustRun(t, dir, "scan", "-root", dir, "-output", fullDB)
	full, err := helper.ReadDb(fullDB)
	if err != nil {
		t.Fatalf("read full db: %v", err)
	}
	assertSameGraph(t, incr, full)
}
