package goscanner

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	"github.com/Rhuan-Marques/aracne/internal/topology/golang"
)

// syntheticModulePath is the module a .go file belongs to when no go.mod encloses it.
//
// Go itself named such code "_/<dir>" in GOPATH days, and the same spelling works here: it is
// stable when the project moves (unlike the root's own name), it cannot collide with a real
// module path or with the dotted module IDs Python and JavaScript mint, and no real import path
// starts with it -- so every import in such a file is classified external, which is right for
// code that has no module to import from.
const syntheticModulePath = "_"

// moduleLayout says which Go module each directory under a scan root belongs to.
//
// WHY THERE IS MORE THAN ONE. The scanner used to read <root>/go.mod and fail outright without
// it, and that failure took every other language down with it: a monorepo with its module in
// backend/, a go.work root with a/go.mod and b/go.mod, or a Python project with one stray
// tools/gen.go got no topology at all. A Go file belongs to its NEAREST enclosing go.mod, and
// its package ID is that module's path plus the directory relative to that module's root --
// which is the import path Go itself would use, so calls between the modules resolve.
//
// A root go.mod with no go.mod below it keeps the old single-module behaviour byte for byte:
// the corpus tests pin those IDs. A nested go.mod under it is its own module; see
// loadModuleLayout.
type moduleLayout struct {
	root  string            // absolute scan root
	dirs  map[string]string // absolute module directory -> declared module path
	paths []string          // every declared module path under root, sorted
}

// goModule is what ParseFile needs to know about the module a directory belongs to.
type goModule struct {
	modulePath string
	pkgPath    golang.PackagePath
	// siblings are the other modules declared under the same root. An import of one of them is
	// code this scan indexes, not a dependency -- the go.work case.
	siblings []string
}

// loadModuleLayout finds the modules under absRoot: every go.mod below the root (outside the
// directories the scan prunes) declares a module, and one that declares nothing is ignored so
// its files fall to the next module up.
//
// A root go.mod must parse -- it is the module the whole tree belongs to by default -- but it
// does not end the search. A go.mod further down starts a SEPARATE module, as it does for the
// go tool (GOROOT/src is `std` with `cmd` nested inside it, and a tools/ or examples/ module
// under a repo root is common): its files are imported as `<nested module>/...`, not as a
// subdirectory of the outer module, and naming them under the outer module minted ids nothing
// can import and turned every import inside the nested module into an external dependency.
// A tree with no nested go.mod gets exactly the single-module layout it always had.
func loadModuleLayout(absRoot string) (*moduleLayout, error) {
	l := &moduleLayout{root: absRoot, dirs: make(map[string]string)}
	if _, err := os.Stat(filepath.Join(absRoot, "go.mod")); err == nil {
		if _, err := readModulePath(absRoot); err != nil {
			return nil, err
		}
	}
	filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipGoDir(absRoot, path, d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "go.mod" {
			return nil
		}
		dir := filepath.Dir(path)
		if modulePath, err := readModulePath(dir); err == nil {
			l.dirs[dir] = modulePath
			l.paths = append(l.paths, modulePath)
		}
		return nil
	})
	sort.Strings(l.paths)
	return l, nil
}

// forDir resolves the module and package of a directory: its nearest enclosing go.mod, or the
// synthetic module rooted at the scan root when there is none.
func (l *moduleLayout) forDir(dir string) goModule {
	modDir, modulePath := l.root, syntheticModulePath
	for d := dir; ; {
		if mp, ok := l.dirs[d]; ok {
			modDir, modulePath = d, mp
			break
		}
		parent := filepath.Dir(d)
		if d == l.root || parent == d {
			break
		}
		d = parent
	}
	mod := goModule{
		modulePath: modulePath,
		pkgPath:    getPackagePath(modDir, dir, modulePath),
	}
	for _, p := range l.paths {
		if p != modulePath {
			mod.siblings = append(mod.siblings, p)
		}
	}
	return mod
}

// skipGoDir reports whether a Go walk prunes the directory at path. The root is never pruned by
// its own basename: WalkDir never visits the root's ancestors, so only the root itself can match
// on a name it did not choose.
func skipGoDir(root, path, name string) bool {
	if path == root {
		return false
	}
	if name == "vendor" || name == ".git" || name == "node_modules" || strings.HasPrefix(name, ".") {
		return true
	}
	return domain.PathPruneDir(path)
}

// hasGoSource reports whether collectGoFiles would find anything under root, stopping at the
// first hit.
func hasGoSource(root string) bool {
	found := false
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipGoDir(root, path, d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if isGoSourceName(d.Name()) && !domain.PathHidden(path) {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// isGoSourceName reports whether a file name is a non-test Go source file.
func isGoSourceName(name string) bool {
	return strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
}

// readModulePath reads the path the module directive of <root>/go.mod declares.
func readModulePath(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("go.mod not found at %s: %w", root, err)
	}
	if mp := parseModuleDirective(data); mp != "" {
		return mp, nil
	}
	return "", fmt.Errorf("no module declaration in go.mod")
}

// parseModuleDirective returns the module path a go.mod declares, or "" when it declares none.
//
// Every Go resource ID starts with this string, so it has to be the path itself and nothing
// else. go.mod allows a trailing // comment on the line (a "// Deprecated: ..." notice is the
// common one), a quoted path, and the parenthesised block form; taking the rest of the line
// verbatim turned `module example.com/c // my module` into a module named with its comment,
// and `module "example.com/q"` into one named with its quotes -- and then no import path
// matched the module, so every internal import was filed as a dependency.
func parseModuleDirective(data []byte) string {
	inBlock := false
	for _, line := range strings.Split(string(data), "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if inBlock {
			switch line {
			case "":
				continue
			case ")":
				inBlock = false
				continue
			}
			return unquoteModulePath(line)
		}
		rest, ok := strings.CutPrefix(line, "module")
		if !ok || rest == "" || !strings.ContainsRune(" \t\"`(", rune(rest[0])) {
			continue
		}
		rest = strings.TrimSpace(rest)
		if rest == "(" {
			inBlock = true
			continue
		}
		if mp := unquoteModulePath(rest); mp != "" {
			return mp
		}
	}
	return ""
}

// unquoteModulePath reads the single module-path token at the start of s, quoted or not.
func unquoteModulePath(s string) string {
	if s[0] == '"' || s[0] == '`' {
		if end := strings.IndexByte(s[1:], s[0]); end >= 0 {
			if mp, err := strconv.Unquote(s[:end+2]); err == nil {
				return mp
			}
		}
		return strings.Trim(s, "\"`")
	}
	if fields := strings.Fields(s); len(fields) > 0 {
		return fields[0]
	}
	return ""
}

// moduleDirOf returns the directory of the module a file belongs to. pkgPath is the module path
// plus the file's directory relative to the module root (getPackagePath), so the module root is
// the file's directory with that relative part taken off again. It reports false when pkgPath
// is not under modulePath.
func moduleDirOf(fileDir string, pkgPath golang.PackagePath, modulePath string) (string, bool) {
	rel, ok := strings.CutPrefix(string(pkgPath), modulePath)
	if !ok || (rel != "" && rel[0] != '/') {
		return "", false
	}
	dir := filepath.Clean(fileDir)
	rel = filepath.FromSlash(strings.TrimPrefix(rel, "/"))
	if rel == "" {
		return dir, true
	}
	suffix := string(filepath.Separator) + rel
	if !strings.HasSuffix(dir, suffix) {
		return "", false
	}
	return strings.TrimSuffix(dir, suffix), true
}

// importName is the name an import without an explicit alias binds in the importing file.
//
// Go binds the imported package's DECLARED name, not the last element of its path. They usually
// agree, but not for a versioned directory (api/v2 declaring package api), a directory whose
// name is not an identifier (hyphen-pkg declaring package hyphen), or any deliberate mismatch --
// and keying the import map by the path element made every call through such an import resolve
// to nothing. For an import of this file's own module the declaring directory is on disk, so its
// package clause is read; anything else keeps the path element, which is all there is to go on.
func importName(impPath, modDir, modulePath string) string {
	last := impPath[strings.LastIndex(impPath, "/")+1:]
	if modDir == "" || !isInternalImport(impPath, modulePath) {
		return last
	}
	rel := strings.TrimPrefix(strings.TrimPrefix(impPath, modulePath), "/")
	if name := declaredPackageName(filepath.Join(modDir, filepath.FromSlash(rel)), last); name != "" {
		return name
	}
	return last
}

// pkgNameEntry is one cached declaredPackageName answer, valid while the file it was read from
// is unchanged.
type pkgNameEntry struct {
	name    string
	file    string
	modTime time.Time
	size    int64
}

// pkgNameCache maps a package directory to its pkgNameEntry. Every file importing a package asks
// for the same directory, and a scan parses each file twice, so without it the same package
// clause would be read once per import site.
var pkgNameCache sync.Map

// declaredPackageName returns the package name the Go files in dir declare, or "" when dir holds
// none. preferred -- the import path's last element -- wins whenever any file declares it, so the
// ordinary package costs one package clause. A "main" clause is skipped: an importable package
// cannot be main, and a `//go:build ignore` generator sitting next to the package usually is.
// Files Go itself ignores (a leading "_" or ".") are skipped too.
func declaredPackageName(dir, preferred string) string {
	if v, ok := pkgNameCache.Load(dir); ok {
		e := v.(pkgNameEntry)
		if fi, err := os.Stat(e.file); err == nil && fi.ModTime().Equal(e.modTime) && fi.Size() == e.size {
			return e.name
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var first pkgNameEntry
	for _, de := range entries {
		name := de.Name()
		if de.IsDir() || !isGoSourceName(name) || strings.HasPrefix(name, "_") || strings.HasPrefix(name, ".") {
			continue
		}
		path := filepath.Join(dir, name)
		fi, err := os.Stat(path)
		if err != nil {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.PackageClauseOnly)
		if err != nil || f.Name == nil || f.Name.Name == "main" {
			continue
		}
		e := pkgNameEntry{name: f.Name.Name, file: path, modTime: fi.ModTime(), size: fi.Size()}
		if e.name == preferred {
			pkgNameCache.Store(dir, e)
			return e.name
		}
		if first.name == "" {
			first = e
		}
	}
	if first.name == "" {
		return ""
	}
	pkgNameCache.Store(dir, first)
	return first.name
}
