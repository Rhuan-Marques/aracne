package rustscanner

import (
	"context"
	"fmt"
	"os"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	rustgrammar "github.com/smacker/go-tree-sitter/rust"

	"github.com/Rhuan-Marques/aracne/internal/topology/contract"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	rust "github.com/Rhuan-Marques/aracne/internal/topology/rust"
)

// tree-sitter-rust node type names (verified against the bundled grammar via an
// AST probe). Where the spec and the real grammar diverged, the grammar wins
// (see the package doc note in scanner.go).
const (
	ntFunctionItem          = "function_item"
	ntFunctionSignatureItem = "function_signature_item"
	ntStructItem            = "struct_item"
	ntEnumItem              = "enum_item"
	ntUnionItem             = "union_item"
	ntTraitItem             = "trait_item"
	ntImplItem              = "impl_item"
	ntModItem               = "mod_item"
	ntConstItem             = "const_item"
	ntStaticItem            = "static_item"
	ntTypeItem              = "type_item"
	ntMacroDefinition       = "macro_definition"
	ntUseDeclaration        = "use_declaration"
	ntAttributeItem         = "attribute_item"
	ntInnerAttributeItem    = "inner_attribute_item"
	ntAttribute             = "attribute"
	ntVisibilityModifier    = "visibility_modifier"
	ntFunctionModifiers     = "function_modifiers"
	ntParameters            = "parameters"
	ntParameter             = "parameter"
	ntSelfParameter         = "self_parameter"
	ntMutableSpecifier      = "mutable_specifier"
	ntFieldDeclarationList  = "field_declaration_list"
	ntOrderedFieldDeclList  = "ordered_field_declaration_list"
	ntFieldDeclaration      = "field_declaration"
	ntFieldIdentifier       = "field_identifier"
	ntEnumVariantList       = "enum_variant_list"
	ntEnumVariant           = "enum_variant"
	ntDeclarationList       = "declaration_list"
	ntTraitBounds           = "trait_bounds"
	ntAssociatedType        = "associated_type"
	ntTypeParameters        = "type_parameters"
	ntTypeIdentifier        = "type_identifier"
	ntScopedTypeIdentifier  = "scoped_type_identifier"
	ntScopedIdentifier      = "scoped_identifier"
	ntScopedUseList         = "scoped_use_list"
	ntUseList               = "use_list"
	ntUseAsClause           = "use_as_clause"
	ntUseWildcard           = "use_wildcard"
	ntIdentifier            = "identifier"
	ntCrate                 = "crate"
	ntSelf                  = "self"
	ntSuper                 = "super"
	ntCallExpression        = "call_expression"
	ntFieldExpression       = "field_expression"
	ntLetDeclaration        = "let_declaration"
	ntStructExpression      = "struct_expression"
	ntTokenTree             = "token_tree"
	ntReferenceType         = "reference_type"
	ntGenericType           = "generic_type"
	ntConstrainedTypeParam  = "constrained_type_parameter"
	ntGenericFunction       = "generic_function"
	ntLineComment           = "line_comment"
	ntBlockComment          = "block_comment"
)

// rustBody holds the call/let/struct-literal/reference records extracted from a
// function or method body, resolved later in analyzeFunctionBody.
type rustBody struct {
	Calls   []rustCall
	Lets    []rustLet
	Structs []string
	Refs    []rustRef
}

// rustCall records one call expression form found in a body.
type rustCall struct {
	Func     string   // direct call: `f()`
	PathType string   // path call `Type::name()`: the type segment
	PathSegs []string // path call: the whole path before the name (`crate::factory` in `crate::factory::f()`)
	PathName string   // path call: the trailing name
	ObjName  string   // method call `obj.method()`: the receiver text
	Method   string   // method call: the method name
	IsSelf   bool     // receiver is `self`, or path head is `Self`
	// Args is one token per argument written at the call site, "" where the argument's
	// type could not be read. Recorded so a later scan can ask whether the call still fits
	// its callee rather than whether the callee merely changed.
	Args []string
}

// rustLet records a `let pat = value;` binding whose value lets us type the var.
type rustLet struct {
	Name      string
	CallType  string   // value `Type::name()` -> Type
	CallPath  []string // value `a::b::name()` -> [a, b], the whole path before the name
	CallName  string   // value `Type::name()` -> name
	CallFunc  string   // value `f()`
	StructLit string   // value `Type { .. }`
	AliasOf   string   // value is a bare identifier
	TypeAnn   string   // the annotation in `let c: Circle = ..`, which types the var outright
}

// rustRef records a value-position identifier/path reference (for const/static
// and external-dependency usage).
type rustRef struct {
	Segs []string
}

// FunctionParse pairs a parsed free function/macro resource with its body record.
// Module is the module path the function is declared in: the file's own, or an inline
// `mod x { .. }` inside it, which is the scope its body resolves names in.
type FunctionParse struct {
	Function rust.RustFunction
	Body     *rustBody
	Module   string
}

// implMethod is one method/associated function inside an impl block, with its
// final ID deferred until the impl's target type is resolved in the matcher.
type implMethod struct {
	Name         string
	Input        []rust.VariableDefinition
	Output       []rust.VariableDefinition
	IsAsync      bool
	IsUnsafe     bool
	IsConst      bool
	Receiver     string
	IsAssociated bool
	Visibility   string
	Exported     bool
	Loc          domain.Location
	Body         *rustBody
}

// implBlock is a raw `impl` block: the target type name, an optional implemented
// trait name, and its methods (all with deferred IDs).
type implBlock struct {
	TypeName  string
	TraitName string
	Methods   []implMethod
	Module    string // the enclosing module path (see FunctionParse.Module)
}

// useLeaf is one flattened `use` import leaf: the local binding name and the full
// "::"-segmented path it points at.
type useLeaf struct {
	LocalName string
	Path      []string
	IsGlob    bool
	Module    string // the module path the `use` is written in (see FunctionParse.Module)
}

// rustImport binds a local name to either an internal resource ID or an external
// crate dependency.
type rustImport struct {
	Internal bool
	ID       string
	Dep      string
}

// resolvedMethod carries a resolved impl method's final ID, its body, and its
// receiver type ID, so body analysis can run after impl attachment.
type resolvedMethod struct {
	ID     string
	Body   *rustBody
	Recv   string
	Module string // the impl's enclosing module path
}

// ParseResult is the per-file parse output. Use/impl resolution and body analysis
// are deferred to resolveTopology; ImportMap and MethodFns are filled there.
type ParseResult struct {
	FileID          string
	ModulePath      string
	FileDescription string
	Structs         []rust.RustStruct
	Traits          []rust.RustTrait
	NamedTypes      []rust.RustNamedType
	Variables       []rust.RustVariable
	Functions       []FunctionParse
	Impls           []implBlock
	Uses            []useLeaf

	ImportMap map[string]rustImport
	MethodFns []resolvedMethod
}

// parseState carries the source bytes and accumulating parse result through the
// recursive item walk.
type parseState struct {
	src []byte
	pr  *ParseResult
}

// nodeText returns a node's source text.
func nodeText(n *sitter.Node, src []byte) string {
	if n == nil {
		return ""
	}
	return n.Content(src)
}

// startLine returns the 1-indexed start line of a node.
func startLine(n *sitter.Node) int { return int(n.StartPoint().Row) + 1 }

// endLine returns the 1-indexed end line of a node.
func endLine(n *sitter.Node) int { return int(n.EndPoint().Row) + 1 }

// loc builds a domain.Location for a node within the given file.
func loc(file string, n *sitter.Node) domain.Location {
	return domain.Location{Path: file, StartsAt: startLine(n), EndsAt: endLine(n)}
}

// childByType returns the first named child of the given type, or nil.
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

// hasChildToken reports whether any (named or anonymous) child has the given type.
func hasChildToken(n *sitter.Node, token string) bool {
	if n == nil {
		return false
	}
	for i := 0; i < int(n.ChildCount()); i++ {
		if n.Child(i).Type() == token {
			return true
		}
	}
	return false
}

// ParseFile parses a single Rust source file into a ParseResult. modulePath is
// the file's "::"-qualified module path, computed from the crate model.
func ParseFile(filePath, modulePath string) (*ParseResult, error) {
	src, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	parser := sitter.NewParser()
	defer parser.Close()
	parser.SetLanguage(rustgrammar.GetLanguage())

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

	pr := &ParseResult{FileID: filePath, ModulePath: modulePath}
	st := &parseState{src: src, pr: pr}
	st.walkItems(root, modulePath)
	return pr, nil
}

// walkItems iterates a container's items (source_file or an inline mod body),
// tracking the preceding attributes for each item and the module-path prefix.
func (st *parseState) walkItems(container *sitter.Node, prefix string) {
	var pending []*sitter.Node
	for i := 0; i < int(container.NamedChildCount()); i++ {
		n := container.NamedChild(i)
		switch n.Type() {
		case ntAttributeItem:
			pending = append(pending, n)
			continue
		case ntInnerAttributeItem, ntLineComment, ntBlockComment:
			// A comment (doc comments included) between an attribute and its item is
			// not an item: the attributes still apply to what follows.
			continue
		}
		attrs := pending
		pending = nil
		// Skip `#[test]`/`#[cfg(test)]` items and their whole subtree.
		if isTestAttr(attrs, st.src) {
			continue
		}
		st.parseItem(n, prefix, attrs)
	}
}

// parseItem dispatches a single module-level item to its parser.
func (st *parseState) parseItem(n *sitter.Node, prefix string, attrs []*sitter.Node) {
	switch n.Type() {
	case ntStructItem:
		st.parseStruct(n, prefix, attrs, false)
	case ntUnionItem:
		st.parseStruct(n, prefix, attrs, true)
	case ntEnumItem:
		st.parseEnum(n, prefix, attrs)
	case ntTraitItem:
		st.parseTrait(n, prefix)
	case ntFunctionItem, ntFunctionSignatureItem:
		st.parseFreeFn(n, prefix)
	case ntImplItem:
		st.parseImpl(n, prefix)
	case ntModItem:
		if body := n.ChildByFieldName("body"); body != nil {
			name := nodeText(n.ChildByFieldName("name"), st.src)
			if name != "" {
				st.walkItems(body, prefix+"::"+name)
			}
		}
	case ntConstItem:
		st.parseVar(n, prefix, false)
	case ntStaticItem:
		st.parseVar(n, prefix, true)
	case ntTypeItem:
		st.parseTypeAlias(n, prefix)
	case ntMacroDefinition:
		st.parseMacro(n, prefix)
	case ntUseDeclaration:
		st.parseUse(n, prefix)
	}
}

// parseVisibility maps a visibility_modifier to (visibility, exported).
func parseVisibility(item *sitter.Node, src []byte) (string, bool) {
	vm := childByType(item, ntVisibilityModifier)
	if vm == nil {
		return "private", false
	}
	t := strings.TrimSpace(nodeText(vm, src))
	switch {
	case t == "pub":
		return "public", true
	case t == "pub(crate)":
		return "crate", true
	case t == "pub(super)":
		return "super", true
	case t == "pub(self)":
		return "private", false
	case strings.HasPrefix(t, "pub(in"):
		inner := strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(t, "pub(in")), ")")
		return "restricted:" + strings.TrimSpace(inner), true
	case strings.HasPrefix(t, "pub("):
		return "crate", true
	default:
		return "public", true
	}
}

// readModifiers reads a function_modifiers child for async/unsafe/const flags.
func readModifiers(fnNode *sitter.Node) (isAsync, isUnsafe, isConst bool) {
	mods := childByType(fnNode, ntFunctionModifiers)
	if mods == nil {
		// `const fn` may carry the const as a function_modifiers child too; if
		// not present, check direct tokens.
		isConst = hasChildToken(fnNode, "const")
		isAsync = hasChildToken(fnNode, "async")
		isUnsafe = hasChildToken(fnNode, "unsafe")
		return
	}
	isAsync = hasChildToken(mods, "async")
	isUnsafe = hasChildToken(mods, "unsafe")
	isConst = hasChildToken(mods, "const")
	return
}

// parseStruct parses a struct_item or union_item.
func (st *parseState) parseStruct(n *sitter.Node, prefix string, attrs []*sitter.Node, isUnion bool) {
	name := nodeText(n.ChildByFieldName("name"), st.src)
	if name == "" {
		return
	}
	vis, exported := parseVisibility(n, st.src)
	s := rust.RustStruct{
		ID:          prefix + "::" + name,
		Name:        name,
		Loc:         loc(st.pr.FileID, n),
		Connections: map[rust.ConnectionKind][]string{},
		Derives:     extractDerives(attrs, st.src),
		Generics:    extractGenerics(n, st.src),
		IsUnion:     isUnion,
		Visibility:  vis,
		Exported:    exported,
	}
	body := n.ChildByFieldName("body")
	switch {
	case body == nil:
		s.IsUnit = true
	case body.Type() == ntOrderedFieldDeclList:
		s.IsTuple = true
		s.Fields = tupleFields(body, st.src)
	default:
		s.Fields = namedFields(body, st.src)
	}
	st.pr.Structs = append(st.pr.Structs, s)
}

// parseEnum parses an enum_item.
func (st *parseState) parseEnum(n *sitter.Node, prefix string, attrs []*sitter.Node) {
	name := nodeText(n.ChildByFieldName("name"), st.src)
	if name == "" {
		return
	}
	vis, exported := parseVisibility(n, st.src)
	s := rust.RustStruct{
		ID:          prefix + "::" + name,
		Name:        name,
		Loc:         loc(st.pr.FileID, n),
		Connections: map[rust.ConnectionKind][]string{},
		Derives:     extractDerives(attrs, st.src),
		Generics:    extractGenerics(n, st.src),
		IsEnum:      true,
		Visibility:  vis,
		Exported:    exported,
	}
	if body := n.ChildByFieldName("body"); body != nil {
		for i := 0; i < int(body.NamedChildCount()); i++ {
			v := body.NamedChild(i)
			if v.Type() == ntEnumVariant {
				if vn := nodeText(v.ChildByFieldName("name"), st.src); vn != "" {
					s.Variants = append(s.Variants, vn)
				}
			}
		}
	}
	st.pr.Structs = append(st.pr.Structs, s)
}

// parseTrait parses a trait_item: supertrait bounds, associated types/consts, and
// inline method declarations (HasDefault when a body is present).
func (st *parseState) parseTrait(n *sitter.Node, prefix string) {
	name := nodeText(n.ChildByFieldName("name"), st.src)
	if name == "" {
		return
	}
	vis, exported := parseVisibility(n, st.src)
	t := rust.RustTrait{
		ID:          prefix + "::" + name,
		Name:        name,
		Loc:         loc(st.pr.FileID, n),
		Connections: map[rust.ConnectionKind][]string{},
		Generics:    extractGenerics(n, st.src),
		Bounds:      extractTraitBounds(n, st.src),
		Visibility:  vis,
		Exported:    exported,
	}
	if body := n.ChildByFieldName("body"); body != nil {
		for i := 0; i < int(body.NamedChildCount()); i++ {
			m := body.NamedChild(i)
			switch m.Type() {
			case ntFunctionSignatureItem:
				t.Methods = append(t.Methods, rust.FunctionDefinition{
					Name:       nodeText(m.ChildByFieldName("name"), st.src),
					Input:      extractParams(m, st.src),
					Output:     returnTypeDefs(m, st.src),
					HasDefault: false,
				})
			case ntFunctionItem:
				t.Methods = append(t.Methods, rust.FunctionDefinition{
					Name:       nodeText(m.ChildByFieldName("name"), st.src),
					Input:      extractParams(m, st.src),
					Output:     returnTypeDefs(m, st.src),
					HasDefault: true,
				})
			case ntAssociatedType:
				if an := nodeText(m.ChildByFieldName("name"), st.src); an != "" {
					t.AssocTypes = append(t.AssocTypes, an)
				}
			case ntConstItem:
				if cn := nodeText(m.ChildByFieldName("name"), st.src); cn != "" {
					t.AssocConsts = append(t.AssocConsts, cn)
				}
			}
		}
	}
	st.pr.Traits = append(st.pr.Traits, t)
}

// parseFreeFn parses a module-level function (or trait-method-less signature).
func (st *parseState) parseFreeFn(n *sitter.Node, prefix string) {
	name := nodeText(n.ChildByFieldName("name"), st.src)
	if name == "" {
		return
	}
	vis, exported := parseVisibility(n, st.src)
	isAsync, isUnsafe, isConst := readModifiers(n)
	fn := rust.RustFunction{
		ID:          prefix + "::" + name,
		Name:        name,
		Input:       extractParams(n, st.src),
		Output:      returnTypeDefs(n, st.src),
		Loc:         loc(st.pr.FileID, n),
		Connections: map[rust.ConnectionKind][]string{},
		IsAsync:     isAsync,
		IsUnsafe:    isUnsafe,
		IsConst:     isConst,
		Visibility:  vis,
		Exported:    exported,
	}
	var body *rustBody
	if b := n.ChildByFieldName("body"); b != nil {
		body = collectBody(b, st.src)
	}
	st.pr.Functions = append(st.pr.Functions, FunctionParse{Function: fn, Body: body, Module: prefix})
}

// parseImpl parses an impl block into a raw implBlock (target/trait names +
// methods); IDs are resolved later in attachImpls.
func (st *parseState) parseImpl(n *sitter.Node, prefix string) {
	typeName := typePath(n.ChildByFieldName("type"), st.src)
	if typeName == "" {
		return
	}
	traitName := typePath(n.ChildByFieldName("trait"), st.src)
	impl := implBlock{TypeName: typeName, TraitName: traitName, Module: prefix}

	body := n.ChildByFieldName("body")
	if body == nil {
		st.pr.Impls = append(st.pr.Impls, impl)
		return
	}
	for i := 0; i < int(body.NamedChildCount()); i++ {
		m := body.NamedChild(i)
		if m.Type() != ntFunctionItem && m.Type() != ntFunctionSignatureItem {
			continue
		}
		name := nodeText(m.ChildByFieldName("name"), st.src)
		if name == "" {
			continue
		}
		vis, exported := parseVisibility(m, st.src)
		isAsync, isUnsafe, isConst := readModifiers(m)
		recv, isAssoc := receiverFlavor(m)
		im := implMethod{
			Name:         name,
			Input:        extractParams(m, st.src),
			Output:       returnTypeDefs(m, st.src),
			IsAsync:      isAsync,
			IsUnsafe:     isUnsafe,
			IsConst:      isConst,
			Receiver:     recv,
			IsAssociated: isAssoc,
			Visibility:   vis,
			Exported:     exported,
			Loc:          loc(st.pr.FileID, m),
		}
		if b := m.ChildByFieldName("body"); b != nil {
			im.Body = collectBody(b, st.src)
		}
		impl.Methods = append(impl.Methods, im)
	}
	st.pr.Impls = append(st.pr.Impls, impl)
}

// parseVar parses a const_item or static_item.
func (st *parseState) parseVar(n *sitter.Node, prefix string, isStatic bool) {
	name := nodeText(n.ChildByFieldName("name"), st.src)
	if name == "" {
		return
	}
	vis, exported := parseVisibility(n, st.src)
	v := rust.RustVariable{
		ID:         prefix + "::" + name,
		Name:       name,
		Location:   loc(st.pr.FileID, n),
		IsConst:    !isStatic,
		IsStatic:   isStatic,
		Mutable:    isStatic && childByType(n, ntMutableSpecifier) != nil,
		Visibility: vis,
		Exported:   exported,
	}
	if t := n.ChildByFieldName("type"); t != nil {
		v.Typing = strings.TrimSpace(nodeText(t, st.src))
	}
	if val := n.ChildByFieldName("value"); val != nil {
		v.Value = valuePtr(nodeText(val, st.src))
	}
	st.pr.Variables = append(st.pr.Variables, v)
}

// parseTypeAlias parses a type_item (`type X = Y`).
func (st *parseState) parseTypeAlias(n *sitter.Node, prefix string) {
	name := nodeText(n.ChildByFieldName("name"), st.src)
	if name == "" {
		return
	}
	vis, exported := parseVisibility(n, st.src)
	underlying := ""
	if t := n.ChildByFieldName("type"); t != nil {
		underlying = strings.TrimSpace(nodeText(t, st.src))
		if len(underlying) > 200 {
			underlying = underlying[:200]
		}
	}
	st.pr.NamedTypes = append(st.pr.NamedTypes, rust.RustNamedType{
		ID:          prefix + "::" + name,
		Name:        name,
		Underlying:  underlying,
		Generics:    extractGenerics(n, st.src),
		Loc:         loc(st.pr.FileID, n),
		Connections: map[rust.ConnectionKind][]string{},
		Visibility:  vis,
		Exported:    exported,
	})
}

// parseMacro parses a macro_rules! definition, modeled as a function.
func (st *parseState) parseMacro(n *sitter.Node, prefix string) {
	name := nodeText(n.ChildByFieldName("name"), st.src)
	if name == "" {
		return
	}
	fn := rust.RustFunction{
		ID:          prefix + "::" + name,
		Name:        name,
		Loc:         loc(st.pr.FileID, n),
		Connections: map[rust.ConnectionKind][]string{},
		IsMacro:     true,
		Visibility:  "private",
		Exported:    false,
	}
	st.pr.Functions = append(st.pr.Functions, FunctionParse{Function: fn, Module: prefix})
}

// parseUse flattens a `use` declaration into per-leaf bindings.
func (st *parseState) parseUse(n *sitter.Node, prefix string) {
	arg := n.ChildByFieldName("argument")
	if arg == nil {
		return
	}
	first := len(st.pr.Uses)
	st.flattenUse(arg, nil)
	for i := first; i < len(st.pr.Uses); i++ {
		st.pr.Uses[i].Module = prefix
	}
}

// flattenUse walks a use-tree node, expanding groups and recording each leaf.
func (st *parseState) flattenUse(node *sitter.Node, prefix []string) {
	switch node.Type() {
	case ntScopedIdentifier:
		path := pathSegments(node.ChildByFieldName("path"), st.src)
		name := nodeText(node.ChildByFieldName("name"), st.src)
		full := concat(prefix, path, name)
		st.pr.Uses = append(st.pr.Uses, useLeaf{LocalName: name, Path: full})
	case ntIdentifier, ntCrate, ntSelf, ntSuper, ntTypeIdentifier:
		name := nodeText(node, st.src)
		if node.Type() == ntSelf && len(prefix) > 0 {
			// `use crate::shapes::{self}` binds the module `shapes` itself.
			st.pr.Uses = append(st.pr.Uses, useLeaf{LocalName: prefix[len(prefix)-1], Path: append([]string{}, prefix...)})
			return
		}
		st.pr.Uses = append(st.pr.Uses, useLeaf{LocalName: name, Path: append(append([]string{}, prefix...), name)})
	case ntUseAsClause:
		path := pathSegments(node.ChildByFieldName("path"), st.src)
		alias := nodeText(node.ChildByFieldName("alias"), st.src)
		full := append(append([]string{}, prefix...), path...)
		if len(full) > 1 && full[len(full)-1] == "self" {
			full = full[:len(full)-1] // `{self as s}` binds the module itself as `s`
		}
		st.pr.Uses = append(st.pr.Uses, useLeaf{LocalName: alias, Path: full})
	case ntScopedUseList:
		newPrefix := append(append([]string{}, prefix...), pathSegments(node.ChildByFieldName("path"), st.src)...)
		if list := node.ChildByFieldName("list"); list != nil {
			for i := 0; i < int(list.NamedChildCount()); i++ {
				st.flattenUse(list.NamedChild(i), newPrefix)
			}
		}
	case ntUseList:
		for i := 0; i < int(node.NamedChildCount()); i++ {
			st.flattenUse(node.NamedChild(i), prefix)
		}
	case ntUseWildcard:
		// The grammar gives use_wildcard no `path` field: the glob's path is its (only)
		// named child -- `crate::factory` in `use crate::factory::*`, absent in `{*}`.
		modPrefix := append([]string{}, prefix...)
		if node.NamedChildCount() > 0 {
			modPrefix = append(modPrefix, pathSegments(node.NamedChild(0), st.src)...)
		}
		st.pr.Uses = append(st.pr.Uses, useLeaf{Path: modPrefix, IsGlob: true})
	}
}

// pathSegments flattens a (possibly nested) scoped path node into its segments.
func pathSegments(node *sitter.Node, src []byte) []string {
	if node == nil {
		return nil
	}
	switch node.Type() {
	case ntScopedIdentifier, ntScopedTypeIdentifier:
		path := pathSegments(node.ChildByFieldName("path"), src)
		name := nodeText(node.ChildByFieldName("name"), src)
		return append(path, name)
	default:
		return []string{nodeText(node, src)}
	}
}

// concat joins a prefix, a middle slice, and a trailing element into a new slice.
func concat(prefix, mid []string, last string) []string {
	out := append([]string{}, prefix...)
	out = append(out, mid...)
	return append(out, last)
}

// extractTraitBounds reads a trait's supertrait bounds (`trait A: B + C`).
func extractTraitBounds(n *sitter.Node, src []byte) []string {
	b := n.ChildByFieldName("bounds")
	if b == nil {
		return nil
	}
	var out []string
	for i := 0; i < int(b.NamedChildCount()); i++ {
		c := b.NamedChild(i)
		switch c.Type() {
		case ntTypeIdentifier:
			out = append(out, nodeText(c, src))
		case ntScopedTypeIdentifier:
			if nm := c.ChildByFieldName("name"); nm != nil {
				out = append(out, nodeText(nm, src))
			}
		case ntGenericType:
			if nm := baseTypeName(c, src); nm != "" {
				out = append(out, nm)
			}
		}
	}
	return out
}

// extractGenerics reads declared type-parameter names from a type_parameters node.
func extractGenerics(n *sitter.Node, src []byte) []string {
	tp := n.ChildByFieldName("type_parameters")
	if tp == nil {
		return nil
	}
	var out []string
	for i := 0; i < int(tp.NamedChildCount()); i++ {
		c := tp.NamedChild(i)
		switch c.Type() {
		case ntTypeIdentifier:
			out = append(out, nodeText(c, src))
		case ntConstrainedTypeParam:
			if id := childByType(c, ntTypeIdentifier); id != nil {
				out = append(out, nodeText(id, src))
			}
		}
	}
	return out
}

// extractDerives reads trait names out of preceding `#[derive(...)]` attributes.
func extractDerives(attrs []*sitter.Node, src []byte) []string {
	var out []string
	for _, a := range attrs {
		at := childByType(a, ntAttribute)
		if at == nil {
			continue
		}
		if nodeText(childByType(at, ntIdentifier), src) != "derive" {
			continue
		}
		tt := at.ChildByFieldName("arguments")
		if tt == nil {
			tt = childByType(at, ntTokenTree)
		}
		if tt == nil {
			continue
		}
		for i := 0; i < int(tt.NamedChildCount()); i++ {
			c := tt.NamedChild(i)
			switch c.Type() {
			case ntIdentifier, ntTypeIdentifier:
				out = append(out, nodeText(c, src))
			case ntScopedIdentifier, ntScopedTypeIdentifier:
				if nm := c.ChildByFieldName("name"); nm != nil {
					out = append(out, nodeText(nm, src))
				}
			}
		}
	}
	return out
}

// isTestAttr reports whether any preceding attribute marks the item as test-only
// (`#[test]` or `#[cfg(test)]`), so its subtree can be skipped.
func isTestAttr(attrs []*sitter.Node, src []byte) bool {
	for _, a := range attrs {
		at := childByType(a, ntAttribute)
		if at == nil {
			continue
		}
		name := nodeText(childByType(at, ntIdentifier), src)
		if name == "test" {
			return true
		}
		if name == "cfg" {
			tt := at.ChildByFieldName("arguments")
			if tt == nil {
				tt = childByType(at, ntTokenTree)
			}
			if tt != nil && cfgRequiresTest(nodeText(tt, src)) {
				return true
			}
		}
	}
	return false
}

// cfgRequiresTest reports whether a `cfg(...)` predicate -- the parenthesised argument
// text, `(test)` or `(all(test, feature = "x"))` -- can only hold in a test build, so the
// item it gates never exists in the code being mapped. `not(test)`, `any(test, unix)` and
// `feature = "contest"` gate code that does exist outside tests, so they do not.
func cfgRequiresTest(pred string) bool {
	pred = strings.TrimSpace(pred)
	if strings.HasPrefix(pred, "(") && strings.HasSuffix(pred, ")") {
		pred = strings.TrimSpace(pred[1 : len(pred)-1])
	}
	if pred == "test" {
		return true
	}
	open := strings.IndexByte(pred, '(')
	if open < 0 || !strings.HasSuffix(pred, ")") {
		return false
	}
	op := strings.TrimSpace(pred[:open])
	args := splitCfgArgs(pred[open+1 : len(pred)-1])
	switch op {
	case "all":
		for _, a := range args {
			if cfgRequiresTest(a) {
				return true
			}
		}
	case "any":
		if len(args) == 0 {
			return false
		}
		for _, a := range args {
			if !cfgRequiresTest(a) {
				return false
			}
		}
		return true
	}
	return false
}

// splitCfgArgs splits a cfg predicate list at its top-level commas, leaving commas inside
// nested parentheses and string literals alone.
func splitCfgArgs(s string) []string {
	var out []string
	depth, start := 0, 0
	inStr := false
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case inStr:
			if c == '\\' {
				i++
			} else if c == '"' {
				inStr = false
			}
		case c == '"':
			inStr = true
		case c == '(':
			depth++
		case c == ')':
			depth--
		case c == ',' && depth == 0:
			out = append(out, strings.TrimSpace(s[start:i]))
			start = i + 1
		}
	}
	if last := strings.TrimSpace(s[start:]); last != "" {
		out = append(out, last)
	}
	return out
}

// receiverFlavor inspects a method's parameters for a self_parameter, returning
// the receiver flavor ("value"/"ref"/"ref_mut") and whether it is associated
// (no self).
func receiverFlavor(fnNode *sitter.Node) (string, bool) {
	params := fnNode.ChildByFieldName("parameters")
	if params == nil {
		return "", true
	}
	self := childByType(params, ntSelfParameter)
	if self == nil {
		// `self: Pin<&mut Self>` / `self: &Self` is a typed receiver: the grammar
		// makes it an ordinary parameter whose pattern is `self`.
		for i := 0; i < int(params.NamedChildCount()); i++ {
			if c := params.NamedChild(i); isTypedSelfParam(c) {
				if t := c.ChildByFieldName("type"); t != nil && t.Type() == ntReferenceType {
					if childByType(t, ntMutableSpecifier) != nil {
						return "ref_mut", false
					}
					return "ref", false
				}
				return "value", false // `self: Box<Self>`, `self: Pin<&mut Self>`
			}
		}
		return "", true
	}
	if hasChildToken(self, "&") {
		if hasChildToken(self, ntMutableSpecifier) {
			return "ref_mut", false
		}
		return "ref", false
	}
	return "value", false
}

// isTypedSelfParam reports whether a parameter node is a typed receiver
// (`self: Box<Self>`, `mut self: Pin<&mut Self>`): its pattern is `self`.
func isTypedSelfParam(c *sitter.Node) bool {
	if c == nil || c.Type() != ntParameter {
		return false
	}
	pat := c.ChildByFieldName("pattern")
	return pat != nil && pat.Type() == ntSelf
}

// extractParams reads `parameter` nodes (skipping the receiver, typed or not) into defs.
func extractParams(fnNode *sitter.Node, src []byte) []rust.VariableDefinition {
	p := fnNode.ChildByFieldName("parameters")
	if p == nil {
		return nil
	}
	var out []rust.VariableDefinition
	for i := 0; i < int(p.NamedChildCount()); i++ {
		c := p.NamedChild(i)
		if c.Type() != ntParameter || isTypedSelfParam(c) {
			continue
		}
		def := rust.VariableDefinition{}
		if pat := c.ChildByFieldName("pattern"); pat != nil {
			def.Name = nodeText(pat, src)
		}
		if t := c.ChildByFieldName("type"); t != nil {
			def.Typing = strings.TrimSpace(nodeText(t, src))
		}
		out = append(out, def)
	}
	return out
}

// returnTypeDefs reads a function's return_type into a single output def.
func returnTypeDefs(fnNode *sitter.Node, src []byte) []rust.VariableDefinition {
	rt := fnNode.ChildByFieldName("return_type")
	if rt == nil {
		return nil
	}
	t := strings.TrimSpace(nodeText(rt, src))
	if t == "" {
		return nil
	}
	return []rust.VariableDefinition{{Typing: t}}
}

// namedFields reads a field_declaration_list (struct/union/struct-variant body).
func namedFields(body *sitter.Node, src []byte) []rust.VariableDefinition {
	var out []rust.VariableDefinition
	for i := 0; i < int(body.NamedChildCount()); i++ {
		f := body.NamedChild(i)
		if f.Type() != ntFieldDeclaration {
			continue
		}
		def := rust.VariableDefinition{}
		if nm := f.ChildByFieldName("name"); nm != nil {
			def.Name = nodeText(nm, src)
		}
		if t := f.ChildByFieldName("type"); t != nil {
			def.Typing = strings.TrimSpace(nodeText(t, src))
		}
		out = append(out, def)
	}
	return out
}

// tupleFields reads an ordered_field_declaration_list (tuple struct), naming each
// field by position.
func tupleFields(body *sitter.Node, src []byte) []rust.VariableDefinition {
	var out []rust.VariableDefinition
	idx := 0
	for i := 0; i < int(body.NamedChildCount()); i++ {
		c := body.NamedChild(i)
		typ := strings.TrimSpace(c.Content(src))
		// ordered fields expose a `type` field; otherwise treat any named type
		// node as a field.
		switch c.Type() {
		case ntFieldDeclaration:
			if t := c.ChildByFieldName("type"); t != nil {
				typ = strings.TrimSpace(nodeText(t, src))
			}
		case ntBlockComment, ntLineComment, ntVisibilityModifier, ntAttributeItem:
			// `struct Meters(pub f64)`: the `pub` qualifies the field that follows; it is
			// not a field of its own.
			continue
		}
		out = append(out, rust.VariableDefinition{Name: itoa(idx), Typing: typ})
		idx++
	}
	return out
}

// baseTypeName strips references/generics/lifetimes from a type node down to its
// base type identifier.
func baseTypeName(n *sitter.Node, src []byte) string {
	if n == nil {
		return ""
	}
	switch n.Type() {
	case ntTypeIdentifier, ntIdentifier:
		return nodeText(n, src)
	case ntScopedTypeIdentifier, ntScopedIdentifier:
		if nm := n.ChildByFieldName("name"); nm != nil {
			return nodeText(nm, src)
		}
	case ntGenericType:
		if t := n.ChildByFieldName("type"); t != nil {
			return baseTypeName(t, src)
		}
	case ntReferenceType:
		if t := n.ChildByFieldName("type"); t != nil {
			return baseTypeName(t, src)
		}
	}
	return normType(nodeText(n, src))
}

// typePath is baseTypeName that keeps a scoped path's qualifier -- `crate::b::Thing`
// rather than `Thing` -- so resolution can follow the path the source wrote instead of
// guessing from the bare name.
func typePath(n *sitter.Node, src []byte) string {
	if n == nil {
		return ""
	}
	switch n.Type() {
	case ntScopedTypeIdentifier, ntScopedIdentifier:
		return strings.Join(pathSegments(n, src), "::")
	case ntGenericType, ntReferenceType:
		if t := n.ChildByFieldName("type"); t != nil {
			return typePath(t, src)
		}
	}
	return baseTypeName(n, src)
}

// collectBody walks a function/method body, recording calls, lets, struct
// literals, and value-position references.
func collectBody(body *sitter.Node, src []byte) *rustBody {
	rb := &rustBody{}
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil {
			return
		}
		switch n.Type() {
		case ntLetDeclaration:
			name := ""
			if pat := n.ChildByFieldName("pattern"); pat != nil && pat.Type() == ntIdentifier {
				name = nodeText(pat, src)
			}
			if name != "" {
				l := classifyLet(name, n.ChildByFieldName("value"), src)
				if t := n.ChildByFieldName("type"); t != nil {
					l.TypeAnn = strings.TrimSpace(nodeText(t, src))
				}
				rb.Lets = append(rb.Lets, l)
			}
		case ntCallExpression:
			recordCall(n, src, rb)
		case ntStructExpression:
			if name := typePath(n.ChildByFieldName("name"), src); name != "" {
				rb.Structs = append(rb.Structs, name)
			}
		case ntScopedIdentifier:
			if !isCallFunction(n) {
				rb.Refs = append(rb.Refs, rustRef{Segs: pathSegments(n, src)})
			}
		case ntIdentifier:
			if isBareValueRef(n) {
				rb.Refs = append(rb.Refs, rustRef{Segs: []string{nodeText(n, src)}})
			}
		}
		for i := 0; i < int(n.NamedChildCount()); i++ {
			walk(n.NamedChild(i))
		}
	}
	walk(body)
	return rb
}

// recordCall classifies one call_expression into a rustCall.
func recordCall(n *sitter.Node, src []byte, rb *rustBody) {
	fn := unwrapTurbofish(n.ChildByFieldName("function"))
	if fn == nil {
		return
	}
	args := callArgTokens(n)
	switch fn.Type() {
	case ntIdentifier:
		rb.Calls = append(rb.Calls, rustCall{Func: nodeText(fn, src), Args: args})
	case ntScopedIdentifier:
		path := fn.ChildByFieldName("path")
		name := nodeText(fn.ChildByFieldName("name"), src)
		head := lastPathSeg(path, src)
		rb.Calls = append(rb.Calls, rustCall{
			PathType: head, PathSegs: pathSegments(path, src), PathName: name, IsSelf: head == "Self", Args: args,
		})
	case ntFieldExpression:
		val := fn.ChildByFieldName("value")
		field := nodeText(fn.ChildByFieldName("field"), src)
		c := rustCall{Method: field, Args: args}
		if val != nil {
			if val.Type() == ntSelf {
				c.IsSelf = true
				c.ObjName = "self"
			} else if val.Type() == ntIdentifier {
				c.ObjName = nodeText(val, src)
			}
		}
		rb.Calls = append(rb.Calls, c)
	}
}

// unwrapTurbofish returns the callee expression of a call written with explicit type
// arguments -- `generic_fn::<i32>()`, `x.collect::<Vec<_>>()` -- which the grammar wraps
// in a generic_function node; any other node is returned unchanged.
func unwrapTurbofish(fn *sitter.Node) *sitter.Node {
	if fn != nil && fn.Type() == ntGenericFunction {
		return fn.ChildByFieldName("function")
	}
	return fn
}

// callArgTokens reads one token per argument of a call.
//
// Literals only. Anything else -- an identifier, a nested call, an operator expression --
// is left empty rather than guessed, because a wrong token is a false warning about correct
// code, which is the failure this whole mechanism exists to remove. Rust's arity check does
// not depend on these; they only sharpen it.
func callArgTokens(call *sitter.Node) []string {
	args := call.ChildByFieldName("arguments")
	if args == nil {
		return nil
	}
	n := int(args.NamedChildCount())
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, contract.LiteralToken(args.NamedChild(i).Type()))
	}
	return out
}

// classifyLet inspects a let value to type the bound variable.
func classifyLet(name string, val *sitter.Node, src []byte) rustLet {
	l := rustLet{Name: name}
	if val == nil {
		return l
	}
	switch val.Type() {
	case ntCallExpression:
		fn := unwrapTurbofish(val.ChildByFieldName("function"))
		if fn == nil {
			return l
		}
		switch fn.Type() {
		case ntIdentifier:
			l.CallFunc = nodeText(fn, src)
		case ntScopedIdentifier:
			l.CallType = lastPathSeg(fn.ChildByFieldName("path"), src)
			l.CallPath = pathSegments(fn.ChildByFieldName("path"), src)
			l.CallName = nodeText(fn.ChildByFieldName("name"), src)
		}
	case ntStructExpression:
		l.StructLit = typePath(val.ChildByFieldName("name"), src)
	case ntIdentifier:
		l.AliasOf = nodeText(val, src)
	}
	return l
}

// lastPathSeg returns the trailing segment name of a scoped path node.
func lastPathSeg(node *sitter.Node, src []byte) string {
	if node == nil {
		return ""
	}
	switch node.Type() {
	case ntScopedIdentifier, ntScopedTypeIdentifier:
		if nm := node.ChildByFieldName("name"); nm != nil {
			return nodeText(nm, src)
		}
	}
	return nodeText(node, src)
}

// isCallFunction reports whether a node is the `function` child of a call.
func isCallFunction(n *sitter.Node) bool {
	p := n.Parent()
	return p != nil && p.Type() == ntCallExpression && p.ChildByFieldName("function") == n
}

// isBareValueRef reports whether a bare identifier sits in a value position worth
// recording as a const/static/dep reference (not a call target, pattern, path
// component, field name, or parameter pattern).
func isBareValueRef(n *sitter.Node) bool {
	p := n.Parent()
	if p == nil {
		return false
	}
	switch p.Type() {
	case ntCallExpression:
		return p.ChildByFieldName("function") != n
	case ntLetDeclaration:
		return p.ChildByFieldName("pattern") != n
	case ntScopedIdentifier, ntScopedTypeIdentifier, ntFieldExpression,
		ntParameter, ntStructExpression:
		return false
	}
	return true
}

// valuePtr returns a pointer to a truncated string value, or nil for empty.
func valuePtr(s string) *any {
	s = strings.TrimSpace(s)
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

// itoa renders a small non-negative int without importing strconv.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}
