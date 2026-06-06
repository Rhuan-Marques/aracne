package goscanner

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"

	"ltp/internal/topology/domain"
	"ltp/internal/topology/golang"
)

type bodyAnalyzer struct {
	pr         *ParseResult
	gt         *golang.GolangTopology
	conn       map[golang.ConnectionKind][]string
	varTypeMap map[string]golang.StructID
	varIfaceMap map[string]golang.InterfaceID
	callerID   golang.FunctionID
	warnings   *map[string]domain.TopologyWarning
	knownNames map[string]bool
}

func newBodyAnalyzer(pr *ParseResult, gt *golang.GolangTopology, funcInput []golang.VariableDefinition, receiverName string, receiverStruct *golang.StructID, callerID golang.FunctionID, extraKnownNames []string) *bodyAnalyzer {
	ba := &bodyAnalyzer{
		pr:          pr,
		gt:          gt,
		conn:        make(map[golang.ConnectionKind][]string),
		varTypeMap:  make(map[string]golang.StructID),
		varIfaceMap: make(map[string]golang.InterfaceID),
		callerID:    callerID,
		warnings:    &gt.Warnings,
		knownNames:  make(map[string]bool),
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
		}
		if sid := paramTypeNameToStruct(param.Typing, pr.PkgPath, pr.ImportMap, pr.ModulePath); sid != nil {
			if _, ok := gt.Structs[*sid]; ok {
				ba.varTypeMap[param.Name] = *sid
			}
		}
		if iid := paramTypeNameToInterface(param.Typing, pr.PkgPath, pr.ImportMap, pr.ModulePath, gt); iid != nil {
			ba.varIfaceMap[param.Name] = *iid
		}
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

func (ba *bodyAnalyzer) add(kind golang.ConnectionKind, id string) {
	ids := ba.conn[kind]
	if !containsString(ids, id) {
		ba.conn[kind] = append(ids, id)
	}
}

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

func analyzeFunctionBody(body *ast.BlockStmt, pr *ParseResult, gt *golang.GolangTopology, funcInput []golang.VariableDefinition, receiverName string, receiverStruct *golang.StructID, callerID golang.FunctionID, typeParamNames []string) map[golang.ConnectionKind][]string {
	localTypeNames := collectLocalTypeNames(body)
	localVarNames := collectLocalVarNames(body)
	extraKnownNames := make([]string, 0, len(typeParamNames)+len(localTypeNames)+len(localVarNames))
	extraKnownNames = append(extraKnownNames, typeParamNames...)
	extraKnownNames = append(extraKnownNames, localTypeNames...)
	extraKnownNames = append(extraKnownNames, localVarNames...)
	ba := newBodyAnalyzer(pr, gt, funcInput, receiverName, receiverStruct, callerID, extraKnownNames)

	ast.Inspect(body, func(n ast.Node) bool {
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
		}
		return true
	})

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

func (ba *bodyAnalyzer) resolveCallExpr(call *ast.CallExpr) {
	fun := call.Fun
	if ile, ok := fun.(*ast.IndexListExpr); ok {
		fun = ile.X
	}
	switch fun := fun.(type) {
	case *ast.Ident:
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

		ba.addWarning(domain.WarnUseMissingNode, string(pkgFuncID),
			fmt.Sprintf("function %s calls %s which does not exist in package %s", ba.callerID, fun.Name, ba.pr.PkgPath))

	case *ast.SelectorExpr:
		switch x := fun.X.(type) {
		case *ast.Ident:
			ba.resolveQualifiedCall(x.Name, fun.Sel.Name)
		}
	}
}

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

		ba.addWarning(domain.WarnUseMissingNode, string(fnTargetID),
			fmt.Sprintf("function %s calls %s which does not exist", ba.callerID, fnTargetID))
		return
	}

	if structID, ok := ba.varTypeMap[xName]; ok {
		if str, ok := ba.gt.Structs[structID]; ok {
			foundDirect := false
			for _, mid := range str.Methods() {
				m, ok := ba.gt.Functions[mid]
				if ok && m.Name == selName {
					ba.add(golang.ConnCalls, string(mid))
					ba.add(golang.ConnUsesStruct, string(structID))
					foundDirect = true
					break
				}
			}
			// Always check interfaces for ConnUsesIface tracking
			for _, iface := range ba.gt.Interfaces {
				if !isImplementedBy(iface, structID) {
					continue
				}
				for _, reqMethod := range iface.Methods {
					if reqMethod.Name == selName {
						ba.add(golang.ConnUsesIface, string(iface.ID))
						if !foundDirect {
							for _, implStructID := range iface.ImplementedBy() {
								implStr, ok := ba.gt.Structs[implStructID]
								if !ok {
									continue
								}
								for _, mid := range implStr.Methods() {
									m, ok := ba.gt.Functions[mid]
									if ok && m.Name == selName {
										ba.add(golang.ConnCalls, string(mid))
									}
								}
							}
						}
						return
					}
				}
			}
			if foundDirect {
				return
			}
		}
		return
	}

	if ifaceID, ok := ba.varIfaceMap[xName]; ok {
		if iface, ok := ba.gt.Interfaces[ifaceID]; ok {
			for _, reqMethod := range iface.Methods {
				if reqMethod.Name == selName {
					ba.add(golang.ConnUsesIface, string(ifaceID))
					for _, implStructID := range iface.ImplementedBy() {
						implStr, ok := ba.gt.Structs[implStructID]
						if !ok {
							continue
						}
						for _, mid := range implStr.Methods() {
							m, ok := ba.gt.Functions[mid]
							if ok && m.Name == selName {
								ba.add(golang.ConnCalls, string(mid))
							}
						}
					}
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

func isImplementedBy(iface golang.GolangInterface, structID golang.StructID) bool {
	for _, implID := range iface.ImplementedBy() {
		if implID == structID {
			return true
		}
	}
	return false
}

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
			if impPath, ok := ba.pr.ImportMap[x.Name]; ok && strings.HasPrefix(impPath, ba.pr.ModulePath) {
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

func (ba *bodyAnalyzer) resolveIdentRef(ident *ast.Ident) {
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

func (ba *bodyAnalyzer) resolveAssignStmt(stmt *ast.AssignStmt) {
	if stmt.Tok != token.DEFINE {
		return
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
		}
	}
}

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

	returnType := targetFunc.Output[0].Typing
	if returnType == "" {
		return
	}

	if sid := paramTypeNameToStruct(returnType, ba.pr.PkgPath, ba.pr.ImportMap, ba.pr.ModulePath); sid != nil {
		if _, ok := ba.gt.Structs[*sid]; ok {
			ba.varTypeMap[name] = *sid
			return
		}
	}

	if iid := paramTypeNameToInterface(returnType, ba.pr.PkgPath, ba.pr.ImportMap, ba.pr.ModulePath, ba.gt); iid != nil {
		ba.varIfaceMap[name] = *iid
	}
}

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

func containsString(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
