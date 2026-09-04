package tests_test

// Python edge-case ISOLATED suite.
//
// These cases are crash-prone (duplicate resource/connection IDs that abort the
// write), require a bespoke minimal package layout, or depend on cross-module
// name collisions. Each builds a self-contained mini-corpus in a temp dir so a
// crash only fails that one test — never the shared testing_ground scan.
//
// As in the corpus suite, assertions encode the IDEAL behavior and are left red
// where the scanner falls short. Relative imports (Level>0) are used so the
// sibling modules are treated as internal regardless of the temp root name.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// Q is the resource-ID prefix for the isolated mini-corpora (root leaf "proj").
// Under id-scheme 2 a module path is repo-relative, so there is no project-root leaf
// in an ID any more. Kept as an empty constant rather than deleted so the Q+"..."
// call sites stay readable as "the ID of <that module>".
const Q = ""

// isolatedRoot writes the given rel-path -> content files under <tmp>/proj and
// returns that root.
func isolatedRoot(t *testing.T, files map[string]string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "proj")
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		writeFile(t, p, content)
	}
	return root
}

// scanIsolatedOK scans the mini-corpus and fails (red) if the scan crashes —
// encoding "this construct should not abort the topology write". Returns the
// topology when the scan succeeds.
func scanIsolatedOK(t *testing.T, files map[string]string) *domain.Topology {
	t.Helper()
	root := isolatedRoot(t, files)
	db := filepath.Join(t.TempDir(), "iso.db")
	out, err := runLtp(t, root, "scan", "-all", "-root", root, "-output", db)
	if err != nil {
		t.Fatalf("IDEAL: scan should not crash on this construct, but it did:\n%s", out)
	}
	return readTopo(t, db)
}

// PYI1: @overload — repeated function name. IDEAL: collapses to one logical
// function resource without aborting the write on duplicate IDs.
func TestPyIso_Overload(t *testing.T) {
	t.Parallel()
	topo := scanIsolatedOK(t, map[string]string{
		"m.py": `from typing import overload


class A:
    pass


class B:
    pass


@overload
def parse(x: int) -> A: ...
@overload
def parse(x: str) -> B: ...
def parse(x):
    return A() if isinstance(x, int) else B()
`,
	})
	assertResPresent(t, topo, "iso", Q+"m.parse")
}

// PYI2: property getter/setter/deleter share one name. IDEAL: one property
// resource, no duplicate-ID write abort.
func TestPyIso_PropertySetterDeleter(t *testing.T) {
	t.Parallel()
	topo := scanIsolatedOK(t, map[string]string{
		"m.py": `class Temperature:
    def __init__(self) -> None:
        self._c = 0.0

    @property
    def celsius(self) -> float:
        return self._c

    @celsius.setter
    def celsius(self, value: float) -> None:
        self._c = value

    @celsius.deleter
    def celsius(self) -> None:
        self._c = 0.0
`,
	})
	assertResPresent(t, topo, "iso", Q+"m.Temperature.celsius")
}

// PYI3: `from .m import *` then use a star-imported symbol. IDEAL: the call
// resolves to the source module's function.
func TestPyIso_StarImport(t *testing.T) {
	t.Parallel()
	topo := scanIsolatedOK(t, map[string]string{
		"__init__.py": "",
		"m.py":        "def helper() -> int:\n    return 1\n",
		"use.py": `from .m import *


def run() -> int:
    return helper()
`,
	})
	// GAP: star-imported helper not resolved.
	assertHasConn(t, topo, "iso", Q+"use.run", connCalls, Q+"m.helper")
}

// PYI4: circular imports a <-> b. IDEAL: both directions resolve and the scan
// doesn't crash or drop edges.
func TestPyIso_CircularImport(t *testing.T) {
	t.Parallel()
	topo := scanIsolatedOK(t, map[string]string{
		"__init__.py": "",
		"a.py": `from .b import beta


def alpha() -> int:
    return beta()
`,
		"b.py": `from .a import alpha


def gamma() -> int:
    return alpha()


def beta() -> int:
    return 2
`,
	})
	assertHasConn(t, topo, "iso", Q+"a.alpha", connCalls, Q+"b.beta")
	assertHasConn(t, topo, "iso", Q+"b.gamma", connCalls, Q+"a.alpha")
}

// PYI5: two modules define a class named User; a consumer imports ONE of them
// and annotates a parameter with it. IDEAL: the method call resolves to the
// imported class, not to the other same-named class via a global fallback.
func TestPyIso_CrossModuleNameCollision(t *testing.T) {
	t.Parallel()
	topo := scanIsolatedOK(t, map[string]string{
		"__init__.py": "",
		"api.py": `class User:
    def from_api(self) -> str:
        return "api"
`,
		"db.py": `class User:
    def from_db(self) -> str:
        return "db"
`,
		"consumer.py": `from .db import User


def handle(u: User) -> str:
    return u.from_db()
`,
	})
	assertHasConn(t, topo, "iso", Q+"consumer.handle", connCalls, Q+"db.User.from_db")
	assertNoConn(t, topo, "iso", Q+"consumer.handle", connCalls, Q+"api.User.from_db")
}

// PYI6: a module-level name bound in two sibling control-flow branches. This is
// the documented duplicate-resource-ID bug. IDEAL: the scan does not crash.
func TestPyIso_SiblingBranchNameBinding(t *testing.T) {
	t.Parallel()
	scanIsolatedOK(t, map[string]string{
		"m.py": `import os

if os.environ.get("X"):
    SETTING = "a"
else:
    SETTING = "b"


def get() -> str:
    return SETTING
`,
	})
}

// PYI7: deep relative import `from .. import top_helper`. IDEAL: an
// imports_module edge to the parent package and a resolved call.
func TestPyIso_DeepRelativeImport(t *testing.T) {
	t.Parallel()
	topo := scanIsolatedOK(t, map[string]string{
		"pkg/__init__.py":     "def top_helper() -> int:\n    return 1\n",
		"pkg/sub/__init__.py": "",
		"pkg/sub/child.py": `from .. import top_helper


def use() -> int:
    return top_helper()
`,
	})
	childID := findIDBySuffix(t, topo, "pkg/sub/child.py")
	assertConnTargetSuffix(t, topo, "iso", childID, "imports_module", "pkg/__init__.py")
	// GAP candidate: the deep-relative call should resolve.
	assertHasConn(t, topo, "iso", Q+"pkg/sub/child.use", connCalls, Q+"pkg/__init__.top_helper")
}

// PYI8: a conditional/try import. IDEAL: the dependency is still recorded.
func TestPyIso_ConditionalImport(t *testing.T) {
	t.Parallel()
	topo := scanIsolatedOK(t, map[string]string{
		"m.py": `try:
    import json
except ImportError:  # pragma: no cover
    json = None


def dump(x) -> str:
    return json.dumps(x)
`,
	})
	mID := findIDBySuffix(t, topo, "/m.py")
	assertHasConn(t, topo, "iso", mID, "imports_dependency", "json")
}
