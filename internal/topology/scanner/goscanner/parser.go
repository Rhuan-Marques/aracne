package goscanner

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"

	"llm-topology/internal/topology/domain"
	"llm-topology/internal/topology/golang"
)

// Holds the result of parsing a single Go file. Contains the file ID, package path, module/root paths, imported dependencies, extracted structs, interfaces, functions, external variables, and an import path mapping.
type ParseResult struct {
	FileID          string
	FileDescription string
	PkgPath         golang.PackagePath
	ModulePath      string
	RootPath        string
	InternalImports []golang.PackagePath
	ExternalImports []golang.Dependancy
	Structs         []golang.GolangStruct
	Interfaces      []golang.GolangInterface
	Functions       []FunctionParse
	ExternalVars    []golang.GolangExternalVar
	ImportMap       map[string]string
}

// Holds a parsed Go function together with its optional AST body and receiver name, used during intermediate parsing before topology insertion.
type FunctionParse struct {
	Function     golang.GolangFunction
	Body         *ast.BlockStmt
	ReceiverName string
}

// Parses a single Go source file using go/parser, extracts the AST, and builds a ParseResult containing the file's package, imports (internal vs. external), structs, interfaces, functions, and external variables. Returns the parse result or an error.
func ParseFile(filePath string, pkgPath golang.PackagePath, modulePath, rootPath string) (*ParseResult, error) {
	fset := token.NewFileSet()
	astFile, err := parser.ParseFile(fset, filePath, nil, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse error in %s: %w", filePath, err)
	}

	pr := &ParseResult{
		FileID:          filePath,
		FileDescription: commentText(astFile.Doc),
		PkgPath:         pkgPath,
		ModulePath:      modulePath,
		RootPath:        rootPath,
		ImportMap:       make(map[string]string),
	}

	for _, imp := range astFile.Imports {
		impPath := strings.Trim(imp.Path.Value, "\"")
		var alias string
		if imp.Name != nil {
			alias = imp.Name.Name
		} else {
			parts := strings.Split(impPath, "/")
			alias = parts[len(parts)-1]
		}
		pr.ImportMap[alias] = impPath

		if strings.HasPrefix(impPath, modulePath) {
			pr.InternalImports = append(pr.InternalImports, golang.PackagePath(impPath))
		} else {
			pr.ExternalImports = append(pr.ExternalImports, golang.Dependancy{
				PackagePath: golang.DependancyPath(impPath),
			})
		}
	}

	for _, decl := range astFile.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			if err := pr.processGenDecl(d, fset); err != nil {
				return nil, err
			}
		case *ast.FuncDecl:
			pr.processFuncDecl(d, fset)
		}
	}

	return pr, nil
}

// Processes a generic declaration (GenDecl) node from the AST, extracting external variable/constant declarations for VAR/CONST tokens and delegating type specifications to processTypeSpec.
func (pr *ParseResult) processGenDecl(genDecl *ast.GenDecl, fset *token.FileSet) error {
	for _, spec := range genDecl.Specs {
		switch s := spec.(type) {
		case *ast.ValueSpec:
			if genDecl.Tok == token.VAR || genDecl.Tok == token.CONST {
				for _, name := range s.Names {
					typing := ""
					if s.Type != nil {
						typing = exprToString(s.Type)
					}

					id := golang.ExternalVarID(string(pr.PkgPath) + "." + name.Name)
					loc := locationFromNode(fset, genDecl, pr.FileID)

					pr.ExternalVars = append(pr.ExternalVars, golang.GolangExternalVar{
						ID:          id,
						Name:        name.Name,
						Description: pickComment(s.Doc, genDecl.Doc),
						Typing:      typing,
						Location:    loc,
					})
				}
			}
		case *ast.TypeSpec:
			if err := pr.processTypeSpec(s, fset, genDecl); err != nil {
				return err
			}
		}
	}
	return nil
}

// Dispatches AST type declarations to the appropriate processor based on whether the type is a struct or interface.
func (pr *ParseResult) processTypeSpec(typeSpec *ast.TypeSpec, fset *token.FileSet, genDecl *ast.GenDecl) error {
	switch t := typeSpec.Type.(type) {
	case *ast.StructType:
		return pr.processStruct(typeSpec, t, fset, genDecl)
	case *ast.InterfaceType:
		return pr.processInterface(typeSpec, t, fset, genDecl)
	}
	return nil
}

// Processes a parsed AST struct type declaration, extracting field definitions, package/dependency references, doc comments, and location. Appends the resulting GolangStruct to the ParseResult's Structs slice. Called during file parsing.
func (pr *ParseResult) processStruct(typeSpec *ast.TypeSpec, st *ast.StructType, fset *token.FileSet, genDecl *ast.GenDecl) error {
	id := golang.StructID(string(pr.PkgPath) + "." + typeSpec.Name.Name)
	loc := locationFromNode(fset, typeSpec, pr.FileID)

	var params []golang.VariableDefinition
	var pkgRefs []golang.PackagePath
	var depRefs []golang.DependancyPath
	refSeen := make(map[string]bool)

	for _, field := range st.Fields.List {
		ft := exprToString(field.Type)
		if len(field.Names) == 0 {
			params = append(params, golang.VariableDefinition{
				Name:   exprToString(field.Type),
				Typing: ft,
			})
		} else {
			for _, name := range field.Names {
				params = append(params, golang.VariableDefinition{
					Name:   name.Name,
					Typing: ft,
				})
			}
		}
		extractTypeRefs(field.Type, pr.ImportMap, pr.ModulePath, refSeen, &pkgRefs, &depRefs)
	}

	conns := make(map[golang.ConnectionKind][]string)
	if len(pkgRefs) > 0 {
		conns[golang.ConnUsesPkg] = castStrings(pkgRefs)
	}
	if len(depRefs) > 0 {
		conns[golang.ConnUsesDep] = castStrings(depRefs)
	}

	pr.Structs = append(pr.Structs, golang.GolangStruct{
		ID:          id,
		Name:        typeSpec.Name.Name,
		Description: pickComment(typeSpec.Doc, genDecl.Doc),
		Params:      params,
		Loc:         loc,
		Connections: conns,
	})
	return nil
}

// Processes an AST interface type declaration, extracting method signatures, package/dependency references, and storing the parsed interface in the ParseResult's Interfaces slice.
func (pr *ParseResult) processInterface(typeSpec *ast.TypeSpec, it *ast.InterfaceType, fset *token.FileSet, genDecl *ast.GenDecl) error {
	id := golang.InterfaceID(string(pr.PkgPath) + "." + typeSpec.Name.Name)
	loc := locationFromNode(fset, typeSpec, pr.FileID)

	var methods []golang.FunctionDefinition
	var pkgRefs []golang.PackagePath
	var depRefs []golang.DependancyPath
	refSeen := make(map[string]bool)

	for _, field := range it.Methods.List {
		switch t := field.Type.(type) {
		case *ast.FuncType:
			def := golang.FunctionDefinition{
				Name:   field.Names[0].Name,
				Input:  parseFieldList(t.Params),
				Output: parseFieldList(t.Results),
			}
			methods = append(methods, def)
			extractParamsTypeRefs(t.Params, pr.ImportMap, pr.ModulePath, refSeen, &pkgRefs, &depRefs)
			extractParamsTypeRefs(t.Results, pr.ImportMap, pr.ModulePath, refSeen, &pkgRefs, &depRefs)
		case *ast.Ident:
			methods = append(methods, golang.FunctionDefinition{
				Name: t.Name,
			})
		case *ast.SelectorExpr:
			name := exprToString(field.Type)
			methods = append(methods, golang.FunctionDefinition{
				Name: name,
			})
			extractTypeRefs(field.Type, pr.ImportMap, pr.ModulePath, refSeen, &pkgRefs, &depRefs)
		}
	}

	conns := make(map[golang.ConnectionKind][]string)
	if len(pkgRefs) > 0 {
		conns[golang.ConnUsesPkg] = castStrings(pkgRefs)
	}
	if len(depRefs) > 0 {
		conns[golang.ConnUsesDep] = castStrings(depRefs)
	}

	pr.Interfaces = append(pr.Interfaces, golang.GolangInterface{
		ID:          id,
		Name:        typeSpec.Name.Name,
		Description: pickComment(typeSpec.Doc, genDecl.Doc),
		Methods:     methods,
		Loc:         loc,
		Connections: conns,
	})
	return nil
}

// Parses an AST function/method declaration, extracts its name, parameters, results, receiver (if method), and source location, then appends a FunctionParse entry to the ParseResult.
func (pr *ParseResult) processFuncDecl(funcDecl *ast.FuncDecl, fset *token.FileSet) {
	loc := locationFromNode(fset, funcDecl, pr.FileID)
	params := parseFieldList(funcDecl.Type.Params)
	results := parseFieldList(funcDecl.Type.Results)

	f := golang.GolangFunction{
		Name:        funcDecl.Name.Name,
		Description: commentText(funcDecl.Doc),
		Input:       params,
		Output:      results,
		Loc:         loc,
		Connections: make(map[golang.ConnectionKind][]string),
	}

	var receiverName string
	if funcDecl.Recv != nil {
		recvType := exprToString(funcDecl.Recv.List[0].Type)
		structName := strings.TrimPrefix(recvType, "*")
		structID := golang.StructID(string(pr.PkgPath) + "." + structName)
		f.MethodFrom = &structID
		f.ID = golang.FunctionID(string(pr.PkgPath) + ".(" + structName + ")." + funcDecl.Name.Name)
		if len(funcDecl.Recv.List[0].Names) > 0 {
			receiverName = funcDecl.Recv.List[0].Names[0].Name
		}
	} else {
		f.ID = golang.FunctionID(string(pr.PkgPath) + "." + funcDecl.Name.Name)
	}

	fi := FunctionParse{Function: f, ReceiverName: receiverName}
	if funcDecl.Body != nil {
		fi.Body = funcDecl.Body
	}

	pr.Functions = append(pr.Functions, fi)
}

// Converts an ast.Expr to its string representation, handling identifiers, selectors, pointers, arrays, maps, channels, function types, and other common Go expression forms.
func exprToString(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.StarExpr:
		return "*" + exprToString(e.X)
	case *ast.SelectorExpr:
		return exprToString(e.X) + "." + e.Sel.Name
	case *ast.ArrayType:
		if e.Len == nil {
			return "[]" + exprToString(e.Elt)
		}
		return "[" + exprToString(e.Len) + "]" + exprToString(e.Elt)
	case *ast.MapType:
		return "map[" + exprToString(e.Key) + "]" + exprToString(e.Value)
	case *ast.InterfaceType:
		return "interface{}"
	case *ast.Ellipsis:
		return "..." + exprToString(e.Elt)
	case *ast.FuncType:
		return "func(...)"
	case *ast.ChanType:
		return "chan " + exprToString(e.Value)
	case *ast.BasicLit:
		return e.Value
	case *ast.ParenExpr:
		return "(" + exprToString(e.X) + ")"
	case *ast.IndexExpr:
		return exprToString(e.X) + "[" + exprToString(e.Index) + "]"
	case *ast.StructType:
		return "struct{...}"
	default:
		return fmt.Sprintf("%T", expr)
	}
}

// Parses an AST FieldList into a slice of VariableDefinitions. Handles unnamed fields (embedded types) and named fields. Takes *ast.FieldList, returns []golang.VariableDefinition.
func parseFieldList(fl *ast.FieldList) []golang.VariableDefinition {
	if fl == nil {
		return nil
	}
	var result []golang.VariableDefinition
	for _, field := range fl.List {
		ft := exprToString(field.Type)
		if len(field.Names) == 0 {
			result = append(result, golang.VariableDefinition{Typing: ft})
		} else {
			for _, name := range field.Names {
				result = append(result, golang.VariableDefinition{
					Name:   name.Name,
					Typing: ft,
				})
			}
		}
	}
	return result
}

// Extracts clean text from an AST comment group by stripping comment markers (//, /*, */) and joining non-empty lines with newlines. Returns empty string for nil groups.
func commentText(group *ast.CommentGroup) string {
	if group == nil {
		return ""
	}
	var comments []string
	for _, c := range group.List {
		text := strings.TrimSpace(strings.TrimPrefix(c.Text, "//"))
		text = strings.TrimSpace(strings.TrimPrefix(text, "/*"))
		text = strings.TrimSpace(strings.TrimSuffix(text, "*/"))
		if text != "" {
			comments = append(comments, text)
		}
	}
	return strings.Join(comments, "\n")
}

// Returns the text of the preferred comment group if non-empty, otherwise falls back to the fallback comment group. Used to select the best doc comment for a resource.
func pickComment(preferred, fallback *ast.CommentGroup) string {
	desc := commentText(preferred)
	if desc != "" {
		return desc
	}
	return commentText(fallback)
}

// Extracts package and dependency references from an AST expression by dispatching to extractTypeRefsRec for recursive type traversal. Accumulates results into the provided packages and deps slices.
func extractTypeRefs(expr ast.Expr, importMap map[string]string, modulePath string, seen map[string]bool, packages *[]golang.PackagePath, deps *[]golang.DependancyPath) {
	extractTypeRefsRec(expr, importMap, modulePath, seen, packages, deps)
}

// Recursively walks an AST expression node to extract type references, resolving import aliases to populate packages and external dependencies. Handles SelectorExpr, StarExpr, ArrayType, MapType, ChanType, IndexExpr, and ParenExpr.
func extractTypeRefsRec(expr ast.Expr, importMap map[string]string, modulePath string, seen map[string]bool, packages *[]golang.PackagePath, deps *[]golang.DependancyPath) {
	switch e := expr.(type) {
	case *ast.SelectorExpr:
		alias := rootIdent(e.X)
		if impPath, ok := importMap[alias]; ok && !seen[impPath] {
			seen[impPath] = true
			if strings.HasPrefix(impPath, modulePath) {
				*packages = append(*packages, golang.PackagePath(impPath))
			} else {
				*deps = append(*deps, golang.DependancyPath(impPath))
			}
		}
	case *ast.StarExpr:
		extractTypeRefsRec(e.X, importMap, modulePath, seen, packages, deps)
	case *ast.ArrayType:
		extractTypeRefsRec(e.Elt, importMap, modulePath, seen, packages, deps)
	case *ast.MapType:
		extractTypeRefsRec(e.Key, importMap, modulePath, seen, packages, deps)
		extractTypeRefsRec(e.Value, importMap, modulePath, seen, packages, deps)
	case *ast.ChanType:
		extractTypeRefsRec(e.Value, importMap, modulePath, seen, packages, deps)
	case *ast.IndexExpr:
		extractTypeRefsRec(e.X, importMap, modulePath, seen, packages, deps)
		extractTypeRefsRec(e.Index, importMap, modulePath, seen, packages, deps)
	case *ast.ParenExpr:
		extractTypeRefsRec(e.X, importMap, modulePath, seen, packages, deps)
	}
}

// Iterates over an AST field list (function parameters or results) and extracts type references for package and dependency tracking.
func extractParamsTypeRefs(fl *ast.FieldList, importMap map[string]string, modulePath string, seen map[string]bool, packages *[]golang.PackagePath, deps *[]golang.DependancyPath) {
	if fl == nil {
		return
	}
	for _, field := range fl.List {
		extractTypeRefs(field.Type, importMap, modulePath, seen, packages, deps)
	}
}

// Extracts the root identifier from an AST expression, traversing through selector expressions (e.g., pkg.Func -> pkg).
func rootIdent(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return rootIdent(e.X)
	default:
		return ""
	}
}

// Converts an AST node position to a domain.Location with file path and line range (start/end) using the file set.
func locationFromNode(fset *token.FileSet, node ast.Node, filePath string) domain.Location {
	if node == nil {
		return domain.Location{Path: filePath}
	}
	start := fset.Position(node.Pos())
	end := fset.Position(node.End())
	return domain.Location{
		StartsAt: start.Line,
		EndsAt:   end.Line,
		Path:     filePath,
	}
}

// Generic function that converts a slice of typed string IDs to a plain string slice.
func castStrings[T ~string](ids []T) []string {
	result := make([]string, len(ids))
	for i, id := range ids {
		result[i] = string(id)
	}
	return result
}
