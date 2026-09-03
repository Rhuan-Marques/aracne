package pyscanner

import (
	"sort"
	"strings"

	"aracne/internal/topology/contract"
	"aracne/internal/topology/python"
)

// Extracts function dependencies by resolving references and calls within the function body.
func analyzeFunctionBody(body *pyFunc, pr *ParseResult, gt *python.PythonTopology, funcInput []python.VariableDefinition, receiverClass *python.ClassID) map[python.ConnectionKind][]string {
	conn := make(map[python.ConnectionKind][]string)

	if body == nil {
		return conn
	}

	// current is the call being resolved, or nil outside the call loop. Reading it in add()
	// records the argument shape at every ConnCalls site without threading it through each.
	var current *pyBodyCall
	var callSites []string
	add := func(kind python.ConnectionKind, id string) {
		// Recorded BEFORE the edge dedupe below returns. Two calls to the same callee are
		// one edge but two call sites, and they are exactly the interesting case: a
		// function called once correctly and once with the wrong arguments must still
		// report the wrong one.
		if kind == python.ConnCalls && current != nil {
			rec := contract.EncodeCallSite(pyCallSite(id, current))
			if rec != "" && !containsPyRec(callSites, rec) {
				callSites = append(callSites, rec)
			}
		}
		ids := conn[kind]
		for _, existing := range ids {
			if existing == id {
				return
			}
		}
		conn[kind] = append(ids, id)
	}

	resolveBodyReferences(body, pr, gt, add)

	bodyCalls := body.BodyCalls
	if bodyCalls == nil {
		bodyCalls = []pyBodyCall{}
	}
	bodyAssigns := body.BodyAssign
	if bodyAssigns == nil {
		bodyAssigns = []pyBodyAssign{}
	}
	resolveBodyCallRefs(bodyCalls, bodyAssigns, pr, gt, add, funcInput, receiverClass, &current)

	// Sorted: the at-scale suite compares connection sets across scan modes byte-for-byte.
	if len(callSites) > 0 {
		sort.Strings(callSites)
		conn[python.ConnectionKind(contract.CallSitesConn)] = callSites
	}

	return conn
}

// Resolves method and function calls within a function body by mapping variable types and matching calls to class methods or global functions.
func resolveBodyCallRefs(bodyCalls []pyBodyCall, bodyAssigns []pyBodyAssign, pr *ParseResult, gt *python.PythonTopology, add func(kind python.ConnectionKind, id string), funcInput []python.VariableDefinition, receiverClass *python.ClassID, cur **pyBodyCall) {
	varTypeMap := make(map[string]python.ClassID)

	// Methods reach sibling methods through the receiver; map self/cls to the
	// enclosing class so self.method() / cls.method() resolve to Calls edges.
	if receiverClass != nil {
		if _, ok := gt.Classes[*receiverClass]; ok {
			varTypeMap["self"] = *receiverClass
			varTypeMap["cls"] = *receiverClass
		}
	}

	// Build from parameters with type annotations
	for _, param := range funcInput {
		if param.Typing == "" || param.Name == "" || param.Name == "self" || param.Name == "cls" {
			continue
		}
		if cid, ok := classIDForType(param, pr, gt); ok {
			varTypeMap[param.Name] = cid
		}
	}

	// Build from local assignments
	for _, assign := range bodyAssigns {
		if assign.Name == "" {
			continue
		}
		if assign.ValueType != "" {
			if cid, ok := resolveAssignClassID(assign.ValueType, pr, gt); ok {
				varTypeMap[assign.Name] = cid
				continue
			}
		}
		// `x = a <op> b`: type x as the class produced by a's operator dunder
		// (e.g. Vector.__add__ -> Vector) so a later x.method() / x[i] resolves.
		if assign.OpLeft != "" && assign.OpDunder != "" {
			if leftCID, ok := varTypeMap[assign.OpLeft]; ok {
				if cid, ok := dunderReturnClassID(leftCID, assign.OpDunder, pr, gt); ok {
					varTypeMap[assign.Name] = cid
				}
			}
		}
	}

	// Resolve method calls
	for ci := range bodyCalls {
		call := bodyCalls[ci]
		// nil when a caller resolves calls without recording their shapes, which the
		// focused unit tests do.
		if cur != nil {
			*cur = &bodyCalls[ci]
		}
		if call.ObjectName == "" || call.MethodName == "" {
			// Direct function call: resolve via same-module or imported name.
			if fid := lookupFuncByName(call.Func, pr, gt); fid != "" {
				add(python.ConnCalls, string(fid))
			} else if cid := lookupClassByName(call.Func, pr, gt); cid != "" {
				// Not a function: a bare class reference (instantiation in a
				// comprehension / context manager, or a match class-pattern)
				// with no trailing method call still uses the class.
				if receiverClass == nil || cid != *receiverClass {
					add(python.ConnUsesClass, string(cid))
				}
			}
			continue
		}

		// super().method(...) resolves against the receiver class's bases.
		if call.ObjectName == "super" {
			if receiverClass != nil {
				resolveSuperMethod(*receiverClass, call.MethodName, gt, add)
			}
			continue
		}

		classID, ok := varTypeMap[call.ObjectName]
		if !ok {
			// `mod.func()` / `mod.Class()` where mod is an imported module alias
			// (not a typed local): resolve the dotted call against that module.
			if fid := lookupFuncByName(call.Func, pr, gt); fid != "" {
				add(python.ConnCalls, string(fid))
			} else if cid := lookupClassByName(call.Func, pr, gt); cid != "" {
				if receiverClass == nil || cid != *receiverClass {
					add(python.ConnUsesClass, string(cid))
				}
			}
			continue
		}

		cls, ok := gt.Classes[classID]
		if !ok {
			continue
		}

		if receiverClass == nil || classID != *receiverClass {
			add(python.ConnUsesClass, string(classID))
		}

		// Find the method on the class
		for _, mid := range cls.Methods() {
			m, ok := gt.Functions[mid]
			if ok && m.Name == call.MethodName {
				add(python.ConnCalls, string(mid))
				break
			}
		}
	}
}

// resolveSuperMethod resolves a super().method(...) call by walking the receiver
// class's base classes breadth-first (MRO order) and recording a Calls edge to
// the first inherited method whose name matches methodName.
func resolveSuperMethod(receiver python.ClassID, methodName string, gt *python.PythonTopology, add func(kind python.ConnectionKind, id string)) {
	if methodName == "" {
		return
	}
	visited := map[python.ClassID]bool{receiver: true}
	var queue []python.ClassID
	if cls, ok := gt.Classes[receiver]; ok {
		queue = append(queue, cls.Inherits()...)
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if visited[cur] {
			continue
		}
		visited[cur] = true
		cls, ok := gt.Classes[cur]
		if !ok {
			continue
		}
		for _, mid := range cls.Methods() {
			if m, ok := gt.Functions[mid]; ok && m.Name == methodName {
				add(python.ConnCalls, string(mid))
				return
			}
		}
		queue = append(queue, cls.Inherits()...)
	}
}

// dunderReturnClassID resolves the class produced by leftClass.<dunder>(...),
// used to type the result of a binary operation (e.g. `c = a + b` where a's
// __add__ returns a class). It mirrors resolveAssignClassID's return-type
// resolution and tolerates a forward-reference (quoted) return annotation.
func dunderReturnClassID(leftClass python.ClassID, dunder string, pr *ParseResult, gt *python.PythonTopology) (python.ClassID, bool) {
	cls, ok := gt.Classes[leftClass]
	if !ok {
		return "", false
	}
	for _, mid := range cls.Methods() {
		m, ok := gt.Functions[mid]
		if !ok || m.Name != dunder || len(m.Output) == 0 {
			continue
		}
		out := m.Output[0]
		if out.TypingID != "" && classExists(python.ClassID(out.TypingID), gt) {
			return python.ClassID(out.TypingID), true
		}
		typing := strings.Trim(out.Typing, "'\"")
		if typing == "" {
			return "", false
		}
		if cid := python.ClassID(pyModulePath(pr.ModuleRoot, m.Loc.Path) + "." + typing); classExists(cid, gt) {
			return cid, true
		}
		if cid := lookupClassByName(typing, pr, gt); cid != "" {
			return cid, true
		}
		return "", false
	}
	return "", false
}

// pyBuiltinTypes are predeclared/typing names that are never topology classes,
// so canonicalTypeID skips producing a (bogus) candidate id for them.
var pyBuiltinTypes = map[string]bool{
	"str": true, "int": true, "float": true, "bool": true, "bytes": true,
	"complex": true, "bytearray": true, "memoryview": true, "None": true,
	"object": true, "type": true, "list": true, "dict": true, "set": true,
	"tuple": true, "frozenset": true, "range": true,
	"Any": true, "List": true, "Dict": true, "Set": true, "Tuple": true,
	"FrozenSet": true, "Optional": true, "Union": true, "Callable": true,
	"Iterable": true, "Iterator": true, "Sequence": true, "Mapping": true,
	"MutableMapping": true, "Awaitable": true, "Coroutine": true,
	"Generator": true, "AsyncIterator": true, "AsyncIterable": true,
	"Self": true, "ClassVar": true, "Final": true, "Annotated": true,
	"Literal": true, "Type": true,
}

// canonicalTypeID returns the canonical topology ID for the type named by
// `typing`, resolved against the DEFINING file's imports. It returns "" for
// builtin/typing names, collapsed generics, and unknown aliases. The result is a
// candidate id — consumers must still verify it exists. Now that IDs are
// module-qualified, an imported type resolves to the target module's path plus
// the imported symbol, so a caller in another file can follow it without the
// defining file's import context.
func canonicalTypeID(typing string, modulePath string, importMap map[string]string, importTargets map[string]pyImportTarget) string {
	t := typing
	if t == "" || strings.ContainsAny(t, " \t[](){}|,'\"") {
		return ""
	}
	// `from m import Name` -> alias Name binds a symbol of internal module m.
	if tgt, ok := importTargets[t]; ok && tgt.Symbol != "" {
		return tgt.ModulePath + "." + tgt.Symbol
	}
	// `m.Name` via `import m` / `from . import m` (m is a module alias).
	if i := strings.Index(t, "."); i > 0 {
		alias := t[:i]
		if tgt, ok := importTargets[alias]; ok {
			return tgt.ModulePath + "." + t[i+1:]
		}
		if impPath, ok := importMap[alias]; ok {
			// External dependency: keep the dotted path (it won't match an
			// internal id, but preserves the prior behaviour for ext types).
			return impPath + "." + t[i+1:]
		}
		return ""
	}
	// Bare external symbol (`from numpy import ndarray`): keep the dotted path.
	if impPath, ok := importMap[t]; ok {
		return impPath
	}
	if pyBuiltinTypes[t] {
		return ""
	}
	return modulePath + "." + t
}

// lookupClassByName resolves a class reference name to a ClassID using the
// current file's context: same module, an imported symbol (`from m import Name`),
// or a dotted `m.Name` reference where m is an imported module alias.
func lookupClassByName(name string, pr *ParseResult, gt *python.PythonTopology) python.ClassID {
	if cid := python.ClassID(pr.ModulePath + "." + name); classExists(cid, gt) {
		return cid
	}
	if tgt, ok := pr.ImportTargets[name]; ok {
		if tgt.Symbol != "" {
			if cid := python.ClassID(tgt.ModulePath + "." + tgt.Symbol); classExists(cid, gt) {
				return cid
			}
		}
		// `from . import X` / `from .. import X` where X is a class in the
		// package __init__ rather than a submodule.
		if tgt.PkgSymbol != "" {
			if cid := python.ClassID(tgt.PkgModulePath + "." + tgt.PkgSymbol); classExists(cid, gt) {
				return cid
			}
		}
	}
	if i := strings.Index(name, "."); i > 0 {
		if tgt, ok := pr.ImportTargets[name[:i]]; ok {
			if cid := python.ClassID(tgt.ModulePath + "." + name[i+1:]); classExists(cid, gt) {
				return cid
			}
		}
	}
	for _, sp := range starModulePrefixes(name, pr) {
		if cid := python.ClassID(sp + name); classExists(cid, gt) {
			return cid
		}
	}
	return ""
}

// lookupFuncByName resolves a function call name to a FunctionID using the same
// rules as lookupClassByName (same module, imported symbol, dotted reference).
func lookupFuncByName(name string, pr *ParseResult, gt *python.PythonTopology) python.FunctionID {
	if fid := python.FunctionID(pr.ModulePath + "." + name); funcExists(fid, gt) {
		return fid
	}
	if tgt, ok := pr.ImportTargets[name]; ok {
		if tgt.Symbol != "" {
			if fid := python.FunctionID(tgt.ModulePath + "." + tgt.Symbol); funcExists(fid, gt) {
				return fid
			}
		}
		// `from . import X` / `from .. import X` where X is a function in the
		// package __init__ rather than a submodule.
		if tgt.PkgSymbol != "" {
			if fid := python.FunctionID(tgt.PkgModulePath + "." + tgt.PkgSymbol); funcExists(fid, gt) {
				return fid
			}
		}
	}
	if i := strings.Index(name, "."); i > 0 {
		if tgt, ok := pr.ImportTargets[name[:i]]; ok {
			if fid := python.FunctionID(tgt.ModulePath + "." + name[i+1:]); funcExists(fid, gt) {
				return fid
			}
		}
	}
	for _, sp := range starModulePrefixes(name, pr) {
		if fid := python.FunctionID(sp + name); funcExists(fid, gt) {
			return fid
		}
	}
	return ""
}

// lookupExtVarByName resolves a module-level variable reference to an
// ExternalVarID using same-module and imported-symbol resolution.
func lookupExtVarByName(name string, pr *ParseResult, gt *python.PythonTopology) python.ExternalVarID {
	if vid := python.ExternalVarID(pr.ModulePath + "." + name); extVarExists(vid, gt) {
		return vid
	}
	if tgt, ok := pr.ImportTargets[name]; ok {
		if tgt.Symbol != "" {
			if vid := python.ExternalVarID(tgt.ModulePath + "." + tgt.Symbol); extVarExists(vid, gt) {
				return vid
			}
		}
		// `from . import X` / `from .. import X` where X is a module-level var in
		// the package __init__ rather than a submodule.
		if tgt.PkgSymbol != "" {
			if vid := python.ExternalVarID(tgt.PkgModulePath + "." + tgt.PkgSymbol); extVarExists(vid, gt) {
				return vid
			}
		}
	}
	if i := strings.Index(name, "."); i > 0 {
		if tgt, ok := pr.ImportTargets[name[:i]]; ok {
			if vid := python.ExternalVarID(tgt.ModulePath + "." + name[i+1:]); extVarExists(vid, gt) {
				return vid
			}
		}
	}
	for _, sp := range starModulePrefixes(name, pr) {
		if vid := python.ExternalVarID(sp + name); extVarExists(vid, gt) {
			return vid
		}
	}
	return ""
}

// starModulePrefixes returns the candidate ID prefixes that a bare `name` could
// carry when made visible by a `from .m import *` star import. It yields nothing
// for a dotted name (star imports only bind bare top-level names). Each star
// module is tried both as a plain module (`prefix.`) and as a package
// (`prefix/__init__.`).
func starModulePrefixes(name string, pr *ParseResult) []string {
	if len(pr.StarImports) == 0 || strings.Contains(name, ".") {
		return nil
	}
	out := make([]string, 0, len(pr.StarImports)*2)
	for _, sp := range pr.StarImports {
		out = append(out, sp+".", sp+"/__init__.")
	}
	return out
}

// Checks whether a class ID exists in the topology.
func classExists(id python.ClassID, gt *python.PythonTopology) bool {
	_, ok := gt.Classes[id]
	return ok
}

// Checks whether a function ID exists in the Python topology's function registry.
func funcExists(id python.FunctionID, gt *python.PythonTopology) bool {
	_, ok := gt.Functions[id]
	return ok
}

// Checks if an external variable exists in the Python topology.
func extVarExists(id python.ExternalVarID, gt *python.PythonTopology) bool {
	_, ok := gt.ExternalVars[id]
	return ok
}

// classIDForType resolves a VariableDefinition's annotated type to a class ID,
// preferring the precomputed canonical TypingID (resolved in the defining file's
// context) and falling back to name-based resolution in the current file.
func classIDForType(vd python.VariableDefinition, pr *ParseResult, gt *python.PythonTopology) (python.ClassID, bool) {
	if vd.TypingID != "" {
		if classExists(python.ClassID(vd.TypingID), gt) {
			return python.ClassID(vd.TypingID), true
		}
	}
	if cid := lookupClassByName(vd.Typing, pr, gt); cid != "" {
		return cid, true
	}
	return "", false
}

// resolveAssignClassID infers the class type of `x` in `x = <valueType>(...)`.
// valueType is the callee/name: a constructor (x is that class) or a function (x
// is the function's return type). For function calls it prefers the callee's
// precomputed return TypingID so cross-file return types resolve without the
// callee's import context (fixing transitive `x = make(); x.method()`), and
// falls back to a return type resolved in the callee's own module.
func resolveAssignClassID(valueType string, pr *ParseResult, gt *python.PythonTopology) (python.ClassID, bool) {
	if cid := lookupClassByName(valueType, pr, gt); cid != "" {
		return cid, true
	}
	if fid := lookupFuncByName(valueType, pr, gt); fid != "" {
		fn := gt.Functions[fid]
		if len(fn.Output) > 0 {
			out := fn.Output[0]
			if out.TypingID != "" && classExists(python.ClassID(out.TypingID), gt) {
				return python.ClassID(out.TypingID), true
			}
			if out.Typing != "" {
				calleeModule := pyModulePath(pr.ModuleRoot, fn.Loc.Path)
				if cid := python.ClassID(calleeModule + "." + out.Typing); classExists(cid, gt) {
					return cid, true
				}
			}
		}
	}
	return "", false
}

// Resolves type references in function decorators, parameters, and return types.
func resolveBodyReferences(body *pyFunc, pr *ParseResult, gt *python.PythonTopology, add func(kind python.ConnectionKind, id string)) {
	seen := make(map[string]bool)
	for _, dec := range body.Decorators {
		resolveDecoratorRef(dec, pr, gt, add, seen)
	}

	seen2 := make(map[string]bool)
	for _, param := range body.Params {
		resolveAnnotationRefs(param.TypeRefs, pr, gt, add, seen2)
		if len(param.TypeRefs) == 0 && param.Typing != "" {
			resolveTypeRef(param.Typing, pr, gt, add, seen2)
		}
	}
	for _, result := range body.Results {
		resolveAnnotationRefs(result.TypeRefs, pr, gt, add, seen2)
		if len(result.TypeRefs) == 0 && result.Typing != "" {
			resolveTypeRef(result.Typing, pr, gt, add, seen2)
		}
	}
}

// resolveAnnotationRefs resolves every inner type name extracted from a
// (possibly composite/generic) annotation — Optional[X], Union[A,B], A | B,
// list[X], dict[k,V], Callable[[A],B], quoted forward refs, and alias names — to
// its edge, so each named class yields its own uses_class edge.
func resolveAnnotationRefs(refs []string, pr *ParseResult, gt *python.PythonTopology, add func(kind python.ConnectionKind, id string), seen map[string]bool) {
	for _, ref := range refs {
		resolveAnnotationRef(ref, pr, gt, add, seen, 0)
	}
}

// resolveAnnotationRef resolves a single type name to a uses_class / calls /
// uses_dependency edge. A name that is a module-level type alias (PEP 695
// `type X = ...` or `X = list[Y]`) expands to the inner types it aliases,
// transitively (bounded by depth to guard against alias cycles).
func resolveAnnotationRef(name string, pr *ParseResult, gt *python.PythonTopology, add func(kind python.ConnectionKind, id string), seen map[string]bool, depth int) {
	if name == "" {
		return
	}
	if cid := lookupClassByName(name, pr, gt); cid != "" {
		add(python.ConnUsesClass, string(cid))
		return
	}
	if fid := lookupFuncByName(name, pr, gt); fid != "" {
		if !seen[string(fid)] {
			seen[string(fid)] = true
			add(python.ConnCalls, string(fid))
		}
		return
	}
	if depth < 8 {
		if aliasRefs, ok := pr.TypeAliases[name]; ok {
			for _, r := range aliasRefs {
				if r != name {
					resolveAnnotationRef(r, pr, gt, add, seen, depth+1)
				}
			}
			return
		}
	}
	addExternalDep(name, pr, add)
}

// Resolves a decorator name to a function or class connection, handling both local lookups and external dependencies.
func resolveDecoratorRef(dec string, pr *ParseResult, gt *python.PythonTopology, add func(kind python.ConnectionKind, id string), seen map[string]bool) {
	if fid := lookupFuncByName(dec, pr, gt); fid != "" {
		if !seen[string(fid)] {
			seen[string(fid)] = true
			add(python.ConnCalls, string(fid))
		}
		return
	}
	if cid := lookupClassByName(dec, pr, gt); cid != "" {
		add(python.ConnUsesClass, string(cid))
		return
	}
	addExternalDep(dec, pr, add)
}

// Resolves a type annotation to a class or function connection, treating it as either used or called based on lookup results.
func resolveTypeRef(typeName string, pr *ParseResult, gt *python.PythonTopology, add func(kind python.ConnectionKind, id string), seen map[string]bool) {
	clean := cleanTypeName(typeName)
	if clean == "" {
		return
	}

	if cid := lookupClassByName(clean, pr, gt); cid != "" {
		add(python.ConnUsesClass, string(cid))
		return
	}
	if fid := lookupFuncByName(clean, pr, gt); fid != "" {
		if !seen[string(fid)] {
			seen[string(fid)] = true
			add(python.ConnCalls, string(fid))
		}
		return
	}
	addExternalDep(clean, pr, add)
}

// resolveValueRef resolves a bare or dotted value reference (a class-var
// initializer) to the class/function/variable it names.
func resolveValueRef(value string, pr *ParseResult, gt *python.PythonTopology, add func(kind python.ConnectionKind, id string)) {
	if value == "" || value == "None" || value == "True" || value == "False" {
		return
	}
	if cid := lookupClassByName(value, pr, gt); cid != "" {
		add(python.ConnUsesClass, string(cid))
		return
	}
	if fid := lookupFuncByName(value, pr, gt); fid != "" {
		add(python.ConnCalls, string(fid))
		return
	}
	if vid := lookupExtVarByName(value, pr, gt); vid != "" {
		add(python.ConnUsesExtVar, string(vid))
		return
	}
	addExternalDep(value, pr, add)
}

// addExternalDep records a uses_dependency edge when `ref`'s top-level alias is
// an external (non-internal) import.
func addExternalDep(ref string, pr *ParseResult, add func(kind python.ConnectionKind, id string)) {
	alias := ref
	if i := strings.Index(ref, "."); i > 0 {
		alias = ref[:i]
	}
	if _, internal := pr.ImportTargets[alias]; internal {
		return
	}
	if impPath, ok := pr.ImportMap[alias]; ok {
		add(python.ConnUsesDep, impPath)
	}
}

// cleanTypeName strips pointer/optional markers and generic parameters from a
// type expression, leaving the bare (possibly dotted) type name.
func cleanTypeName(typeName string) string {
	clean := strings.TrimPrefix(typeName, "*")
	clean = strings.TrimSuffix(clean, "?")
	if i := strings.Index(clean, "["); i >= 0 {
		clean = clean[:i]
	}
	return clean
}

// Marks __init__ methods as constructors in their parent classes within the topology.
func detectConstructors(gt *python.PythonTopology) {
	for _, fn := range gt.Functions {
		if fn.MethodFrom == nil {
			continue
		}
		if fn.Name == "__init__" {
			cls := gt.Classes[*fn.MethodFrom]
			cls.Constructor = &fn.ID
			gt.Classes[*fn.MethodFrom] = cls
		}
	}
}

type PythonFunctionID = string

// Gathers unique external dependencies from all modules into the topology's dependency list.
func collectDependencies(gt *python.PythonTopology) {
	seen := make(map[python.DependancyPath]bool)
	for _, mod := range gt.Modules {
		for _, dep := range mod.DependenciesImported() {
			if !seen[dep.PackagePath] {
				seen[dep.PackagePath] = true
				gt.Dependencies = append(gt.Dependencies, dep)
			}
		}
	}
}

// Rebuilds class-to-method connections by populating ConnHasMethod edges from functions marked with MethodFrom references.
func populateClassMethods(gt *python.PythonTopology) {
	// Clear existing has_method edges first so re-running over the full
	// topology during an incremental UpdateFile rebuilds them from scratch
	// instead of appending duplicates (mirrors goscanner.populateStructMethods).
	for id, cls := range gt.Classes {
		delete(cls.Connections, python.ConnHasMethod)
		gt.Classes[id] = cls
	}
	for _, f := range gt.Functions {
		if f.MethodFrom != nil {
			cls := gt.Classes[*f.MethodFrom]
			if cls.Connections == nil {
				cls.Connections = make(map[python.ConnectionKind][]string)
			}
			cls.Connections[python.ConnHasMethod] = append(cls.Connections[python.ConnHasMethod], string(f.ID))
			gt.Classes[*f.MethodFrom] = cls
		}
	}
}

// Deduplicates connection maps by removing duplicate IDs within each connection kind while preserving order.
func uniqueConns(conns map[python.ConnectionKind][]string) map[python.ConnectionKind][]string {
	result := make(map[python.ConnectionKind][]string, len(conns))
	for k, v := range conns {
		seen := make(map[string]bool)
		var deduped []string
		for _, id := range v {
			if !seen[id] {
				seen[id] = true
				deduped = append(deduped, id)
			}
		}
		if len(deduped) > 0 {
			result[k] = deduped
		}
	}
	return result
}

// Resolves class variable references to their types, populating class connection maps with the referenced values.
func resolveClassVarRefs(pr *ParseResult, gt *python.PythonTopology) {
	for _, ref := range pr.ClassVarRefs {
		cls, exists := gt.Classes[ref.ClassID]
		if !exists {
			continue
		}
		if cls.Connections == nil {
			cls.Connections = make(map[python.ConnectionKind][]string)
		}

		add := func(kind python.ConnectionKind, id string) {
			for _, existing := range cls.Connections[kind] {
				if existing == id {
					return
				}
			}
			cls.Connections[kind] = append(cls.Connections[kind], id)
		}

		resolveValueRef(ref.RefValue, pr, gt, add)
		gt.Classes[ref.ClassID] = cls
	}
}

// resolveMetaclassRefs resolves each `class C(metaclass=Meta)` reference to a
// uses_class edge from C to the metaclass.
func resolveMetaclassRefs(pr *ParseResult, gt *python.PythonTopology) {
	for _, ref := range pr.MetaclassRefs {
		cls, exists := gt.Classes[ref.ClassID]
		if !exists {
			continue
		}
		mid := lookupClassByName(ref.Name, pr, gt)
		if mid == "" {
			continue
		}
		if cls.Connections == nil {
			cls.Connections = make(map[python.ConnectionKind][]string)
		}
		found := false
		for _, existing := range cls.Connections[python.ConnUsesClass] {
			if existing == string(mid) {
				found = true
				break
			}
		}
		if !found {
			cls.Connections[python.ConnUsesClass] = append(cls.Connections[python.ConnUsesClass], string(mid))
		}
		gt.Classes[ref.ClassID] = cls
	}
}

// pyCallSite reads the argument shape of one Python call.
//
// Python's arity is binding -- a missing required argument is a TypeError, not a silently
// undefined parameter -- so this is a real check, unlike in JavaScript. Keyword arguments
// are carried by name because they can satisfy a positional parameter, and a name that
// matches no parameter is itself a genuine error that no other language here can detect.
func pyCallSite(calleeID string, c *pyBodyCall) contract.CallSite {
	site := contract.CallSite{
		CalleeID: calleeID,
		N:        c.ArgC,
		Variadic: c.Starred,
		Kwargs:   c.KwNames,
	}
	anyKnown := false
	types := make([]*string, 0, len(c.ArgTypes))
	for _, tok := range c.ArgTypes {
		if tok == "" {
			types = append(types, nil)
			continue
		}
		anyKnown = true
		t := tok
		types = append(types, &t)
	}
	if anyKnown {
		site.Types = types
	}
	return site
}

func containsPyRec(recs []string, rec string) bool {
	for _, r := range recs {
		if r == rec {
			return true
		}
	}
	return false
}
