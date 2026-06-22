package jsscanner

import (
	"os"
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	tsjavascript "github.com/smacker/go-tree-sitter/javascript"
	tstsx "github.com/smacker/go-tree-sitter/typescript/tsx"
	tstypescript "github.com/smacker/go-tree-sitter/typescript/typescript"

	"aracne/internal/topology/domain"
	js "aracne/internal/topology/javascript"
)

// tree-sitter node type names (verified against the bundled javascript + typescript grammars).
const (
	ntFunctionDeclaration   = "function_declaration"
	ntGeneratorFuncDecl     = "generator_function_declaration"
	ntFunctionExpression    = "function_expression"
	ntGeneratorFunction     = "generator_function"
	ntArrowFunction         = "arrow_function"
	ntFunctionSignature     = "function_signature"
	ntClassDeclaration      = "class_declaration"
	ntAbstractClassDecl     = "abstract_class_declaration"
	ntClassBody             = "class_body"
	ntClassHeritage         = "class_heritage"
	ntExtendsClause         = "extends_clause"
	ntExtendsTypeClause     = "extends_type_clause"
	ntImplementsClause      = "implements_clause"
	ntMethodDefinition      = "method_definition"
	ntAbstractMethodSig     = "abstract_method_signature"
	ntFieldDefinition       = "field_definition"
	ntPublicFieldDefinition = "public_field_definition"
	ntInterfaceDeclaration  = "interface_declaration"
	ntInterfaceBody         = "interface_body"
	ntPropertySignature     = "property_signature"
	ntMethodSignature       = "method_signature"
	ntTypeAliasDeclaration  = "type_alias_declaration"
	ntEnumDeclaration       = "enum_declaration"
	ntEnumBody              = "enum_body"
	ntEnumAssignment        = "enum_assignment"
	ntInternalModule        = "internal_module"
	ntModule                = "module"
	ntAmbientDeclaration    = "ambient_declaration"
	ntLexicalDeclaration    = "lexical_declaration"
	ntVariableDeclaration   = "variable_declaration"
	ntVariableDeclarator    = "variable_declarator"
	ntImportStatement       = "import_statement"
	ntImportClause          = "import_clause"
	ntNamedImports          = "named_imports"
	ntImportSpecifier       = "import_specifier"
	ntNamespaceImport       = "namespace_import"
	ntExportStatement       = "export_statement"
	ntExportClause          = "export_clause"
	ntExportSpecifier       = "export_specifier"
	ntCallExpression        = "call_expression"
	ntNewExpression         = "new_expression"
	ntMemberExpression      = "member_expression"
	ntIdentifier            = "identifier"
	ntTypeIdentifier        = "type_identifier"
	ntPropertyIdentifier    = "property_identifier"
	ntStatementBlock        = "statement_block"
	ntFormalParameters      = "formal_parameters"
	ntAssignmentExpression  = "assignment_expression"
	ntExpressionStatement   = "expression_statement"
	ntObject                = "object"
	ntPair                  = "pair"
	ntShorthandProperty     = "shorthand_property_identifier"
	ntString                = "string"
	ntAssignmentPattern     = "assignment_pattern"
	ntRestPattern           = "rest_pattern"
	ntObjectPattern         = "object_pattern"
	ntShorthandPropPattern  = "shorthand_property_identifier_pattern"
	ntPairPattern           = "pair_pattern"
	ntRequiredParameter     = "required_parameter"
	ntOptionalParameter     = "optional_parameter"
	ntTypeAnnotation        = "type_annotation"
	ntGenericType           = "generic_type"
	ntTypeArguments         = "type_arguments"
	ntArrayType             = "array_type"
	ntAccessibilityModifier = "accessibility_modifier"
	ntDecorator             = "decorator"
)

// grammarForFile selects the tree-sitter grammar by file extension. `.ts` cannot use the
// JSX grammar because `<T>` is ambiguous with JSX, so TypeScript and TSX have distinct grammars.
func grammarForFile(path string) *sitter.Language {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".tsx":
		return tstsx.GetLanguage()
	case ".ts", ".mts", ".cts":
		return tstypescript.GetLanguage()
	default: // .js, .jsx, .mjs, .cjs
		return tsjavascript.GetLanguage()
	}
}

// importInfo records how a local binding maps to an imported module symbol.
type importInfo struct {
	Source       string
	Internal     bool
	ImportedName string // "default" for default imports; "" for namespaces
	Namespace    bool
}

// Represents a parsed JavaScript function with parameters, return type, async/generator flags, and captured calls and assignments
type jsFunc struct {
	Name        string
	Params      []js.VariableDefinition
	Output      []js.VariableDefinition
	IsAsync     bool
	IsGenerator bool
	BodyCalls   []jsBodyCall
	BodyAssigns []jsBodyAssign
}

// Represents a function or method call in a function body with object, method, or function name and line number
type jsBodyCall struct {
	ObjectName string
	MethodName string
	Func       string
	IsNew      bool
	LineNo     int
}

// Represents a variable assignment in a function body with optional constructor call, method call, alias, or TypeScript type annotation
type jsBodyAssign struct {
	Name         string
	NewClass     string // value is `new X(...)`
	CallFunc     string // value is `foo(...)`
	AliasOf      string // value is a bare identifier
	DeclaredType string // TypeScript `const x: Foo = ...` annotation
}

// Holds a parsed JavaScript/TypeScript function and its associated AST body node.
type FunctionParse struct {
	Function js.JavaScriptFunction
	Body     *jsFunc
}

// Contains the parsed structure of a JavaScript/TypeScript file including functions, classes, imports, exports, and type definitions.
type ParseResult struct {
	FileID          string
	FileDescription string
	PkgPath         js.PackagePath
	ModuleRoot      string
	ModulePath      string
	InternalImports []string
	ExternalImports []js.JavaScriptDependancy
	ImportMap       map[string]importInfo
	Exports         map[string]string
	Classes         []js.JavaScriptClass
	Functions       []FunctionParse
	ExternalVars    []js.JavaScriptExternalVar
	Interfaces      []js.JavaScriptInterface
	NamedTypes      []js.JavaScriptNamedType
}

// Extracts the text content of a tree-sitter node from source bytes.
func nodeText(n *sitter.Node, src []byte) string {
	if n == nil {
		return ""
	}
	return n.Content(src)
}

// Returns the 1-indexed starting line number of a tree-sitter node.
func startLine(n *sitter.Node) int { return int(n.StartPoint().Row) + 1 }
// Returns the 1-indexed ending line number of an AST node.
func endLine(n *sitter.Node) int   { return int(n.EndPoint().Row) + 1 }

// Finds the first named child node matching a given type.
func childByType(n *sitter.Node, typ string) *sitter.Node {
	if n == nil {
		return nil
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(i)
		if c.Type() == typ {
			return c
		}
	}
	return nil
}

// Checks whether a tree-sitter node has a child with a given token type.
func hasChildToken(n *sitter.Node, token string) bool {
	for i := 0; i < int(n.ChildCount()); i++ {
		if n.Child(i).Type() == token {
			return true
		}
	}
	return false
}

// Removes surrounding quotes (", ', or `) from a string specifier.
func trimSpecifier(s string) string { return strings.Trim(s, "\"'`") }

// Checks whether a specifier string is a relative import path (starts with ./, ../, or is . or ..).
func isRelativeSpecifier(s string) bool {
	return strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../") || s == "." || s == ".."
}

// ParseFile parses a single JavaScript/JSX/TypeScript/TSX file into a ParseResult.
func ParseFile(filePath string, pkgPath js.PackagePath, moduleRoot string) (*ParseResult, error) {
	src, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	parser := sitter.NewParser()
	defer parser.Close()
	parser.SetLanguage(grammarForFile(filePath))

	tree := parser.Parse(nil, src)
	defer tree.Close()
	root := tree.RootNode()

	pr := &ParseResult{
		FileID:     filePath,
		PkgPath:    pkgPath,
		ModuleRoot: moduleRoot,
		ModulePath: jsModulePath(moduleRoot, filePath),
		ImportMap:  make(map[string]importInfo),
		Exports:    make(map[string]string),
	}

	for i := 0; i < int(root.NamedChildCount()); i++ {
		parseTopLevel(root.NamedChild(i), false, src, pr)
	}

	return pr, nil
}

// Dispatches top-level JavaScript/TypeScript declarations (imports, exports, functions, classes, interfaces, enums, etc.) to their respective parsers.
func parseTopLevel(node *sitter.Node, exported bool, src []byte, pr *ParseResult) {
	switch node.Type() {
	case ntImportStatement:
		parseImport(node, src, pr)
	case ntExportStatement:
		parseExport(node, src, pr)
	case ntFunctionDeclaration, ntGeneratorFuncDecl, ntFunctionSignature:
		pr.Functions = append(pr.Functions, parseFunctionDecl(node, src, pr, exported))
	case ntClassDeclaration, ntAbstractClassDecl:
		parseClass(node, src, pr, exported)
	case ntInterfaceDeclaration:
		parseInterface(node, src, pr)
	case ntTypeAliasDeclaration:
		parseTypeAlias(node, src, pr)
	case ntEnumDeclaration:
		parseEnum(node, src, pr)
	case ntInternalModule, ntModule:
		parseNamespace(node, src, pr)
	case ntAmbientDeclaration:
		for i := 0; i < int(node.NamedChildCount()); i++ {
			parseTopLevel(node.NamedChild(i), exported, src, pr)
		}
	case ntLexicalDeclaration, ntVariableDeclaration:
		parseVarDeclaration(node, src, pr, exported)
	case ntExpressionStatement:
		if assign := childByType(node, ntAssignmentExpression); assign != nil {
			parseCommonJSExport(assign, src, pr)
		}
	}
}

// Extracts import source and bindings from an ES6 import statement, tracking internal vs external imports and mapping local names to their sources.
func parseImport(node *sitter.Node, src []byte, pr *ParseResult) {
	srcNode := node.ChildByFieldName("source")
	if srcNode == nil {
		srcNode = childByType(node, ntString)
	}
	source := trimSpecifier(nodeText(srcNode, src))
	if source == "" {
		return
	}
	internal := isRelativeSpecifier(source)
	if internal {
		pr.InternalImports = append(pr.InternalImports, source)
	} else {
		pr.ExternalImports = append(pr.ExternalImports, js.JavaScriptDependancy{PackagePath: js.DependancyPath(source)})
	}

	clause := childByType(node, ntImportClause)
	if clause == nil {
		return // side-effect import
	}
	for i := 0; i < int(clause.NamedChildCount()); i++ {
		c := clause.NamedChild(i)
		switch c.Type() {
		case ntIdentifier:
			pr.ImportMap[nodeText(c, src)] = importInfo{Source: source, Internal: internal, ImportedName: "default"}
		case ntNamespaceImport:
			if id := childByType(c, ntIdentifier); id != nil {
				pr.ImportMap[nodeText(id, src)] = importInfo{Source: source, Internal: internal, Namespace: true}
			}
		case ntNamedImports:
			for j := 0; j < int(c.NamedChildCount()); j++ {
				spec := c.NamedChild(j)
				if spec.Type() != ntImportSpecifier {
					continue
				}
				name := nodeText(spec.ChildByFieldName("name"), src)
				local := name
				if alias := spec.ChildByFieldName("alias"); alias != nil {
					local = nodeText(alias, src)
				}
				if local != "" {
					pr.ImportMap[local] = importInfo{Source: source, Internal: internal, ImportedName: name}
				}
			}
		}
	}
}

// Processes ES6 export statements, handling declarations, defaults, and named export specifiers.
func parseExport(node *sitter.Node, src []byte, pr *ParseResult) {
	if decl := node.ChildByFieldName("declaration"); decl != nil {
		parseTopLevel(decl, true, src, pr)
		recordDeclExports(decl, src, pr, hasChildToken(node, "default"))
		return
	}
	if hasChildToken(node, "default") {
		if id := childByType(node, ntIdentifier); id != nil {
			pr.Exports["default"] = nodeText(id, src)
		}
		return
	}
	if clause := childByType(node, ntExportClause); clause != nil {
		for i := 0; i < int(clause.NamedChildCount()); i++ {
			spec := clause.NamedChild(i)
			if spec.Type() != ntExportSpecifier {
				continue
			}
			name := nodeText(spec.ChildByFieldName("name"), src)
			exported := name
			if alias := spec.ChildByFieldName("alias"); alias != nil {
				exported = nodeText(alias, src)
			}
			if exported != "" {
				pr.Exports[exported] = name
			}
		}
	}
}

// Records exported declarations (functions, classes, variables, types) and maps default exports to their target.
func recordDeclExports(decl *sitter.Node, src []byte, pr *ParseResult, isDefault bool) {
	switch decl.Type() {
	case ntFunctionDeclaration, ntGeneratorFuncDecl, ntFunctionSignature,
		ntClassDeclaration, ntAbstractClassDecl, ntInterfaceDeclaration,
		ntTypeAliasDeclaration, ntEnumDeclaration:
		name := nodeText(decl.ChildByFieldName("name"), src)
		if name == "" {
			return
		}
		pr.Exports[name] = name
		if isDefault {
			pr.Exports["default"] = name
		}
	case ntLexicalDeclaration, ntVariableDeclaration:
		for i := 0; i < int(decl.NamedChildCount()); i++ {
			d := decl.NamedChild(i)
			if d.Type() != ntVariableDeclarator {
				continue
			}
			if name := nodeText(d.ChildByFieldName("name"), src); name != "" {
				pr.Exports[name] = name
			}
		}
	}
}

// Extracts CommonJS exports from module.exports and exports.* assignments.
func parseCommonJSExport(assign *sitter.Node, src []byte, pr *ParseResult) {
	left := assign.ChildByFieldName("left")
	right := assign.ChildByFieldName("right")
	if left == nil || left.Type() != ntMemberExpression {
		return
	}
	obj := nodeText(left.ChildByFieldName("object"), src)
	prop := nodeText(left.ChildByFieldName("property"), src)

	if obj == "exports" || strings.HasSuffix(obj, ".exports") {
		if prop != "" && prop != "exports" {
			pr.Exports[prop] = localExportName(right, src, prop)
			return
		}
	}
	if obj == "module" && prop == "exports" && right != nil {
		switch right.Type() {
		case ntIdentifier:
			pr.Exports["default"] = nodeText(right, src)
		case ntObject:
			for i := 0; i < int(right.NamedChildCount()); i++ {
				p := right.NamedChild(i)
				switch p.Type() {
				case ntShorthandProperty:
					n := nodeText(p, src)
					pr.Exports[n] = n
				case ntPair:
					key := nodeText(p.ChildByFieldName("key"), src)
					if key != "" {
						pr.Exports[key] = localExportName(p.ChildByFieldName("value"), src, key)
					}
				}
			}
		}
	}
}

// Extracts export name from a tree-sitter node, returning node text if it's an identifier, otherwise the fallback string.
func localExportName(value *sitter.Node, src []byte, fallback string) string {
	if value != nil && value.Type() == ntIdentifier {
		return nodeText(value, src)
	}
	return fallback
}

// Parses function declarations and extracts metadata including parameters, return types, async/generator flags, and body calls.
func parseFunctionDecl(node *sitter.Node, src []byte, pr *ParseResult, exported bool) FunctionParse {
	name := nodeText(node.ChildByFieldName("name"), src)
	isGen := node.Type() == ntGeneratorFuncDecl || hasChildToken(node, "*")
	isAsync := hasChildToken(node, "async")
	body := node.ChildByFieldName("body")

	raw := &jsFunc{
		Name:        name,
		Params:      extractParams(node, src),
		Output:      returnTypeDefs(node, src),
		IsAsync:     isAsync,
		IsGenerator: isGen,
	}
	raw.BodyCalls, raw.BodyAssigns = collectBody(body, src)

	fn := js.JavaScriptFunction{
		ID:          pr.ModulePath + "." + name,
		Name:        name,
		Input:       raw.Params,
		Output:      raw.Output,
		Loc:         domain.Location{Path: pr.FileID, StartsAt: startLine(node), EndsAt: endLine(node)},
		Connections: make(map[js.ConnectionKind][]string),
		IsAsync:     isAsync,
		IsGenerator: isGen,
		Kind:        "function",
		Exported:    exported,
	}
	return FunctionParse{Function: fn, Body: raw}
}

// Parses a class declaration node, extracting class metadata, methods, and base classes, then appends to ParseResult.
func parseClass(node *sitter.Node, src []byte, pr *ParseResult, exported bool) {
	name := nodeText(node.ChildByFieldName("name"), src)
	if name == "" {
		return
	}
	classID := pr.ModulePath + "." + name
	bases, impls := extractHeritage(node, src)

	cls := js.JavaScriptClass{
		ID:            classID,
		Name:          name,
		Bases:         bases,
		ImplementsRaw: impls,
		IsAbstract:    node.Type() == ntAbstractClassDecl,
		Decorators:    extractDecorators(node, src),
		Loc:           domain.Location{Path: pr.FileID, StartsAt: startLine(node), EndsAt: endLine(node)},
		Connections:   make(map[js.ConnectionKind][]string),
		Exported:      exported,
	}
	pr.Classes = append(pr.Classes, cls)

	body := node.ChildByFieldName("body")
	if body == nil || body.Type() != ntClassBody {
		return
	}
	for i := 0; i < int(body.NamedChildCount()); i++ {
		m := body.NamedChild(i)
		switch m.Type() {
		case ntMethodDefinition:
			pr.Functions = append(pr.Functions, parseMethod(m, src, pr, classID, false))
		case ntAbstractMethodSig:
			pr.Functions = append(pr.Functions, parseMethod(m, src, pr, classID, true))
		}
	}
}

// Parses a class method definition, extracting name, parameters, return type, modifiers (async, static, getter/setter), and body calls/assignments.
func parseMethod(node *sitter.Node, src []byte, pr *ParseResult, classID string, abstract bool) FunctionParse {
	name := nodeText(node.ChildByFieldName("name"), src)
	isAsync := hasChildToken(node, "async")
	isGen := hasChildToken(node, "*")
	isStatic := hasChildToken(node, "static")
	kind := "method"
	if hasChildToken(node, "get") {
		kind = "getter"
	} else if hasChildToken(node, "set") {
		kind = "setter"
	}
	access := ""
	if am := childByType(node, ntAccessibilityModifier); am != nil {
		access = nodeText(am, src)
	}
	body := node.ChildByFieldName("body")

	raw := &jsFunc{
		Name:        name,
		Params:      extractParams(node, src),
		Output:      returnTypeDefs(node, src),
		IsAsync:     isAsync,
		IsGenerator: isGen,
	}
	raw.BodyCalls, raw.BodyAssigns = collectBody(body, src)

	cid := js.ClassID(classID)
	fn := js.JavaScriptFunction{
		ID:            classID + "." + name,
		Name:          name,
		Input:         raw.Params,
		Output:        raw.Output,
		Loc:           domain.Location{Path: pr.FileID, StartsAt: startLine(node), EndsAt: endLine(node)},
		Connections:   make(map[js.ConnectionKind][]string),
		MethodFrom:    &cid,
		IsAsync:       isAsync,
		IsGenerator:   isGen,
		IsStatic:      isStatic,
		IsAbstract:    abstract,
		Kind:          kind,
		Accessibility: access,
		Decorators:    extractDecorators(node, src),
	}
	return FunctionParse{Function: fn, Body: raw}
}

// Parses a TypeScript interface declaration into a JavaScriptInterface, extracting name, base types, methods, and properties.
func parseInterface(node *sitter.Node, src []byte, pr *ParseResult) {
	name := nodeText(node.ChildByFieldName("name"), src)
	if name == "" {
		return
	}
	iface := js.JavaScriptInterface{
		ID:          pr.ModulePath + "." + name,
		Name:        name,
		Loc:         domain.Location{Path: pr.FileID, StartsAt: startLine(node), EndsAt: endLine(node)},
		Connections: make(map[js.ConnectionKind][]string),
	}

	for i := 0; i < int(node.NamedChildCount()); i++ {
		c := node.NamedChild(i)
		if c.Type() != ntExtendsTypeClause {
			continue
		}
		for j := 0; j < int(c.NamedChildCount()); j++ {
			tc := c.NamedChild(j)
			if tc.Type() == ntTypeArguments {
				continue
			}
			if bn := baseNameFromExpr(tc, src); bn != "" {
				iface.Bases = append(iface.Bases, bn)
			}
		}
	}

	if body := node.ChildByFieldName("body"); body != nil {
		for i := 0; i < int(body.NamedChildCount()); i++ {
			m := body.NamedChild(i)
			switch m.Type() {
			case ntMethodSignature:
				iface.Methods = append(iface.Methods, js.FunctionDefinition{
					Name:   nodeText(m.ChildByFieldName("name"), src),
					Input:  extractParams(m, src),
					Output: returnTypeDefs(m, src),
				})
			case ntPropertySignature:
				typing := ""
				if ta := m.ChildByFieldName("type"); ta != nil {
					typing = typeAnnotationName(ta, src)
				}
				iface.Properties = append(iface.Properties, js.VariableDefinition{
					Name:   nodeText(m.ChildByFieldName("name"), src),
					Typing: typing,
				})
			}
		}
	}

	pr.Interfaces = append(pr.Interfaces, iface)
}

// Extracts TypeScript type alias declarations and records them as named types in the parse result.
func parseTypeAlias(node *sitter.Node, src []byte, pr *ParseResult) {
	name := nodeText(node.ChildByFieldName("name"), src)
	if name == "" {
		return
	}
	underlying := nodeText(node.ChildByFieldName("value"), src)
	if len(underlying) > 200 {
		underlying = underlying[:200]
	}
	pr.NamedTypes = append(pr.NamedTypes, js.JavaScriptNamedType{
		ID:          pr.ModulePath + "." + name,
		Name:        name,
		Kind:        "type",
		Underlying:  underlying,
		Loc:         domain.Location{Path: pr.FileID, StartsAt: startLine(node), EndsAt: endLine(node)},
		Connections: make(map[js.ConnectionKind][]string),
	})
}

// Parses TypeScript enum declarations and records their members as named types.
func parseEnum(node *sitter.Node, src []byte, pr *ParseResult) {
	name := nodeText(node.ChildByFieldName("name"), src)
	if name == "" {
		return
	}
	var members []string
	if body := node.ChildByFieldName("body"); body != nil {
		for i := 0; i < int(body.NamedChildCount()); i++ {
			m := body.NamedChild(i)
			switch m.Type() {
			case ntPropertyIdentifier, ntIdentifier:
				members = append(members, nodeText(m, src))
			case ntEnumAssignment:
				if nm := m.ChildByFieldName("name"); nm != nil {
					members = append(members, nodeText(nm, src))
				}
			}
		}
	}
	pr.NamedTypes = append(pr.NamedTypes, js.JavaScriptNamedType{
		ID:          pr.ModulePath + "." + name,
		Name:        name,
		Kind:        "enum",
		Underlying:  "enum",
		Members:     members,
		Loc:         domain.Location{Path: pr.FileID, StartsAt: startLine(node), EndsAt: endLine(node)},
		Connections: make(map[js.ConnectionKind][]string),
	})
}

// parseNamespace recurses into a `namespace`/`module` body, qualifying nested resource IDs
// with the namespace name so they stay unique within the file.
func parseNamespace(node *sitter.Node, src []byte, pr *ParseResult) {
	nsName := nodeText(node.ChildByFieldName("name"), src)
	body := node.ChildByFieldName("body")
	if body == nil || nsName == "" {
		return
	}
	saved := pr.ModulePath
	pr.ModulePath = saved + "." + nsName
	for i := 0; i < int(body.NamedChildCount()); i++ {
		parseTopLevel(body.NamedChild(i), false, src, pr)
	}
	pr.ModulePath = saved
}

// Processes variable declarations, handling function expressions, require bindings, and external variables with type annotations.
func parseVarDeclaration(node *sitter.Node, src []byte, pr *ParseResult, exported bool) {
	for i := 0; i < int(node.NamedChildCount()); i++ {
		d := node.NamedChild(i)
		if d.Type() != ntVariableDeclarator {
			continue
		}
		nameNode := d.ChildByFieldName("name")
		value := d.ChildByFieldName("value")

		if value != nil && value.Type() == ntCallExpression && isRequireCall(value, src) {
			if source := trimSpecifier(requireSource(value, src)); source != "" {
				parseRequireBinding(nameNode, source, src, pr)
			}
			continue
		}

		if nameNode == nil || nameNode.Type() != ntIdentifier {
			continue
		}
		name := nodeText(nameNode, src)

		if value != nil {
			switch value.Type() {
			case ntArrowFunction, ntFunctionExpression, ntGeneratorFunction:
				pr.Functions = append(pr.Functions, parseFunctionValue(name, value, src, pr, exported))
				continue
			}
		}

		v := js.JavaScriptExternalVar{
			ID:       pr.ModulePath + "." + name,
			Name:     name,
			Value:    valuePtr(nodeText(value, src)),
			Exported: exported,
			Location: domain.Location{Path: pr.FileID, StartsAt: startLine(d), EndsAt: endLine(d)},
		}
		if ta := d.ChildByFieldName("type"); ta != nil {
			v.Typing = typeAnnotationName(ta, src)
		}
		pr.ExternalVars = append(pr.ExternalVars, v)
	}
}

// Processes CommonJS require binding patterns, mapping imported names (including destructured properties) to their source module.
func parseRequireBinding(nameNode *sitter.Node, source string, src []byte, pr *ParseResult) {
	internal := isRelativeSpecifier(source)
	if internal {
		pr.InternalImports = append(pr.InternalImports, source)
	} else {
		pr.ExternalImports = append(pr.ExternalImports, js.JavaScriptDependancy{PackagePath: js.DependancyPath(source)})
	}
	if nameNode == nil {
		return
	}
	switch nameNode.Type() {
	case ntIdentifier:
		pr.ImportMap[nodeText(nameNode, src)] = importInfo{Source: source, Internal: internal, Namespace: true}
	case ntObjectPattern:
		for i := 0; i < int(nameNode.NamedChildCount()); i++ {
			p := nameNode.NamedChild(i)
			switch p.Type() {
			case ntShorthandProperty, ntShorthandPropPattern:
				n := nodeText(p, src)
				pr.ImportMap[n] = importInfo{Source: source, Internal: internal, ImportedName: n}
			case ntPair, ntPairPattern:
				key := nodeText(p.ChildByFieldName("key"), src)
				local := nodeText(p.ChildByFieldName("value"), src)
				if local == "" {
					local = key
				}
				if local != "" {
					pr.ImportMap[local] = importInfo{Source: source, Internal: internal, ImportedName: key}
				}
			}
		}
	}
}

// Parses function expressions and arrow functions, extracting metadata and distinguishing between function and arrow kinds.
func parseFunctionValue(name string, value *sitter.Node, src []byte, pr *ParseResult, exported bool) FunctionParse {
	isAsync := hasChildToken(value, "async")
	isGen := value.Type() == ntGeneratorFunction || hasChildToken(value, "*")
	body := value.ChildByFieldName("body")

	raw := &jsFunc{
		Name:        name,
		Params:      extractParams(value, src),
		Output:      returnTypeDefs(value, src),
		IsAsync:     isAsync,
		IsGenerator: isGen,
	}
	raw.BodyCalls, raw.BodyAssigns = collectBody(body, src)

	kind := "function"
	if value.Type() == ntArrowFunction {
		kind = "arrow"
	}
	fn := js.JavaScriptFunction{
		ID:          pr.ModulePath + "." + name,
		Name:        name,
		Input:       raw.Params,
		Output:      raw.Output,
		Loc:         domain.Location{Path: pr.FileID, StartsAt: startLine(value), EndsAt: endLine(value)},
		Connections: make(map[js.ConnectionKind][]string),
		IsAsync:     isAsync,
		IsGenerator: isGen,
		Kind:        kind,
		Exported:    exported,
	}
	return FunctionParse{Function: fn, Body: raw}
}

// Extracts function parameters from a function node, handling both single and multi-parameter declarations.
func extractParams(fnNode *sitter.Node, src []byte) []js.VariableDefinition {
	var params []js.VariableDefinition
	p := fnNode.ChildByFieldName("parameters")
	if p == nil {
		if single := fnNode.ChildByFieldName("parameter"); single != nil {
			params = append(params, js.VariableDefinition{Name: nodeText(single, src)})
		}
		return params
	}
	for i := 0; i < int(p.NamedChildCount()); i++ {
		params = append(params, paramDef(p.NamedChild(i), src))
	}
	return params
}

// Parses a parameter node into a VariableDefinition, extracting name and optional type annotation.
func paramDef(c *sitter.Node, src []byte) js.VariableDefinition {
	switch c.Type() {
	case ntIdentifier:
		return js.VariableDefinition{Name: nodeText(c, src)}
	case ntRequiredParameter, ntOptionalParameter:
		def := js.VariableDefinition{}
		if pat := c.ChildByFieldName("pattern"); pat != nil {
			def.Name = nodeText(pat, src)
		}
		if ta := c.ChildByFieldName("type"); ta != nil {
			def.Typing = typeAnnotationName(ta, src)
		}
		return def
	case ntAssignmentPattern:
		if left := c.ChildByFieldName("left"); left != nil {
			return js.VariableDefinition{Name: nodeText(left, src)}
		}
	case ntRestPattern:
		if id := childByType(c, ntIdentifier); id != nil {
			return js.VariableDefinition{Name: "..." + nodeText(id, src)}
		}
	}
	return js.VariableDefinition{Name: nodeText(c, src)}
}

// returnTypeDefs extracts a function/method `return_type` annotation as a single output def.
func returnTypeDefs(node *sitter.Node, src []byte) []js.VariableDefinition {
	rt := node.ChildByFieldName("return_type")
	if rt == nil {
		return nil
	}
	if t := typeAnnotationName(rt, src); t != "" {
		return []js.VariableDefinition{{Typing: t}}
	}
	return nil
}

// typeAnnotationName returns the resolvable user type name inside a type_annotation, or "".
func typeAnnotationName(ta *sitter.Node, src []byte) string {
	for i := 0; i < int(ta.NamedChildCount()); i++ {
		return typeRefName(ta.NamedChild(i), src)
	}
	return ""
}

// Extracts the name from a TypeScript/JavaScript type reference node, handling identifiers, generics, arrays, and member expressions.
func typeRefName(n *sitter.Node, src []byte) string {
	switch n.Type() {
	case ntTypeIdentifier, ntIdentifier:
		return nodeText(n, src)
	case ntGenericType:
		if nm := n.ChildByFieldName("name"); nm != nil {
			return typeRefName(nm, src)
		}
	case ntArrayType:
		for i := 0; i < int(n.NamedChildCount()); i++ {
			return typeRefName(n.NamedChild(i), src)
		}
	case ntMemberExpression:
		if prop := n.ChildByFieldName("property"); prop != nil {
			return nodeText(prop, src)
		}
	}
	return ""
}

// Extracts base classes and implemented interfaces from a class node's extends and implements clauses.
func extractHeritage(classNode *sitter.Node, src []byte) (bases, impls []string) {
	h := childByType(classNode, ntClassHeritage)
	if h == nil {
		return
	}
	collect := func(clause *sitter.Node, dst *[]string) {
		for i := 0; i < int(clause.NamedChildCount()); i++ {
			c := clause.NamedChild(i)
			if c.Type() == ntTypeArguments {
				continue
			}
			if name := baseNameFromExpr(c, src); name != "" {
				*dst = append(*dst, name)
			}
		}
	}
	for i := 0; i < int(h.NamedChildCount()); i++ {
		c := h.NamedChild(i)
		switch c.Type() {
		case ntExtendsClause:
			collect(c, &bases)
		case ntImplementsClause:
			collect(c, &impls)
		default:
			// JavaScript grammar: the base expression is a direct child of class_heritage.
			if name := baseNameFromExpr(c, src); name != "" {
				bases = append(bases, name)
			}
		}
	}
	return
}

// Recursively extracts the base identifier name from an expression node, unwrapping generics and member access.
func baseNameFromExpr(n *sitter.Node, src []byte) string {
	if n == nil {
		return ""
	}
	switch n.Type() {
	case ntIdentifier, ntTypeIdentifier:
		return nodeText(n, src)
	case ntGenericType:
		if nm := n.ChildByFieldName("name"); nm != nil {
			return baseNameFromExpr(nm, src)
		}
	case ntMemberExpression:
		if prop := n.ChildByFieldName("property"); prop != nil {
			return nodeText(prop, src)
		}
	case ntCallExpression:
		if f := n.ChildByFieldName("function"); f != nil {
			return baseNameFromExpr(f, src)
		}
	}
	return ""
}

// Extracts decorator names from a class or function node, parsing decorator syntax and removing parentheses if present.
func extractDecorators(node *sitter.Node, src []byte) []string {
	var decs []string
	for i := 0; i < int(node.ChildCount()); i++ {
		c := node.Child(i)
		if c.Type() != ntDecorator {
			continue
		}
		t := strings.TrimPrefix(strings.TrimSpace(nodeText(c, src)), "@")
		if idx := strings.IndexAny(t, "("); idx >= 0 {
			t = t[:idx]
		}
		if t != "" {
			decs = append(decs, t)
		}
	}
	return decs
}

// Recursively extracts function calls and variable assignments from a function body AST node.
func collectBody(bodyNode *sitter.Node, src []byte) ([]jsBodyCall, []jsBodyAssign) {
	var calls []jsBodyCall
	var assigns []jsBodyAssign
	if bodyNode == nil {
		return calls, assigns
	}

	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		switch n.Type() {
		case ntCallExpression:
			if fn := n.ChildByFieldName("function"); fn != nil {
				switch fn.Type() {
				case ntIdentifier:
					calls = append(calls, jsBodyCall{Func: nodeText(fn, src), LineNo: startLine(n)})
				case ntMemberExpression:
					obj := fn.ChildByFieldName("object")
					prop := fn.ChildByFieldName("property")
					if obj != nil && prop != nil {
						calls = append(calls, jsBodyCall{
							ObjectName: nodeText(obj, src),
							MethodName: nodeText(prop, src),
							Func:       nodeText(fn, src),
							LineNo:     startLine(n),
						})
					}
				}
			}
		case ntNewExpression:
			if c := n.ChildByFieldName("constructor"); c != nil {
				calls = append(calls, jsBodyCall{Func: baseNameFromExpr(c, src), IsNew: true, LineNo: startLine(n)})
			}
		case ntVariableDeclarator:
			if name := n.ChildByFieldName("name"); name != nil && name.Type() == ntIdentifier {
				a := assignFrom(nodeText(name, src), n.ChildByFieldName("value"), src)
				if ta := n.ChildByFieldName("type"); ta != nil {
					a.DeclaredType = typeAnnotationName(ta, src)
				}
				assigns = append(assigns, a)
			}
		case ntAssignmentExpression:
			if left := n.ChildByFieldName("left"); left != nil && left.Type() == ntIdentifier {
				assigns = append(assigns, assignFrom(nodeText(left, src), n.ChildByFieldName("right"), src))
			}
		}
		for i := 0; i < int(n.NamedChildCount()); i++ {
			walk(n.NamedChild(i))
		}
	}
	walk(bodyNode)
	return calls, assigns
}

// Parses an assignment value node to extract new class instantiations, function calls, or variable aliases.
func assignFrom(name string, val *sitter.Node, src []byte) jsBodyAssign {
	a := jsBodyAssign{Name: name}
	if val == nil {
		return a
	}
	switch val.Type() {
	case ntNewExpression:
		if c := val.ChildByFieldName("constructor"); c != nil {
			a.NewClass = baseNameFromExpr(c, src)
		}
	case ntCallExpression:
		if f := val.ChildByFieldName("function"); f != nil && f.Type() == ntIdentifier {
			a.CallFunc = nodeText(f, src)
		}
	case ntIdentifier:
		a.AliasOf = nodeText(val, src)
	}
	return a
}

// Checks if a call node represents a require() function invocation
func isRequireCall(call *sitter.Node, src []byte) bool {
	f := call.ChildByFieldName("function")
	return f != nil && f.Type() == ntIdentifier && nodeText(f, src) == "require"
}

// Extracts the string argument from a require() call node.
func requireSource(call *sitter.Node, src []byte) string {
	args := call.ChildByFieldName("arguments")
	if args == nil {
		return ""
	}
	if s := childByType(args, ntString); s != nil {
		return nodeText(s, src)
	}
	return ""
}

// Returns a pointer to a string value, truncated to 500 chars; returns nil for empty strings.
func valuePtr(s string) *any {
	if s == "" {
		return nil
	}
	const maxLen = 500
	if len(s) > maxLen {
		s = s[:maxLen]
	}
	var v any = s
	return &v
}

// jsModulePath returns the file-scoped ID namespace for a source file: the project-root base
// name joined with the file's path relative to root, extension stripped, separators "/".
func jsModulePath(root, file string) string {
	base := filepath.Base(root)
	rel, err := filepath.Rel(root, file)
	if err != nil {
		rel = filepath.Base(file)
	}
	rel = strings.ReplaceAll(rel, "\\", "/")
	rel = strings.TrimSuffix(rel, filepath.Ext(rel))
	if rel == "." || rel == "" {
		return base
	}
	return base + "/" + rel
}
