package pyscanner

import (
	"strings"

	"aracne/internal/topology/python"
)

func analyzeFunctionBody(body *pyFunc, pr *ParseResult, gt *python.PythonTopology, funcInput []python.VariableDefinition, receiverClass *python.ClassID) map[python.ConnectionKind][]string {
	conn := make(map[python.ConnectionKind][]string)

	if body == nil {
		return conn
	}

	add := func(kind python.ConnectionKind, id string) {
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
	resolveBodyCallRefs(bodyCalls, bodyAssigns, pr, gt, add, funcInput, receiverClass)

	return conn
}

func resolveBodyCallRefs(bodyCalls []pyBodyCall, bodyAssigns []pyBodyAssign, pr *ParseResult, gt *python.PythonTopology, add func(kind python.ConnectionKind, id string), funcInput []python.VariableDefinition, receiverClass *python.ClassID) {
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
		if assign.ValueType == "" || assign.Name == "" {
			continue
		}
		if cid, ok := resolveAssignClassID(assign.ValueType, pr, gt); ok {
			varTypeMap[assign.Name] = cid
		}
	}

	// Resolve method calls
	for _, call := range bodyCalls {
		if call.ObjectName == "" || call.MethodName == "" {
			// Direct function call: resolve via same-module or imported name.
			if fid := lookupFuncByName(call.Func, pr, gt); fid != "" {
				add(python.ConnCalls, string(fid))
			}
			continue
		}

		classID, ok := varTypeMap[call.ObjectName]
		if !ok {
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
	if tgt, ok := pr.ImportTargets[name]; ok && tgt.Symbol != "" {
		if cid := python.ClassID(tgt.ModulePath + "." + tgt.Symbol); classExists(cid, gt) {
			return cid
		}
	}
	if i := strings.Index(name, "."); i > 0 {
		if tgt, ok := pr.ImportTargets[name[:i]]; ok {
			if cid := python.ClassID(tgt.ModulePath + "." + name[i+1:]); classExists(cid, gt) {
				return cid
			}
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
	if tgt, ok := pr.ImportTargets[name]; ok && tgt.Symbol != "" {
		if fid := python.FunctionID(tgt.ModulePath + "." + tgt.Symbol); funcExists(fid, gt) {
			return fid
		}
	}
	if i := strings.Index(name, "."); i > 0 {
		if tgt, ok := pr.ImportTargets[name[:i]]; ok {
			if fid := python.FunctionID(tgt.ModulePath + "." + name[i+1:]); funcExists(fid, gt) {
				return fid
			}
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
	if tgt, ok := pr.ImportTargets[name]; ok && tgt.Symbol != "" {
		if vid := python.ExternalVarID(tgt.ModulePath + "." + tgt.Symbol); extVarExists(vid, gt) {
			return vid
		}
	}
	if i := strings.Index(name, "."); i > 0 {
		if tgt, ok := pr.ImportTargets[name[:i]]; ok {
			if vid := python.ExternalVarID(tgt.ModulePath + "." + name[i+1:]); extVarExists(vid, gt) {
				return vid
			}
		}
	}
	return ""
}

func classExists(id python.ClassID, gt *python.PythonTopology) bool {
	_, ok := gt.Classes[id]
	return ok
}

func funcExists(id python.FunctionID, gt *python.PythonTopology) bool {
	_, ok := gt.Functions[id]
	return ok
}

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

func resolveBodyReferences(body *pyFunc, pr *ParseResult, gt *python.PythonTopology, add func(kind python.ConnectionKind, id string)) {
	seen := make(map[string]bool)
	for _, dec := range body.Decorators {
		resolveDecoratorRef(dec, pr, gt, add, seen)
	}

	seen2 := make(map[string]bool)
	for _, param := range body.Params {
		if param.Typing != "" {
			resolveTypeRef(param.Typing, pr, gt, add, seen2)
		}
	}
	for _, result := range body.Results {
		if result.Typing != "" {
			resolveTypeRef(result.Typing, pr, gt, add, seen2)
		}
	}
}

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
