package pyscanner

import (
	"path/filepath"
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
			// Direct function call - try to resolve
			funcID := python.FunctionID(string(pr.PkgPath) + "." + call.Func)
			if _, exists := gt.Functions[funcID]; exists {
				add(python.ConnCalls, string(funcID))
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

// canonicalTypeID returns the canonical topology class ID for the type named by
// `typing`, resolved against the DEFINING file's import map. It returns "" for
// builtin/typing names, collapsed generics, and unknown aliases. The result is a
// candidate id — consumers must still verify it exists. Computing it at parse
// time lets cross-package consumers resolve a type without the defining file's
// import context.
func canonicalTypeID(typing string, pkgPath python.PackagePath, importMap map[string]string) string {
	t := typing
	if t == "" || strings.ContainsAny(t, " \t[](){}|,'\"") {
		return ""
	}
	// `from mod import Name` -> importMap[Name] == "mod.Name" (full path).
	if impPath, ok := importMap[t]; ok {
		return impPath
	}
	// `mod.Name` via `import mod` -> importMap[mod] == "mod".
	if i := strings.Index(t, "."); i > 0 {
		if impPath, ok := importMap[t[:i]]; ok {
			return impPath + "." + t[i+1:]
		}
		return ""
	}
	if pyBuiltinTypes[t] {
		return ""
	}
	return string(pkgPath) + "." + t
}

// lookupClassByName resolves a class reference name to a ClassID using the
// current file's context: same package, a `from m import Name` (importMap[name]
// holds the full path), or a dotted `m.Name` reference (importMap[alias]).
func lookupClassByName(name string, pr *ParseResult, gt *python.PythonTopology) python.ClassID {
	cid := python.ClassID(string(pr.PkgPath) + "." + name)
	if _, ok := gt.Classes[cid]; ok {
		return cid
	}
	if impPath, ok := pr.ImportMap[name]; ok {
		if _, ok := gt.Classes[python.ClassID(impPath)]; ok {
			return python.ClassID(impPath)
		}
	}
	if i := strings.Index(name, "."); i > 0 {
		if impPath, ok := pr.ImportMap[name[:i]]; ok {
			cid = python.ClassID(impPath + "." + name[i+1:])
			if _, ok := gt.Classes[cid]; ok {
				return cid
			}
		}
	}
	return ""
}

// lookupFuncByName resolves a function call name to a FunctionID using the same
// rules as lookupClassByName (same package, from-import, dotted reference).
func lookupFuncByName(name string, pr *ParseResult, gt *python.PythonTopology) python.FunctionID {
	fid := python.FunctionID(string(pr.PkgPath) + "." + name)
	if _, ok := gt.Functions[fid]; ok {
		return fid
	}
	if impPath, ok := pr.ImportMap[name]; ok {
		if _, ok := gt.Functions[python.FunctionID(impPath)]; ok {
			return python.FunctionID(impPath)
		}
	}
	if i := strings.Index(name, "."); i > 0 {
		if impPath, ok := pr.ImportMap[name[:i]]; ok {
			fid = python.FunctionID(impPath + "." + name[i+1:])
			if _, ok := gt.Functions[fid]; ok {
				return fid
			}
		}
	}
	return ""
}

// classIDForType resolves a VariableDefinition's annotated type to a class ID,
// preferring the precomputed canonical TypingID (resolved in the defining file's
// context) and falling back to name-based resolution in the current file.
func classIDForType(vd python.VariableDefinition, pr *ParseResult, gt *python.PythonTopology) (python.ClassID, bool) {
	if vd.TypingID != "" {
		if _, ok := gt.Classes[python.ClassID(vd.TypingID)]; ok {
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
// precomputed return TypingID so cross-package return types resolve without the
// callee's import context (fixing transitive `x = make(); x.method()`), and
// falls back to a same-package return type when TypingID is absent.
func resolveAssignClassID(valueType string, pr *ParseResult, gt *python.PythonTopology) (python.ClassID, bool) {
	if cid := lookupClassByName(valueType, pr, gt); cid != "" {
		return cid, true
	}
	if fid := lookupFuncByName(valueType, pr, gt); fid != "" {
		fn := gt.Functions[fid]
		if len(fn.Output) > 0 {
			out := fn.Output[0]
			if out.TypingID != "" {
				if _, ok := gt.Classes[python.ClassID(out.TypingID)]; ok {
					return python.ClassID(out.TypingID), true
				}
			}
			if out.Typing != "" {
				cid := python.ClassID(string(pr.PkgPath) + "." + out.Typing)
				if _, ok := gt.Classes[cid]; ok {
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
	parts := strings.Split(dec, ".")
	if len(parts) >= 2 {
		alias := parts[0]
		if impPath, ok := pr.ImportMap[alias]; ok {
			if isInternal(impPath, pr.ModuleRoot) {
				add(python.ConnUsesPkg, string(impPath))
			} else {
				add(python.ConnUsesDep, string(impPath))
			}
		}
	}
}

func resolveTypeRef(typeName string, pr *ParseResult, gt *python.PythonTopology, add func(kind python.ConnectionKind, id string), seen map[string]bool) {
	clean := strings.TrimPrefix(typeName, "*")
	clean = strings.TrimSuffix(clean, "?")
	if strings.Contains(clean, "[") {
		clean = clean[:strings.Index(clean, "[")]
	}

	rootBase := filepath.Base(pr.ModuleRoot)

	if strings.Contains(clean, ".") {
		parts := strings.Split(clean, ".")
		if len(parts) >= 2 {
			alias := parts[0]
			if impPath, ok := pr.ImportMap[alias]; ok {
				if isInternal(impPath, pr.ModuleRoot) {
					add(python.ConnUsesPkg, string(impPath))
					symbol := strings.Join(parts[1:], ".")
					if kind, id := tryResolveSymbol(string(impPath), symbol, rootBase, gt); kind != "" {
						add(kind, id)
					}
				} else {
					add(python.ConnUsesDep, string(impPath))
				}
			}
		}
		return
	}

	classID := python.ClassID(string(pr.PkgPath) + "." + clean)
	if _, exists := gt.Classes[classID]; exists {
		add(python.ConnUsesClass, string(classID))
	}

	funcID := PythonFunctionID(string(pr.PkgPath) + "." + clean)
	if _, exists := gt.Functions[python.FunctionID(funcID)]; exists && !seen[funcID] {
		seen[funcID] = true
		add(python.ConnCalls, funcID)
	}

	if impPath, ok := pr.ImportMap[clean]; ok {
		if kind, id := tryResolveSymbol(impPath, "", rootBase, gt); kind != "" {
			add(kind, id)
		}
	}
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

func resolveValueRef(value string, pr *ParseResult, gt *python.PythonTopology, add func(kind python.ConnectionKind, id string)) {
	if value == "" || value == "None" || value == "True" || value == "False" {
		return
	}

	rootBase := filepath.Base(pr.ModuleRoot)

	if strings.Contains(value, ".") {
		parts := strings.Split(value, ".")
		if len(parts) >= 2 {
			alias := parts[0]
			if impPath, ok := pr.ImportMap[alias]; ok {
				if isInternal(impPath, pr.ModuleRoot) {
					add(python.ConnUsesPkg, string(impPath))
				} else {
					add(python.ConnUsesDep, string(impPath))
				}
				symbol := strings.Join(parts[1:], ".")
				if kind, id := tryResolveSymbol(impPath, symbol, rootBase, gt); kind != "" {
					add(kind, id)
				}
				return
			}
		}
		return
	}

	if impPath, ok := pr.ImportMap[value]; ok {
		if isInternal(impPath, pr.ModuleRoot) {
			add(python.ConnUsesPkg, string(impPath))
		} else {
			add(python.ConnUsesDep, string(impPath))
		}
		if kind, id := tryResolveSymbol(impPath, "", rootBase, gt); kind != "" {
			add(kind, id)
		}
		return
	}

	funcID := python.FunctionID(string(pr.PkgPath) + "." + value)
	if _, exists := gt.Functions[python.FunctionID(funcID)]; exists {
		add(python.ConnCalls, funcID)
		return
	}

	classID := python.ClassID(string(pr.PkgPath) + "." + value)
	if _, exists := gt.Classes[classID]; exists {
		add(python.ConnUsesClass, string(classID))
		return
	}

	extVarID := python.ExternalVarID(string(pr.PkgPath) + "." + value)
	if _, exists := gt.ExternalVars[extVarID]; exists {
		add(python.ConnUsesExtVar, string(extVarID))
		return
	}
}

func tryResolveSymbol(impPath string, symbol string, rootBase string, gt *python.PythonTopology) (python.ConnectionKind, string) {
	fullPath := impPath
	if symbol != "" {
		fullPath = impPath + "." + symbol
	}

	candidates := []string{fullPath, rootBase + "." + fullPath}

	for _, candidate := range candidates {
		if _, exists := gt.Classes[python.ClassID(candidate)]; exists {
			return python.ConnUsesClass, candidate
		}
		if _, exists := gt.Functions[python.FunctionID(candidate)]; exists {
			return python.ConnCalls, candidate
		}
	}

	return "", ""
}
