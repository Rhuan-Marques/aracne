package pyscanner

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// Python import classification used to ask a single question: does this import's first
// segment equal the BASENAME OF THE PROJECT DIRECTORY?
//
//	isInternal(importPath, moduleRoot) -> parts[0] == filepath.Base(moduleRoot)
//
// That assumes the checkout directory *is* the top-level package, which is true for almost
// no real project:
//
//   - src layouts (flask, and most packaging-modern projects) never match. `from flask.app
//     import X` in a checkout named `worktree` — or `flask-3.0.1`, or any fork name — is
//     classified EXTERNAL, so the import becomes a fake third-party dependency and the
//     call/usage edge is never drawn.
//   - even flat layouts resolve to the wrong path: with root dir `flask`, `import
//     flask.app` strips `flask` and looks for `<root>/app.py` rather than
//     `<root>/flask/app.py`.
//
// The visible damage was a whole class of missing Python edges, plus — once id-scheme 2
// removed the directory prefix that had been keeping the two namespaces apart — genuine ID
// collisions between a misclassified dependency (whose ID is the raw import string) and
// the real module-qualified symbol of the same name.
//
// This file replaces the guess with the actual rule Python uses: an absolute import
// resolves against a set of IMPORT ROOTS (what would be on sys.path), and a directory is a
// package when it holds `__init__.py`.

// importRoots is the discovered sys.path-like set for one project root.
type importRoots struct {
	// roots are directories an absolute import resolves against, shallowest first.
	roots []string
	// topLevel is the set of importable top-level names across all roots — package
	// directories (holding __init__.py) and bare module files.
	topLevel map[string]bool
}

var (
	importRootsCache sync.Map // moduleRoot -> *importRoots
)

// rootsFor returns the memoized import roots for a project root. Discovery walks the tree
// once per scan; every parse of every file then reuses it.
func rootsFor(moduleRoot string) *importRoots {
	if moduleRoot == "" {
		return &importRoots{topLevel: map[string]bool{}}
	}
	key, err := filepath.Abs(moduleRoot)
	if err != nil {
		key = moduleRoot
	}
	if cached, ok := importRootsCache.Load(key); ok {
		return cached.(*importRoots)
	}
	discovered := discoverImportRoots(key)
	actual, _ := importRootsCache.LoadOrStore(key, discovered)
	return actual.(*importRoots)
}

// ResetImportRootsCache clears the discovery cache. Tests that build a tree, scan it, then
// mutate the tree need this; a normal scan never does.
func ResetImportRootsCache() {
	importRootsCache.Range(func(k, _ any) bool {
		importRootsCache.Delete(k)
		return true
	})
}

// pyRootSkipDirs are never import roots and never contain first-party packages.
var pyRootSkipDirs = map[string]bool{
	".git": true, ".aracne": true, "node_modules": true, "__pycache__": true,
	".venv": true, "venv": true, ".tox": true, ".mypy_cache": true, ".pytest_cache": true,
	"site-packages": true, "dist": true, "build": true, ".eggs": true,
}

// discoverImportRoots finds the directories absolute imports resolve against.
//
// A directory holding `__init__.py` whose parent does NOT is a top-level package, and its
// parent is therefore an import root — that is exactly how `src/flask/__init__.py` makes
// `src/` a root and `flask` importable. The project root is always a root too, so a flat
// layout of bare modules keeps working.
func discoverImportRoots(absRoot string) *importRoots {
	out := &importRoots{topLevel: map[string]bool{}}
	rootSet := map[string]bool{absRoot: true}

	hasInit := func(dir string) bool {
		_, err := os.Stat(filepath.Join(dir, "__init__.py"))
		return err == nil
	}

	filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		name := d.Name()
		if path != absRoot && (pyRootSkipDirs[name] || strings.HasPrefix(name, ".")) {
			return filepath.SkipDir
		}
		// An ignored directory holds no import roots: its packages are never
		// parsed, so letting one name a root would only invent import targets
		// that resolve to nothing.
		if path != absRoot && domain.PathPruneDir(path) {
			return filepath.SkipDir
		}
		if path == absRoot || !hasInit(path) {
			return nil
		}
		parent := filepath.Dir(path)
		// A package whose parent is also a package is a SUBpackage; only the outermost
		// one names an import root.
		if hasInit(parent) {
			return filepath.SkipDir // everything below is a subpackage too
		}
		rootSet[parent] = true
		out.topLevel[name] = true
		return filepath.SkipDir // do not descend into the package itself
	})

	for r := range rootSet {
		out.roots = append(out.roots, r)
	}
	// Shallowest first, so `import x` prefers the outermost match.
	sort.Slice(out.roots, func(i, j int) bool {
		di, dj := strings.Count(out.roots[i], string(os.PathSeparator)), strings.Count(out.roots[j], string(os.PathSeparator))
		if di != dj {
			return di < dj
		}
		return out.roots[i] < out.roots[j]
	})

	// Bare top-level modules (`<root>/utils.py`) are importable as `utils`.
	for _, r := range out.roots {
		entries, err := os.ReadDir(r)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".py") {
				continue
			}
			stem := strings.TrimSuffix(e.Name(), ".py")
			if stem != "__init__" {
				out.topLevel[stem] = true
			}
		}
	}
	return out
}

// isInternalImport reports whether a dotted import path names first-party code.
func (ir *importRoots) isInternalImport(importPath string) bool {
	if ir == nil || importPath == "" {
		return false
	}
	first := importPath
	if i := strings.Index(first, "."); i >= 0 {
		first = first[:i]
	}
	return ir.topLevel[first]
}

// resolveStem maps a dotted module path to the file stem it lives at, trying each import
// root. Returns "" when no root contains it.
//
// The stem is returned WITHOUT an extension; callers try `<stem>.py` then
// `<stem>/__init__.py`, matching Python's own module-vs-package lookup.
func (ir *importRoots) resolveStem(dotted string) string {
	if ir == nil || dotted == "" {
		return ""
	}
	segs := strings.Split(dotted, ".")
	for _, root := range ir.roots {
		stem := filepath.Join(append([]string{root}, segs...)...)
		if _, err := os.Stat(stem + ".py"); err == nil {
			return stem
		}
		if _, err := os.Stat(filepath.Join(stem, "__init__.py")); err == nil {
			return stem
		}
	}
	return ""
}

// bestStem is resolveStem with a fallback: when nothing exists on disk (a module the
// scanner has not written yet, or a namespace package), place it under the first root that
// owns its top-level name so the ID is still stable and predictable.
func (ir *importRoots) bestStem(dotted string) (string, bool) {
	if stem := ir.resolveStem(dotted); stem != "" {
		return stem, true
	}
	if !ir.isInternalImport(dotted) {
		return "", false
	}
	segs := strings.Split(dotted, ".")
	for _, root := range ir.roots {
		if _, err := os.Stat(filepath.Join(root, segs[0])); err == nil {
			return filepath.Join(append([]string{root}, segs...)...), true
		}
		if _, err := os.Stat(filepath.Join(root, segs[0]+".py")); err == nil {
			return filepath.Join(append([]string{root}, segs...)...), true
		}
	}
	return "", false
}
