package jsscanner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	tsjavascript "github.com/smacker/go-tree-sitter/javascript"
	tstsx "github.com/smacker/go-tree-sitter/typescript/tsx"
	tstypescript "github.com/smacker/go-tree-sitter/typescript/typescript"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology/contract"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	js "github.com/Rhuan-Marques/aracne/internal/topology/javascript"
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
	ntClass                 = "class"
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
	ntImportRequireClause   = "import_require_clause"
	ntNamedImports          = "named_imports"
	ntImportSpecifier       = "import_specifier"
	ntNamespaceImport       = "namespace_import"
	ntDynamicImport         = "import"
	ntAwaitExpression       = "await_expression"
	ntExportStatement       = "export_statement"
	ntExportClause          = "export_clause"
	ntExportSpecifier       = "export_specifier"
	ntNamespaceExport       = "namespace_export"
	ntCallExpression        = "call_expression"
	ntNewExpression         = "new_expression"
	ntMemberExpression      = "member_expression"
	ntParenthesizedExpr     = "parenthesized_expression"
	ntNonNullExpression     = "non_null_expression"
	ntAsExpression          = "as_expression"
	ntSuper                 = "super"
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
	ntComputedPropertyName  = "computed_property_name"
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
	ntUnionType             = "union_type"
	ntPredefinedType        = "predefined_type"
	ntNestedTypeIdentifier  = "nested_type_identifier"
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

// namedReExport records a named ESM re-export (`export {Orig as Exported} from
// "./src"`): the name a consumer sees (Exported), the original name in the
// source module (Original, possibly "default"), and the source specifier.
type namedReExport struct {
	Exported string
	Original string
	Source   string
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
	ObjectName         string
	ObjectNewClass     string // class name when the receiver is an inline `new X(...)` expression
	ObjectCallFunc     string // function name when the receiver is an inline `f(...)` call expression
	ObjectCastClass    string // class name when the receiver is a TS cast `(x as T)` / `(<T>x)`
	ObjectMethodObject string // receiver variable when the receiver is an inline method call `v.m(...)`
	ObjectMethodName   string // method name when the receiver is an inline method call `v.m(...)`
	MethodName         string
	Func               string
	IsNew              bool
	LineNo             int
	// Args is one token per argument written at the call site, "" where the argument's
	// type could not be read; ArgC is -1 when a spread hides the real count.
	Args []string
	ArgC int
}

// Represents a variable assignment in a function body with optional constructor call, method call, alias, or TypeScript type annotation
type jsBodyAssign struct {
	Name            string
	NewClass        string // value is `new X(...)`
	CallFunc        string // value is `foo(...)`
	AliasOf         string // value is a bare identifier
	DeclaredType    string // TypeScript `const x: Foo = ...` annotation
	DeclaredTypeArg string // first generic type argument of the annotation, e.g. Circle in Box<Circle>
}

// Holds a parsed JavaScript/TypeScript function and its associated AST body node.
type FunctionParse struct {
	Function js.JavaScriptFunction
	Body     *jsFunc
	// Overload marks a bodiless TypeScript overload declaration. Every declaration of an
	// overload set mints the same id as the implementation, so applyParsedFile needs to
	// know which of the collisions is the implementation and which are the signatures to
	// fold into it.
	Overload bool
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
	// ReExportAll holds source specifiers re-exported wholesale via CommonJS
	// `module.exports = <whole-module require binding>` or ESM `export * from
	// "./src"`. Resolved to module IDs in resolveTopology and surfaced as
	// re_exports_module edges.
	ReExportAll []string
	// ReExportNamed holds named ESM re-exports (`export {Orig as Exported} from
	// "./src"`). Resolved in resolveTopology to per-module re-export targets.
	ReExportNamed []namedReExport
	Classes       []js.JavaScriptClass
	Functions     []FunctionParse
	ExternalVars  []js.JavaScriptExternalVar
	Interfaces    []js.JavaScriptInterface
	NamedTypes    []js.JavaScriptNamedType
	// SyntaxError is set when the parser had to recover from a part of the file it could not
	// read. The recovered declarations are kept, but may be incomplete or misplaced -- a
	// function after an unclosed class body can land inside the class -- so the scan records
	// it as a file error instead of presenting the structure as sound.
	SyntaxError string
	// namespaces holds the scopes opened by a `namespace`/`module` block, so name resolution
	// can walk out through them and only them (an object literal's `obj.m` prefix is not a
	// scope a bare name can see into).
	namespaces map[string]bool
	// heritageQualifiers maps a class ID to the qualifier of each base written as a member
	// (`extends React.Component` -> Component: React), which Bases does not keep.
	heritageQualifiers map[string]map[string]string
	// aliases is the file's tsconfig/jsconfig module mapping, loaded on first use.
	aliases       *pathAliases
	aliasesLoaded bool
}

// internalSpecifier decides whether an import specifier names a file of this project, and
// returns it in the form resolveSpecifier takes: a relative specifier as written, and a
// tsconfig/jsconfig alias (`@app/models/shape`, a baseUrl-relative `models/shape`) as the
// absolute module path it maps to. Anything else is a package.
func (pr *ParseResult) internalSpecifier(source string) (string, bool) {
	if isRelativeSpecifier(source) {
		return source, true
	}
	if !pr.aliasesLoaded {
		pr.aliases, pr.aliasesLoaded = loadPathAliases(pr.ModuleRoot, filepath.Dir(pr.FileID)), true
	}
	if abs, ok := pr.aliases.resolve(pr.ModuleRoot, source); ok {
		return abs, true
	}
	return source, false
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
func endLine(n *sitter.Node) int { return int(n.EndPoint().Row) + 1 }

// memberName resolves the static name of a class member's `name` node. For a
// computed property name backed by a static literal (e.g. `["dynamic"]` or
// `[42]`), it returns the literal value so the resource ID is well-formed
// (`Class.dynamic`). For genuinely dynamic names (identifiers, member/Symbol
// expressions) it returns the inner expression text, which keeps the ID free
// of bracket/quote syntax.
func memberName(nameNode *sitter.Node, src []byte) string {
	if nameNode == nil {
		return ""
	}
	if nameNode.Type() != ntComputedPropertyName {
		return nodeText(nameNode, src)
	}
	inner := nameNode.NamedChild(0)
	if inner == nil {
		return strings.Trim(nodeText(nameNode, src), "[]")
	}
	switch inner.Type() {
	case ntString:
		// Static string literal name → use the property name (drop the quotes).
		return trimSpecifier(nodeText(inner, src))
	default:
		// Numbers, identifiers, member/Symbol expressions, etc.: use the raw
		// expression text so the ID has no bracket/quote syntax.
		return nodeText(inner, src)
	}
}

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

	// ParseCtx, not the deprecated Parse: it is the only form that reports a parse
	// failure instead of handing back a tree to walk. The context is Background because
	// LanguageScanner.Scan takes none -- making a parse cancellable means threading one
	// through the interface, which is a change to every scanner, not to this line.
	tree, err := parser.ParseCtx(context.Background(), nil, src)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", filePath, err)
	}
	defer tree.Close()
	root := tree.RootNode()

	pr := &ParseResult{
		FileID:     filePath,
		PkgPath:    pkgPath,
		ModuleRoot: moduleRoot,
		ModulePath: jsModulePathFor(moduleRoot, filePath),
		ImportMap:  make(map[string]importInfo),
		Exports:    make(map[string]string),
	}

	for i := 0; i < int(root.NamedChildCount()); i++ {
		parseTopLevel(root.NamedChild(i), false, src, pr)
	}
	mergeAccessorPairs(pr)
	recordHeritageImports(pr)
	recordLocalExportAliases(pr)
	if root.HasError() {
		pr.SyntaxError = fmt.Sprintf("parse error at line %d: part of this file could not be parsed, "+
			"so the declarations recovered from it may be incomplete or misplaced", firstErrorLine(root))
	}

	return pr, nil
}

// firstErrorLine returns the 1-based line of the first node the parser could not read:
// an ERROR node, or a token it had to invent (MISSING). An ERROR node can open with
// declarations that parsed cleanly (it may even be the whole file), so the line reported is
// that of its first child that did not.
func firstErrorLine(n *sitter.Node) int {
	if n.IsMissing() {
		return startLine(n)
	}
	for i := 0; i < int(n.ChildCount()); i++ {
		c := n.Child(i)
		if c.HasError() || c.IsMissing() {
			return firstErrorLine(c)
		}
		if n.IsError() && !c.IsNamed() {
			return startLine(c)
		}
	}
	return startLine(n)
}

// mergeAccessorPairs folds a getter and a setter of the same name into one resource. Both
// mint `Class.name`, so the later declaration used to replace the earlier one and the
// resource read as the setter alone. The merged resource spans both declarations and carries
// both bodies' references; its signature is the getter's, the shape a read of the property
// has.
func mergeAccessorPairs(pr *ParseResult) {
	isAccessor := func(kind string) bool { return kind == "getter" || kind == "setter" }
	first := make(map[string]int)
	out := pr.Functions[:0]
	for _, fp := range pr.Functions {
		f := fp.Function
		if !isAccessor(f.Kind) {
			out = append(out, fp)
			continue
		}
		i, seen := first[f.ID]
		if !seen || out[i].Function.Kind == f.Kind {
			first[f.ID] = len(out)
			out = append(out, fp)
			continue
		}
		prim, other := out[i], fp
		if prim.Function.Kind != "getter" {
			prim, other = other, prim
		}
		prim.Function.Loc.StartsAt = min(prim.Function.Loc.StartsAt, other.Function.Loc.StartsAt)
		prim.Function.Loc.EndsAt = max(prim.Function.Loc.EndsAt, other.Function.Loc.EndsAt)
		prim.Function.Decorators = append(prim.Function.Decorators, other.Function.Decorators...)
		if prim.Body != nil && other.Body != nil {
			body := *prim.Body
			body.BodyCalls = append(append([]jsBodyCall(nil), body.BodyCalls...), other.Body.BodyCalls...)
			body.BodyAssigns = append(append([]jsBodyAssign(nil), body.BodyAssigns...), other.Body.BodyAssigns...)
			prim.Body = &body
		}
		out[i] = prim
	}
	pr.Functions = out
}

// recordHeritageImports records, for every class and interface, the imports behind the names
// in its `extends`/`implements` clauses. The inheritance passes resolve those names over the
// whole topology, where this file's import map no longer exists.
func recordHeritageImports(pr *ParseResult) {
	collect := func(qualifiers map[string]string, names ...[]string) map[string]js.HeritageImport {
		var out map[string]js.HeritageImport
		for _, list := range names {
			for _, name := range list {
				imp, ok := js.HeritageImport{}, false
				if q, qualified := qualifiers[name]; qualified {
					// `extends shapes.Base` of `import * as shapes`, or `extends React.Component`
					// of a package: a member of that module.
					if info, bound := pr.ImportMap[q]; bound && (info.Namespace || !info.Internal) {
						imp, ok = js.HeritageImport{Source: info.Source, Name: name}, true
					}
				} else if info, bound := pr.ImportMap[name]; bound {
					imp, ok = js.HeritageImport{Source: info.Source, Name: info.ImportedName}, true
				}
				if !ok {
					continue
				}
				if out == nil {
					out = make(map[string]js.HeritageImport)
				}
				out[name] = imp
			}
		}
		return out
	}
	for i := range pr.Classes {
		c := &pr.Classes[i]
		c.HeritageImports = collect(pr.heritageQualifiers[c.ID], c.Bases, c.ImplementsRaw)
	}
	for i := range pr.Interfaces {
		pr.Interfaces[i].HeritageImports = collect(nil, pr.Interfaces[i].Bases)
	}
}

// recordLocalExportAliases turns an export that is not simply a declaration under its own
// name into a named re-export an importer can follow: `export {impl as renamed}` and
// CommonJS `module.exports = {renamed: impl}` point `renamed` at this module's `impl`, and
// `export {x}` of an imported x points at the module x came from. Resolution looks names up
// by ID, and neither `lib.renamed` nor `lib.x` exists.
func recordLocalExportAliases(pr *ParseResult) {
	names := make([]string, 0, len(pr.Exports))
	for exported := range pr.Exports {
		names = append(names, exported)
	}
	sort.Strings(names) // deterministic ReExportNamed order
	for _, exported := range names {
		local := pr.Exports[exported]
		if exported == "default" || local == "" {
			continue
		}
		if info, ok := pr.ImportMap[local]; ok && info.Internal && !info.Namespace {
			pr.ReExportNamed = append(pr.ReExportNamed, namedReExport{Exported: exported, Original: info.ImportedName, Source: info.Source})
			continue
		}
		if local != exported {
			// Source "" is this module itself.
			pr.ReExportNamed = append(pr.ReExportNamed, namedReExport{Exported: exported, Original: local})
		}
	}
}

// Dispatches top-level JavaScript/TypeScript declarations (imports, exports, functions, classes, interfaces, enums, etc.) to their respective parsers.
func parseTopLevel(node *sitter.Node, exported bool, src []byte, pr *ParseResult) {
	switch node.Type() {
	case ntImportStatement:
		parseImport(node, src, pr)
	case ntExportStatement:
		parseExport(node, src, pr)
	case ntFunctionDeclaration, ntGeneratorFuncDecl, ntFunctionSignature:
		pr.Functions = append(pr.Functions, parseFunctionDecl(node, src, pr, exported, ""))
	case ntClassDeclaration, ntAbstractClassDecl:
		parseClass(node, src, pr, exported, "", "")
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
			child := node.NamedChild(i)
			// `declare global { ... }` wraps its augmentations in a statement_block
			// (unlike `declare module "x" { ... }`, whose child is a `module` node).
			// Recurse into the block so the global members are extracted.
			if child.Type() == ntStatementBlock {
				for j := 0; j < int(child.NamedChildCount()); j++ {
					parseTopLevel(child.NamedChild(j), exported, src, pr)
				}
				continue
			}
			parseTopLevel(child, exported, src, pr)
		}
	case ntLexicalDeclaration, ntVariableDeclaration:
		parseVarDeclaration(node, src, pr, exported)
	case ntExpressionStatement:
		if assign := childByType(node, ntAssignmentExpression); assign != nil {
			parseCommonJSExport(assign, src, pr)
		}
		// A top-level `namespace A { ... }` that is not exported parses as an expression
		// statement wrapping the namespace, not as a declaration.
		if ns := childByType(node, ntInternalModule); ns != nil {
			parseNamespace(ns, src, pr)
		}
	}
}

// Extracts import source and bindings from an ES6 import statement, tracking internal vs external imports and mapping local names to their sources.
func parseImport(node *sitter.Node, src []byte, pr *ParseResult) {
	// TypeScript import-equals: `import x = require("./mod")`. tree-sitter models
	// this as an import_require_clause inside the import_statement (not an
	// import_clause). Route it through parseRequireBinding so the binding is
	// registered as a whole-module namespace import, like `import * as x`,
	// letting member access (`x.Class`) resolve.
	if rc := childByType(node, ntImportRequireClause); rc != nil {
		srcNode := rc.ChildByFieldName("source")
		if srcNode == nil {
			srcNode = childByType(rc, ntString)
		}
		if source := trimSpecifier(nodeText(srcNode, src)); source != "" {
			parseRequireBinding(childByType(rc, ntIdentifier), source, src, pr)
		}
		return
	}

	srcNode := node.ChildByFieldName("source")
	if srcNode == nil {
		srcNode = childByType(node, ntString)
	}
	source := trimSpecifier(nodeText(srcNode, src))
	if source == "" {
		return
	}
	source, internal := pr.internalSpecifier(source)
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
	// `@Component({...}) export class Widget {}`: the decorators belong to the
	// export statement, not to the class declaration inside it.
	classesBefore := len(pr.Classes)
	defer func() {
		if decs := extractDecorators(node, src); len(decs) > 0 && len(pr.Classes) > classesBefore {
			cls := &pr.Classes[classesBefore]
			cls.Decorators = append(decs, cls.Decorators...)
			cls.Loc.StartsAt = min(cls.Loc.StartsAt, startLine(node))
		}
	}()
	if decl := node.ChildByFieldName("declaration"); decl != nil {
		parseTopLevel(decl, true, src, pr)
		recordDeclExports(decl, src, pr, hasChildToken(node, "default"))
		return
	}
	if hasChildToken(node, "default") {
		// Anonymous default-exported function/class declarations land in the
		// `value` field as expressions (function_expression / class) rather than
		// the `declaration` field, and carry no name. Extract them as resources
		// named "default" so the module default_export resolves.
		if value := node.ChildByFieldName("value"); value != nil {
			switch value.Type() {
			case ntFunctionExpression, ntGeneratorFunction:
				fp := parseFunctionDecl(value, src, pr, true, "default")
				pr.Functions = append(pr.Functions, fp)
				pr.Exports["default"] = fp.Function.Name
				return
			case ntClass:
				parseClass(value, src, pr, true, "default", "")
				pr.Exports["default"] = "default"
				return
			}
		}
		if id := childByType(node, ntIdentifier); id != nil {
			pr.Exports["default"] = nodeText(id, src)
		}
		return
	}
	// A `from "./src"` source turns an export clause / `*` into a re-export that
	// forwards to another module's bindings instead of binding local names.
	source := trimSpecifier(nodeText(node.ChildByFieldName("source"), src))
	if source != "" {
		source, _ = pr.internalSpecifier(source)
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
			if exported == "" {
				continue
			}
			if source != "" {
				// `export {Orig as Exported} from "./src"`: forward to the source
				// module's symbol, preserving the name translation.
				pr.ReExportNamed = append(pr.ReExportNamed, namedReExport{Exported: exported, Original: name, Source: source})
			} else {
				pr.Exports[exported] = name
			}
		}
		return
	}
	// Clause-less re-exports. `export * from "./src"` flattens the source
	// module's namespace, so it is a whole-module re-export. `export * as ns
	// from "./src"` instead binds a single namespace object; its members are not
	// flattened, so it must NOT become a whole-module re-export edge.
	if source == "" {
		return
	}
	nsExport := childByType(node, ntNamespaceExport)
	if nsExport == nil {
		pr.ReExportAll = append(pr.ReExportAll, source)
		return
	}
	// `export * as ns from "./src"`: one binding standing for a whole module. Recorded as a
	// named re-export whose original name is the wholeModuleReExport marker, because that is
	// what an importer of `ns` needs to follow -- `ns.f()` is a name in ./src, not a name
	// here. Until this was recorded at all, such a barrel simply lost every symbol behind it.
	if alias := nsExport.NamedChild(0); alias != nil {
		if name := nodeText(alias, src); name != "" {
			pr.ReExportNamed = append(pr.ReExportNamed,
				namedReExport{Exported: name, Original: wholeModuleReExport, Source: source})
		}
	}
}

// wholeModuleReExport is the Original a namespace re-export (`export * as ns from "./src"`)
// records: the binding stands for the module itself rather than for one of its symbols.
const wholeModuleReExport = "*"

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
			if right != nil {
				switch right.Type() {
				case ntArrowFunction, ntFunctionExpression, ntGeneratorFunction:
					pr.Functions = append(pr.Functions, parseFunctionValue(prop, right, src, pr, true))
					pr.Exports[prop] = prop
					return
				case ntClass:
					parseClass(right, src, pr, true, prop, "")
					clsName := nodeText(right.ChildByFieldName("name"), src)
					if clsName == "" {
						clsName = prop
					}
					pr.Exports[prop] = clsName
					return
				}
			}
			pr.Exports[prop] = localExportName(right, src, prop)
			return
		}
	}
	if obj == "module" && prop == "exports" && right != nil {
		switch right.Type() {
		case ntIdentifier:
			name := nodeText(right, src)
			// `module.exports = <whole-module require binding>` re-exports the
			// required module's named exports through this module, rather than
			// binding a single default. Record the re-export source so consumers
			// resolve names through to the required module.
			if info, ok := pr.ImportMap[name]; ok && info.Namespace && info.Internal {
				pr.ReExportAll = append(pr.ReExportAll, info.Source)
				return
			}
			pr.Exports["default"] = name
		case ntClass:
			// `module.exports = class Widget {...}`: the class IS the module's
			// value, i.e. its default export. An anonymous class is named
			// "default", like an anonymous `export default class {}`.
			parseClass(right, src, pr, true, "default", "")
			clsName := nodeText(right.ChildByFieldName("name"), src)
			if clsName == "" {
				clsName = "default"
			}
			pr.Exports["default"] = clsName
		case ntFunctionExpression, ntGeneratorFunction, ntArrowFunction:
			// `module.exports = function build(){}` / `= () => ...`: likewise the
			// default export, named after the function or "default" when anonymous.
			name := nodeText(right.ChildByFieldName("name"), src)
			if name == "" {
				name = "default"
			}
			pr.Functions = append(pr.Functions, parseFunctionValue(name, right, src, pr, true))
			pr.Exports["default"] = name
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
func parseFunctionDecl(node *sitter.Node, src []byte, pr *ParseResult, exported bool, defaultName string) FunctionParse {
	name := nodeText(node.ChildByFieldName("name"), src)
	if name == "" {
		name = defaultName
	}
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
	raw.BodyCalls, raw.BodyAssigns = collectBody(node.ChildByFieldName("parameters"), body, src, pr)

	fn := js.JavaScriptFunction{
		ID:          pr.ModulePath + "." + name,
		Name:        name,
		Input:       signatureParams(raw.Params),
		Output:      raw.Output,
		Loc:         domain.Location{Path: pr.FileID, StartsAt: startLine(node), EndsAt: endLine(node)},
		Connections: make(map[js.ConnectionKind][]string),
		IsAsync:     isAsync,
		IsGenerator: isGen,
		Kind:        "function",
		Exported:    exported,
	}
	return FunctionParse{Function: fn, Body: raw, Overload: node.Type() == ntFunctionSignature}
}

// Parses a class declaration node, extracting class metadata, methods, and base classes, then appends to ParseResult.
func parseClass(node *sitter.Node, src []byte, pr *ParseResult, exported bool, defaultName, forceName string) {
	name := forceName
	if name == "" {
		name = nodeText(node.ChildByFieldName("name"), src)
	}
	if name == "" {
		name = defaultName
	}
	if name == "" {
		return
	}
	classID := pr.ModulePath + "." + name
	bases, impls := extractHeritage(node, src)
	if q := heritageQualifiers(node, src); len(q) > 0 {
		if pr.heritageQualifiers == nil {
			pr.heritageQualifiers = make(map[string]map[string]string)
		}
		pr.heritageQualifiers[classID] = q
	}

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
	// The TypeScript grammar puts a method's decorators BEFORE it, as siblings in the class
	// body (JavaScript's nests them inside the method); pending collects them for the member
	// they precede.
	var pending []*sitter.Node
	for i := 0; i < int(body.NamedChildCount()); i++ {
		m := body.NamedChild(i)
		var fp FunctionParse
		switch m.Type() {
		case ntDecorator:
			pending = append(pending, m)
			continue
		case ntMethodDefinition:
			fp = parseMethod(m, src, pr, classID, false)
		case ntAbstractMethodSig:
			fp = parseMethod(m, src, pr, classID, true)
		case ntFieldDefinition, ntPublicFieldDefinition:
			var ok bool
			if fp, ok = parseFieldFunction(m, src, pr, classID); !ok {
				pending = nil
				continue
			}
		default:
			pending = nil
			continue
		}
		if len(pending) > 0 {
			var decs []string
			for _, d := range pending {
				if t := decoratorName(d, src); t != "" {
					decs = append(decs, t)
				}
			}
			fp.Function.Decorators = append(decs, fp.Function.Decorators...)
			fp.Function.Loc.StartsAt = startLine(pending[0])
			pending = nil
		}
		pr.Functions = append(pr.Functions, fp)
	}
}

// parseFieldFunction extracts a class field whose value is a function (`handle = (e) => {…}`,
// `static make = function () {…}`) as a method of the class: it is called like one, and it
// satisfies an interface method like one. A field holding anything else is not a resource.
// The span is the whole field, so a read shows the name it is called by.
func parseFieldFunction(field *sitter.Node, src []byte, pr *ParseResult, classID string) (FunctionParse, bool) {
	value := field.ChildByFieldName("value")
	if value == nil {
		return FunctionParse{}, false
	}
	switch value.Type() {
	case ntArrowFunction, ntFunctionExpression, ntGeneratorFunction:
	default:
		return FunctionParse{}, false
	}
	nameNode := field.ChildByFieldName("name") // TypeScript
	if nameNode == nil {
		nameNode = field.ChildByFieldName("property") // JavaScript
	}
	name := memberName(nameNode, src)
	if name == "" {
		return FunctionParse{}, false
	}
	fp := parseFunctionValue(name, value, src, pr, false)
	cid := js.ClassID(classID)
	fp.Function.ID = classID + "." + name
	fp.Function.MethodFrom = &cid
	fp.Function.IsStatic = hasChildToken(field, "static")
	fp.Function.Decorators = extractDecorators(field, src)
	if am := childByType(field, ntAccessibilityModifier); am != nil {
		fp.Function.Accessibility = nodeText(am, src)
	}
	fp.Function.Loc.StartsAt = startLine(field)
	fp.Function.Loc.EndsAt = endLine(field)
	return fp, true
}

// Parses a class method definition, extracting name, parameters, return type, modifiers (async, static, getter/setter), and body calls/assignments.
func parseMethod(node *sitter.Node, src []byte, pr *ParseResult, classID string, abstract bool) FunctionParse {
	name := memberName(node.ChildByFieldName("name"), src)
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
	raw.BodyCalls, raw.BodyAssigns = collectBody(node.ChildByFieldName("parameters"), body, src, pr)

	cid := js.ClassID(classID)
	fn := js.JavaScriptFunction{
		ID:            classID + "." + name,
		Name:          name,
		Input:         signatureParams(raw.Params),
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
// typeParamNames reads the declared type-parameter names of a generic declaration
// (`interface Repo<T, K>` -> ["T", "K"]). Conformance needs them to tell a position written
// against the interface's own parameter from one written against a concrete type.
func typeParamNames(n *sitter.Node, src []byte) []string {
	tp := n.ChildByFieldName("type_parameters")
	if tp == nil {
		tp = childByType(n, "type_parameters")
	}
	if tp == nil {
		return nil
	}
	var out []string
	for i := 0; i < int(tp.NamedChildCount()); i++ {
		c := tp.NamedChild(i)
		if c.Type() != "type_parameter" {
			continue
		}
		name := c.ChildByFieldName("name")
		if name == nil {
			name = childByType(c, ntTypeIdentifier)
		}
		if text := nodeText(name, src); text != "" {
			out = append(out, text)
		}
	}
	return out
}

func parseInterface(node *sitter.Node, src []byte, pr *ParseResult) {
	name := nodeText(node.ChildByFieldName("name"), src)
	if name == "" {
		return
	}
	iface := js.JavaScriptInterface{
		ID:          pr.ModulePath + "." + name,
		Name:        name,
		Generics:    typeParamNames(node, src),
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
					Name:     nodeText(m.ChildByFieldName("name"), src),
					Input:    signatureParams(extractParams(m, src)),
					Output:   returnTypeDefs(m, src),
					Optional: hasChildToken(m, "?"),
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
	if pr.namespaces == nil {
		pr.namespaces = make(map[string]bool)
	}
	// `namespace A.B {}` opens A as well as A.B.
	for scope := pr.ModulePath; len(scope) > len(saved); scope = scope[:strings.LastIndex(scope, ".")] {
		pr.namespaces[scope] = true
	}
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

		// Dynamic import bound to a variable:
		//   const mod = await import("./shapes.js")
		//   const mod = import("./shapes.js")
		// Treat it like a namespace require so the module dependency is
		// registered and member access (`mod.Circle`) resolves.
		if dyn := dynamicImportCall(value); dyn != nil {
			if source := trimSpecifier(requireSource(dyn, src)); source != "" {
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
			case ntClass:
				// A class expression assigned to a binding (`const X = class {...}`)
				// is extracted as a class named after the binding, just like class
				// declarations and function expressions. Any inner class name is
				// shadow-scoped, so the binding name wins (and matches the export).
				parseClass(value, src, pr, exported, "", name)
				continue
			case ntObject:
				// Object-literal members (method shorthand, getter/setter, and
				// arrow/function-expression properties) become callable function
				// resources named `<var>.<member>`, so member calls
				// (`geometryOps.makeCircle()`) resolve to them. The binding itself
				// is still registered as a variable below (no `continue`).
				parseObjectLiteralMethods(name, value, src, pr, exported)
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
	source, internal := pr.internalSpecifier(source)
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
	raw.BodyCalls, raw.BodyAssigns = collectBody(value.ChildByFieldName("parameters"), body, src, pr)

	kind := "function"
	if value.Type() == ntArrowFunction {
		kind = "arrow"
	}
	fn := js.JavaScriptFunction{
		ID:          pr.ModulePath + "." + name,
		Name:        name,
		Input:       signatureParams(raw.Params),
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

// parseObjectLiteralMethods extracts callable resources from an object-literal
// bound to a variable (`const ops = { make(){...}, scale: (x)=>..., get y(){} }`).
// Method shorthand, getters/setters, and arrow/function-expression properties
// each become a top-level function resource named `<var>.<member>`, so member
// calls (`ops.make()`) resolve to them. They carry no MethodFrom: the owning
// object is a variable, not a class.
func parseObjectLiteralMethods(objVar string, obj *sitter.Node, src []byte, pr *ParseResult, exported bool) {
	for i := 0; i < int(obj.NamedChildCount()); i++ {
		m := obj.NamedChild(i)
		var nameNode, fn *sitter.Node
		switch m.Type() {
		case ntMethodDefinition:
			nameNode, fn = m.ChildByFieldName("name"), m
		case ntPair:
			val := m.ChildByFieldName("value")
			if val == nil {
				continue
			}
			switch val.Type() {
			case ntArrowFunction, ntFunctionExpression, ntGeneratorFunction:
				nameNode, fn = m.ChildByFieldName("key"), val
			default:
				continue
			}
		default:
			continue
		}
		name := memberName(nameNode, src)
		if name == "" {
			continue
		}
		pr.Functions = append(pr.Functions, parseObjectMember(objVar, name, fn, src, pr, exported))
	}
}

// parseObjectMember builds the function resource for a single object-literal
// member. `fn` is the member's function node (the method_definition itself for
// shorthand/accessors, or the arrow/function-expression value for a property).
func parseObjectMember(objVar, name string, fn *sitter.Node, src []byte, pr *ParseResult, exported bool) FunctionParse {
	isAsync := hasChildToken(fn, "async")
	isGen := fn.Type() == ntGeneratorFunction || hasChildToken(fn, "*")
	body := fn.ChildByFieldName("body")

	raw := &jsFunc{
		Name:        name,
		Params:      extractParams(fn, src),
		Output:      returnTypeDefs(fn, src),
		IsAsync:     isAsync,
		IsGenerator: isGen,
	}
	raw.BodyCalls, raw.BodyAssigns = collectBody(fn.ChildByFieldName("parameters"), body, src, pr)

	kind := "method"
	switch fn.Type() {
	case ntArrowFunction:
		kind = "arrow"
	case ntFunctionExpression:
		kind = "function"
	default: // method_definition
		if hasChildToken(fn, "get") {
			kind = "getter"
		} else if hasChildToken(fn, "set") {
			kind = "setter"
		}
	}

	jf := js.JavaScriptFunction{
		ID:          pr.ModulePath + "." + objVar + "." + name,
		Name:        name,
		Input:       signatureParams(raw.Params),
		Output:      raw.Output,
		Loc:         domain.Location{Path: pr.FileID, StartsAt: startLine(fn), EndsAt: endLine(fn)},
		Connections: make(map[js.ConnectionKind][]string),
		IsAsync:     isAsync,
		IsGenerator: isGen,
		Kind:        kind,
		Exported:    exported,
	}
	return FunctionParse{Function: jf, Body: raw}
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

// signatureParams is a parameter list as a caller sees it. TypeScript's `this` parameter
// (`m(this: C, x: number)`) only types the receiver and is never passed, so counting it made
// every correct call look one argument short. The body analysis keeps the full list: the
// annotation still types `this` inside the body.
func signatureParams(params []js.VariableDefinition) []js.VariableDefinition {
	if len(params) > 0 && params[0].Name == "this" {
		return params[1:]
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
			def.Typing = declaredTypeName(ta, src)
			def.Annotation = annotationText(ta, src)
		}
		// "b?: string" and "b: string = x" may both be omitted at the call site. The
		// grammar gives the first its own node type; the second keeps a value child.
		def.Optional = c.Type() == ntOptionalParameter || c.ChildByFieldName("value") != nil
		if pat := c.ChildByFieldName("pattern"); pat != nil && pat.Type() == ntRestPattern {
			def.Variadic = true
		}
		return def
	case ntAssignmentPattern:
		if left := c.ChildByFieldName("left"); left != nil {
			return js.VariableDefinition{Name: nodeText(left, src), Optional: true}
		}
	case ntRestPattern:
		if id := childByType(c, ntIdentifier); id != nil {
			return js.VariableDefinition{Name: "..." + nodeText(id, src), Variadic: true}
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
	if t := declaredTypeName(rt, src); t != "" {
		return []js.VariableDefinition{{Typing: t, Annotation: annotationText(rt, src)}}
	}
	return nil
}

// annotationText is the declared type as written -- "Item[]", "Map<string, Item[]>",
// "Item | null" -- normalised for whitespace, alongside the base name declaredTypeName
// reduces it to.
//
// The reduction is not a shortcoming of declaredTypeName: Typing exists to resolve to a
// topology resource, and `Item[]` resolves to Item. But a signature is compared for
// equality, and on that text `items: Item` and `items: Item[]` are the same declaration --
// so the edit between them, which breaks every caller, used to pass as no change at all.
//
// Capped like a type alias's underlying text: an inline object type can run to hundreds of
// characters, and the field is only ever compared, never re-parsed.
func annotationText(ta *sitter.Node, src []byte) string {
	if ta == nil {
		return ""
	}
	n := ta
	if ta.NamedChildCount() > 0 {
		n = ta.NamedChild(0) // drop the ":" the type_annotation node carries
	}
	t := normalizeTypeText(nodeText(n, src))
	if len(t) > 200 {
		t = t[:200]
	}
	return t
}

// normalizeTypeText drops the whitespace a type annotation's spelling is free to vary in,
// keeping only the space that separates two words (`readonly string[]`). Without this,
// reformatting `Map<string, Item>` to `Map<string,Item>` would read as a signature change
// and warn every caller about an edit that changed nothing.
func normalizeTypeText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if !isTypeSpace(s[i]) {
			b.WriteByte(s[i])
			continue
		}
		for i+1 < len(s) && isTypeSpace(s[i+1]) {
			i++
		}
		var prev, next byte
		if b.Len() > 0 {
			prev = b.String()[b.Len()-1]
		}
		if i+1 < len(s) {
			next = s[i+1]
		}
		if isWordByte(prev) && isWordByte(next) {
			b.WriteByte(' ')
		}
	}
	return b.String()
}

func isTypeSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

func isWordByte(c byte) bool {
	return c == '_' || c == '$' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// declaredTypeName is typeAnnotationName for a parameter or return type, falling back to a
// predefined type's own text -- number, string, boolean, void. typeAnnotationName yields only
// names that can resolve to a resource, which is right for following a value's type and wrong
// for a signature: without the fallback `x: number` and `x: string` are the same declaration,
// so neither the signature-change check nor the call-site check can tell them apart.
func declaredTypeName(ta *sitter.Node, src []byte) string {
	if t := typeAnnotationName(ta, src); t != "" {
		return t
	}
	if ta.NamedChildCount() > 0 {
		if n := ta.NamedChild(0); n.Type() == ntPredefinedType {
			return nodeText(n, src)
		}
	}
	return ""
}

// typeAnnotationName returns the resolvable user type name inside a type_annotation, or "".
//
// The FIRST named child is the whole answer -- a type_annotation wraps exactly one type. This
// was written as a `for` loop that returns on its first iteration, which reads as "search the
// children" and is not: it ran once whatever the count. Spelled as the bounds check it always
// was, so the next reader does not have to work that out (and staticcheck stops reporting a
// loop whose condition never changes).
func typeAnnotationName(ta *sitter.Node, src []byte) string {
	if ta.NamedChildCount() == 0 {
		return ""
	}
	return typeRefName(ta.NamedChild(0), src)
}

// typeAnnotationArg returns the first generic type argument of a type annotation,
// e.g. "Circle" for `: Box<Circle>`, or "" when the annotation is non-generic.
func typeAnnotationArg(ta *sitter.Node, src []byte) string {
	if ta.NamedChildCount() == 0 {
		return ""
	}
	n := ta.NamedChild(0)
	if n.Type() != ntGenericType {
		return ""
	}
	args := n.ChildByFieldName("type_arguments")
	if args == nil {
		args = childByType(n, ntTypeArguments)
	}
	if args == nil || args.NamedChildCount() == 0 {
		return ""
	}
	return typeRefName(args.NamedChild(0), src)
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
		// The element type is the first named child; the loop only ever ran once.
		if n.NamedChildCount() > 0 {
			return typeRefName(n.NamedChild(0), src)
		}
	case ntUnionType:
		// Union (`A | B`): resolve against the first member that yields a
		// usable type name (conservative — a method call on a union-typed
		// value resolves to its first member).
		for i := 0; i < int(n.NamedChildCount()); i++ {
			if name := typeRefName(n.NamedChild(i), src); name != "" {
				return name
			}
		}
	case ntNestedTypeIdentifier:
		// Namespace-qualified type (`Geo.Point`): keep the full dotted name so it
		// resolves to the namespace-scoped resource (ID `<module>.Geo.Point`).
		if mod := n.ChildByFieldName("module"); mod != nil {
			if name := n.ChildByFieldName("name"); name != nil {
				return nodeText(mod, src) + "." + nodeText(name, src)
			}
		}
		return nodeText(n, src)
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

// heritageQualifiers maps each base written as a member expression (`extends React.Component`)
// to its qualifier (`React`), for the ones extractHeritage records under the member name.
func heritageQualifiers(classNode *sitter.Node, src []byte) map[string]string {
	var out map[string]string
	var visit func(n *sitter.Node)
	visit = func(n *sitter.Node) {
		for i := 0; i < int(n.NamedChildCount()); i++ {
			c := n.NamedChild(i)
			switch c.Type() {
			case ntExtendsClause, ntImplementsClause:
				visit(c)
			case ntMemberExpression:
				obj, prop := c.ChildByFieldName("object"), c.ChildByFieldName("property")
				if obj != nil && prop != nil && obj.Type() == ntIdentifier {
					if out == nil {
						out = make(map[string]string)
					}
					out[nodeText(prop, src)] = nodeText(obj, src)
				}
			}
		}
	}
	if h := childByType(classNode, ntClassHeritage); h != nil {
		visit(h)
	}
	return out
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
		// Mixin / HOF base (e.g. Tagged(Timestamped(Circle))): the real base
		// class is the innermost class identifier argument, not the called
		// mixin function. Best-effort: resolve the first argument that yields a
		// name, recursing through nested mixin calls.
		if args := n.ChildByFieldName("arguments"); args != nil {
			for i := 0; i < int(args.NamedChildCount()); i++ {
				if name := baseNameFromExpr(args.NamedChild(i), src); name != "" {
					return name
				}
			}
		}
		// Fall back to the called function name (factory-style base).
		if f := n.ChildByFieldName("function"); f != nil {
			return baseNameFromExpr(f, src)
		}
	}
	return ""
}

// unwrapReceiver strips TypeScript receiver wrappers that do not change which
// value a method is called on: parentheses (`( … )`) and non-null assertions
// (`x!`). It deliberately leaves an `as`/`<T>` cast in place so callers can read
// the cast's target type (see castTypeNode). `(c as Circle)!.m()` therefore
// unwraps to the inner `as_expression`.
func unwrapReceiver(n *sitter.Node) *sitter.Node {
	for n != nil {
		switch n.Type() {
		case ntParenthesizedExpr, ntNonNullExpression:
			inner := n.NamedChild(0)
			if inner == nil {
				return n
			}
			n = inner
		default:
			return n
		}
	}
	return n
}

// castTypeNode returns the target-type node of a TypeScript cast receiver
// (`x as T` -> the `T` node), or nil when n is not a cast. The cast type is the
// last named child of an `as_expression`.
func castTypeNode(n *sitter.Node) *sitter.Node {
	if n == nil || n.Type() != ntAsExpression {
		return nil
	}
	cnt := int(n.NamedChildCount())
	if cnt == 0 {
		return nil
	}
	return n.NamedChild(cnt - 1)
}

// Extracts decorator names from a class or function node, parsing decorator syntax and removing parentheses if present.
func extractDecorators(node *sitter.Node, src []byte) []string {
	var decs []string
	for i := 0; i < int(node.ChildCount()); i++ {
		c := node.Child(i)
		if c.Type() != ntDecorator {
			continue
		}
		if t := decoratorName(c, src); t != "" {
			decs = append(decs, t)
		}
	}
	return decs
}

// decoratorName returns a decorator's name without the `@` and any call arguments.
func decoratorName(dec *sitter.Node, src []byte) string {
	t := strings.TrimPrefix(strings.TrimSpace(nodeText(dec, src)), "@")
	if idx := strings.IndexAny(t, "("); idx >= 0 {
		t = t[:idx]
	}
	return t
}

// jsCallArgs reads the argument count and one token per argument of a call.
//
// Literals only. Anything else -- an identifier, a nested call, an operator expression --
// is left unknown rather than guessed, because a wrong token is a false warning about
// correct code. A spread argument hides the real count, so the count is reported unknown.
func jsCallArgs(call *sitter.Node) (int, []string) {
	args := call.ChildByFieldName("arguments")
	if args == nil {
		return -1, nil
	}
	n := int(args.NamedChildCount())
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		child := args.NamedChild(i)
		if child.Type() == "spread_element" {
			return -1, nil
		}
		out = append(out, contract.LiteralToken(child.Type()))
	}
	return n, out
}

// Recursively extracts function calls and variable assignments from a function body AST node.
func collectBody(paramsNode, bodyNode *sitter.Node, src []byte, pr *ParseResult) ([]jsBodyCall, []jsBodyAssign) {
	var calls []jsBodyCall
	var assigns []jsBodyAssign

	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		// Whatever the switch below appends for a call belongs to THIS call, so its
		// argument shape is stamped on right after it, rather than repeated at each of the
		// several append sites. Stamping happens BEFORE the recursion into children: a
		// nested call is a separate walk that records its own arguments, and deferring
		// would hand it this call's.
		before := len(calls)

		switch n.Type() {
		case ntCallExpression:
			if fn := n.ChildByFieldName("function"); fn != nil {
				if fn.Type() == ntDynamicImport {
					// Dynamic `import("./mod.js")` inside a body: register the
					// module dependency and, when bound to a variable
					// (`const m = await import(...)`), record the binding as a
					// namespace import so member access (`m.Class`) resolves.
					registerDynamicImport(n, src, pr)
				}
				switch fn.Type() {
				case ntIdentifier:
					calls = append(calls, jsBodyCall{Func: nodeText(fn, src), LineNo: startLine(n)})
				case ntSuper:
					// `super(...)` constructor call: resolved against the
					// enclosing class's base-class constructor.
					calls = append(calls, jsBodyCall{
						ObjectName: "super",
						MethodName: "constructor",
						Func:       nodeText(fn, src),
						LineNo:     startLine(n),
					})
				case ntMemberExpression:
					obj := fn.ChildByFieldName("object")
					prop := fn.ChildByFieldName("property")
					if obj != nil && prop != nil {
						// Unwrap TS receiver wrappers so the underlying
						// expression drives resolution: parentheses `( … )` and
						// non-null assertions `x!`. An `as`/`<T>` cast is left
						// in place so its target type can be captured below.
						recv := unwrapReceiver(obj)
						bc := jsBodyCall{
							ObjectName: nodeText(recv, src),
							MethodName: nodeText(prop, src),
							Func:       nodeText(fn, src),
							LineNo:     startLine(n),
						}
						// Method called on a TS cast (`(x as Circle).bar()` or
						// `(<Circle>x).bar()`): the cast's target type drives
						// resolution regardless of the operand's static type.
						if t := castTypeNode(recv); t != nil {
							bc.ObjectCastClass = typeRefName(t, src)
						}
						// Method chained directly on a `new` expression
						// (`new Foo().bar()`): record the constructed class so
						// the call resolves like the intermediate-variable form.
						if recv.Type() == ntNewExpression {
							if c := recv.ChildByFieldName("constructor"); c != nil {
								bc.ObjectNewClass = baseNameFromExpr(c, src)
							}
						}
						// Method chained directly on a call expression
						// (`getCircle().bar()`): record the called function so
						// the call resolves via the callee's return type.
						if recv.Type() == ntCallExpression {
							if f := recv.ChildByFieldName("function"); f != nil {
								switch f.Type() {
								case ntIdentifier:
									bc.ObjectCallFunc = nodeText(f, src)
								case ntMemberExpression:
									// Method chained on a method call
									// (`b.get().area()`): record the inner
									// receiver + method so the chain resolves
									// via the inner method's return type
									// (incl. a propagated generic type argument).
									io := f.ChildByFieldName("object")
									ip := f.ChildByFieldName("property")
									if io != nil && io.Type() == ntIdentifier && ip != nil {
										bc.ObjectMethodObject = nodeText(io, src)
										bc.ObjectMethodName = nodeText(ip, src)
									}
								}
							}
						}
						calls = append(calls, bc)
					}
				}
			}
		case ntNewExpression:
			if c := n.ChildByFieldName("constructor"); c != nil {
				bc := jsBodyCall{Func: baseNameFromExpr(c, src), IsNew: true, LineNo: startLine(n)}
				// `new ns.Class()`: record the namespace qualifier so the
				// class resolves through a namespace import (incl. dynamic
				// `await import(...)`).
				if c.Type() == ntMemberExpression {
					if obj := c.ChildByFieldName("object"); obj != nil && obj.Type() == ntIdentifier {
						bc.ObjectName = nodeText(obj, src)
					}
				}
				calls = append(calls, bc)
			}
		case ntVariableDeclarator:
			if name := n.ChildByFieldName("name"); name != nil && name.Type() == ntIdentifier {
				a := assignFrom(nodeText(name, src), n.ChildByFieldName("value"), src)
				if ta := n.ChildByFieldName("type"); ta != nil {
					a.DeclaredType = typeAnnotationName(ta, src)
					a.DeclaredTypeArg = typeAnnotationArg(ta, src)
				}
				assigns = append(assigns, a)
			}
		case ntAssignmentExpression:
			if left := n.ChildByFieldName("left"); left != nil && left.Type() == ntIdentifier {
				assigns = append(assigns, assignFrom(nodeText(left, src), n.ChildByFieldName("right"), src))
			}
		}
		if t := n.Type(); t == ntCallExpression || t == ntNewExpression {
			argc, args := jsCallArgs(n)
			for i := before; i < len(calls); i++ {
				calls[i].ArgC = argc
				calls[i].Args = args
			}
		}

		for i := 0; i < int(n.NamedChildCount()); i++ {
			walk(n.NamedChild(i))
		}
	}
	// Default-parameter initializers are part of the function body: a call in a
	// default value (e.g. `f(c = makeCircle(1))`) should produce a call edge
	// from the enclosing function. walk handles nil nodes safely.
	walk(paramsNode)
	walk(bodyNode)
	return calls, assigns
}

// registerDynamicImport handles a dynamic `import("./mod.js")` call expression
// found inside a function body. It registers the imported module as a file
// dependency (so the imports_module edge is created) and, when the import
// result is bound to a variable (`const m = await import(...)` or
// `const m = import(...)`), records the binding as a namespace import so member
// access (`m.Class`) resolves like a static `import * as m`.
func registerDynamicImport(call *sitter.Node, src []byte, pr *ParseResult) {
	source := trimSpecifier(requireSource(call, src))
	if source == "" {
		return
	}
	p := call.Parent()
	if p != nil && p.Type() == ntAwaitExpression {
		p = p.Parent()
	}
	var name *sitter.Node
	if p != nil && p.Type() == ntVariableDeclarator {
		name = p.ChildByFieldName("name")
	}
	parseRequireBinding(name, source, src, pr)
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

// dynamicImportCall unwraps a value node to the underlying dynamic `import("...")`
// call expression, peeling an enclosing `await`. Returns nil when the value is
// not a dynamic import.
func dynamicImportCall(value *sitter.Node) *sitter.Node {
	if value == nil {
		return nil
	}
	if value.Type() == ntAwaitExpression {
		value = value.NamedChild(0)
	}
	if value != nil && value.Type() == ntCallExpression {
		if f := value.ChildByFieldName("function"); f != nil && f.Type() == ntDynamicImport {
			return value
		}
	}
	return nil
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

// jsModulePath returns the file-scoped ID namespace for a source file: the file's path
// relative to the project root, extension stripped, separators "/".
//
// The project-root BASE NAME used to be prefixed here. It was removed in id-scheme 2 — the
// directory a checkout happens to live in appears nowhere in the source, so an ID built
// from it is unguessable. What remains is the repo-relative path, which is what an import
// specifier already looks like. See pyModulePath for the full rationale.
func jsModulePath(root, file string) string {
	rel, err := filepath.Rel(root, file)
	if err != nil {
		rel = filepath.Base(file)
	}
	rel = strings.ReplaceAll(rel, "\\", "/")
	rel = strings.TrimSuffix(rel, filepath.Ext(rel))
	if rel == "." || rel == "" {
		return ""
	}
	return rel
}

// moduleExtOrder ranks the ECMAScript extensions the way an extension-less import picks
// between files sharing a stem (resolveSpecifier's order; TypeScript's own resolution
// prefers .ts over the .js compiled next to it).
var moduleExtOrder = []string{".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs"}

// jsModulePathFor is the ID namespace a file's declarations are minted under: jsModulePath,
// unless another source file in the same directory shares the stem and ranks before this
// one in moduleExtOrder -- `util.js` beside `util.cjs`, a compiled `util.js` beside
// `util.ts`. Stripping the extension gave both the same namespace, so they minted the same
// IDs and one silently replaced the other. The lower-ranked file keeps its extension
// (`util.cjs.fmt`); the file an extension-less import reaches keeps the plain path, and a
// file with no such sibling is unaffected.
//
// Decided from the files on disk, so a full scan and an incremental update agree. A
// TypeScript sibling counts for a JavaScript file although another scanner owns it: both
// languages' IDs share one graph.
func jsModulePathFor(root, file string) string {
	base := jsModulePath(root, file)
	ext := filepath.Ext(file)
	stem := strings.TrimSuffix(file, ext)
	for i, e := range moduleExtOrder {
		if e == ext {
			break
		}
		sibling := stem + e
		lang := "typescript"
		if i >= 4 { // past .cts
			lang = "javascript"
		}
		if info, err := os.Stat(sibling); err == nil && info.Mode().IsRegular() && helper.IsSourceFile(root, sibling, lang) {
			return base + ext
		}
	}
	return base
}
