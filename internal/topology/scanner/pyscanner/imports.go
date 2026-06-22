package pyscanner

import (
	"path/filepath"
	"strings"

	"aracne/internal/topology/python"
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
}

// resolveInternalImport maps one internal import to its target module using pure
// path math. ok is false when the import cannot be placed in the internal tree.
func resolveInternalImport(imp pyImport, importerFile, moduleRoot string) (pyImportTarget, bool) {
	rootBase := filepath.Base(moduleRoot)
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
			// Absolute from-import: the module starts with the root package name.
			parts := strings.Split(imp.Module, ".")
			if len(parts) == 0 || !strings.EqualFold(parts[0], rootBase) {
				return pyImportTarget{}, false
			}
			modSegs = parts[1:]
		}

		if imp.Module == "" {
			// `from .[.] import <submodule>`: the imported name is a submodule,
			// so the alias binds a module (no symbol).
			stem := filepath.Join(append([]string{baseDir}, strings.Split(imp.Name, ".")...)...)
			return pyImportTarget{ModulePath: pyModulePath(moduleRoot, stem+".py"), FilePath: stem + ".py"}, true
		}

		// `from [.]module import <symbol>`: the symbol lives in the module file.
		symbol := strings.TrimPrefix(imp.Name, imp.Module+".")
		stem := filepath.Join(append([]string{baseDir}, modSegs...)...)
		return pyImportTarget{ModulePath: pyModulePath(moduleRoot, stem+".py"), FilePath: stem + ".py", Symbol: symbol}, true
	}

	// Plain `import a.b.c`: Name is the dotted module; the alias binds the module.
	parts := strings.Split(imp.Name, ".")
	if len(parts) == 0 || !strings.EqualFold(parts[0], rootBase) {
		return pyImportTarget{}, false
	}
	stem := filepath.Join(append([]string{moduleRoot}, parts[1:]...)...)
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
		}
	}
	return out
}
