package pyscanner

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/topology/contract"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	"github.com/Rhuan-Marques/aracne/internal/topology/python"
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
				addConstructorCall(cid, call, gt, add)
			} else if missing := missingImportedName(call.Func, pr, gt); missing != "" {
				add(python.ConnectionKind(domain.MissingRefsConn), missing)
			}
			continue
		}

		// super().method(...) resolves against the receiver class's bases.
		if call.ObjectName == "super" {
			if receiverClass != nil {
				resolveInheritedMethod(*receiverClass, call.MethodName, gt, add)
			}
			continue
		}

		classID, ok := varTypeMap[call.ObjectName]
		if !ok {
			// `mod.func()` / `mod.Class()` where mod is an imported module alias
			// (not a typed local): resolve the dotted call against that module.
			if fid := lookupFuncByName(call.Func, pr, gt); fid != "" {
				if cur != nil && passesReceiverExplicitly(fid, call, pr, gt) {
					shifted := withoutExplicitReceiver(call)
					*cur = &shifted
				}
				add(python.ConnCalls, string(fid))
			} else if cid := lookupClassByName(call.Func, pr, gt); cid != "" {
				if receiverClass == nil || cid != *receiverClass {
					add(python.ConnUsesClass, string(cid))
				}
				addConstructorCall(cid, call, gt, add)
			} else if missing := missingModuleMember(call.ObjectName, call.MethodName, pr, gt); missing != "" {
				add(python.ConnectionKind(domain.MissingRefsConn), missing)
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

		// Find the method on the class, then on what it inherits. Stopping at the class's
		// own methods left the ordinary OOP case -- calling a method the class inherits --
		// with no edge at all, so the base method showed no callers and a change to it
		// warned none of them.
		found := false
		for _, mid := range cls.Methods() {
			m, ok := gt.Functions[mid]
			if ok && m.Name == call.MethodName {
				add(python.ConnCalls, string(mid))
				found = true
				break
			}
		}
		if !found {
			resolveInheritedMethod(classID, call.MethodName, gt, add)
		}
	}
}

// addConstructorCall records `Cls(...)` as the call to Cls.__init__ it is, so the call site
// is kept and a constructor signature change is judged against it like any other call.
//
// Only an __init__ the class itself declares is linked. With none, the constructor that runs
// is inherited or object's, and resolving it through bases, metaclasses and __new__ would be
// a guess; a guessed callee is a false warning. The @dataclass constructor the parser
// synthesizes is skipped for the same reason: it is built from every class-level name,
// without defaults, ClassVar or field() options, so its arity is not the real one. A class
// pattern (`case Cls(...)`) runs no constructor at all.
func addConstructorCall(cid python.ClassID, call pyBodyCall, gt *python.PythonTopology, add func(kind python.ConnectionKind, id string)) {
	if call.Pattern {
		return
	}
	cls, ok := gt.Classes[cid]
	if !ok {
		return
	}
	initID := python.FunctionID(string(cid) + ".__init__")
	fn, ok := gt.Functions[initID]
	if !ok || fn.MethodFrom == nil || *fn.MethodFrom != cid {
		return
	}
	// The synthesized constructor carries the class's own span; a written `def __init__`
	// always starts below the `class` line.
	if fn.Loc.Path == cls.Loc.Path && fn.Loc.StartsAt == cls.Loc.StartsAt {
		return
	}
	add(python.ConnCalls, string(initID))
}

// resolveInheritedMethod walks a class's bases breadth-first (MRO order) and records a
// Calls edge to the first INHERITED method whose name matches methodName. It serves both
// callers of an inherited method: super().method(...), which names the bases explicitly,
// and a plain receiver.method() the class does not declare itself.
func resolveInheritedMethod(receiver python.ClassID, methodName string, gt *python.PythonTopology, add func(kind python.ConnectionKind, id string)) {
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
	// `from m import Name` -> alias Name binds a symbol of internal module m (for
	// `from . import Name`, a name defined in the package's __init__.py).
	if tgt, ok := importTargets[t]; ok {
		if refs := tgt.symbolRefs(); len(refs) > 0 {
			return refs[0].Module + "." + refs[0].Name
		}
	}
	// `m.Name` via `import m` / `from . import m` / `from pkg import m` (m binds a
	// module), or `Cls.Inner` via `from m import Cls` (a member of the bound symbol).
	if i := strings.Index(t, "."); i > 0 {
		alias := t[:i]
		if tgt, ok := importTargets[alias]; ok {
			if mods := tgt.moduleRefs(); len(mods) > 0 {
				return mods[0] + "." + t[i+1:]
			}
			if refs := tgt.symbolRefs(); len(refs) > 0 {
				return refs[0].Module + "." + refs[0].Name + "." + t[i+1:]
			}
			return ""
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
	if id := resolveImportedName(name, pr, gt, func(id string) bool { return classExists(python.ClassID(id), gt) }); id != "" {
		return python.ClassID(id)
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
	if id := resolveImportedName(name, pr, gt, func(id string) bool { return funcExists(python.FunctionID(id), gt) }); id != "" {
		return python.FunctionID(id)
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
	if id := resolveImportedName(name, pr, gt, func(id string) bool { return extVarExists(python.ExternalVarID(id), gt) }); id != "" {
		return python.ExternalVarID(id)
	}
	for _, sp := range starModulePrefixes(name, pr) {
		if vid := python.ExternalVarID(sp + name); extVarExists(vid, gt) {
			return vid
		}
	}
	return ""
}

// resolveImportedName resolves name through the file's internal imports to the first
// candidate ID that exists, as judged by exists (which fixes the resource kind). A bare
// name is the symbol its alias binds (`from pkg import helper`, `from . import helper`).
// A dotted `alias.rest` is rest inside the module the alias binds (`import pkg`,
// `from pkg import sub`), else a member of the symbol it binds (`Cls.method`). A name the
// target module does not define itself is then followed through that module's own imports:
// the re-export `from .sub import subf` in pkg/__init__.py.
func resolveImportedName(name string, pr *ParseResult, gt *python.PythonTopology, exists func(id string) bool) string {
	refs, members := importRefs(name, pr)
	for _, r := range refs {
		if id := r.Module + "." + r.Name; exists(id) {
			return id
		}
	}
	for _, id := range members {
		if exists(id) {
			return id
		}
	}
	for _, r := range refs {
		if id := followReexport(r, pr, gt, exists); id != "" {
			return id
		}
	}
	return ""
}

// importRefs lists what name may denote through the file's internal imports, in the order
// resolveImportedName tries them: refs are symbols by the module expected to define them,
// members are IDs nested inside an imported symbol. For a dotted `a.b.rest`, the longest
// import-bound prefix is tried first, so `import pkg.sub` lets `pkg.sub.f` reach f in
// pkg/sub.py while `pkg` itself still names the package.
func importRefs(name string, pr *ParseResult) (refs []pySymbolRef, members []string) {
	if tgt, ok := pr.ImportTargets[name]; ok {
		refs = append(refs, tgt.symbolRefs()...)
	}
	for i := strings.LastIndex(name, "."); i > 0; i = strings.LastIndex(name[:i], ".") {
		tgt, ok := pr.ImportTargets[name[:i]]
		if !ok {
			continue
		}
		rest := name[i+1:]
		for _, m := range tgt.moduleRefs() {
			refs = append(refs, pySymbolRef{Module: m, Name: rest})
		}
		for _, r := range tgt.symbolRefs() {
			members = append(members, r.Module+"."+r.Name+"."+rest)
		}
	}
	return refs, members
}

// pyMaxReexportDepth bounds how many import hops followReexport walks.
const pyMaxReexportDepth = 4

// followReexport looks for ref.Name in the modules ref.Module imports, breadth-first, when
// ref.Module does not define it itself -- `from pkg import subf` where pkg/__init__.py only
// does `from .sub import subf`. The first module found to define the name is taken as its
// binding: the result is that ID when it is of the kind exists asks for, and "" otherwise.
// Each level is walked in sorted order because a full scan and an incremental one see a
// module's import edges in different orders, and they must pick the same binding.
func followReexport(ref pySymbolRef, pr *ParseResult, gt *python.PythonTopology, exists func(id string) bool) string {
	if ref.Module == "" || strings.Contains(ref.Name, ".") || pyNameDefined(ref.Module+"."+ref.Name, gt) {
		return ""
	}
	visited := map[string]bool{ref.Module: true}
	frontier := []string{ref.Module}
	for depth := 0; depth < pyMaxReexportDepth && len(frontier) > 0; depth++ {
		var next []string
		for _, modPath := range frontier {
			mod, ok := gt.Modules[python.ModuleID(filepath.Join(pr.ModuleRoot, filepath.FromSlash(modPath)+".py"))]
			if !ok {
				continue
			}
			var targets []string
			for _, mid := range mod.ModulesImported() {
				targets = append(targets, pyModulePath(pr.ModuleRoot, string(mid)))
			}
			sort.Strings(targets)
			for _, tp := range targets {
				if visited[tp] {
					continue
				}
				visited[tp] = true
				if id := tp + "." + ref.Name; pyNameDefined(id, gt) {
					if exists(id) {
						return id
					}
					return ""
				}
				next = append(next, tp)
			}
		}
		frontier = next
	}
	return ""
}

// pyNameDefined reports whether id names a class, function or module-level variable.
func pyNameDefined(id string, gt *python.PythonTopology) bool {
	return classExists(python.ClassID(id), gt) || funcExists(python.FunctionID(id), gt) || extVarExists(python.ExternalVarID(id), gt)
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

// passesReceiverExplicitly reports whether a call spells its receiver out --
// Base.m(self, x), Base.__init__(self, ...) -- which passes it as the first argument while
// the stored signature has it stripped, so the call counts one argument too many and can
// never fit. Judged from the callee: only an instance method of the class the call names
// takes a receiver this way. A staticmethod declares none, and a classmethod gets its cls
// bound by the attribute lookup, so neither is shifted.
func passesReceiverExplicitly(fid python.FunctionID, call pyBodyCall, pr *ParseResult, gt *python.PythonTopology) bool {
	fn, ok := gt.Functions[fid]
	if !ok || fn.MethodFrom == nil || call.ObjectName == "" || call.ArgC <= 0 {
		return false
	}
	if isStaticMethod(fn.Decorators) || isClassMethod(fn.Decorators) {
		return false
	}
	return lookupClassByName(call.ObjectName, pr, gt) == *fn.MethodFrom
}

// withoutExplicitReceiver drops the leading receiver argument from a recorded shape.
func withoutExplicitReceiver(c pyBodyCall) pyBodyCall {
	c.ArgC--
	if len(c.ArgTypes) > 0 {
		c.ArgTypes = c.ArgTypes[1:]
	}
	return c
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

// missingImportedName is the id `name` was imported as when that import names a project module
// the graph holds and the module declares no such name -- `from a import fun2` with a.py present and
// no fun2 in it. "" whenever the name could be something this resolver does not model: a submodule,
// an import it cannot place in the project, a module that is not indexed, or a re-export it can
// still follow. See domain.MissingRefsConn.
func missingImportedName(name string, pr *ParseResult, gt *python.PythonTopology) string {
	tgt, ok := pr.ImportTargets[name]
	if !ok || tgt.Symbol == "" || tgt.SubModulePath != "" || tgt.PkgSymbol != "" || strings.Contains(name, ".") {
		return ""
	}
	stem := strings.TrimSuffix(tgt.FilePath, ".py")
	if _, ok := findModuleFile(stem, gt); !ok {
		return ""
	}
	if _, ok := findModuleFile(filepath.Join(stem, tgt.Symbol), gt); ok {
		return ""
	}
	id := tgt.ModulePath + "." + tgt.Symbol
	if pyNameDefined(id, gt) || followReexport(pySymbolRef{Module: tgt.ModulePath, Name: tgt.Symbol}, pr, gt, func(id string) bool { return pyNameDefined(id, gt) }) != "" {
		return ""
	}
	return id
}

// missingModuleMember is missingImportedName for `mod.name()` through `import mod`: the member id,
// when mod is an indexed project module that declares no such name.
func missingModuleMember(alias, member string, pr *ParseResult, gt *python.PythonTopology) string {
	tgt, ok := pr.ImportTargets[alias]
	if !ok || tgt.Symbol != "" || tgt.ModulePath == "" || member == "" {
		return ""
	}
	stem := strings.TrimSuffix(tgt.FilePath, ".py")
	if _, ok := findModuleFile(stem, gt); !ok {
		return ""
	}
	if _, ok := findModuleFile(filepath.Join(stem, member), gt); ok {
		return ""
	}
	id := tgt.ModulePath + "." + member
	if pyNameDefined(id, gt) {
		return ""
	}
	return id
}
