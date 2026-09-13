package pyscanner

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/topology/python"
)

// pyImportTarget describes the module a single internal import points at,
// computed from path math and the files on disk (no topology lookup). It powers
// both the file->file import edges and cross-file symbol resolution now that
// Python IDs are module-qualified.
type pyImportTarget struct {
	// ModulePath is the module-qualified ID prefix of the target module
	// (e.g. "pkg/shapes"), used to build canonical symbol IDs. A package keys its
	// resources under its __init__.py, so a target that is a package reads
	// "pkg/__init__" (see stemModulePath).
	ModulePath string
	// FilePath is the candidate absolute path of the target as a module file
	// (".../shapes.py"); existence is not verified at parse time, and
	// findModuleFile also tries the package form (".../shapes/__init__.py").
	FilePath string
	// Symbol is the imported name when the alias binds a member of the target
	// module (`from .shapes import Circle` -> "Circle"); it is "" when the alias
	// binds a module itself (`from . import shapes`, `import pkg.shapes`).
	Symbol string
	// SubModulePath is set for `from P import X` / `from .P import X` when X is a
	// submodule of package P (P/X.py or P/X/__init__.py exists): the ID prefix of
	// that submodule, since the alias then binds the module rather than a name
	// defined in P/__init__.py. "" for every other import form.
	SubModulePath string
	// PkgModulePath/PkgSymbol describe the package-symbol fallback for a
	// `from . import X` / `from .. import X` import: X is usually a submodule
	// (ModulePath/FilePath above) but may instead be a name defined in the
	// package's __init__.py. PkgModulePath is that __init__ module's ID prefix
	// and PkgSymbol is X. Both are "" for every other import form.
	PkgModulePath string
	PkgSymbol     string
}

// pySymbolRef names a symbol by the module expected to define it.
type pySymbolRef struct {
	Module string
	Name   string
}

// symbolRefs returns the symbols the alias itself may name, in the order Python
// looks: the imported name in its module (`from pkg import helper` ->
// pkg/__init__.helper), then, for `from . import X`, X in the package's __init__.py.
func (t pyImportTarget) symbolRefs() []pySymbolRef {
	var out []pySymbolRef
	if t.Symbol != "" {
		out = append(out, pySymbolRef{Module: t.ModulePath, Name: t.Symbol})
	}
	if t.PkgSymbol != "" {
		out = append(out, pySymbolRef{Module: t.PkgModulePath, Name: t.PkgSymbol})
	}
	return out
}

// moduleRefs returns the ID prefixes of the modules the alias may bind, so that
// `alias.name` is `name` inside one of them: the imported module itself
// (`import pkg`, `from . import sub`), or the submodule a from-import names
// (`from pkg import sub`).
func (t pyImportTarget) moduleRefs() []string {
	var out []string
	if t.Symbol == "" && t.ModulePath != "" {
		out = append(out, t.ModulePath)
	}
	if t.SubModulePath != "" {
		out = append(out, t.SubModulePath)
	}
	return out
}

// resolveInternalImport maps one internal import to its target module using pure
// path math. ok is false when the import cannot be placed in the internal tree.
func resolveInternalImport(imp pyImport, importerFile, moduleRoot string) (pyImportTarget, bool) {
	roots := rootsFor(moduleRoot)
	isFrom := imp.Module != "" || imp.Level > 0

	if isFrom {
		// Only the relative branch below uses this; the absolute one returns before it is
		// read, which is why seeding it with moduleRoot was a dead assignment.
		var baseDir string
		var modSegs []string
		if imp.Level > 0 {
			// Relative import: level 1 is the importer's own directory, each
			// extra level ascends one parent.
			baseDir = filepath.Dir(importerFile)
			for i := 1; i < imp.Level; i++ {
				baseDir = filepath.Dir(baseDir)
			}
			if imp.Module != "" {
				modSegs = strings.Split(imp.Module, ".")
			}
		} else {
			// Absolute from-import: resolve the dotted module against the import roots
			// (what would be on sys.path) rather than assuming its first segment is the
			// project directory's name.
			stem, ok := roots.bestStem(imp.Module)
			if !ok {
				return pyImportTarget{}, false
			}
			symbol := strings.TrimPrefix(imp.Name, imp.Module+".")
			return pyImportTarget{
				ModulePath:    stemModulePath(moduleRoot, stem),
				FilePath:      stem + ".py",
				Symbol:        symbol,
				SubModulePath: submodulePath(moduleRoot, stem, symbol),
			}, true
		}

		if imp.Module == "" {
			// `from .[.] import <name>`: name is usually a submodule, so the alias
			// binds a module (no symbol). But name may instead be a symbol defined
			// in the package's __init__.py; record that fallback so a consumer can
			// resolve it when the submodule file doesn't exist.
			stem := filepath.Join(append([]string{baseDir}, strings.Split(imp.Name, ".")...)...)
			pkgInit := filepath.Join(baseDir, "__init__.py")
			return pyImportTarget{
				ModulePath:    stemModulePath(moduleRoot, stem),
				FilePath:      stem + ".py",
				PkgModulePath: pyModulePath(moduleRoot, pkgInit),
				PkgSymbol:     imp.Name,
			}, true
		}

		// `from [.]module import <symbol>`: the symbol lives in the module file, or
		// in the package's __init__.py -- or is a submodule of that package.
		symbol := strings.TrimPrefix(imp.Name, imp.Module+".")
		stem := filepath.Join(append([]string{baseDir}, modSegs...)...)
		return pyImportTarget{
			ModulePath:    stemModulePath(moduleRoot, stem),
			FilePath:      stem + ".py",
			Symbol:        symbol,
			SubModulePath: submodulePath(moduleRoot, stem, symbol),
		}, true
	}

	// Plain `import a.b.c`: Name is the dotted module; the alias binds the module.
	stem, ok := roots.bestStem(imp.Name)
	if !ok {
		return pyImportTarget{}, false
	}
	return pyImportTarget{ModulePath: stemModulePath(moduleRoot, stem), FilePath: stem + ".py"}, true
}

// stemModulePath is the ID prefix of the module at stem (an absolute path without
// extension). A package keys its resources under its __init__.py, so when stem is a
// package directory rather than a module file the prefix is "<stem>/__init__" --
// `from pkg import helper` has to reach pkg/__init__.helper, not pkg.helper. The
// module-file form wins when both exist, as in findModuleFile, and is kept when
// neither exists yet so the candidate ID stays predictable.
func stemModulePath(moduleRoot, stem string) string {
	if !pathExists(stem + ".py") {
		if pkgInit := filepath.Join(stem, "__init__.py"); pathExists(pkgInit) {
			return pyModulePath(moduleRoot, pkgInit)
		}
	}
	return pyModulePath(moduleRoot, stem+".py")
}

// submodulePath is the ID prefix of submodule name of the package at stem, or ""
// when no such module exists on disk. It is what lets `from pkg import sub` bind
// pkg/sub.py (or pkg/sub/__init__.py) when sub is not a name defined in
// pkg/__init__.py.
func submodulePath(moduleRoot, stem, name string) string {
	if name == "" || name == "*" || strings.Contains(name, ".") {
		return ""
	}
	sub := filepath.Join(stem, name)
	if pathExists(sub + ".py") {
		return pyModulePath(moduleRoot, sub+".py")
	}
	if pkgInit := filepath.Join(sub, "__init__.py"); pathExists(pkgInit) {
		return pyModulePath(moduleRoot, pkgInit)
	}
	return ""
}

// pathExists reports whether p exists on disk.
func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// findModuleFile resolves a module file stem (path without extension) to a
// module id present in the topology, trying `<stem>.py` then
// `<stem>/__init__.py` (the package form).
func findModuleFile(stem string, gt *python.PythonTopology) (python.ModuleID, bool) {
	if candidate := python.ModuleID(stem + ".py"); moduleExists(candidate, gt) {
		return candidate, true
	}
	if candidate := python.ModuleID(filepath.Join(stem, "__init__.py")); moduleExists(candidate, gt) {
		return candidate, true
	}
	return "", false
}

// Checks whether a module exists in the Python topology.
func moduleExists(id python.ModuleID, gt *python.PythonTopology) bool {
	_, ok := gt.Modules[id]
	return ok
}

// resolvePyModuleImports resolves a file's internal imports to the specific
// module files they pull in, returning the target module ids (deduped). Targets
// that don't resolve to an existing module file are skipped.
func resolvePyModuleImports(pr *ParseResult, gt *python.PythonTopology) []python.ModuleID {
	var out []python.ModuleID
	seen := make(map[python.ModuleID]bool)
	emit := func(mid python.ModuleID) {
		if mid != "" && !seen[mid] {
			seen[mid] = true
			out = append(out, mid)
		}
	}
	for _, imp := range pr.InternalImportRecords {
		tgt, ok := resolveInternalImport(imp, pr.FileID, pr.ModuleRoot)
		if !ok {
			continue
		}
		stem := strings.TrimSuffix(tgt.FilePath, ".py")
		// Prefer the symbol-as-submodule file when it exists (e.g.
		// `from .pkg import submod` where submod is a subpackage/module).
		if tgt.Symbol != "" {
			if mid, ok := findModuleFile(filepath.Join(stem, tgt.Symbol), gt); ok {
				emit(mid)
				continue
			}
		}
		if mid, ok := findModuleFile(stem, gt); ok {
			emit(mid)
			continue
		}
		// `from . import X` where X is not a submodule but a symbol defined in
		// the package's __init__.py: the import edge points at that package.
		if tgt.PkgModulePath != "" {
			if mid, ok := findModuleFile(filepath.Dir(stem), gt); ok {
				emit(mid)
			}
		}
	}
	return out
}
