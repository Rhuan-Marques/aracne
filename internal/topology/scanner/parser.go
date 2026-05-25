package scanner

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"

	"llm-topology/internal/topology/domain"
)

// ParseResult holds all extracted information from a single .go file. It
// carries the parsed declarations (structs, interfaces, functions, variables),
// the import map for resolving cross-package references, and metadata about
// the file's package membership. This intermediate representation is consumed
// by the analyzer orchestration layer to populate the final Topology.
type ParseResult struct {
	FilePath        string
	FileDescription string
	PkgPath         domain.PackagePath
	ModulePath      string
	RootPath        string
	InternalImports []domain.PackagePath
	ExternalImports []domain.Dependancy
	Structs         []StructParse
	Interfaces      []InterfaceParse
	Functions       []FunctionParse
	ExternalVars    []domain.ExternalVar
	ImportMap       map[string]string
}

// StructParse wraps a parsed Struct with its intermediate metadata.
type StructParse struct {
	Struct domain.Struct
}

// InterfaceParse wraps a parsed Interface with its intermediate metadata.
type InterfaceParse struct {
	Interface domain.Interface
}

// FunctionParse wraps a parsed Function along with its raw AST body.
type FunctionParse struct {
	Function domain.Function
	Body     *ast.BlockStmt
}

// ParseFile parses a single .go file using the standard library's go/parser and
// go/ast packages. It extracts the file's imports (categorized as internal or
// external), processes all top-level declarations (GenDecl for types, vars,
// consts and FuncDecl for functions/methods), and returns a ParseResult with
// all discovered topology elements ready for subsequent assembly phases.
func ParseFile(filePath string, pkgPath domain.PackagePath, modulePath, rootPath string) (*ParseResult, error) {
	fset := token.NewFileSet()
	astFile, err := parser.ParseFile(fset, filePath, nil, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse error in %s: %w", filePath, err)
	}

	pr := &ParseResult{
		FilePath:        filePath,
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
			pr.InternalImports = append(pr.InternalImports, domain.PackagePath(impPath))
		} else {
			pr.ExternalImports = append(pr.ExternalImports, domain.Dependancy{
				PackagePath: domain.DependancyPath(impPath),
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

// processGenDecl handles ast.GenDecl nodes which encompass import declarations,
// type definitions, and variable/constant specifications. For ValueSpec children
// (VAR and CONST tokens) it extracts package-level variables. For TypeSpec
// children it delegates to processTypeSpec for struct and interface extraction.
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

					id := domain.ExternalVarID(string(pr.PkgPath) + "." + name.Name)
					loc := locationFromNode(fset, genDecl, pr.FilePath)

					pr.ExternalVars = append(pr.ExternalVars, domain.ExternalVar{
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

// processTypeSpec dispatches type declarations to the appropriate handler
// based on the underlying type. It currently recognizes StructType and
// InterfaceType nodes, delegating to processStruct and processInterface
// respectively. Other type declarations (e.g. type aliases, map types) are
// silently skipped as they fall outside the current topology scope.
func (pr *ParseResult) processTypeSpec(typeSpec *ast.TypeSpec, fset *token.FileSet, genDecl *ast.GenDecl) error {
	switch t := typeSpec.Type.(type) {
	case *ast.StructType:
		return pr.processStruct(typeSpec, t, fset, genDecl)
	case *ast.InterfaceType:
		return pr.processInterface(typeSpec, t, fset, genDecl)
	}
	return nil
}

// processStruct extracts a Struct topology element from an ast.StructType node.
// It iterates over the struct's field list to build the Params slice, handles
// both named and embedded (anonymous) fields, and picks up the documentation
// comment from either the TypeSpec or the enclosing GenDecl for the Description.
func (pr *ParseResult) processStruct(typeSpec *ast.TypeSpec, st *ast.StructType, fset *token.FileSet, genDecl *ast.GenDecl) error {
	id := domain.StructID(string(pr.PkgPath) + "." + typeSpec.Name.Name)
	loc := locationFromNode(fset, typeSpec, pr.FilePath)

	var params []domain.VariableDefinition
	var pkgRefs []domain.PackagePath
	var depRefs []domain.DependancyPath
	refSeen := make(map[string]bool)

	for _, field := range st.Fields.List {
		ft := exprToString(field.Type)
		if len(field.Names) == 0 {
			params = append(params, domain.VariableDefinition{
				Name:   exprToString(field.Type),
				Typing: ft,
			})
		} else {
			for _, name := range field.Names {
				params = append(params, domain.VariableDefinition{
					Name:   name.Name,
					Typing: ft,
				})
			}
		}
		extractTypeRefs(field.Type, pr.ImportMap, pr.ModulePath, refSeen, &pkgRefs, &depRefs)
	}

	pr.Structs = append(pr.Structs, StructParse{
		Struct: domain.Struct{
			ID:               id,
			Name:             typeSpec.Name.Name,
			Description:      pickComment(typeSpec.Doc, genDecl.Doc),
			Params:           params,
			Loc:              loc,
			PackagesUsed:     pkgRefs,
			DependanciesUsed: depRefs,
		},
	})
	return nil
}

// processInterface extracts an Interface topology element from an ast.InterfaceType
// node. It converts each interface method entry into a FunctionDefinition with
// the method name, input parameters, and output return types. Embedded interfaces
// (both local and external via SelectorExpr) are recorded as minimal definitions
// with only the embedded interface name populated.
func (pr *ParseResult) processInterface(typeSpec *ast.TypeSpec, it *ast.InterfaceType, fset *token.FileSet, genDecl *ast.GenDecl) error {
	id := domain.InterfaceID(string(pr.PkgPath) + "." + typeSpec.Name.Name)
	loc := locationFromNode(fset, typeSpec, pr.FilePath)

	var methods []domain.FunctionDefinition
	var pkgRefs []domain.PackagePath
	var depRefs []domain.DependancyPath
	refSeen := make(map[string]bool)

	for _, field := range it.Methods.List {
		switch t := field.Type.(type) {
		case *ast.FuncType:
			def := domain.FunctionDefinition{
				Name:   field.Names[0].Name,
				Input:  parseFieldList(t.Params),
				Output: parseFieldList(t.Results),
			}
			methods = append(methods, def)
			extractParamsTypeRefs(t.Params, pr.ImportMap, pr.ModulePath, refSeen, &pkgRefs, &depRefs)
			extractParamsTypeRefs(t.Results, pr.ImportMap, pr.ModulePath, refSeen, &pkgRefs, &depRefs)
		case *ast.Ident:
			methods = append(methods, domain.FunctionDefinition{
				Name: t.Name,
			})
		case *ast.SelectorExpr:
			name := exprToString(field.Type)
			methods = append(methods, domain.FunctionDefinition{
				Name: name,
			})
			extractTypeRefs(field.Type, pr.ImportMap, pr.ModulePath, refSeen, &pkgRefs, &depRefs)
		}
	}

	pr.Interfaces = append(pr.Interfaces, InterfaceParse{
		Interface: domain.Interface{
			ID:               id,
			Name:             typeSpec.Name.Name,
			Description:      pickComment(typeSpec.Doc, genDecl.Doc),
			Methods:          methods,
			Loc:              loc,
			PackagesUsed:     pkgRefs,
			DependanciesUsed: depRefs,
		},
	})
	return nil
}

// processFuncDecl extracts a Function topology element from an ast.FuncDecl node.
// It distinguishes between regular functions and methods by checking for a
// receiver clause: methods have their MethodFrom field set to the parent struct's
// ID and use a parenthesized receiver notation in their FunctionID, while plain
// functions store only the package-qualified name.
func (pr *ParseResult) processFuncDecl(funcDecl *ast.FuncDecl, fset *token.FileSet) {
	loc := locationFromNode(fset, funcDecl, pr.FilePath)
	params := parseFieldList(funcDecl.Type.Params)
	results := parseFieldList(funcDecl.Type.Results)

	f := domain.Function{
		Name:        funcDecl.Name.Name,
		Description: commentText(funcDecl.Doc),
		Input:       params,
		Output:      results,
		Loc:         loc,
	}

	if funcDecl.Recv != nil {
		recvType := exprToString(funcDecl.Recv.List[0].Type)
		structName := strings.TrimPrefix(recvType, "*")
		structID := domain.StructID(string(pr.PkgPath) + "." + structName)
		f.MethodFrom = &structID
		f.ID = domain.FunctionID(string(pr.PkgPath) + ".(" + structName + ")." + funcDecl.Name.Name)
	} else {
		f.ID = domain.FunctionID(string(pr.PkgPath) + "." + funcDecl.Name.Name)
	}

	fi := FunctionParse{Function: f}
	if funcDecl.Body != nil {
		fi.Body = funcDecl.Body
	}

	pr.Functions = append(pr.Functions, fi)
}

// exprToString converts an arbitrary ast.Expr node into its string
// representation. It handles all common Go expression types including basic
// identifiers, selectors (pkg.Type), pointers, arrays, maps, slices, channels,
// function types, generics, and composite parentheses. This is used to produce
// the Typing field in VariableDefinitions and for signature comparison during
// interface matching. Unrecognized expression types are rendered as their Go
// type name as a fallback.
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

// parseFieldList converts an ast.FieldList (used in function signatures and
// struct definitions) into a slice of VariableDefinitions. Each field may have
// multiple names sharing the same type, producing one VariableDefinition per
// name. Fields without names (anonymous or return types without names) produce
// entries with only the Typing field populated.
func parseFieldList(fl *ast.FieldList) []domain.VariableDefinition {
	if fl == nil {
		return nil
	}
	var result []domain.VariableDefinition
	for _, field := range fl.List {
		ft := exprToString(field.Type)
		if len(field.Names) == 0 {
			result = append(result, domain.VariableDefinition{Typing: ft})
		} else {
			for _, name := range field.Names {
				result = append(result, domain.VariableDefinition{
					Name:   name.Name,
					Typing: ft,
				})
			}
		}
	}
	return result
}

// commentText extracts a clean text representation from an ast.CommentGroup.
// It strips the // and /* */ comment delimiters from each comment line and
// joins non-empty lines with newlines. Returns an empty string when the
// comment group is nil, enabling safe use as the Description source for any
// topology element that may or may not have documentation.
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

// pickComment returns the text of the preferred comment group if non-empty,
// otherwise falling back to the fallback group. This implements a priority
// system for documentation comments where the most specific comment (e.g. on
// the TypeSpec itself) takes precedence over the broader enclosing comment
// (e.g. on the GenDecl for grouped type declarations).
func pickComment(preferred, fallback *ast.CommentGroup) string {
	desc := commentText(preferred)
	if desc != "" {
		return desc
	}
	return commentText(fallback)
}

// extractTypeRefs walks an AST type expression to discover references to types
// from other packages. SelectorExpr nodes like http.Client or pkg.Type are
// resolved against the file's import map and categorized as internal (same
// module) or external (third-party dependency) references.
func extractTypeRefs(expr ast.Expr, importMap map[string]string, modulePath string, seen map[string]bool, packages *[]domain.PackagePath, deps *[]domain.DependancyPath) {
	extractTypeRefsRec(expr, importMap, modulePath, seen, packages, deps)
}

func extractTypeRefsRec(expr ast.Expr, importMap map[string]string, modulePath string, seen map[string]bool, packages *[]domain.PackagePath, deps *[]domain.DependancyPath) {
	switch e := expr.(type) {
	case *ast.SelectorExpr:
		alias := rootIdent(e.X)
		if impPath, ok := importMap[alias]; ok && !seen[impPath] {
			seen[impPath] = true
			if strings.HasPrefix(impPath, modulePath) {
				*packages = append(*packages, domain.PackagePath(impPath))
			} else {
				*deps = append(*deps, domain.DependancyPath(impPath))
			}
		}
		extractTypeRefsRec(e.Sel, importMap, modulePath, seen, packages, deps)
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

// extractParamsTypeRefs applies extractTypeRefs to every parameter/return type
// in a function signature field list, used for interface method type references.
func extractParamsTypeRefs(fl *ast.FieldList, importMap map[string]string, modulePath string, seen map[string]bool, packages *[]domain.PackagePath, deps *[]domain.DependancyPath) {
	if fl == nil {
		return
	}
	for _, field := range fl.List {
		extractTypeRefs(field.Type, importMap, modulePath, seen, packages, deps)
	}
}

// rootIdent returns the top-level identifier name from a chain of selector
// expressions. For http.Client it returns "http"; for pkg.sub.Type it returns
// "pkg". This allows resolving the import alias against the import map.
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

// locationFromNode computes a Location value from an AST node's positional
// information. It uses the token.FileSet to translate the node's byte offsets
// into human-readable line numbers for the StartsAt and EndsAt fields. Returns
// a Location with zero line values when the node is nil, such as for synthetic
// elements that don't correspond to actual source positions.
func locationFromNode(fset *token.FileSet, node ast.Node, filePath string) domain.Location {
	if node == nil {
		return domain.Location{Path: domain.FilePath(filePath)}
	}
	start := fset.Position(node.Pos())
	end := fset.Position(node.End())
	return domain.Location{
		StartsAt: start.Line,
		EndsAt:   end.Line,
		Path:     domain.FilePath(filePath),
	}
}
