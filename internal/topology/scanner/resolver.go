package scanner

import (
	"go/ast"
	"strings"

	"llm-topology/internal/topology/domain"
)

// relationshipInfo accumulates all cross-references discovered during the analysis of a single function body.
type relationshipInfo struct {
	functionsUsed    []domain.FunctionID
	structsUsed      []domain.StructID
	interfacesUsed   []domain.InterfaceID
	externalVarsUsed []domain.ExternalVarID
	packagesUsed     []domain.PackagePath
	dependenciesUsed []domain.DependancyPath
}

// bodyAnalyzer is the execution context for a single function body analysis pass.
type bodyAnalyzer struct {
	pr   *ParseResult
	topo *domain.Topology
}

// analyzeFunctionBody is the entry point for extracting usage relationships
// from a single function body. It uses ast.Inspect to walk every node in the
// body's AST, dispatching to specialized resolvers for CallExpr (function
// calls), CompositeLit (struct literals), and Ident (variable/type references).
// The returned relationshipInfo is later merged into the function's topology
// entry to form the complete call graph and dependency network.
func analyzeFunctionBody(body *ast.BlockStmt, pr *ParseResult, topo *domain.Topology) relationshipInfo {
	ba := &bodyAnalyzer{pr: pr, topo: topo}
	var ri relationshipInfo

	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CallExpr:
			ri.merge(ba.resolveCallExpr(node))
		case *ast.CompositeLit:
			ri.merge(ba.resolveCompositeLit(node))
		case *ast.Ident:
			ri.merge(ba.resolveIdentRef(node))
		}
		return true
	})

	return ri
}

// resolveCallExpr analyzes an ast.CallExpr to determine which function or
// method is being invoked. For bare identifiers (foo()), it searches the
// current package's declared functions. For selector expressions (pkg.Func()
// or var.Method()), it delegates to resolveQualifiedCall which checks imports,
// known struct methods, and performs a brute-force search across all struct
// methods as a best-effort resolution strategy.
func (ba *bodyAnalyzer) resolveCallExpr(call *ast.CallExpr) relationshipInfo {
	var ri relationshipInfo

	switch fun := call.Fun.(type) {
	case *ast.Ident:
		for _, f := range ba.pr.Functions {
			if f.Function.Name == fun.Name && f.Function.MethodFrom == nil {
				ri.addFunctionUsed(f.Function.ID)
				return ri
			}
		}

	case *ast.SelectorExpr:
		switch x := fun.X.(type) {
		case *ast.Ident:
			ri.merge(ba.resolveQualifiedCall(x.Name, fun.Sel.Name))
		}
	}

	return ri
}

// resolveQualifiedCall handles dotted references like pkg.Func(), var.Method(),
// or Type.Method(). It first checks if the left-hand side matches an import
// alias to resolve cross-package function calls. If the import belongs to the
// same module, it records the function usage and internal package reference;
// otherwise it records an external dependency usage. When no import match is
// found, it searches struct methods in the current package and finally does a
// brute-force search across all known struct methods by name alone, accepting
// potential false positives in exchange for broader coverage.
func (ba *bodyAnalyzer) resolveQualifiedCall(xName, selName string) relationshipInfo {
	var ri relationshipInfo

	if impPath, ok := ba.pr.ImportMap[xName]; ok {
		if strings.HasPrefix(impPath, ba.pr.ModulePath) {
			internalPkg := domain.PackagePath(impPath)
			targetID := domain.FunctionID(string(internalPkg) + "." + selName)
			if _, exists := ba.topo.Functions[targetID]; exists {
				ri.addFunctionUsed(targetID)
				ri.addPackageUsed(internalPkg)
				return ri
			}
		} else {
			ri.addDependencyUsed(domain.DependancyPath(impPath))
			return ri
		}
	}

	structID := domain.StructID(string(ba.pr.PkgPath) + "." + xName)
	if str, ok := ba.topo.Struct[structID]; ok {
		for _, mid := range str.Methods {
			m := ba.topo.Functions[mid]
			if m.Name == selName {
				ri.addFunctionUsed(mid)
				ri.addStructUsed(structID)
				return ri
			}
		}
	}

	for _, str := range ba.topo.Struct {
		for _, mid := range str.Methods {
			m := ba.topo.Functions[mid]
			if m.Name == selName {
				ri.addFunctionUsed(mid)
				ri.addStructUsed(str.ID)
				return ri
			}
		}
	}

	return ri
}

// resolveCompositeLit identifies struct type usage from composite literal
// expressions like Foo{...} or pkg.Foo{...}. It checks whether the literal's
// type name corresponds to a known struct in the current package or, for
// qualified names, in an imported internal package. This captures structural
// dependencies where functions create or initialize struct values.
func (ba *bodyAnalyzer) resolveCompositeLit(lit *ast.CompositeLit) relationshipInfo {
	var ri relationshipInfo

	switch t := lit.Type.(type) {
	case *ast.Ident:
		structID := domain.StructID(string(ba.pr.PkgPath) + "." + t.Name)
		if _, exists := ba.topo.Struct[structID]; exists {
			ri.addStructUsed(structID)
		}
	case *ast.SelectorExpr:
		if x, ok := t.X.(*ast.Ident); ok {
			if impPath, ok := ba.pr.ImportMap[x.Name]; ok && strings.HasPrefix(impPath, ba.pr.ModulePath) {
				internalPkg := domain.PackagePath(impPath)
				structID := domain.StructID(string(internalPkg) + "." + t.Sel.Name)
				if _, exists := ba.topo.Struct[structID]; exists {
					ri.addStructUsed(structID)
					ri.addPackageUsed(internalPkg)
				}
			}
		}
	}

	return ri
}

// resolveIdentRef handles bare identifier references that appear as standalone
// expressions within a function body. It checks whether the identifier name
// matches a known package-level variable (ExternalVar) or struct type in the
// current package. This captures direct variable reads and implicit type
// references that are not part of a composite literal or call expression.
func (ba *bodyAnalyzer) resolveIdentRef(ident *ast.Ident) relationshipInfo {
	var ri relationshipInfo

	varID := domain.ExternalVarID(string(ba.pr.PkgPath) + "." + ident.Name)
	if _, exists := ba.topo.ExternalVars[varID]; exists {
		ri.addExternalVarUsed(varID)
	}

	structID := domain.StructID(string(ba.pr.PkgPath) + "." + ident.Name)
	if _, exists := ba.topo.Struct[structID]; exists {
		ri.addStructUsed(structID)
	}

	return ri
}

// addFunctionUsed appends a function ID to the relationship set while guarding
// against duplicates. Each of these add* methods follows the same guard pattern
// to satisfy the topology contract that no relationship list contains the same
// ID more than once, regardless of how many times the function references it.
func (ri *relationshipInfo) addFunctionUsed(id domain.FunctionID) {
	if !contains(ri.functionsUsed, id) {
		ri.functionsUsed = append(ri.functionsUsed, id)
	}
}

// addStructUsed records a struct type reference in the relationship set. The
// duplicate guard ensures each struct appears at most once per function's
// StructsUsed list even if multiple expressions reference the same type.
func (ri *relationshipInfo) addStructUsed(id domain.StructID) {
	if !contains(ri.structsUsed, id) {
		ri.structsUsed = append(ri.structsUsed, id)
	}
}

// addInterfaceUsed records an interface method invocation in the relationship
// set. This captures which interfaces a function interacts with, enabling
// higher-level dependency analysis across abstraction boundaries.
func (ri *relationshipInfo) addInterfaceUsed(id domain.InterfaceID) {
	if !contains(ri.interfacesUsed, id) {
		ri.interfacesUsed = append(ri.interfacesUsed, id)
	}
}

// addExternalVarUsed records a package-level variable or constant reference.
// Each variable appears at most once even when accessed multiple times within
// the same function body.
func (ri *relationshipInfo) addExternalVarUsed(id domain.ExternalVarID) {
	if !contains(ri.externalVarsUsed, id) {
		ri.externalVarsUsed = append(ri.externalVarsUsed, id)
	}
}

// addPackageUsed records a reference to an internal package (same module).
// This is populated when a function calls another function from a different
// internal package or uses a type from one, enabling cross-package dependency
// tracking within the analyzed repository.
func (ri *relationshipInfo) addPackageUsed(pkg domain.PackagePath) {
	if !contains(ri.packagesUsed, pkg) {
		ri.packagesUsed = append(ri.packagesUsed, pkg)
	}
}

// addDependencyUsed records a reference to an external third-party package.
// When a function calls imported functions or uses types from external
// packages, their import paths are recorded here to build the external
// dependency footprint of each function.
func (ri *relationshipInfo) addDependencyUsed(dep domain.DependancyPath) {
	if !contains(ri.dependenciesUsed, dep) {
		ri.dependenciesUsed = append(ri.dependenciesUsed, dep)
	}
}

// merge combines another relationshipInfo into the receiver without creating
// duplicates. It is used to aggregate results from multiple resolver methods
// (resolveCallExpr, resolveCompositeLit, resolveIdentRef) during the same
// function body analysis pass, ensuring all discovered references are captured
// in a single consolidated set before insertion into the topology.
func (ri *relationshipInfo) merge(other relationshipInfo) {
	for _, id := range other.functionsUsed {
		ri.addFunctionUsed(id)
	}
	for _, id := range other.structsUsed {
		ri.addStructUsed(id)
	}
	for _, id := range other.interfacesUsed {
		ri.addInterfaceUsed(id)
	}
	for _, id := range other.externalVarsUsed {
		ri.addExternalVarUsed(id)
	}
	for _, pkg := range other.packagesUsed {
		ri.addPackageUsed(pkg)
	}
	for _, dep := range other.dependenciesUsed {
		ri.addDependencyUsed(dep)
	}
}

// contains is a generic linear search helper that checks whether a comparable
// element exists in a slice. It is used by all add* methods on relationshipInfo
// to enforce the uniqueness contract, and by any other code path that needs to
// check membership before appending to a relationship slice.
func contains[T comparable](slice []T, item T) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
