package pyscanner

import (
	"os"
	"path/filepath"
	"testing"
)

// Import classification decides whether `from flask.app import X` draws an internal edge
// or invents a third-party dependency. It used to answer that by comparing the import's
// first segment to the project DIRECTORY's name, which is wrong for every layout that is
// not "the checkout is the package" — see importroots.go.

func write(t *testing.T, dir, rel, body string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// srcLayout builds the shape that used to fail completely: the package lives under src/,
// and the checkout directory is named something unrelated to it.
func srcLayout(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "some-checkout-name")
	write(t, root, "src/flask/__init__.py", "")
	write(t, root, "src/flask/app.py", "class Flask:\n    pass\n")
	write(t, root, "src/flask/helpers.py", "from flask.app import Flask\n")
	ResetImportRootsCache()
	return root
}

func TestSrcLayoutImportsAreInternal(t *testing.T) {
	root := srcLayout(t)
	if !isInternal("flask.app", root) {
		t.Fatal("`flask.app` must be internal in a src/ layout; it was classified as a " +
			"third-party dependency because the checkout is not named 'flask'")
	}
	if !isInternal("flask", root) {
		t.Fatal("the top-level package itself must be internal")
	}
}

func TestGenuinelyExternalImportsStayExternal(t *testing.T) {
	// The fix must not make everything internal — that would invent edges instead of
	// missing them.
	root := srcLayout(t)
	for _, ext := range []string{"os", "werkzeug.routing", "numpy", "click"} {
		if isInternal(ext, root) {
			t.Fatalf("%q is third-party and must stay external", ext)
		}
	}
}

func TestFlatLayoutResolvesToTheRightFile(t *testing.T) {
	// The old code stripped the first segment and looked under the root, so `import
	// pkg.mod` in a checkout named `pkg` resolved to `<root>/mod.py` rather than
	// `<root>/pkg/mod.py`.
	root := filepath.Join(t.TempDir(), "pkg")
	write(t, root, "pkg/__init__.py", "")
	write(t, root, "pkg/mod.py", "X = 1\n")
	ResetImportRootsCache()

	stem := rootsFor(root).resolveStem("pkg.mod")
	want := filepath.Join(root, "pkg", "mod")
	if stem != want {
		t.Fatalf("resolveStem(pkg.mod) = %q, want %q", stem, want)
	}
}

func TestPackageDirectoryResolvesViaInit(t *testing.T) {
	root := srcLayout(t)
	stem := rootsFor(root).resolveStem("flask")
	if want := filepath.Join(root, "src", "flask"); stem != want {
		t.Fatalf("resolveStem(flask) = %q, want the package dir %q", stem, want)
	}
}

func TestSubpackagesDoNotBecomeImportRoots(t *testing.T) {
	// Only the OUTERMOST package names a root. If `src/flask/json/` were treated as one,
	// `import json` would resolve to first-party code and shadow the stdlib.
	root := srcLayout(t)
	write(t, root, "src/flask/json/__init__.py", "")
	ResetImportRootsCache()

	if isInternal("json", root) {
		t.Fatal("a subpackage must not become importable as a top-level name")
	}
	if !isInternal("flask.json", root) {
		t.Fatal("the subpackage must still be reachable through its parent")
	}
}

func TestBareTopLevelModuleIsImportable(t *testing.T) {
	root := filepath.Join(t.TempDir(), "proj")
	write(t, root, "utils.py", "def f():\n    pass\n")
	ResetImportRootsCache()
	if !isInternal("utils", root) {
		t.Fatal("a bare module at the project root is importable as its stem")
	}
}

func TestVendoredAndCacheDirsAreNotRoots(t *testing.T) {
	// A vendored package inside .venv/ or node_modules/ must not make its name look
	// first-party.
	root := filepath.Join(t.TempDir(), "proj")
	write(t, root, "src/app/__init__.py", "")
	write(t, root, ".venv/lib/site-packages/requests/__init__.py", "")
	write(t, root, "node_modules/thing/pkg/__init__.py", "")
	ResetImportRootsCache()

	if !isInternal("app", root) {
		t.Fatal("the real package must be internal")
	}
	for _, vendored := range []string{"requests", "thing", "pkg"} {
		if isInternal(vendored, root) {
			t.Fatalf("%q comes from a vendored/cache dir and must not be first-party", vendored)
		}
	}
}

func TestCacheIsInvalidatedWhenAPackageAppears(t *testing.T) {
	// `arac serve` is long-lived. A package created after startup must be picked up, or
	// the process classifies against a stale sys.path for its whole lifetime.
	root := filepath.Join(t.TempDir(), "proj")
	write(t, root, "src/app/__init__.py", "")
	ResetImportRootsCache()
	if isInternal("later", root) {
		t.Fatal("precondition: `later` should not exist yet")
	}

	write(t, root, "src/later/__init__.py", "")
	if isInternal("later", root) {
		t.Fatal("precondition: the cache should still be warm and unaware of it")
	}
	ResetImportRootsCache() // what UpdateFile does when an __init__.py changes
	if !isInternal("later", root) {
		t.Fatal("after invalidation the new package must be visible")
	}
}
