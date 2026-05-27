package goscanner

import (
	"go/ast"
	"strings"

	"llm-topology/internal/topology/golang"
)

// Walks Go AST nodes within function bodies to resolve call graphs, struct usage, interface references, and external variable references, building connection maps for the topology.
type bodyAnalyzer struct {
	pr         *ParseResult
	gt         *golang.GolangTopology
	conn       map[golang.ConnectionKind][]string
	varTypeMap map[string]golang.StructID
}

// Creates and initializes a bodyAnalyzer for function body analysis, setting up connection maps and resolving parameter and receiver variable types to their corresponding struct IDs for call graph resolution.
func newBodyAnalyzer(pr *ParseResult, gt *golang.GolangTopology, funcInput []golang.VariableDefinition, receiverName string, receiverStruct *golang.StructID) *bodyAnalyzer {
	ba := &bodyAnalyzer{
		pr:         pr,
		gt:         gt,
		conn:       make(map[golang.ConnectionKind][]string),
		varTypeMap: make(map[string]golang.StructID),
	}

	for _, param := range funcInput {
		if sid := paramTypeNameToStruct(param.Typing, pr.PkgPath, pr.ImportMap, pr.ModulePath); sid != nil {
			if _, ok := gt.Structs[*sid]; ok {
				ba.varTypeMap[param.Name] = *sid
			}
		}
	}

	if receiverName != "" && receiverStruct != nil {
		if _, ok := gt.Structs[*receiverStruct]; ok {
			ba.varTypeMap[receiverName] = *receiverStruct
		}
	}

	return ba
}

// Converts a type expression string to a StructID by resolving package aliases via the import map, trimming pointer prefixes, and only returning IDs for types within the module.
func paramTypeNameToStruct(typing string, pkgPath golang.PackagePath, importMap map[string]string, modulePath string) *golang.StructID {
	t := strings.TrimPrefix(typing, "*")

	if idx := strings.Index(t, "."); idx > 0 {
		alias := t[:idx]
		typeName := t[idx+1:]
		if impPath, ok := importMap[alias]; ok && strings.HasPrefix(impPath, modulePath) {
			sid := golang.StructID(impPath + "." + typeName)
			return &sid
		}
		return nil
	}

	sid := golang.StructID(string(pkgPath) + "." + t)
	return &sid
}

// Adds a connection of the given kind and ID to the bodyAnalyzer's connection map, preventing duplicates via containsString check. Takes kind ConnectionKind and id string, returns nothing.
func (ba *bodyAnalyzer) add(kind golang.ConnectionKind, id string) {
	ids := ba.conn[kind]
	if !containsString(ids, id) {
		ba.conn[kind] = append(ids, id)
	}
}

// Walks the AST of a function body to resolve call expressions, composite literals, and identifier references, returning collected connection data (calls, struct usage, variable usage).
func analyzeFunctionBody(body *ast.BlockStmt, pr *ParseResult, gt *golang.GolangTopology, funcInput []golang.VariableDefinition, receiverName string, receiverStruct *golang.StructID) map[golang.ConnectionKind][]string {
	ba := newBodyAnalyzer(pr, gt, funcInput, receiverName, receiverStruct)

	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CallExpr:
			ba.resolveCallExpr(node)
		case *ast.CompositeLit:
			ba.resolveCompositeLit(node)
		case *ast.Ident:
			ba.resolveIdentRef(node)
		}
		return true
	})

	return ba.conn
}

// Resolves a function call expression by dispatching to the appropriate handler: direct identifier calls are matched to package-level functions, and selector expressions (x.Func) are delegated to resolveQualifiedCall.
func (ba *bodyAnalyzer) resolveCallExpr(call *ast.CallExpr) {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		for _, f := range ba.pr.Functions {
			if f.Function.Name == fun.Name && f.Function.MethodFrom == nil {
				ba.add(golang.ConnCalls, string(f.Function.ID))
				return
			}
		}

	case *ast.SelectorExpr:
		switch x := fun.X.(type) {
		case *ast.Ident:
			ba.resolveQualifiedCall(x.Name, fun.Sel.Name)
		}
	}
}

// Resolves a qualified call expression (e.g. pkg.Func or struct.Method). Checks import maps for internal package calls, falls back to checking struct methods via varTypeMap or direct struct type lookup, and records call and usage connections.
func (ba *bodyAnalyzer) resolveQualifiedCall(xName, selName string) {
	if impPath, ok := ba.pr.ImportMap[xName]; ok {
		if strings.HasPrefix(impPath, ba.pr.ModulePath) {
			internalPkg := golang.PackagePath(impPath)
			targetID := golang.FunctionID(string(internalPkg) + "." + selName)
			if _, exists := ba.gt.Functions[targetID]; exists {
				ba.add(golang.ConnCalls, string(targetID))
				ba.add(golang.ConnUsesPkg, string(internalPkg))
				return
			}
		} else {
			ba.add(golang.ConnUsesDep, string(impPath))
			return
		}
	}

	if structID, ok := ba.varTypeMap[xName]; ok {
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

// Resolves composite literal expressions (e.g., MyStruct{...}) by identifying the struct type and recording struct usage and package references in the topology.
func (ba *bodyAnalyzer) resolveCompositeLit(lit *ast.CompositeLit) {
	switch t := lit.Type.(type) {
	case *ast.Ident:
		structID := golang.StructID(string(ba.pr.PkgPath) + "." + t.Name)
		if _, exists := ba.gt.Structs[structID]; exists {
			ba.add(golang.ConnUsesStruct, string(structID))
		}
	case *ast.SelectorExpr:
		if x, ok := t.X.(*ast.Ident); ok {
			if impPath, ok := ba.pr.ImportMap[x.Name]; ok && strings.HasPrefix(impPath, ba.pr.ModulePath) {
				internalPkg := golang.PackagePath(impPath)
				structID := golang.StructID(string(internalPkg) + "." + t.Sel.Name)
				if _, exists := ba.gt.Structs[structID]; exists {
					ba.add(golang.ConnUsesStruct, string(structID))
					ba.add(golang.ConnUsesPkg, string(internalPkg))
				}
			}
		}
	}
}

// Resolves an ast.Ident reference by checking if the identifier matches an external variable or struct in the parsed topology, and records the appropriate connection (ConnUsesExtVar or ConnUsesStruct). Takes *ast.Ident, returns nothing.
func (ba *bodyAnalyzer) resolveIdentRef(ident *ast.Ident) {
	varID := golang.ExternalVarID(string(ba.pr.PkgPath) + "." + ident.Name)
	if _, exists := ba.gt.ExternalVars[varID]; exists {
		ba.add(golang.ConnUsesExtVar, string(varID))
	}

	structID := golang.StructID(string(ba.pr.PkgPath) + "." + ident.Name)
	if _, exists := ba.gt.Structs[structID]; exists {
		ba.add(golang.ConnUsesStruct, string(structID))
	}
}

// Checks whether a given string item exists in a string slice by linear search. Returns true if found, false otherwise.
func containsString(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
