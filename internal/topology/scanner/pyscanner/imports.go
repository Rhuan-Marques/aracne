package pyscanner

import (
	"path/filepath"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/topology/python"
)

// pyImportTarget describes the module a single internal import points at,
// computed purely from path math (no topology lookup). It powers both the
// file->file import edges and cross-file symbol resolution now that Python IDs
// are module-qualified.
type pyImportTarget struct {
	// ModulePath is the module-qualified ID prefix of the target file
	// (e.g. "proj/pkg/shapes"), used to build canonical symbol IDs.
	ModulePath string
	// FilePath is the candidate absolute path of the target module file
	// (".../shapes.py"); existence is not verified at parse time.
	FilePath string
	// Symbol is the imported name when the alias binds a member of the target
	// module (`from .shapes import Circle` -> "Circle"); it is "" when the alias
	// binds a module itself (`from . import shapes`, `import pkg.shapes`).
	Symbol string
	// PkgModulePath/PkgSymbol describe the package-symbol fallback for a
	// `from . import X` / `from .. import X` import: X is usually a submodule
	// (ModulePath/FilePath above) but may instead be a name defined in the
	// package's __init__.py. PkgModulePath is that __init__ module's ID prefix
	// and PkgSymbol is X. Both are "" for every other import form.
	PkgModulePath string
	PkgSymbol     string
}

// resolveInternalImport maps one internal import to its target module using pure
// path math. ok is false when the import cannot be placed in the internal tree.
func resolveInternalImport(imp pyImport, importerFile, moduleRoot string) (pyImportTarget, bool) {
	roots := rootsFor(moduleRoot)
	isFrom := imp.Module != "" || imp.Level > 0

	if isFrom {
		baseDir := moduleRoot
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
				ModulePath: pyModulePath(moduleRoot, stem+".py"),
				FilePath:   stem + ".py",
				Symbol:     symbol,
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
				ModulePath:    pyModulePath(moduleRoot, stem+".py"),
				FilePath:      stem + ".py",
				PkgModulePath: pyModulePath(moduleRoot, pkgInit),
				PkgSymbol:     imp.Name,
			}, true
		}

		// `from [.]module import <symbol>`: the symbol lives in the module file.
		symbol := strings.TrimPrefix(imp.Name, imp.Module+".")
		stem := filepath.Join(append([]string{baseDir}, modSegs...)...)
		return pyImportTarget{ModulePath: pyModulePath(moduleRoot, stem+".py"), FilePath: stem + ".py", Symbol: symbol}, true
	}

	// Plain `import a.b.c`: Name is the dotted module; the alias binds the module.
	stem, ok := roots.bestStem(imp.Name)
	if !ok {
		return pyImportTarget{}, false
	}
	return pyImportTarget{ModulePath: pyModulePath(moduleRoot, stem+".py"), FilePath: stem + ".py"}, true
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
