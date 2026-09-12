package goscanner

import (
	"fmt"
	"go/ast"
	"go/token"
	"sort"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/topology/contract"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	"github.com/Rhuan-Marques/aracne/internal/topology/golang"
)

// Analyzes function bodies to extract variable types, interface types, and connection metadata during Go code parsing.
type bodyAnalyzer struct {
	pr           *ParseResult
	gt           *golang.GolangTopology
	conn         map[golang.ConnectionKind][]string
	varTypeMap   map[string]golang.StructID
	varIfaceMap  map[string]golang.InterfaceID
	varMethodMap map[string]golang.FunctionID
	// varNamedTypeMap holds the locals typed with a non-struct named type (`type Celsius
	// float64`). Such a type has a method set and takes part in interface matching like any
	// other, but the body analyzer used to track struct- and interface-typed variables only,
	// so a call through one recorded no calls edge and its method could change shape without
	// warning anybody.
	varNamedTypeMap map[string]golang.NamedTypeID
	callerID        golang.FunctionID
	warnings        *map[string]domain.TopologyWarning
	// knownNames are names bound for the whole body without a syntax tree to say so: the
	// builtins, and the parameters, receiver and type parameters the caller hands in. Every
	// other binding is read from the syntax tree by isLocal, which knows its scope.
	knownNames map[string]bool
	// body is the function body being analyzed; isLocal uses it to tell a local declaration
	// from a package-level one.
	body *ast.BlockStmt

	// currentCall is the call expression being resolved, or nil outside one. Every
	// ConnCalls edge in this file is emitted from resolveCallExpr, directly or through
	// resolveQualifiedCall (its only caller), so reading this in add() records the
	// argument shape at all six emission sites without threading the node through each.
	currentCall *ast.CallExpr
	// callSites accumulates the encoded records, in resolution order.
	callSites []string
	// paramTypes maps an enclosing parameter's name to its declared type. Forwarding a
	// parameter is the one argument form whose type is exactly known without inference.
	paramTypes map[string]string
	// shadowed are names the body redeclares. A local that reuses a parameter's name makes
	// paramTypes wrong for it, and a wrong argument type is a false warning about correct
	// code -- so the name is dropped rather than trusted. Reading a nil map is false, which
	// is the safe direction only because paramTypes is consulted for nothing else.
	shadowed map[string]bool
}

// Creates a bodyAnalyzer instance initialized with function parameters, receiver, and known names for code body traversal.
func newBodyAnalyzer(pr *ParseResult, gt *golang.GolangTopology, funcInput []golang.VariableDefinition, receiverName string, receiverStruct *golang.StructID, callerID golang.FunctionID, extraKnownNames []string) *bodyAnalyzer {
	ba := &bodyAnalyzer{
		pr:              pr,
		gt:              gt,
		conn:            make(map[golang.ConnectionKind][]string),
		varTypeMap:      make(map[string]golang.StructID),
		varIfaceMap:     make(map[string]golang.InterfaceID),
		varMethodMap:    make(map[string]golang.FunctionID),
		varNamedTypeMap: make(map[string]golang.NamedTypeID),
		callerID:        callerID,
		warnings:        &gt.Warnings,
		knownNames:      make(map[string]bool),
		paramTypes:      make(map[string]string),
	}

	for name := range goBuiltins {
		ba.knownNames[name] = true
	}
	for _, name := range extraKnownNames {
		ba.knownNames[name] = true
	}

	for _, param := range funcInput {
		if param.Name != "_" {
			ba.knownNames[param.Name] = true
			ba.paramTypes[param.Name] = param.Typing
		}
		ba.resolveVarType(param.Name, param)
	}

	if receiverName != "" && receiverStruct != nil {
		ba.knownNames[receiverName] = true
		if _, ok := gt.Structs[*receiverStruct]; ok {
			ba.varTypeMap[receiverName] = *receiverStruct
		}
		// also check if receiver is an interface (unusual but possible)
		ifaceID := golang.InterfaceID(string(*receiverStruct))
		if _, ok := gt.Interfaces[ifaceID]; ok {
			ba.varIfaceMap[receiverName] = ifaceID
		}
		// A method of a non-struct named type carries its receiver type in the same field;
		// without this, one method of Celsius calling another through the receiver resolved
		// to nothing.
		ntID := golang.NamedTypeID(string(*receiverStruct))
		if _, ok := gt.NamedTypes[ntID]; ok {
			ba.varNamedTypeMap[receiverName] = ntID
		}
	}

	return ba
}

// Resolves a type string to an interface ID using the package path and import map, returning nil if not found.
func paramTypeNameToInterface(typing string, pkgPath golang.PackagePath, importMap map[string]string, modulePath string, gt *golang.GolangTopology) *golang.InterfaceID {
	t := strings.TrimPrefix(typing, "*")

	var candidateIDs []golang.InterfaceID
	if idx := strings.Index(t, "."); idx > 0 {
		alias := t[:idx]
		typeName := t[idx+1:]
		if impPath, ok := importMap[alias]; ok {
			candidateIDs = append(candidateIDs, golang.InterfaceID(impPath+"."+typeName))
		}
	} else {
		candidateIDs = append(candidateIDs, golang.InterfaceID(string(pkgPath)+"."+t))
	}

	for _, id := range candidateIDs {
		if _, ok := gt.Interfaces[id]; ok {
			return &id
		}
	}
	return nil
}

// paramTypeNameToNamedType resolves a type string to a non-struct named type the topology
// holds, the way paramTypeNameToInterface does for interfaces. Only a plain named type
// qualifies: a composite written around one ([]Celsius, map[string]Celsius) is not that type
// and has none of its methods.
func paramTypeNameToNamedType(typing string, pkgPath golang.PackagePath, importMap map[string]string, gt *golang.GolangTopology) *golang.NamedTypeID {
	id := golang.NamedTypeID(canonicalTypeID(typing, pkgPath, importMap))
	if id == "" {
		return nil
	}
	if _, ok := gt.NamedTypes[id]; !ok {
		return nil
	}
	return &id
}

// Resolves a type string to a struct ID using the package path and import map, without topology lookup.
func paramTypeNameToStruct(typing string, pkgPath golang.PackagePath, importMap map[string]string, modulePath string) *golang.StructID {
	t := strings.TrimPrefix(typing, "*")

	if idx := strings.Index(t, "."); idx > 0 {
		alias := t[:idx]
		typeName := t[idx+1:]
		if impPath, ok := importMap[alias]; ok {
			sid := golang.StructID(impPath + "." + typeName)
			return &sid
		}
		return nil
	}

	sid := golang.StructID(string(pkgPath) + "." + t)
	return &sid
}

// canonicalTypeID returns the canonical topology resource ID (pkgPath.Name) for
// the simple named type written in `typing`, resolved against the DEFINING file's
// import map. It returns "" for composite types ([]T, map, chan, func, generics),
// predeclared/builtin types, and aliases absent from importMap. The result is a
// CANDIDATE id — callers must still verify it exists (a miss is the correct
// missing-node signal). This is computed at parse time so cross-package consumers
// don't need the defining file's import context to resolve the type.
func canonicalTypeID(typing string, pkgPath golang.PackagePath, importMap map[string]string) string {
	t := stripTypeArgs(strings.TrimPrefix(typing, "*"))
	if t == "" || strings.ContainsAny(t, " \t*[]{}()<>") {
		return ""
	}
	if idx := strings.Index(t, "."); idx > 0 {
		alias := t[:idx]
		name := t[idx+1:]
		if impPath, ok := importMap[alias]; ok {
			return impPath + "." + name
		}
		return ""
	}
	if goBuiltins[t] {
		return ""
	}
	return string(pkgPath) + "." + t
}

// stripTypeArgs reduces an instantiated generic type (Box[T], g.Pair[K, V]) to the generic type
// it instantiates, whose resource and methods are the ones an instantiation has. Anything else
// -- a slice, an array, a map, or a type-argument list that does not close at the end -- comes
// back unchanged.
func stripTypeArgs(t string) string {
	open := strings.IndexByte(t, '[')
	if open <= 0 || !strings.HasSuffix(t, "]") || t[:open] == "map" {
		return t
	}
	for _, r := range t[:open] {
		if r != '.' && r != '_' && !('a' <= r && r <= 'z') && !('A' <= r && r <= 'Z') && !('0' <= r && r <= '9') && r < 0x80 {
			return t
		}
	}
	depth := 0
	for i := open; i < len(t); i++ {
		switch t[i] {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 && i != len(t)-1 {
				return t
			}
		}
	}
	return t[:open]
}

// resolveVarType records the struct/interface type of variable `name` from a
// VariableDefinition. It prefers the precomputed canonical TypingID (resolved in
// the type's defining file, so cross-package return types resolve correctly) and
// falls back to the legacy string-based resolution in the current file's context.
func (ba *bodyAnalyzer) resolveVarType(name string, vd golang.VariableDefinition) {
	if name == "" {
		return
	}
	if vd.TypingID != "" {
		if _, ok := ba.gt.Structs[golang.StructID(vd.TypingID)]; ok {
			ba.varTypeMap[name] = golang.StructID(vd.TypingID)
			return
		}
		if _, ok := ba.gt.Interfaces[golang.InterfaceID(vd.TypingID)]; ok {
			ba.varIfaceMap[name] = golang.InterfaceID(vd.TypingID)
			return
		}
		if _, ok := ba.gt.NamedTypes[golang.NamedTypeID(vd.TypingID)]; ok {
			ba.varNamedTypeMap[name] = golang.NamedTypeID(vd.TypingID)
			return
		}
	}
	if vd.Typing == "" {
		return
	}
	if sid := paramTypeNameToStruct(vd.Typing, ba.pr.PkgPath, ba.pr.ImportMap, ba.pr.ModulePath); sid != nil {
		if _, ok := ba.gt.Structs[*sid]; ok {
			ba.varTypeMap[name] = *sid
			return
		}
	}
	if iid := paramTypeNameToInterface(vd.Typing, ba.pr.PkgPath, ba.pr.ImportMap, ba.pr.ModulePath, ba.gt); iid != nil {
		ba.varIfaceMap[name] = *iid
		return
	}
	if nid := paramTypeNameToNamedType(vd.Typing, ba.pr.PkgPath, ba.pr.ImportMap, ba.gt); nid != nil {
		ba.varNamedTypeMap[name] = *nid
	}
}

// Adds a unique connection ID to the body analyzer's connection map if not already present.
func (ba *bodyAnalyzer) add(kind golang.ConnectionKind, id string) {
	ids := ba.conn[kind]
	if !containsString(ids, id) {
		ba.conn[kind] = append(ids, id)
	}
	if kind == golang.ConnCalls {
		ba.recordCallSite(id)
	}
}

// recordCallSite stores what this call passes, so a later scan can ask whether the call
// still fits the callee rather than whether the callee merely changed.
//
// Deduped like the edges themselves: two textually identical calls to the same callee carry
// the same information once. Two calls with DIFFERENT shapes are two records, and the
// database keeps both because the shape is part of the primary key.
func (ba *bodyAnalyzer) recordCallSite(calleeID string) {
	if ba.currentCall == nil || calleeID == "" {
		return
	}
	rec := contract.EncodeCallSite(ba.callSiteOf(calleeID, ba.currentCall))
	if rec != "" && !containsString(ba.callSites, rec) {
		ba.callSites = append(ba.callSites, rec)
	}
}

// callSiteOf reads the argument shape of one call.
func (ba *bodyAnalyzer) callSiteOf(calleeID string, call *ast.CallExpr) contract.CallSite {
	site := contract.CallSite{
		CalleeID: calleeID,
		N:        len(call.Args),
		Variadic: call.Ellipsis != token.NoPos,
	}
	anyKnown := false
	types := make([]*string, 0, len(call.Args))
	for _, arg := range call.Args {
		tok := ba.argToken(arg)
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

// argToken names the type of one argument, or "" when it cannot be known.
//
// Deliberately narrow. A wrong token is a false warning about correct code, which is the
// failure this whole mechanism exists to remove, so anything requiring real inference --
// a call result, a field selection, an index expression, arithmetic on non-literals -- is
// left unknown rather than guessed. What is left is still most arguments in practice:
// literals, and parameters forwarded from the enclosing signature.
//
// A literal is reported as an untyped CLASS, not a type. Go's 5 is an untyped constant
// assignable to every numeric type, so recording "int" and comparing for equality would
// call f(5) against f(x float64) a mismatch. See contract.UntypedInt.
func (ba *bodyAnalyzer) argToken(arg ast.Expr) string {
	switch a := arg.(type) {
	case *ast.BasicLit:
		switch a.Kind {
		case token.INT:
			return contract.UntypedInt
		case token.FLOAT, token.IMAG:
			return contract.UntypedFloat
		case token.STRING:
			return contract.UntypedString
		case token.CHAR:
			return contract.UntypedRune
		}
	case *ast.Ident:
		switch a.Name {
		case "true", "false":
			return contract.UntypedBool
		case "nil":
			return contract.UntypedNil
		}
		// A forwarded parameter carries its declared type exactly, with no inference.
		// A local shadowing a parameter would make this wrong, so only report it when
		// nothing in the body redeclared the name.
		if t, ok := ba.paramTypes[a.Name]; ok && t != "" && !ba.shadowed[a.Name] {
			return t
		}
	}
	return ""
}

// Registers a deduped topology warning with its kind, target ID, and message.
func (ba *bodyAnalyzer) addWarning(kind domain.WarningKind, targetID string, message string) {
	id := fmt.Sprintf("%s@%s@%s", ba.callerID, kind, targetID)
	if ba.warnings == nil {
		return
	}
	if _, exists := (*ba.warnings)[id]; exists {
		return
	}
	(*ba.warnings)[id] = domain.TopologyWarning{
		ID:       id,
		SourceID: string(ba.callerID),
		Kind:     kind,
		TargetID: targetID,
		Message:  message,
	}
}

// Analyzes function body AST to resolve calls, type references, and variable usage into topology connections.
//
// body must come from a parse that resolved objects, as ParseFile's does: which names the
// function binds itself, and in which scope, is read from that resolution (see isLocal).
func analyzeFunctionBody(body *ast.BlockStmt, pr *ParseResult, gt *golang.GolangTopology, funcInput []golang.VariableDefinition, receiverName string, receiverStruct *golang.StructID, callerID golang.FunctionID, typeParamNames []string) map[golang.ConnectionKind][]string {
	localVarNames := collectLocalVarNames(body)
	ba := newBodyAnalyzer(pr, gt, funcInput, receiverName, receiverStruct, callerID, typeParamNames)
	ba.body = body
	ba.shadowed = make(map[string]bool, len(localVarNames))
	for _, n := range localVarNames {
		ba.shadowed[n] = true
	}

	var visit func(n ast.Node) bool
	var walkLit func(lit *ast.CompositeLit, implied ast.Expr)
	// walkElt walks one element or key of a composite literal. An element literal with its
	// type elided ([]T{{...}}, map[K]V{k: {...}}) takes its type from the enclosing literal,
	// which is what decides whether its own keys are field names.
	walkElt := func(e, implied ast.Expr) {
		if cl, ok := e.(*ast.CompositeLit); ok && cl.Type == nil {
			walkLit(cl, implied)
			return
		}
		ast.Inspect(e, visit)
	}
	walkLit = func(lit *ast.CompositeLit, implied ast.Expr) {
		ba.resolveCompositeLit(lit)
		typ := implied
		if lit.Type != nil {
			ast.Inspect(lit.Type, visit)
			typ = lit.Type
		}
		keyType, eltType, fieldKeys := ba.literalShape(typ)
		for _, elt := range lit.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				walkElt(elt, eltType)
				continue
			}
			// In a struct literal the key is a field name, not a reference to anything in
			// scope: &http.Client{Timeout: d} does not use this package's Timeout.
			if _, isIdent := kv.Key.(*ast.Ident); !isIdent || !fieldKeys {
				walkElt(kv.Key, keyType)
			}
			walkElt(kv.Value, eltType)
		}
	}

	visit = func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CallExpr:
			ba.resolveCallExpr(node)
		case *ast.CompositeLit:
			walkLit(node, nil)
			return false
		case *ast.SelectorExpr:
			// Sel is a field or method name, or the name inside a qualified identifier -- never
			// a reference to this package's scope, so only X is walked. The one thing the pair
			// itself names is another package's var or const, which the walk down X cannot see.
			ba.resolveQualifiedVarRef(node)
			ast.Inspect(node.X, visit)
			return false
		case *ast.Field:
			// A field's names declare (a closure's parameter or result, a struct field, an
			// interface method); only its type refers to anything.
			if node.Type != nil {
				ast.Inspect(node.Type, visit)
			}
			return false
		case *ast.Ident:
			ba.resolveIdentRef(node)
		case *ast.AssignStmt:
			ba.resolveAssignStmt(node)
		case *ast.DeclStmt:
			ba.resolveDeclStmt(node)
		case *ast.TypeSwitchStmt:
			// A type switch binds its variable to a different concrete type per
			// case, so walk it manually (resolveTypeSwitchStmt) instead of
			// letting ast.Inspect descend with no per-case type context.
			ba.resolveTypeSwitchStmt(node, visit)
			return false
		}
		return true
	}
	ast.Inspect(body, visit)

	// Records ride out as an ordinary connection kind, which is what makes every existing
	// mechanism apply to them unchanged: they are part of the caller's resourceSignature,
	// so a call whose shape changed marks the caller dirty; and the per-source
	// DELETE-then-reinsert every upsert performs rebuilds them wholesale. Sorted because
	// the at-scale suite compares connection sets across scan modes byte-for-byte, and
	// resolution order is not guaranteed to agree between a cold and an incremental pass.
	if len(ba.callSites) > 0 {
		sort.Strings(ba.callSites)
		ba.conn[golang.ConnectionKind(contract.CallSitesConn)] = ba.callSites
	}

	return ba.conn
}

var goBuiltins = map[string]bool{
	"append": true, "cap": true, "clear": true, "close": true,
	"complex": true, "copy": true, "delete": true, "imag": true,
	"len": true, "make": true, "max": true, "min": true,
	"new": true, "panic": true, "print": true,
	"println": true, "real": true, "recover": true,
	"any": true, "bool": true, "byte": true,
	"comparable": true, "complex64": true, "complex128": true,
	"error": true, "float32": true, "float64": true,
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"rune": true, "string": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"uintptr": true, "nil": true, "true": true, "false": true, "iota": true,
}

// isLocal reports whether id names something the function binds itself -- a parameter,
// result, receiver, type parameter, or a local variable, constant, type or label, its closures'
// included -- rather than a package-level declaration or an imported package.
//
// The answer comes from go/parser's object resolution, which applies the spec's block scoping:
// an identifier bound inside the function carries the object it resolves to, one naming a
// package-level declaration of THIS file carries that top-level declaration, and one declared in
// another file, a builtin or an import name carries nothing. Collecting every name the body
// declares, as this used to, let a local declared anywhere hide the package function it
// shadows only in its own block (process(); for _, process := range hs {...}), and let a
// parameter named like an import be resolved as the package.
func (ba *bodyAnalyzer) isLocal(id *ast.Ident) bool {
	if ba.knownNames[id.Name] {
		return true
	}
	if id.Obj == nil {
		return false
	}
	switch d := id.Obj.Decl.(type) {
	case *ast.FuncDecl:
		return false
	case *ast.ValueSpec:
		return ba.declaredInBody(d)
	case *ast.TypeSpec:
		return ba.declaredInBody(d)
	}
	// A signature *ast.Field, a := or range *ast.AssignStmt, a label, a receiver type
	// parameter: all bindings of this function or of a closure inside it.
	return true
}

// declaredInBody reports whether a var, const or type declaration sits inside the body being
// analyzed rather than at package level.
func (ba *bodyAnalyzer) declaredInBody(n ast.Node) bool {
	return ba.body != nil && ba.body.Lbrace <= n.Pos() && n.Pos() < ba.body.Rbrace
}

// literalShape reads a composite literal's type: the implied type of its keys and of its
// elements, for element literals that elide theirs, and whether an identifier key is a field
// name. Only a map or an array/slice literal -- spelled out, or a named type of this package
// declared as one -- has keys that are expressions; any other type is taken as a struct.
func (ba *bodyAnalyzer) literalShape(typ ast.Expr) (keyType, eltType ast.Expr, fieldKeys bool) {
	for {
		switch t := typ.(type) {
		case *ast.StarExpr:
			typ = t.X
		case *ast.ParenExpr:
			typ = t.X
		case *ast.IndexExpr:
			typ = t.X
		case *ast.IndexListExpr:
			typ = t.X
		case *ast.ArrayType:
			return nil, t.Elt, false
		case *ast.MapType:
			return t.Key, t.Value, false
		case *ast.Ident:
			if !ba.isLocal(t) {
				if nt, ok := ba.gt.NamedTypes[golang.NamedTypeID(string(ba.pr.PkgPath)+"."+t.Name)]; ok &&
					(strings.HasPrefix(nt.Underlying, "map[") || strings.HasPrefix(nt.Underlying, "[")) {
					return nil, nil, false
				}
			}
			return nil, nil, true
		default:
			return nil, nil, true
		}
	}
}

// Collects locally-defined variable names from a function body via assignment and range statements.
func collectLocalVarNames(body *ast.BlockStmt) []string {
	var names []string
	if body == nil {
		return names
	}
	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.AssignStmt:
			if node.Tok == token.DEFINE {
				for _, lhs := range node.Lhs {
					if ident, ok := lhs.(*ast.Ident); ok && ident.Name != "_" {
						names = append(names, ident.Name)
					}
				}
			}
		case *ast.RangeStmt:
			if node.Tok == token.DEFINE || node.Tok == token.ASSIGN {
				if ident, ok := node.Key.(*ast.Ident); ok && ident.Name != "_" {
					names = append(names, ident.Name)
				}
				if ident, ok := node.Value.(*ast.Ident); ok && ident.Name != "_" {
					names = append(names, ident.Name)
				}
			}
		}
		return true
	})
	return names
}

// Resolves function and type calls, creating connections to called functions, structs, and named types with qualified names.
func (ba *bodyAnalyzer) resolveCallExpr(call *ast.CallExpr) {
	// Resolution never recurses -- ast.Inspect descends into a nested call separately and
	// re-enters here -- so a plain assignment is enough to keep each call's arguments with
	// its own edges.
	ba.currentCall = call
	defer func() { ba.currentCall = nil }()

	switch fun := ba.calleeExpr(call.Fun).(type) {
	case *ast.Ident:
		if ba.isLocal(fun) {
			// A local bound to a method value (f := d.Sound) or method expression
			// (g := Dog.Sound) and then invoked resolves to the underlying method.
			if mid, ok := ba.varMethodMap[fun.Name]; ok {
				ba.add(golang.ConnCalls, string(mid))
			}
			return
		}

		for _, f := range ba.pr.Functions {
			if f.Function.Name == fun.Name && f.Function.MethodFrom == nil {
				ba.add(golang.ConnCalls, string(f.Function.ID))
				return
			}
		}

		pkgTypedID := golang.StructID(string(ba.pr.PkgPath) + "." + fun.Name)
		if _, exists := ba.gt.Structs[pkgTypedID]; exists {
			ba.add(golang.ConnUsesStruct, string(pkgTypedID))
			ba.addExternalPkg(ba.pr.PkgPath)
			return
		}

		namedTypeID := golang.NamedTypeID(string(ba.pr.PkgPath) + "." + fun.Name)
		for _, nt := range ba.pr.NamedTypes {
			if nt.Name == fun.Name {
				ba.add(golang.ConnUsesNamedType, string(namedTypeID))
				ba.addExternalPkg(ba.pr.PkgPath)
				return
			}
		}
		if _, exists := ba.gt.NamedTypes[namedTypeID]; exists {
			ba.add(golang.ConnUsesNamedType, string(namedTypeID))
			ba.addExternalPkg(ba.pr.PkgPath)
			return
		}

		pkgFuncID := golang.FunctionID(string(ba.pr.PkgPath) + "." + fun.Name)
		if _, exists := ba.gt.Functions[pkgFuncID]; exists {
			ba.add(golang.ConnCalls, string(pkgFuncID))
			ba.addExternalPkg(ba.pr.PkgPath)
			return
		}

		// A package-level variable of function type is a legitimate call target
		// (var Format = func(s string) string { ... }). resolveIdentRef records
		// the same uses_extvar edge when it walks the identifier, and records no
		// uses_package edge, so mirror it exactly here. What is load-bearing is
		// the return: without it a node that exists is reported as missing.
		pkgVarID := golang.ExternalVarID(string(ba.pr.PkgPath) + "." + fun.Name)
		if _, exists := ba.gt.ExternalVars[pkgVarID]; exists {
			ba.add(golang.ConnUsesExtVar, string(pkgVarID))
			return
		}

		// Iface(x) converts to an interface of this package: a use of it, as the qualified
		// pkg.Iface(x) already is in resolveQualifiedCall, not a call to a missing function.
		pkgIfaceID := golang.InterfaceID(string(ba.pr.PkgPath) + "." + fun.Name)
		if _, exists := ba.gt.Interfaces[pkgIfaceID]; exists {
			ba.add(golang.ConnUsesIface, string(pkgIfaceID))
			ba.addExternalPkg(ba.pr.PkgPath)
			return
		}

		ba.addWarning(domain.WarnUseMissingNode, string(pkgFuncID),
			fmt.Sprintf("function %s calls %s which does not exist in package %s", ba.callerID, pkgFuncID, ba.pr.PkgPath))

	case *ast.SelectorExpr:
		switch x := fun.X.(type) {
		case *ast.Ident:
			ba.resolveQualifiedCall(x, fun.Sel.Name)
		}
	}
}

// calleeExpr strips an explicit instantiation off a call's function expression: F[int](x)
// calls F, as F[int, string](x) does.
//
// Two or more type arguments parse as an *ast.IndexListExpr, which can be nothing else. ONE
// parses as an *ast.IndexExpr, which is also how an element of a slice or map of functions is
// called (handlers[i](x)) -- so that form is unwrapped only when its operand names a function or
// type this topology has, and a call through an indexed variable stays unresolved as before.
func (ba *bodyAnalyzer) calleeExpr(fun ast.Expr) ast.Expr {
	switch f := fun.(type) {
	case *ast.IndexListExpr:
		return f.X
	case *ast.IndexExpr:
		if ba.namesGeneric(f.X) {
			return f.X
		}
	}
	return fun
}

// namesGeneric reports whether x -- the operand of an index expression in call position -- names
// a function or a type rather than a value: a package-level one of this package, or one reached
// through an import.
func (ba *bodyAnalyzer) namesGeneric(x ast.Expr) bool {
	var pkg, name string
	switch e := x.(type) {
	case *ast.Ident:
		if ba.isLocal(e) {
			return false
		}
		for _, f := range ba.pr.Functions {
			if f.Function.Name == e.Name && f.Function.MethodFrom == nil {
				return true
			}
		}
		pkg, name = string(ba.pr.PkgPath), e.Name
	case *ast.SelectorExpr:
		id, ok := e.X.(*ast.Ident)
		if !ok || ba.isLocal(id) {
			return false
		}
		impPath, ok := ba.pr.ImportMap[id.Name]
		if !ok {
			return false
		}
		pkg, name = impPath, e.Sel.Name
	default:
		return false
	}
	target := pkg + "." + name
	if _, ok := ba.gt.Functions[golang.FunctionID(target)]; ok {
		return true
	}
	if _, ok := ba.gt.Structs[golang.StructID(target)]; ok {
		return true
	}
	if _, ok := ba.gt.NamedTypes[golang.NamedTypeID(target)]; ok {
		return true
	}
	_, ok := ba.gt.Interfaces[golang.InterfaceID(target)]
	return ok
}

// addExternalPkg records a uses_package edge only when the package is not the caller's own.
//
// The unqualified branches above resolve a bare name against ba.pr.PkgPath -- the package the
// CALLER is in -- and used to record a uses_package edge to it anyway. A package does not use
// itself, and gotools.goImportBlock renders that edge, so a read of almost any function in a
// multi-file package opened with `import ("<its own package>")`: a self-import, which is a
// compile error in Go, printed inside the fence a read promises is verbatim source. It fired
// only when the callee lived in a DIFFERENT FILE of the same package, because a same-file
// callee is resolved by the ba.pr.Functions loop and returns before reaching here.
func (ba *bodyAnalyzer) addExternalPkg(pkg golang.PackagePath) {
	if pkg == ba.pr.PkgPath {
		return
	}
	ba.add(golang.ConnUsesPkg, string(pkg))
}

// Resolves qualified calls (X.sel) to functions, methods, or interfaces based on import map, variable type map, and struct/interface definitions.
//
// A name the function binds itself is never the package an import of the same name would be:
// func Handle(user *user.User) { user.Save() } calls the parameter's method. So the import map
// is consulted only for a name that is not local, and the variable maps -- which hold nothing
// but locals -- only for one that is.
func (ba *bodyAnalyzer) resolveQualifiedCall(x *ast.Ident, selName string) {
	xName := x.Name
	local := ba.isLocal(x)
	if impPath, ok := ba.pr.ImportMap[xName]; ok && !local {
		internalPkg := golang.PackagePath(impPath)

		fnTargetID := golang.FunctionID(string(internalPkg) + "." + selName)
		if _, exists := ba.gt.Functions[fnTargetID]; exists {
			ba.add(golang.ConnCalls, string(fnTargetID))
			ba.add(golang.ConnUsesPkg, string(internalPkg))
			return
		}

		namedTargetID := golang.NamedTypeID(string(internalPkg) + "." + selName)
		if _, exists := ba.gt.NamedTypes[namedTargetID]; exists {
			ba.add(golang.ConnUsesNamedType, string(namedTargetID))
			ba.add(golang.ConnUsesPkg, string(internalPkg))
			return
		}

		// pkg.T(x) is a conversion, not a call. resolveCompositeLit already
		// resolves the pkg.T{...} form to the same pair of edges; match it.
		structTargetID := golang.StructID(string(internalPkg) + "." + selName)
		if _, exists := ba.gt.Structs[structTargetID]; exists {
			ba.add(golang.ConnUsesStruct, string(structTargetID))
			ba.add(golang.ConnUsesPkg, string(internalPkg))
			return
		}

		ifaceTargetID := golang.InterfaceID(string(internalPkg) + "." + selName)
		if _, exists := ba.gt.Interfaces[ifaceTargetID]; exists {
			ba.add(golang.ConnUsesIface, string(ifaceTargetID))
			ba.add(golang.ConnUsesPkg, string(internalPkg))
			return
		}

		// A func-typed package-level var in another package (var PrepareCmd =
		// func(*exec.Cmd) Runnable) is called as run.PrepareCmd(cmd). Unlike the
		// branches above this deliberately adds NO uses_package edge, mirroring
		// resolveUseMissingWarning's extvar case, which is the incremental path
		// this cold path has to stay byte-identical to.
		extVarTargetID := golang.ExternalVarID(string(internalPkg) + "." + selName)
		if _, exists := ba.gt.ExternalVars[extVarTargetID]; exists {
			ba.add(golang.ConnUsesExtVar, string(extVarTargetID))
			return
		}

		if ba.pr.internalImport(impPath) {
			ba.addWarning(domain.WarnUseMissingNode, string(fnTargetID),
				fmt.Sprintf("function %s calls %s which does not exist", ba.callerID, fnTargetID))
		}
		return
	}

	if structID, ok := ba.varTypeMap[xName]; ok && local {
		// A concrete struct-typed variable resolves to the concrete method only.
		// We deliberately do NOT consult interface.ImplementedBy() here: a cold
		// Scan runs body analysis BEFORE matchStructsToInterfaces, so those edges
		// are empty at this point and a concrete call yields no uses_interface
		// edge. The incremental path loads a gt with those edges already present,
		// so reading them would add a ghost uses_interface edge that the cold scan
		// never produces (see atscale GhostUsesInterfaceOnReparse). Keeping this
		// resolution ImplementedBy-independent makes the two paths agree.
		// structMethodID includes the methods promoted from embedded structs.
		if mid, ok := ba.structMethodID(structID, selName); ok {
			ba.add(golang.ConnCalls, string(mid))
			ba.add(golang.ConnUsesStruct, string(structID))
		}
		return
	}

	if ifaceID, ok := ba.varIfaceMap[xName]; ok && local {
		// An interface-typed variable records only the interface usage. As above,
		// we do NOT fan out to iface.ImplementedBy() implementers' methods: those
		// edges are empty during a cold Scan's body analysis, so adding them on the
		// incremental path (where they are populated) would diverge from the cold
		// scan. Stay ImplementedBy-independent.
		if iface, ok := ba.gt.Interfaces[ifaceID]; ok {
			for _, reqMethod := range iface.Methods {
				if reqMethod.Name == selName {
					ba.add(golang.ConnUsesIface, string(ifaceID))
					return
				}
			}
		}
		return
	}

	if ntID, ok := ba.varNamedTypeMap[xName]; ok && local {
		// A variable of a non-struct named type (`type Celsius float64`): its method set is
		// the type's own, with no embedding to promote through.
		if mid, ok := ba.namedTypeMethodID(ntID, selName); ok {
			ba.add(golang.ConnCalls, string(mid))
			ba.add(golang.ConnUsesNamedType, string(ntID))
		}
		return
	}

	structID := golang.StructID(string(ba.pr.PkgPath) + "." + xName)
	if mid, ok := ba.structMethodID(structID, selName); ok {
		ba.add(golang.ConnCalls, string(mid))
		ba.add(golang.ConnUsesStruct, string(structID))
		return
	}
	// A method EXPRESSION on a named type declared in this package: Celsius.String(c).
	ntID := golang.NamedTypeID(string(ba.pr.PkgPath) + "." + xName)
	if mid, ok := ba.namedTypeMethodID(ntID, selName); ok {
		ba.add(golang.ConnCalls, string(mid))
		ba.add(golang.ConnUsesNamedType, string(ntID))
	}
}

// Resolves composite literal types, creating connections to referenced structs and named types.
func (ba *bodyAnalyzer) resolveCompositeLit(lit *ast.CompositeLit) {
	typ := lit.Type
	if ile, ok := typ.(*ast.IndexListExpr); ok {
		typ = ile.X
	}
	switch t := typ.(type) {
	case *ast.Ident:
		if ba.isLocal(t) {
			return
		}
		structID := golang.StructID(string(ba.pr.PkgPath) + "." + t.Name)
		if _, exists := ba.gt.Structs[structID]; exists {
			ba.add(golang.ConnUsesStruct, string(structID))
			return
		}
		namedTypeID := golang.NamedTypeID(string(ba.pr.PkgPath) + "." + t.Name)
		for _, nt := range ba.pr.NamedTypes {
			if nt.Name == t.Name {
				ba.add(golang.ConnUsesNamedType, string(namedTypeID))
				return
			}
		}
		if _, exists := ba.gt.NamedTypes[namedTypeID]; exists {
			ba.add(golang.ConnUsesNamedType, string(namedTypeID))
			return
		}
		ba.addWarning(domain.WarnUseMissingNode, string(structID),
			fmt.Sprintf("function %s references struct %s which does not exist", ba.callerID, structID))
	case *ast.SelectorExpr:
		if x, ok := t.X.(*ast.Ident); ok {
			if impPath, ok := ba.pr.ImportMap[x.Name]; ok && ba.pr.internalImport(impPath) {
				internalPkg := golang.PackagePath(impPath)
				structID := golang.StructID(string(internalPkg) + "." + t.Sel.Name)
				if _, exists := ba.gt.Structs[structID]; exists {
					ba.add(golang.ConnUsesStruct, string(structID))
					ba.add(golang.ConnUsesPkg, string(internalPkg))
					return
				}
				namedTypeID := golang.NamedTypeID(string(internalPkg) + "." + t.Sel.Name)
				if _, exists := ba.gt.NamedTypes[namedTypeID]; exists {
					ba.add(golang.ConnUsesNamedType, string(namedTypeID))
					ba.add(golang.ConnUsesPkg, string(internalPkg))
					return
				}
				ba.addWarning(domain.WarnUseMissingNode, string(structID),
					fmt.Sprintf("function %s references struct %s which does not exist", ba.callerID, structID))
			}
		}
	}
}

// resolveQualifiedVarRef records the use of another package's var or const written as
// `pkg.Name` outside call position -- `a.Count++`, `return a.MaxRetries`. resolveIdentRef only
// ever looks in the CALLER's package, and the selector walk descends into X alone, so such a
// reference recorded nothing at all while the identical reference inside the declaring package
// recorded uses_extvar -- and deleting the declaration then warned nobody.
//
// No uses_package edge, deliberately: resolveQualifiedCall's extvar branch and
// resolveUseMissingWarning both omit it, and the incremental path re-resolves through the
// latter, so adding one here would make the two paths disagree.
func (ba *bodyAnalyzer) resolveQualifiedVarRef(sel *ast.SelectorExpr) {
	x, ok := sel.X.(*ast.Ident)
	if !ok || ba.isLocal(x) {
		return
	}
	impPath, ok := ba.pr.ImportMap[x.Name]
	if !ok {
		return
	}
	varID := golang.ExternalVarID(impPath + "." + sel.Sel.Name)
	if _, exists := ba.gt.ExternalVars[varID]; exists {
		ba.add(golang.ConnUsesExtVar, string(varID))
	}
}

// Records usage edges for identifiers that reference external variables, structs, or named types in the current package scope.
//
// The walk hands it only identifiers in reference position: a selector's Sel, a struct
// literal's key and a field's names are skipped by analyzeFunctionBody, and a declaration's own
// name is local to isLocal.
func (ba *bodyAnalyzer) resolveIdentRef(ident *ast.Ident) {
	if ident.Name == "_" || ba.isLocal(ident) {
		return
	}
	varID := golang.ExternalVarID(string(ba.pr.PkgPath) + "." + ident.Name)
	if _, exists := ba.gt.ExternalVars[varID]; exists {
		ba.add(golang.ConnUsesExtVar, string(varID))
	}

	structID := golang.StructID(string(ba.pr.PkgPath) + "." + ident.Name)
	if _, exists := ba.gt.Structs[structID]; exists {
		ba.add(golang.ConnUsesStruct, string(structID))
	}

	namedTypeID := golang.NamedTypeID(string(ba.pr.PkgPath) + "." + ident.Name)
	if _, exists := ba.gt.NamedTypes[namedTypeID]; exists {
		ba.add(golang.ConnUsesNamedType, string(namedTypeID))
	}
}

// Processes assignment statements, tracking variable names and resolving types from call expressions and composite literals.
func (ba *bodyAnalyzer) resolveAssignStmt(stmt *ast.AssignStmt) {
	if stmt.Tok != token.DEFINE {
		return
	}

	// Multi-value assignment from a single source: a, b := f() or the comma-ok
	// type assertion d, ok := a.(T).
	if len(stmt.Rhs) == 1 && len(stmt.Lhs) > 1 {
		switch rhs := stmt.Rhs[0].(type) {
		case *ast.CallExpr:
			ba.resolveMultiValueCallAssign(stmt.Lhs, rhs)
			return
		case *ast.TypeAssertExpr:
			ba.resolveTypeAssertAssign(stmt.Lhs, rhs)
			return
		}
	}

	for i, lhs := range stmt.Lhs {
		ident, ok := lhs.(*ast.Ident)
		if !ok || ident.Name == "_" {
			continue
		}

		if i >= len(stmt.Rhs) {
			continue
		}
		switch rhs := stmt.Rhs[i].(type) {
		case *ast.CompositeLit:
			ba.resolveCompositeLitAssign(ident.Name, rhs)
		case *ast.UnaryExpr:
			if rhs.Op == token.AND {
				if compLit, ok := rhs.X.(*ast.CompositeLit); ok {
					ba.resolveCompositeLitAssign(ident.Name, compLit)
				}
			}
		case *ast.CallExpr:
			ba.resolveCallExprAssign(ident.Name, rhs)
		case *ast.SelectorExpr:
			ba.resolveMethodRefAssign(ident.Name, rhs)
		case *ast.TypeAssertExpr:
			// d := a.(T): single-value type assertion binds the concrete type.
			if rhs.Type != nil {
				ba.bindConcreteType(ident.Name, rhs.Type)
			}
		}
	}
}

// resolveMethodRefAssign records that local `name` is bound to a method VALUE
// (recv.Method, where recv is a struct-typed variable) or a method EXPRESSION
// (Type.Method, where Type is a struct in this package). The bound method is
// stored in varMethodMap so a later call through `name` (name(...)) resolves to
// the underlying method in resolveCallExpr.
func (ba *bodyAnalyzer) resolveMethodRefAssign(name string, sel *ast.SelectorExpr) {
	xIdent, ok := sel.X.(*ast.Ident)
	if !ok {
		return
	}
	// Method value: x is a struct-typed variable (d.Sound).
	if structID, ok := ba.varTypeMap[xIdent.Name]; ok {
		if mid, ok := ba.structMethodID(structID, sel.Sel.Name); ok {
			ba.varMethodMap[name] = mid
		}
		return
	}
	// Method expression: x is a struct type in this package (Dog.Sound).
	structID := golang.StructID(string(ba.pr.PkgPath) + "." + xIdent.Name)
	if mid, ok := ba.structMethodID(structID, sel.Sel.Name); ok {
		ba.varMethodMap[name] = mid
	}
}

// maxPromotionDepth bounds how many levels of embedding structMethodID searches.
const maxPromotionDepth = 8

// structMethodID returns the FunctionID of the method named `methodName` in the method set of
// the struct `structID`, if the topology has it: the struct's own method, or one promoted from a
// struct it embeds.
//
// Promotion follows Go's rule: the shallowest depth wins, and a field of that name at a
// shallower depth hides a deeper method. o.Hello() on type Outer struct{ Base } calls
// Base.Hello, and before this resolved to nothing -- no calls edge and no call site, so a
// signature change to Base.Hello never warned the code calling it through Outer.
func (ba *bodyAnalyzer) structMethodID(structID golang.StructID, methodName string) (golang.FunctionID, bool) {
	level := []golang.StructID{structID}
	seen := map[golang.StructID]bool{structID: true}
	for depth := 0; depth < maxPromotionDepth && len(level) > 0; depth++ {
		var next []golang.StructID
		shadowed := false
		for _, sid := range level {
			str, ok := ba.gt.Structs[sid]
			if !ok {
				continue
			}
			for _, mid := range str.Methods() {
				if m, ok := ba.gt.Functions[mid]; ok && m.Name == methodName {
					return mid, true
				}
			}
			for _, p := range str.Params {
				if p.Name == methodName {
					shadowed = true
				}
				if emb, ok := ba.embeddedStruct(sid, p); ok && !seen[emb] {
					seen[emb] = true
					next = append(next, emb)
				}
			}
		}
		if shadowed {
			return "", false
		}
		level = next
	}
	return "", false
}

// namedTypeMethodID returns the FunctionID of the method named `methodName` in the method set
// of the named type `ntID`. A non-struct named type embeds nothing, so unlike structMethodID
// there is no promotion chain to walk.
func (ba *bodyAnalyzer) namedTypeMethodID(ntID golang.NamedTypeID, methodName string) (golang.FunctionID, bool) {
	nt, ok := ba.gt.NamedTypes[ntID]
	if !ok {
		return "", false
	}
	for _, mid := range nt.Connections[golang.ConnHasMethod] {
		if m, ok := ba.gt.Functions[golang.FunctionID(mid)]; ok && m.Name == methodName {
			return golang.FunctionID(mid), true
		}
	}
	return "", false
}

// embeddedStruct returns the struct an embedded field of owner names, when p is one and the
// topology has it. An embedded field is recorded with its type expression as its name
// (processStruct); its TypingID is the type's canonical id, resolved in the declaring file, and
// a field recorded without one falls back to a plain type name in owner's own package.
func (ba *bodyAnalyzer) embeddedStruct(owner golang.StructID, p golang.VariableDefinition) (golang.StructID, bool) {
	return embeddedStructOf(ba.gt, owner, p)
}

// embeddedStructOf is embeddedStruct against a topology rather than a body analyzer, so the
// interface matcher -- which runs with no body in hand -- can walk the same embedding chain.
func embeddedStructOf(gt *golang.GolangTopology, owner golang.StructID, p golang.VariableDefinition) (golang.StructID, bool) {
	if p.Name == "" || p.Name != p.Typing {
		return "", false
	}
	id := p.TypingID
	if id == "" {
		name := stripTypeArgs(strings.TrimPrefix(p.Typing, "*"))
		dot := strings.LastIndex(string(owner), ".")
		if strings.Contains(name, ".") || dot < 0 {
			return "", false
		}
		id = string(owner)[:dot] + "." + name
	}
	if _, ok := gt.Structs[golang.StructID(id)]; !ok {
		return "", false
	}
	return golang.StructID(id), true
}

// Records the type of a variable assigned from a composite literal, mapping it to its struct or interface type.
func (ba *bodyAnalyzer) resolveCompositeLitAssign(name string, lit *ast.CompositeLit) {
	typ := lit.Type
	if ile, ok := typ.(*ast.IndexListExpr); ok {
		typ = ile.X
	}
	switch t := typ.(type) {
	case *ast.Ident:
		if ba.isLocal(t) {
			return
		}
		structID := golang.StructID(string(ba.pr.PkgPath) + "." + t.Name)
		if _, exists := ba.gt.Structs[structID]; exists {
			ba.varTypeMap[name] = structID
			return
		}
		ifaceID := golang.InterfaceID(string(ba.pr.PkgPath) + "." + t.Name)
		if _, exists := ba.gt.Interfaces[ifaceID]; exists {
			ba.varIfaceMap[name] = ifaceID
		}
	case *ast.SelectorExpr:
		if x, ok := t.X.(*ast.Ident); ok {
			if impPath, ok := ba.pr.ImportMap[x.Name]; ok {
				internalPkg := golang.PackagePath(impPath)
				structID := golang.StructID(string(internalPkg) + "." + t.Sel.Name)
				if _, exists := ba.gt.Structs[structID]; exists {
					ba.varTypeMap[name] = structID
					return
				}
				ifaceID := golang.InterfaceID(string(internalPkg) + "." + t.Sel.Name)
				if _, exists := ba.gt.Interfaces[ifaceID]; exists {
					ba.varIfaceMap[name] = ifaceID
				}
			}
		}
	}
}

// selectorCalleeID resolves the function that `x.Sel(...)` targets, for an assignment that
// needs the callee's declared return type. It reports "" when the base is a shape this
// analyzer cannot type. The CALL's own edges are not its business: resolveCallExpr walks the
// same node separately and records those.
//
// IT MIRRORS resolveQualifiedCall, BRANCH FOR BRANCH -- imported package, then a struct-typed
// variable (which includes the enclosing method's receiver, seeded into varTypeMap by
// newBodyAnalyzer), then a struct TYPE name in this package for a method expression. That is
// the whole point of extracting it: two different rules for what `x.Sel` means would let an
// assignment bind a type that disagrees with the call edge recorded for the very same
// expression. The order matters as much as the branches -- a local shadowing a package-level
// type name must resolve as the local, exactly as it does there.
//
// AN INTERFACE-TYPED BASE RESOLVES TO NOTHING, deliberately. resolveQualifiedCall records only
// a uses_interface edge for one and refuses to fan out to ImplementedBy, because those edges
// are empty during a cold scan's body analysis and populated on the incremental path -- read
// them and the two scans stop agreeing. An interface has no single callee whose return type
// could be read, so picking an implementer here would smuggle that same divergence back in
// through the type map. Falling through to the package-struct branch would be worse still: it
// would resolve `s.Get()` against a STRUCT that merely shares the interface variable's name.
//
// Anything else -- a chained `a.B().C()`, an index expression, a call through a func-typed
// field -- returns false and leaves the name untyped, which is what happened to every one of
// these shapes before.
func (ba *bodyAnalyzer) selectorCalleeID(sel *ast.SelectorExpr) (golang.FunctionID, bool) {
	x, ok := sel.X.(*ast.Ident)
	if !ok {
		return "", false
	}
	local := ba.isLocal(x)
	if impPath, ok := ba.pr.ImportMap[x.Name]; ok && !local {
		// A candidate id: the caller verifies it against gt.Functions, as every branch here
		// is verified downstream.
		return golang.FunctionID(impPath + "." + sel.Sel.Name), true
	}
	if structID, ok := ba.varTypeMap[x.Name]; ok && local {
		return ba.structMethodID(structID, sel.Sel.Name)
	}
	if _, ok := ba.varIfaceMap[x.Name]; ok && local {
		return "", false
	}
	if ntID, ok := ba.varNamedTypeMap[x.Name]; ok && local {
		return ba.namedTypeMethodID(ntID, sel.Sel.Name)
	}
	// A method EXPRESSION on a type declared in this package: Dog.Sound(d).
	if mid, ok := ba.structMethodID(golang.StructID(string(ba.pr.PkgPath)+"."+x.Name), sel.Sel.Name); ok {
		return mid, true
	}
	return ba.namedTypeMethodID(golang.NamedTypeID(string(ba.pr.PkgPath)+"."+x.Name), sel.Sel.Name)
}

// Infers the type of a variable assigned from a function call by looking up the function's return type.
func (ba *bodyAnalyzer) resolveCallExprAssign(name string, call *ast.CallExpr) {
	var funcID golang.FunctionID
	switch fn := ba.calleeExpr(call.Fun).(type) {
	case *ast.Ident:
		if ba.isLocal(fn) {
			return
		}
		funcID = golang.FunctionID(string(ba.pr.PkgPath) + "." + fn.Name)
	case *ast.SelectorExpr:
		id, ok := ba.selectorCalleeID(fn)
		if !ok {
			return
		}
		funcID = id
	default:
		return
	}

	if funcID == "" {
		return
	}

	targetFunc, ok := ba.gt.Functions[funcID]
	if !ok {
		return
	}
	if len(targetFunc.Output) == 0 {
		return
	}
	ba.resolveVarType(name, targetFunc.Output[0])
}

// resolveMultiValueCallAssign handles `a, b := f()` where one call feeds several
// LHS names; it maps each name to the struct/interface type of the corresponding
// return value so later method calls on those names resolve.
//
// `mgr, err := s.chatManager()` is the shape that made this matter. The selector branch used
// to resolve an imported PACKAGE only, so a value returned by a METHOD -- the overwhelmingly
// common `x, err := recv.Thing()` -- left x untyped, and every x.Method() after it resolved to
// nothing. No calls edge meant no caller for getCallers to find, which meant a signature
// change to Thing's callee raised no signature_changed warning at all: a function whose only
// callers reach it this way was invisible to the whole warning path. See selectorCalleeID.
func (ba *bodyAnalyzer) resolveMultiValueCallAssign(lhs []ast.Expr, call *ast.CallExpr) {
	var funcID golang.FunctionID
	switch fn := ba.calleeExpr(call.Fun).(type) {
	case *ast.Ident:
		if ba.isLocal(fn) {
			return
		}
		funcID = golang.FunctionID(string(ba.pr.PkgPath) + "." + fn.Name)
	case *ast.SelectorExpr:
		id, ok := ba.selectorCalleeID(fn)
		if !ok {
			return
		}
		funcID = id
	default:
		return
	}
	if funcID == "" {
		return
	}

	targetFunc, ok := ba.gt.Functions[funcID]
	if !ok {
		return
	}

	for i, l := range lhs {
		ident, ok := l.(*ast.Ident)
		if !ok || ident.Name == "_" {
			continue
		}
		if i >= len(targetFunc.Output) {
			continue
		}
		ba.resolveVarType(ident.Name, targetFunc.Output[i])
	}
}

// resolveTypeAssertAssign handles the comma-ok type assertion `d, ok := a.(T)`:
// it marks every LHS name as known and binds the first name to the asserted
// concrete type T so a later method call on it resolves.
func (ba *bodyAnalyzer) resolveTypeAssertAssign(lhs []ast.Expr, assert *ast.TypeAssertExpr) {
	if assert.Type == nil || len(lhs) == 0 {
		return
	}
	if ident, ok := lhs[0].(*ast.Ident); ok && ident.Name != "_" {
		ba.bindConcreteType(ident.Name, assert.Type)
	}
}

// resolveTypeSwitchStmt walks a type switch (`switch v := a.(type) { case T: ... }`)
// manually so the bound variable carries the concrete per-case type while that
// case body is analyzed; method calls on it then resolve to the concrete type's
// method. The switch operand and each case body are still inspected via the
// shared visitor. The binding is restored after the switch (the variable is
// scoped to it).
func (ba *bodyAnalyzer) resolveTypeSwitchStmt(stmt *ast.TypeSwitchStmt, visit func(ast.Node) bool) {
	if stmt.Init != nil {
		ast.Inspect(stmt.Init, visit)
	}

	var varName string
	switch assign := stmt.Assign.(type) {
	case *ast.AssignStmt:
		if assign.Tok == token.DEFINE && len(assign.Lhs) == 1 {
			if ident, ok := assign.Lhs[0].(*ast.Ident); ok && ident.Name != "_" {
				varName = ident.Name
			}
		}
		for _, rhs := range assign.Rhs {
			ast.Inspect(rhs, visit)
		}
	case *ast.ExprStmt:
		ast.Inspect(assign.X, visit)
	}

	prevStruct, hadStruct := ba.varTypeMap[varName]
	prevIface, hadIface := ba.varIfaceMap[varName]
	prevNamed, hadNamed := ba.varNamedTypeMap[varName]

	for _, clause := range stmt.Body.List {
		cc, ok := clause.(*ast.CaseClause)
		if !ok {
			continue
		}
		if varName != "" {
			delete(ba.varTypeMap, varName)
			delete(ba.varIfaceMap, varName)
			delete(ba.varNamedTypeMap, varName)
			// A single concrete type per case gives the variable that type.
			if len(cc.List) == 1 {
				ba.bindConcreteType(varName, cc.List[0])
			}
		}
		for _, bodyStmt := range cc.Body {
			ast.Inspect(bodyStmt, visit)
		}
	}

	if varName != "" {
		delete(ba.varTypeMap, varName)
		delete(ba.varIfaceMap, varName)
		delete(ba.varNamedTypeMap, varName)
		if hadStruct {
			ba.varTypeMap[varName] = prevStruct
		}
		if hadIface {
			ba.varIfaceMap[varName] = prevIface
		}
		if hadNamed {
			ba.varNamedTypeMap[varName] = prevNamed
		}
	}
}

// bindConcreteType records the struct/interface type written as `typeExpr` for
// the local variable `name`, so later method calls on `name` resolve. Used for
// type-assertion-bound (`d := a.(T)`) and type-switch-case-bound (`case T:`)
// variables.
func (ba *bodyAnalyzer) bindConcreteType(name string, typeExpr ast.Expr) {
	typeStr := exprToString(typeExpr)
	if typeStr == "" {
		return
	}
	if sid := paramTypeNameToStruct(typeStr, ba.pr.PkgPath, ba.pr.ImportMap, ba.pr.ModulePath); sid != nil {
		if _, ok := ba.gt.Structs[*sid]; ok {
			ba.varTypeMap[name] = *sid
			return
		}
	}
	if iid := paramTypeNameToInterface(typeStr, ba.pr.PkgPath, ba.pr.ImportMap, ba.pr.ModulePath, ba.gt); iid != nil {
		ba.varIfaceMap[name] = *iid
		return
	}
	if nid := paramTypeNameToNamedType(typeStr, ba.pr.PkgPath, ba.pr.ImportMap, ba.gt); nid != nil {
		ba.varNamedTypeMap[name] = *nid
	}
}

// Extracts variable declarations and maps their types to known structs or interfaces for later use during call resolution.
func (ba *bodyAnalyzer) resolveDeclStmt(decl *ast.DeclStmt) {
	genDecl, ok := decl.Decl.(*ast.GenDecl)
	if !ok || genDecl.Tok != token.VAR {
		return
	}
	for _, spec := range genDecl.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok || vs.Type == nil {
			continue
		}
		typeStr := exprToString(vs.Type)
		if typeStr == "" {
			continue
		}
		for _, name := range vs.Names {
			if name.Name == "_" || ba.knownNames[name.Name] {
				continue
			}

			if sid := paramTypeNameToStruct(typeStr, ba.pr.PkgPath, ba.pr.ImportMap, ba.pr.ModulePath); sid != nil {
				if _, ok := ba.gt.Structs[*sid]; ok {
					ba.varTypeMap[name.Name] = *sid
					continue
				}
			}
			if iid := paramTypeNameToInterface(typeStr, ba.pr.PkgPath, ba.pr.ImportMap, ba.pr.ModulePath, ba.gt); iid != nil {
				ba.varIfaceMap[name.Name] = *iid
				continue
			}
			if nid := paramTypeNameToNamedType(typeStr, ba.pr.PkgPath, ba.pr.ImportMap, ba.gt); nid != nil {
				ba.varNamedTypeMap[name.Name] = *nid
			}
		}
	}
}

// Checks whether a string exists in a slice.
func containsString(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
