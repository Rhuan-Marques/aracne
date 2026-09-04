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
	callerID     golang.FunctionID
	warnings     *map[string]domain.TopologyWarning
	knownNames   map[string]bool

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
		pr:           pr,
		gt:           gt,
		conn:         make(map[golang.ConnectionKind][]string),
		varTypeMap:   make(map[string]golang.StructID),
		varIfaceMap:  make(map[string]golang.InterfaceID),
		varMethodMap: make(map[string]golang.FunctionID),
		callerID:     callerID,
		warnings:     &gt.Warnings,
		knownNames:   make(map[string]bool),
		paramTypes:   make(map[string]string),
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
	t := strings.TrimPrefix(typing, "*")
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
func analyzeFunctionBody(body *ast.BlockStmt, pr *ParseResult, gt *golang.GolangTopology, funcInput []golang.VariableDefinition, receiverName string, receiverStruct *golang.StructID, callerID golang.FunctionID, typeParamNames []string) map[golang.ConnectionKind][]string {
	localTypeNames := collectLocalTypeNames(body)
	localVarNames := collectLocalVarNames(body)
	extraKnownNames := make([]string, 0, len(typeParamNames)+len(localTypeNames)+len(localVarNames))
	extraKnownNames = append(extraKnownNames, typeParamNames...)
	extraKnownNames = append(extraKnownNames, localTypeNames...)
	extraKnownNames = append(extraKnownNames, localVarNames...)
	ba := newBodyAnalyzer(pr, gt, funcInput, receiverName, receiverStruct, callerID, extraKnownNames)
	ba.shadowed = make(map[string]bool, len(localVarNames))
	for _, n := range localVarNames {
		ba.shadowed[n] = true
	}

	var visit func(n ast.Node) bool
	visit = func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CallExpr:
			ba.resolveCallExpr(node)
		case *ast.CompositeLit:
			ba.resolveCompositeLit(node)
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

// Extracts the names of locally defined types from an AST block statement.
func collectLocalTypeNames(body *ast.BlockStmt) []string {
	var names []string
	ast.Inspect(body, func(n ast.Node) bool {
		if ts, ok := n.(*ast.TypeSpec); ok && ts.Name != nil {
			names = append(names, ts.Name.Name)
			return false
		}
		return true
	})
	return names
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

	fun := call.Fun
	if ile, ok := fun.(*ast.IndexListExpr); ok {
		fun = ile.X
	}
	switch fun := fun.(type) {
	case *ast.Ident:
		// A local bound to a method value (f := d.Sound) or method expression
		// (g := Dog.Sound) and then invoked resolves to the underlying method.
		if mid, ok := ba.varMethodMap[fun.Name]; ok {
			ba.add(golang.ConnCalls, string(mid))
			return
		}
		if ba.knownNames[fun.Name] {
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
			ba.add(golang.ConnUsesPkg, string(ba.pr.PkgPath))
			return
		}

		namedTypeID := golang.NamedTypeID(string(ba.pr.PkgPath) + "." + fun.Name)
		for _, nt := range ba.pr.NamedTypes {
			if nt.Name == fun.Name {
				ba.add(golang.ConnUsesNamedType, string(namedTypeID))
				ba.add(golang.ConnUsesPkg, string(ba.pr.PkgPath))
				return
			}
		}
		if _, exists := ba.gt.NamedTypes[namedTypeID]; exists {
			ba.add(golang.ConnUsesNamedType, string(namedTypeID))
			ba.add(golang.ConnUsesPkg, string(ba.pr.PkgPath))
			return
		}

		pkgFuncID := golang.FunctionID(string(ba.pr.PkgPath) + "." + fun.Name)
		if _, exists := ba.gt.Functions[pkgFuncID]; exists {
			ba.add(golang.ConnCalls, string(pkgFuncID))
			ba.add(golang.ConnUsesPkg, string(ba.pr.PkgPath))
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

		ba.addWarning(domain.WarnUseMissingNode, string(pkgFuncID),
			fmt.Sprintf("function %s calls %s which does not exist in package %s", ba.callerID, pkgFuncID, ba.pr.PkgPath))

	case *ast.SelectorExpr:
		switch x := fun.X.(type) {
		case *ast.Ident:
			ba.resolveQualifiedCall(x.Name, fun.Sel.Name)
		}
	}
}

// Resolves qualified calls (X.sel) to functions, methods, or interfaces based on import map, variable type map, and struct/interface definitions.
func (ba *bodyAnalyzer) resolveQualifiedCall(xName, selName string) {
	if impPath, ok := ba.pr.ImportMap[xName]; ok {
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

		if isInternalImport(impPath, ba.pr.ModulePath) {
			ba.addWarning(domain.WarnUseMissingNode, string(fnTargetID),
				fmt.Sprintf("function %s calls %s which does not exist", ba.callerID, fnTargetID))
		}
		return
	}

	if structID, ok := ba.varTypeMap[xName]; ok {
		// A concrete struct-typed variable resolves to the concrete method only.
		// We deliberately do NOT consult interface.ImplementedBy() here: a cold
		// Scan runs body analysis BEFORE matchStructsToInterfaces, so those edges
		// are empty at this point and a concrete call yields no uses_interface
		// edge. The incremental path loads a gt with those edges already present,
		// so reading them would add a ghost uses_interface edge that the cold scan
		// never produces (see atscale GhostUsesInterfaceOnReparse). Keeping this
		// resolution ImplementedBy-independent makes the two paths agree.
		if str, ok := ba.gt.Structs[structID]; ok {
			for _, mid := range str.Methods() {
				m, ok := ba.gt.Functions[mid]
				if ok && m.Name == selName {
					ba.add(golang.ConnCalls, string(mid))
					ba.add(golang.ConnUsesStruct, string(structID))
					break
				}
			}
		}
		return
	}

	if ifaceID, ok := ba.varIfaceMap[xName]; ok {
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

	structID := golang.StructID(string(ba.pr.PkgPath) + "." + xName)
	if str, ok := ba.gt.Structs[structID]; ok {
		for _, mid := range str.Methods() {
			m, ok := ba.gt.Functions[mid]
			if ok && m.Name == selName {
				ba.add(golang.ConnCalls, string(mid))
				ba.add(golang.ConnUsesStruct, string(structID))
				return
			}
		}
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
		if ba.knownNames[t.Name] {
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
			if impPath, ok := ba.pr.ImportMap[x.Name]; ok && isInternalImport(impPath, ba.pr.ModulePath) {
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

// Records usage edges for identifiers that reference external variables, structs, or named types in the current package scope.
func (ba *bodyAnalyzer) resolveIdentRef(ident *ast.Ident) {
	if ident.Name == "_" || ba.knownNames[ident.Name] {
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
		ba.knownNames[ident.Name] = true

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

// structMethodID returns the FunctionID of the method named `methodName` on the
// struct `structID`, if such a method exists in the topology.
func (ba *bodyAnalyzer) structMethodID(structID golang.StructID, methodName string) (golang.FunctionID, bool) {
	str, ok := ba.gt.Structs[structID]
	if !ok {
		return "", false
	}
	for _, mid := range str.Methods() {
		if m, ok := ba.gt.Functions[mid]; ok && m.Name == methodName {
			return mid, true
		}
	}
	return "", false
}

// Records the type of a variable assigned from a composite literal, mapping it to its struct or interface type.
func (ba *bodyAnalyzer) resolveCompositeLitAssign(name string, lit *ast.CompositeLit) {
	typ := lit.Type
	if ile, ok := typ.(*ast.IndexListExpr); ok {
		typ = ile.X
	}
	switch t := typ.(type) {
	case *ast.Ident:
		if ba.knownNames[t.Name] {
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

// Infers the type of a variable assigned from a function call by looking up the function's return type.
func (ba *bodyAnalyzer) resolveCallExprAssign(name string, call *ast.CallExpr) {
	fun := call.Fun
	if ile, ok := fun.(*ast.IndexListExpr); ok {
		fun = ile.X
	}

	var funcID golang.FunctionID
	switch fn := fun.(type) {
	case *ast.Ident:
		if ba.knownNames[fn.Name] {
			return
		}
		funcID = golang.FunctionID(string(ba.pr.PkgPath) + "." + fn.Name)
	case *ast.SelectorExpr:
		if x, ok := fn.X.(*ast.Ident); ok {
			if impPath, ok := ba.pr.ImportMap[x.Name]; ok {
				funcID = golang.FunctionID(impPath + "." + fn.Sel.Name)
			}
		}
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
func (ba *bodyAnalyzer) resolveMultiValueCallAssign(lhs []ast.Expr, call *ast.CallExpr) {
	for _, l := range lhs {
		if ident, ok := l.(*ast.Ident); ok && ident.Name != "_" {
			ba.knownNames[ident.Name] = true
		}
	}

	fun := call.Fun
	if ile, ok := fun.(*ast.IndexListExpr); ok {
		fun = ile.X
	}

	var funcID golang.FunctionID
	switch fn := fun.(type) {
	case *ast.Ident:
		funcID = golang.FunctionID(string(ba.pr.PkgPath) + "." + fn.Name)
	case *ast.SelectorExpr:
		if x, ok := fn.X.(*ast.Ident); ok {
			if impPath, ok := ba.pr.ImportMap[x.Name]; ok {
				funcID = golang.FunctionID(impPath + "." + fn.Sel.Name)
			}
		}
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
	for _, l := range lhs {
		if ident, ok := l.(*ast.Ident); ok && ident.Name != "_" {
			ba.knownNames[ident.Name] = true
		}
	}
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

	for _, clause := range stmt.Body.List {
		cc, ok := clause.(*ast.CaseClause)
		if !ok {
			continue
		}
		if varName != "" {
			delete(ba.varTypeMap, varName)
			delete(ba.varIfaceMap, varName)
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
		if hadStruct {
			ba.varTypeMap[varName] = prevStruct
		}
		if hadIface {
			ba.varIfaceMap[varName] = prevIface
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
			ba.knownNames[name.Name] = true

			if sid := paramTypeNameToStruct(typeStr, ba.pr.PkgPath, ba.pr.ImportMap, ba.pr.ModulePath); sid != nil {
				if _, ok := ba.gt.Structs[*sid]; ok {
					ba.varTypeMap[name.Name] = *sid
					continue
				}
			}
			if iid := paramTypeNameToInterface(typeStr, ba.pr.PkgPath, ba.pr.ImportMap, ba.pr.ModulePath, ba.gt); iid != nil {
				ba.varIfaceMap[name.Name] = *iid
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
