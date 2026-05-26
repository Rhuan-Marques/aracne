package goscanner

import (
	"go/ast"
	"strings"

	"llm-topology/internal/topology/golang"
)

type bodyAnalyzer struct {
	pr   *ParseResult
	gt   *golang.GolangTopology
	conn map[golang.ConnectionKind][]string
}

func newBodyAnalyzer(pr *ParseResult, gt *golang.GolangTopology) *bodyAnalyzer {
	return &bodyAnalyzer{
		pr:   pr,
		gt:   gt,
		conn: make(map[golang.ConnectionKind][]string),
	}
}

func (ba *bodyAnalyzer) add(kind golang.ConnectionKind, id string) {
	ids := ba.conn[kind]
	if !containsString(ids, id) {
		ba.conn[kind] = append(ids, id)
	}
}

func analyzeFunctionBody(body *ast.BlockStmt, pr *ParseResult, gt *golang.GolangTopology) map[golang.ConnectionKind][]string {
	ba := newBodyAnalyzer(pr, gt)

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

	for _, str := range ba.gt.Structs {
		for _, mid := range str.Methods() {
			m := ba.gt.Functions[mid]
			if m.Name == selName {
				ba.add(golang.ConnCalls, string(mid))
				ba.add(golang.ConnUsesStruct, string(str.ID))
				return
			}
		}
	}
}

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

func containsString(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
