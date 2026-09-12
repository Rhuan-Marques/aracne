package javascanner

import (
	"os"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	tsjava "github.com/smacker/go-tree-sitter/java"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	java "github.com/Rhuan-Marques/aracne/internal/topology/java"
)

// hierKind classifies a deferred hierarchy record (resolved to a parent FQN in
// the resolver). The matcher turns each into the correct struct/iface edge.
type hierKind int

const (
	hkExtendsClass hierKind = iota // class extends class -> struct<->struct inherits
	hkExtendsIface                 // interface extends interface -> iface<->iface inherits
	hkImplements                   // class/enum/record implements iface -> struct->iface
	hkEnumConst                    // enum-constant body inherits the enum -> struct<->struct
	hkAnonSuper                    // anonymous class supertype (kind decided at resolution)
)

// hierRec is a deferred parent reference: a child type ID and a raw parent NAME
// to be resolved to a FQN in the resolver (using the child file's import map).
type hierRec struct {
	ChildID    string
	ParentName string
	Kind       hierKind
}

// typeUse records a raw type reference whose resolution produces a uses edge on
// the holder (a class field/bound, or a method/class annotation).
type typeUse struct {
	HolderID string
	TypeName string
}

// javaImport is one import declaration, enriched by the resolver (Internal/Dep).
type javaImport struct {
	FQN       string // type FQN, or package (wildcard), or owner FQN (static)
	Member    string // static member name (static imports)
	LocalName string // bound simple name
	Static    bool
	Wildcard  bool
	Internal  bool   // resolves to a declared internal type
	Dep       string // external coordinate (when not internal)
}

// javaBody holds the call/let/new records extracted from a method/initializer
// body, resolved later in analyzeFunctionBody.
type javaBody struct {
	Lets  []javaLet
	Calls []javaCall
	News  []javaNew
}

// javaLet records a local-variable / resource / loop binding used to type a var.
type javaLet struct {
	Name     string
	DeclType string // declared type text (highest-priority var type)
	Param    bool   // a lambda or catch parameter: types its receiver, records no uses edge
	NewType  string // RHS `new T(...)` -> T
	CallObj  string // RHS `recv.m(...)` receiver text
	CallMeth string // RHS method name
	CallArgs int
	Cast     string // RHS `(T) expr` -> T
}

// javaCall records one method invocation / explicit constructor invocation /
// method reference, resolved to calls/uses edges in analyzeFunctionBody.
type javaCall struct {
	Object    string // receiver text (var or type) or ""
	Method    string // method name (or "<init>")
	ArgCount  int    // -1 => unknown arity (method reference): match all overloads
	IsSelf    bool   // implicit-this or `this` receiver
	Implicit  bool   // no receiver at all: resolved through the enclosing types, then static imports
	IsSuper   bool   // `super` receiver
	CastType  string // receiver was a cast to this type
	FieldRecv string // receiver was `this.<field>` -> field name
}

// javaNew records one object creation `new T(args)` -> uses_struct + ctor call.
type javaNew struct {
	Type     string
	ArgCount int
}

// MethodParse pairs a parsed method/constructor/accessor/initializer resource
// with its body record (nil for synthetic accessors and abstract declarations).
type MethodParse struct {
	Method java.JavaMethod
	Body   *javaBody
}

// ParseResult is the per-file parse output. Import resolution, hierarchy-record
// resolution, and body analysis are deferred to resolveTopology.
type ParseResult struct {
	FileID          string
	Package         string
	FileDescription string
	Classes         []java.JavaClass
	Interfaces      []java.JavaInterface
	Methods         []MethodParse
	Imports         []javaImport
	HierRecords     []hierRec
	TypeUses        []typeUse
	AnnoUses        []typeUse

	// AnonEnclosing maps an anonymous class to the type whose code declares it. Its
	// ID is numbered under the top-level type, so the ID alone skips a member type
	// the anonymous class sits in.
	AnonEnclosing map[string]string

	// Filled by the resolver.
	ImportMap       map[string]javaImport
	Wildcards       []string
	StaticMembers   map[string]string // statically imported member -> owner type FQN (internal or not)
	StaticWildcards []string          // internal owner types of `import static T.*`
}

// parseState carries the source bytes, accumulating parse result, and the
// per-top-level-type anonymous-class counter through the recursive walk.
type parseState struct {
	src         []byte
	pr          *ParseResult
	anonCounter int
	localSeen   map[string]bool // local-class FQNs already emitted (collision disambiguation)
}

// nodeText returns a node's source text, trimmed.
func nodeText(n *sitter.Node, src []byte) string {
	if n == nil {
		return ""
	}
	return strings.TrimSpace(n.Content(src))
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

// ParseFile parses a single Java source file into a ParseResult. The package is
// read from the file's own `package` declaration (the passed value is ignored,
// kept for signature parity), so IDs are a pure function of the source.
func ParseFile(filePath, _ string) (*ParseResult, error) {
	src, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	parser := sitter.NewParser()
	defer parser.Close()
	parser.SetLanguage(tsjava.GetLanguage())

	tree := parser.Parse(nil, src)
	defer tree.Close()
	root := tree.RootNode()

	pr := &ParseResult{
		FileID:        filePath,
		ImportMap:     map[string]javaImport{},
		StaticMembers: map[string]string{},
		AnonEnclosing: map[string]string{},
	}
	st := &parseState{src: src, pr: pr, localSeen: map[string]bool{}}
	st.walkProgram(root)
	return pr, nil
}

// walkProgram processes a file's top-level declarations: package, imports, and
// each top-level type (resetting the anonymous-class counter per top-level type).
func (st *parseState) walkProgram(root *sitter.Node) {
	pkg := ""
	for i := 0; i < int(root.NamedChildCount()); i++ {
		n := root.NamedChild(i)
		switch n.Type() {
		case "package_declaration":
			if id := childByType(n, "scoped_identifier"); id != nil {
				pkg = nodeText(id, st.src)
			} else if id := childByType(n, "identifier"); id != nil {
				pkg = nodeText(id, st.src)
			}
			st.pr.Package = pkg
		case "import_declaration":
			st.parseImport(n)
		case "block_comment":
			if st.pr.FileDescription == "" && len(st.pr.Classes) == 0 && len(st.pr.Interfaces) == 0 {
				st.pr.FileDescription = cleanDoc(n.Content(st.src))
			}
		case "class_declaration", "interface_declaration", "enum_declaration",
			"record_declaration", "annotation_type_declaration":
			name := nodeText(n.ChildByFieldName("name"), st.src)
			fqn := qualify(st.pr.Package, name)
			st.anonCounter = 0
			st.walkTypeDecl(n, fqn, fqn)
		}
	}
}

// qualify joins a package prefix and a simple name into a FQN.
func qualify(pkg, name string) string {
	if pkg == "" {
		return name
	}
	return pkg + "." + name
}

// parseImport records one import declaration (static/wildcard aware).
func (st *parseState) parseImport(n *sitter.Node) {
	id := childByType(n, "scoped_identifier")
	if id == nil {
		id = childByType(n, "identifier")
	}
	if id == nil {
		return
	}
	path := nodeText(id, st.src)
	static := hasToken(n, "static")
	wildcard := childByType(n, "asterisk") != nil
	imp := javaImport{Static: static, Wildcard: wildcard}
	switch {
	case static && !wildcard:
		imp.FQN = dropLastSeg(path)
		imp.Member = lastSegDot(path)
		imp.LocalName = imp.Member
	case wildcard:
		imp.FQN = path
	default:
		imp.FQN = path
		imp.LocalName = lastSegDot(path)
	}
	st.pr.Imports = append(st.pr.Imports, imp)
}

// hasToken reports whether any (named or anonymous) child has the given type.
func hasToken(n *sitter.Node, token string) bool {
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

// modFlags reads a `modifiers` node's keyword tokens into a set.
func modFlags(n *sitter.Node) map[string]bool {
	out := map[string]bool{}
	if n == nil {
		return out
	}
	for i := 0; i < int(n.ChildCount()); i++ {
		c := n.Child(i)
		if c.IsNamed() {
			continue
		}
		switch c.Type() {
		case "public", "private", "protected", "abstract", "static", "final",
			"sealed", "non-sealed", "default", "strictfp", "synchronized",
			"native", "transient", "volatile":
			out[c.Type()] = true
		}
	}
	return out
}

// visibilityOf maps modifier flags to a visibility string and an exported flag.
func visibilityOf(flags map[string]bool) (string, bool) {
	switch {
	case flags["public"]:
		return "public", true
	case flags["protected"]:
		return "protected", false
	case flags["private"]:
		return "private", false
	default:
		return "package", false
	}
}

// annotationNames reads the annotation type names applied via a `modifiers` node.
func annotationNames(n *sitter.Node, src []byte) []string {
	if n == nil {
		return nil
	}
	var out []string
	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(i)
		switch c.Type() {
		case "marker_annotation", "annotation":
			if nm := c.ChildByFieldName("name"); nm != nil {
				out = append(out, nodeText(nm, src))
			}
		}
	}
	return out
}

// walkTypeDecl builds the type resource for a declaration node under the given
// FQN and recurses into its members. topLevelFQN names the enclosing top-level
// type (used for anonymous-class numbering).
func (st *parseState) walkTypeDecl(n *sitter.Node, fqn, topLevelFQN string) {
	switch n.Type() {
	case "class_declaration", "record_declaration", "enum_declaration":
		st.parseClassLike(n, fqn, topLevelFQN)
	case "interface_declaration", "annotation_type_declaration":
		st.parseInterfaceLike(n, fqn)
	}
}

// parseClassLike parses a class/record/enum declaration into a JavaClass plus its
// members, hierarchy records, fields, generics, and (for enums/records) variants,
// components, constant bodies, and synthesized accessors.
func (st *parseState) parseClassLike(n *sitter.Node, fqn, topLevelFQN string) {
	mods := childByType(n, "modifiers")
	flags := modFlags(mods)
	vis, exported := visibilityOf(flags)

	cls := java.JavaClass{
		ID:          fqn,
		Name:        lastSegDot(fqn),
		Loc:         loc(st.pr.FileID, n),
		Connections: map[java.ConnectionKind][]string{},
		IsEnum:      n.Type() == "enum_declaration",
		IsRecord:    n.Type() == "record_declaration",
		IsAbstract:  flags["abstract"],
		IsSealed:    flags["sealed"],
		IsFinal:     flags["final"],
		IsStatic:    flags["static"],
		Generics:    typeParamNames(n, st.src),
		Visibility:  vis,
		Exported:    exported,
	}

	// extends (class only), implements, permits, generic bounds.
	if sc := n.ChildByFieldName("superclass"); sc != nil {
		cls.Bases = typeListTypes(sc, st.src)
		for _, t := range cls.Bases {
			st.pr.HierRecords = append(st.pr.HierRecords, hierRec{ChildID: fqn, ParentName: t, Kind: hkExtendsClass})
		}
	}
	if iflist := n.ChildByFieldName("interfaces"); iflist != nil {
		for _, t := range typeListTypes(iflist, st.src) {
			st.pr.HierRecords = append(st.pr.HierRecords, hierRec{ChildID: fqn, ParentName: t, Kind: hkImplements})
		}
	}
	if perm := n.ChildByFieldName("permits"); perm != nil {
		cls.Permits = typeListTypes(perm, st.src)
	}
	for _, b := range typeParamBounds(n, st.src) {
		st.pr.TypeUses = append(st.pr.TypeUses, typeUse{HolderID: fqn, TypeName: b})
	}
	for _, a := range annotationNames(mods, st.src) {
		st.pr.AnnoUses = append(st.pr.AnnoUses, typeUse{HolderID: fqn, TypeName: a})
	}

	body := n.ChildByFieldName("body")

	if cls.IsRecord {
		cls.Components = paramDefs(n.ChildByFieldName("parameters"), st.src)
		for _, c := range cls.Components {
			st.pr.TypeUses = append(st.pr.TypeUses, typeUse{HolderID: fqn, TypeName: c.Typing})
		}
	}

	// Fields (named declarations inside the body) live inline on the class.
	if body != nil {
		cls.Fields = st.collectFields(body, fqn)
	}

	st.pr.Classes = append(st.pr.Classes, cls)

	// Members.
	if cls.IsEnum {
		st.parseEnumBody(body, fqn, topLevelFQN)
	} else {
		st.parseTypeMembers(body, fqn, topLevelFQN)
	}
	st.buildInitBlocks(body, fqn, topLevelFQN)

	if cls.IsRecord {
		st.synthesizeAccessors(n, fqn, cls.Components)
	}
}

// parseInterfaceLike parses an interface or annotation-type declaration into a
// JavaInterface (with method summaries) plus real method nodes for each member.
func (st *parseState) parseInterfaceLike(n *sitter.Node, fqn string) {
	mods := childByType(n, "modifiers")
	flags := modFlags(mods)
	vis, exported := visibilityOf(flags)

	iface := java.JavaInterface{
		ID:           fqn,
		Name:         lastSegDot(fqn),
		Loc:          loc(st.pr.FileID, n),
		Connections:  map[java.ConnectionKind][]string{},
		Generics:     typeParamNames(n, st.src),
		IsAnnotation: n.Type() == "annotation_type_declaration",
		Visibility:   vis,
		Exported:     exported,
	}
	// extends interfaces (a named child `extends_interfaces`, not a field).
	if ext := childByType(n, "extends_interfaces"); ext != nil {
		for _, t := range typeListTypes(ext, st.src) {
			st.pr.HierRecords = append(st.pr.HierRecords, hierRec{ChildID: fqn, ParentName: t, Kind: hkExtendsIface})
		}
	}
	for _, b := range typeParamBounds(n, st.src) {
		st.pr.TypeUses = append(st.pr.TypeUses, typeUse{HolderID: fqn, TypeName: b})
	}
	for _, a := range annotationNames(mods, st.src) {
		st.pr.AnnoUses = append(st.pr.AnnoUses, typeUse{HolderID: fqn, TypeName: a})
	}

	body := n.ChildByFieldName("body")
	if body != nil {
		for i := 0; i < int(body.NamedChildCount()); i++ {
			m := body.NamedChild(i)
			switch m.Type() {
			case "method_declaration":
				iface.Methods = append(iface.Methods, st.interfaceMethodSummary(m))
				st.parseMethod(m, fqn, fqn)
			case "annotation_type_element_declaration":
				iface.Methods = append(iface.Methods, st.annotationElementSummary(m))
				st.parseAnnotationElement(m, fqn)
			case "class_declaration", "interface_declaration", "enum_declaration",
				"record_declaration", "annotation_type_declaration":
				st.walkTypeDecl(m, fqn+"."+nodeText(m.ChildByFieldName("name"), st.src), fqn)
			}
		}
	}
	st.pr.Interfaces = append(st.pr.Interfaces, iface)
}

// interfaceMethodSummary builds an interface method summary (default/static/
// abstract flags) from a method_declaration node.
func (st *parseState) interfaceMethodSummary(m *sitter.Node) java.FunctionDefinition {
	flags := modFlags(childByType(m, "modifiers"))
	hasBody := m.ChildByFieldName("body") != nil
	return java.FunctionDefinition{
		Name:       nodeText(m.ChildByFieldName("name"), st.src),
		Input:      paramDefs(m.ChildByFieldName("parameters"), st.src),
		Output:     returnDefs(m, st.src),
		HasDefault: flags["default"] || (hasBody && !flags["static"]),
		IsStatic:   flags["static"],
		IsAbstract: !hasBody && !flags["default"] && !flags["static"],
	}
}

// annotationElementSummary builds a summary for an annotation element.
func (st *parseState) annotationElementSummary(m *sitter.Node) java.FunctionDefinition {
	return java.FunctionDefinition{
		Name:       nodeText(m.ChildByFieldName("name"), st.src),
		Output:     returnDefs(m, st.src),
		IsAbstract: true,
	}
}

// parseTypeMembers walks a class/enum body, parsing methods, constructors, nested
// types, and (for enum_body_declarations) nested enum members.
func (st *parseState) parseTypeMembers(body *sitter.Node, typeFQN, topLevelFQN string) {
	if body == nil {
		return
	}
	for i := 0; i < int(body.NamedChildCount()); i++ {
		m := body.NamedChild(i)
		switch m.Type() {
		case "method_declaration":
			st.parseMethod(m, typeFQN, topLevelFQN)
		case "constructor_declaration":
			st.parseConstructor(m, typeFQN, topLevelFQN)
		case "compact_constructor_declaration":
			st.parseCompactConstructor(m, typeFQN, topLevelFQN)
		case "class_declaration", "interface_declaration", "enum_declaration",
			"record_declaration", "annotation_type_declaration":
			st.walkTypeDecl(m, typeFQN+"."+nodeText(m.ChildByFieldName("name"), st.src), topLevelFQN)
		case "enum_body_declarations":
			st.parseTypeMembers(m, typeFQN, topLevelFQN)
		}
	}
}

// parseEnumBody walks an enum body: each constant adds a variant, a constant body
// becomes a struct that inherits the enum, and enum_body_declarations holds the
// regular members.
func (st *parseState) parseEnumBody(body *sitter.Node, enumFQN, topLevelFQN string) {
	if body == nil {
		return
	}
	cls := &st.pr.Classes[len(st.pr.Classes)-1] // the enum we just appended
	for i := 0; i < int(body.NamedChildCount()); i++ {
		m := body.NamedChild(i)
		switch m.Type() {
		case "enum_constant":
			name := nodeText(m.ChildByFieldName("name"), st.src)
			if name != "" {
				cls.Variants = append(cls.Variants, name)
			}
			if cb := m.ChildByFieldName("body"); cb != nil {
				constFQN := enumFQN + "$" + name
				st.pr.Classes = append(st.pr.Classes, java.JavaClass{
					ID:          constFQN,
					Name:        name,
					Loc:         loc(st.pr.FileID, m),
					Connections: map[java.ConnectionKind][]string{},
					Visibility:  "package",
				})
				st.pr.HierRecords = append(st.pr.HierRecords, hierRec{ChildID: constFQN, ParentName: enumFQN, Kind: hkEnumConst})
				st.parseTypeMembers(cb, constFQN, topLevelFQN)
				cls = &st.pr.Classes[indexOfClass(st.pr, enumFQN)] // re-point after slice growth
			}
		case "enum_body_declarations":
			st.parseTypeMembers(m, enumFQN, topLevelFQN)
		}
	}
}

// indexOfClass returns the slice index of a class by ID (-1 if absent).
func indexOfClass(pr *ParseResult, id string) int {
	for i := range pr.Classes {
		if pr.Classes[i].ID == id {
			return i
		}
	}
	return -1
}

// collectFields reads the inline field declarations of a type body, recording
// field-type uses and field annotations on the owning type.
func (st *parseState) collectFields(body *sitter.Node, ownerFQN string) []java.VariableDefinition {
	var out []java.VariableDefinition
	for i := 0; i < int(body.NamedChildCount()); i++ {
		f := body.NamedChild(i)
		if f.Type() != "field_declaration" {
			continue
		}
		typeText := nodeText(f.ChildByFieldName("type"), st.src)
		for _, a := range annotationNames(childByType(f, "modifiers"), st.src) {
			st.pr.AnnoUses = append(st.pr.AnnoUses, typeUse{HolderID: ownerFQN, TypeName: a})
		}
		for j := 0; j < int(f.NamedChildCount()); j++ {
			d := f.NamedChild(j)
			if d.Type() != "variable_declarator" {
				continue
			}
			out = append(out, java.VariableDefinition{
				Name:   nodeText(d.ChildByFieldName("name"), st.src),
				Typing: typeText,
			})
		}
		st.pr.TypeUses = append(st.pr.TypeUses, typeUse{HolderID: ownerFQN, TypeName: typeText})
	}
	return out
}

// parseMethod parses a regular method declaration into a JavaMethod and walks its
// body (which may create anonymous/local classes).
func (st *parseState) parseMethod(m *sitter.Node, typeFQN, topLevelFQN string) {
	name := nodeText(m.ChildByFieldName("name"), st.src)
	if name == "" {
		return
	}
	flags := modFlags(childByType(m, "modifiers"))
	vis, exported := visibilityOf(flags)
	params := m.ChildByFieldName("parameters")
	sig := paramSig(params, st.src)
	id := typeFQN + "." + name + "(" + sig + ")"
	owner := typeFQN
	hasBody := m.ChildByFieldName("body") != nil

	method := java.JavaMethod{
		ID:          id,
		Name:        name,
		Input:       paramDefs(params, st.src),
		Output:      returnDefs(m, st.src),
		Throws:      throwsTypes(m, st.src),
		Loc:         loc(st.pr.FileID, m),
		Connections: map[java.ConnectionKind][]string{},
		MethodFrom:  &owner,
		IsStatic:    flags["static"],
		IsAbstract:  !hasBody && !flags["default"] && !flags["static"],
		IsDefault:   flags["default"],
		Visibility:  vis,
		Exported:    exported,
	}
	for _, a := range annotationNames(childByType(m, "modifiers"), st.src) {
		st.pr.AnnoUses = append(st.pr.AnnoUses, typeUse{HolderID: id, TypeName: a})
	}

	var body *javaBody
	if b := m.ChildByFieldName("body"); b != nil {
		body = &javaBody{}
		st.walkBody(b, id, typeFQN, topLevelFQN, body)
	}
	st.pr.Methods = append(st.pr.Methods, MethodParse{Method: method, Body: body})
}

// parseConstructor parses a constructor declaration into a JavaMethod named
// <init>, carrying its parameter signature.
func (st *parseState) parseConstructor(m *sitter.Node, typeFQN, topLevelFQN string) {
	flags := modFlags(childByType(m, "modifiers"))
	vis, exported := visibilityOf(flags)
	params := m.ChildByFieldName("parameters")
	sig := paramSig(params, st.src)
	id := typeFQN + ".<init>(" + sig + ")"
	owner := typeFQN
	method := java.JavaMethod{
		ID:            id,
		Name:          "<init>",
		Input:         paramDefs(params, st.src),
		Throws:        throwsTypes(m, st.src),
		Loc:           loc(st.pr.FileID, m),
		Connections:   map[java.ConnectionKind][]string{},
		MethodFrom:    &owner,
		IsConstructor: true,
		Visibility:    vis,
		Exported:      exported,
	}
	var body *javaBody
	if b := m.ChildByFieldName("body"); b != nil {
		body = &javaBody{}
		st.walkBody(b, id, typeFQN, topLevelFQN, body)
	}
	st.pr.Methods = append(st.pr.Methods, MethodParse{Method: method, Body: body})
}

// parseCompactConstructor parses a record compact constructor: its implicit
// parameters are the record components, so the ID is <init>(componentSig).
func (st *parseState) parseCompactConstructor(m *sitter.Node, typeFQN, topLevelFQN string) {
	idx := indexOfClass(st.pr, typeFQN)
	var comps []java.VariableDefinition
	if idx >= 0 {
		comps = st.pr.Classes[idx].Components
	}
	sig := defsSig(comps)
	id := typeFQN + ".<init>(" + sig + ")"
	owner := typeFQN
	flags := modFlags(childByType(m, "modifiers"))
	vis, exported := visibilityOf(flags)
	method := java.JavaMethod{
		ID:            id,
		Name:          "<init>",
		Input:         comps,
		Loc:           loc(st.pr.FileID, m),
		Connections:   map[java.ConnectionKind][]string{},
		MethodFrom:    &owner,
		IsConstructor: true,
		Visibility:    vis,
		Exported:      exported,
	}
	var body *javaBody
	if b := m.ChildByFieldName("body"); b != nil {
		body = &javaBody{}
		st.walkBody(b, id, typeFQN, topLevelFQN, body)
	}
	st.pr.Methods = append(st.pr.Methods, MethodParse{Method: method, Body: body})
}

// parseAnnotationElement parses an annotation element as a no-arg method node.
func (st *parseState) parseAnnotationElement(m *sitter.Node, ifaceFQN string) {
	name := nodeText(m.ChildByFieldName("name"), st.src)
	if name == "" {
		return
	}
	id := ifaceFQN + "." + name + "()"
	owner := ifaceFQN
	st.pr.Methods = append(st.pr.Methods, MethodParse{Method: java.JavaMethod{
		ID:          id,
		Name:        name,
		Output:      returnDefs(m, st.src),
		Loc:         loc(st.pr.FileID, m),
		Connections: map[java.ConnectionKind][]string{},
		MethodFrom:  &owner,
		IsAbstract:  true,
		Visibility:  "public",
		Exported:    true,
	}})
}

// synthesizeAccessors creates a synthetic no-arg accessor method per record
// component (Loc = record header), unless an explicit accessor already exists.
func (st *parseState) synthesizeAccessors(recNode *sitter.Node, recFQN string, comps []java.VariableDefinition) {
	existing := map[string]bool{}
	for _, mp := range st.pr.Methods {
		if mp.Method.MethodFrom != nil && *mp.Method.MethodFrom == recFQN && len(mp.Method.Input) == 0 {
			existing[mp.Method.Name] = true
		}
	}
	// The accessor is declared by the record header (`record P(int x, int y)`), not by
	// the body: the whole record's span made reading one accessor return every member.
	header := loc(st.pr.FileID, recNode)
	if params := recNode.ChildByFieldName("parameters"); params != nil {
		header.EndsAt = endLine(params)
	}
	for _, c := range comps {
		if c.Name == "" || existing[c.Name] {
			continue
		}
		id := recFQN + "." + c.Name + "()"
		owner := recFQN
		st.pr.Methods = append(st.pr.Methods, MethodParse{Method: java.JavaMethod{
			ID:          id,
			Name:        c.Name,
			Output:      []java.VariableDefinition{{Typing: c.Typing}},
			Loc:         header,
			Connections: map[java.ConnectionKind][]string{},
			MethodFrom:  &owner,
			IsSynthetic: true,
			Visibility:  "public",
			Exported:    true,
		}})
	}
}

// buildInitBlocks merges all static initializers into one <clinit>() and all
// instance initializers into one <instance-init>(), capturing their calls.
func (st *parseState) buildInitBlocks(body *sitter.Node, typeFQN, topLevelFQN string) {
	if body == nil {
		return
	}
	staticBody := &javaBody{}
	instanceBody := &javaBody{}
	hasStatic, hasInstance := false, false
	var staticLoc, instanceLoc domain.Location
	for i := 0; i < int(body.NamedChildCount()); i++ {
		m := body.NamedChild(i)
		switch m.Type() {
		case "static_initializer":
			if blk := childByType(m, "block"); blk != nil {
				if !hasStatic {
					staticLoc = loc(st.pr.FileID, m)
				}
				hasStatic = true
				st.walkBody(blk, typeFQN+".<clinit>()", typeFQN, topLevelFQN, staticBody)
			}
		case "block":
			if !hasInstance {
				instanceLoc = loc(st.pr.FileID, m)
			}
			hasInstance = true
			st.walkBody(m, typeFQN+".<instance-init>()", typeFQN, topLevelFQN, instanceBody)
		}
	}
	owner := typeFQN
	if hasStatic {
		st.pr.Methods = append(st.pr.Methods, MethodParse{Method: java.JavaMethod{
			ID: typeFQN + ".<clinit>()", Name: "<clinit>", Loc: staticLoc,
			Connections: map[java.ConnectionKind][]string{}, MethodFrom: &owner,
			IsStatic: true, IsSynthetic: true, Visibility: "private",
		}, Body: staticBody})
	}
	if hasInstance {
		st.pr.Methods = append(st.pr.Methods, MethodParse{Method: java.JavaMethod{
			ID: typeFQN + ".<instance-init>()", Name: "<instance-init>", Loc: instanceLoc,
			Connections: map[java.ConnectionKind][]string{}, MethodFrom: &owner,
			IsSynthetic: true, Visibility: "private",
		}, Body: instanceBody})
	}
}

// walkBody walks a method/initializer body, recording calls/news/lets attributed
// to methodID and creating anonymous/local class resources (whose own bodies are
// walked separately). Lambda bodies are descended into, attributing to methodID.
func (st *parseState) walkBody(node *sitter.Node, methodID, enclosingTypeFQN, topLevelFQN string, rb *javaBody) {
	if node == nil {
		return
	}
	switch node.Type() {
	case "object_creation_expression":
		typeName := typeText(node.ChildByFieldName("type"), st.src)
		args := node.ChildByFieldName("arguments")
		rb.News = append(rb.News, javaNew{Type: typeName, ArgCount: argCount(args)})
		if cb := childByType(node, "class_body"); cb != nil {
			st.anonCounter++
			anonFQN := topLevelFQN + "$anon" + itoa(st.anonCounter)
			st.pr.AnonEnclosing[anonFQN] = enclosingTypeFQN
			st.buildAnonClass(node, cb, anonFQN, typeName, topLevelFQN)
			if args != nil {
				for i := 0; i < int(args.NamedChildCount()); i++ {
					st.walkBody(args.NamedChild(i), methodID, enclosingTypeFQN, topLevelFQN, rb)
				}
			}
			return
		}
	case "class_declaration", "enum_declaration", "record_declaration", "interface_declaration":
		if c := childByType(node, "class_body"); c != nil || node.ChildByFieldName("body") != nil {
			name := nodeText(node.ChildByFieldName("name"), st.src)
			localFQN := enclosingTypeFQN + "$" + name
			// Disambiguate same-named local classes declared in sibling scopes of
			// the same enclosing type (deterministic, by source order) so neither
			// is silently lost to an ID collision (e.g. `Helper` in two methods).
			base := localFQN
			for n := 2; st.localSeen[localFQN]; n++ {
				localFQN = base + "#" + itoa(n)
			}
			st.localSeen[localFQN] = true
			st.buildLocalClass(node, localFQN, topLevelFQN)
		}
		return
	case "method_invocation":
		rb.Calls = append(rb.Calls, st.classifyInvocation(node))
	case "explicit_constructor_invocation":
		rb.Calls = append(rb.Calls, st.classifyExplicitCtor(node))
	case "method_reference":
		rb.Calls = append(rb.Calls, st.classifyMethodRef(node))
		return
	case "local_variable_declaration":
		st.recordLet(node, rb)
	case "enhanced_for_statement":
		if t := node.ChildByFieldName("type"); t != nil {
			if nm := node.ChildByFieldName("name"); nm != nil {
				rb.Lets = append(rb.Lets, javaLet{Name: nodeText(nm, st.src), DeclType: typeText(t, st.src)})
			}
		}
	case "resource":
		if t := node.ChildByFieldName("type"); t != nil {
			if nm := node.ChildByFieldName("name"); nm != nil {
				rb.Lets = append(rb.Lets, javaLet{Name: nodeText(nm, st.src), DeclType: typeText(t, st.src)})
			}
		}
	case "catch_formal_parameter":
		// A multi-catch (`A | B e`) has no single type to call through.
		if ct := childByType(node, "catch_type"); ct != nil && ct.NamedChildCount() == 1 {
			if nm := node.ChildByFieldName("name"); nm != nil {
				rb.Lets = append(rb.Lets, javaLet{Name: nodeText(nm, st.src), DeclType: typeText(ct.NamedChild(0), st.src), Param: true})
			}
		}
	case "lambda_expression":
		// Explicitly typed lambda parameters type their receivers like method parameters.
		if ps := node.ChildByFieldName("parameters"); ps != nil && ps.Type() == "formal_parameters" {
			for _, p := range paramDefs(ps, st.src) {
				rb.Lets = append(rb.Lets, javaLet{Name: p.Name, DeclType: p.Typing, Param: true})
			}
		}
		if b := node.ChildByFieldName("body"); b != nil {
			st.walkBody(b, methodID, enclosingTypeFQN, topLevelFQN, rb)
		}
		return
	}
	for i := 0; i < int(node.NamedChildCount()); i++ {
		st.walkBody(node.NamedChild(i), methodID, enclosingTypeFQN, topLevelFQN, rb)
	}
}

// buildAnonClass creates an anonymous-class struct that inherits/implements its
// supertype and owns its declared methods.
func (st *parseState) buildAnonClass(node, classBody *sitter.Node, anonFQN, superName, topLevelFQN string) {
	st.pr.Classes = append(st.pr.Classes, java.JavaClass{
		ID:          anonFQN,
		Name:        lastSegDot(anonFQN),
		Loc:         loc(st.pr.FileID, node),
		Connections: map[java.ConnectionKind][]string{},
		IsAnonymous: true,
		Visibility:  "package",
	})
	if superName != "" {
		st.pr.HierRecords = append(st.pr.HierRecords, hierRec{ChildID: anonFQN, ParentName: superName, Kind: hkAnonSuper})
	}
	st.pr.Classes[len(st.pr.Classes)-1].Fields = st.collectFields(classBody, anonFQN)
	st.parseTypeMembers(classBody, anonFQN, topLevelFQN)
	// Double-brace initialization (`new ArrayList<>() {{ add(x); }}`) is an instance
	// initializer of the anonymous class.
	st.buildInitBlocks(classBody, anonFQN, topLevelFQN)
}

// buildLocalClass creates a local (method-scoped) class struct and its members.
func (st *parseState) buildLocalClass(node *sitter.Node, localFQN, topLevelFQN string) {
	mods := childByType(node, "modifiers")
	flags := modFlags(mods)
	vis, exported := visibilityOf(flags)
	cls := java.JavaClass{
		ID:          localFQN,
		Name:        lastSegDot2(localFQN),
		Loc:         loc(st.pr.FileID, node),
		Connections: map[java.ConnectionKind][]string{},
		IsLocal:     true,
		IsEnum:      node.Type() == "enum_declaration",
		IsRecord:    node.Type() == "record_declaration",
		IsAbstract:  flags["abstract"],
		Generics:    typeParamNames(node, st.src),
		Visibility:  vis,
		Exported:    exported,
	}
	if sc := node.ChildByFieldName("superclass"); sc != nil {
		cls.Bases = typeListTypes(sc, st.src)
		for _, t := range cls.Bases {
			st.pr.HierRecords = append(st.pr.HierRecords, hierRec{ChildID: localFQN, ParentName: t, Kind: hkExtendsClass})
		}
	}
	if iflist := node.ChildByFieldName("interfaces"); iflist != nil {
		for _, t := range typeListTypes(iflist, st.src) {
			st.pr.HierRecords = append(st.pr.HierRecords, hierRec{ChildID: localFQN, ParentName: t, Kind: hkImplements})
		}
	}
	body := node.ChildByFieldName("body")
	if body != nil {
		cls.Fields = st.collectFields(body, localFQN)
	}
	st.pr.Classes = append(st.pr.Classes, cls)
	st.parseTypeMembers(body, localFQN, topLevelFQN)
	st.buildInitBlocks(body, localFQN, topLevelFQN)
}

// classifyInvocation classifies a method_invocation into a javaCall.
func (st *parseState) classifyInvocation(n *sitter.Node) javaCall {
	name := nodeText(n.ChildByFieldName("name"), st.src)
	args := n.ChildByFieldName("arguments")
	c := javaCall{Method: name, ArgCount: argCount(args)}
	obj := n.ChildByFieldName("object")
	if obj == nil {
		c.IsSelf = true
		c.Implicit = true
		return c
	}
	switch obj.Type() {
	case "this":
		c.IsSelf = true
	case "super":
		c.IsSuper = true
	case "identifier":
		c.Object = nodeText(obj, st.src)
	case "field_access":
		if o := obj.ChildByFieldName("object"); o != nil && o.Type() == "this" {
			c.FieldRecv = nodeText(obj.ChildByFieldName("field"), st.src)
		}
	case "cast_expression":
		c.CastType = typeText(obj.ChildByFieldName("type"), st.src)
	case "parenthesized_expression":
		if inner := childByType(obj, "cast_expression"); inner != nil {
			c.CastType = typeText(inner.ChildByFieldName("type"), st.src)
		}
	}
	return c
}

// classifyExplicitCtor classifies this(...)/super(...) into a javaCall.
func (st *parseState) classifyExplicitCtor(n *sitter.Node) javaCall {
	c := javaCall{Method: "<init>", ArgCount: argCount(n.ChildByFieldName("arguments"))}
	ctor := n.ChildByFieldName("constructor")
	if ctor != nil && ctor.Type() == "super" {
		c.IsSuper = true
	} else {
		c.IsSelf = true
	}
	return c
}

// classifyMethodRef classifies a method_reference (Type::m / Type::new) into a
// javaCall with unknown arity (matches all overloads).
func (st *parseState) classifyMethodRef(n *sitter.Node) javaCall {
	var ids []*sitter.Node
	for i := 0; i < int(n.NamedChildCount()); i++ {
		ids = append(ids, n.NamedChild(i))
	}
	c := javaCall{ArgCount: -1}
	if len(ids) >= 1 {
		switch ids[0].Type() {
		case "this": // this::m
			c.IsSelf = true
		case "super": // super::m
			c.IsSuper = true
		default:
			c.Object = nodeText(ids[0], st.src)
		}
	}
	if len(ids) >= 2 {
		c.Method = nodeText(ids[len(ids)-1], st.src)
	} else {
		c.Method = "<init>" // Type::new
	}
	return c
}

// recordLet records a local variable declaration's declarators as lets.
func (st *parseState) recordLet(node *sitter.Node, rb *javaBody) {
	declType := typeText(node.ChildByFieldName("type"), st.src)
	for i := 0; i < int(node.NamedChildCount()); i++ {
		d := node.NamedChild(i)
		if d.Type() != "variable_declarator" {
			continue
		}
		l := javaLet{Name: nodeText(d.ChildByFieldName("name"), st.src), DeclType: declType}
		if val := d.ChildByFieldName("value"); val != nil {
			st.classifyLetValue(val, &l)
		}
		rb.Lets = append(rb.Lets, l)
	}
}

// classifyLetValue inspects a let value to provide a fallback variable type.
func (st *parseState) classifyLetValue(val *sitter.Node, l *javaLet) {
	switch val.Type() {
	case "object_creation_expression":
		l.NewType = typeText(val.ChildByFieldName("type"), st.src)
	case "method_invocation":
		l.CallMeth = nodeText(val.ChildByFieldName("name"), st.src)
		l.CallArgs = argCount(val.ChildByFieldName("arguments"))
		if o := val.ChildByFieldName("object"); o != nil && o.Type() == "identifier" {
			l.CallObj = nodeText(o, st.src)
		}
	case "cast_expression":
		l.Cast = typeText(val.ChildByFieldName("type"), st.src)
	}
}

// ---- type/param helpers ----

// typeText returns a type node's raw source text.
func typeText(n *sitter.Node, src []byte) string {
	if n == nil {
		return ""
	}
	return nodeText(n, src)
}

// typeListTypes returns each type's text from a node holding a type_list (or a
// superclass/super_interfaces wrapper).
func typeListTypes(n *sitter.Node, src []byte) []string {
	if n == nil {
		return nil
	}
	tl := n
	if n.Type() != "type_list" {
		if inner := childByType(n, "type_list"); inner != nil {
			tl = inner
		}
	}
	var out []string
	for i := 0; i < int(tl.NamedChildCount()); i++ {
		c := tl.NamedChild(i)
		if isTypeNode(c.Type()) {
			out = append(out, nodeText(c, src))
		}
	}
	if len(out) == 0 && isTypeNode(tl.Type()) {
		out = append(out, nodeText(tl, src))
	}
	return out
}

// isTypeNode reports whether a node type names a Java type reference.
func isTypeNode(t string) bool {
	switch t {
	case "type_identifier", "scoped_type_identifier", "generic_type", "array_type":
		return true
	}
	return false
}

// typeParamNames reads declared type-parameter names (["T", "U"]).
func typeParamNames(n *sitter.Node, src []byte) []string {
	tp := n.ChildByFieldName("type_parameters")
	if tp == nil {
		return nil
	}
	var out []string
	for i := 0; i < int(tp.NamedChildCount()); i++ {
		c := tp.NamedChild(i)
		if c.Type() == "type_parameter" {
			if id := childByType(c, "type_identifier"); id != nil {
				out = append(out, nodeText(id, src))
			}
		}
	}
	return out
}

// typeParamBounds reads the bound types from type_parameter `type_bound`s.
func typeParamBounds(n *sitter.Node, src []byte) []string {
	tp := n.ChildByFieldName("type_parameters")
	if tp == nil {
		return nil
	}
	var out []string
	for i := 0; i < int(tp.NamedChildCount()); i++ {
		c := tp.NamedChild(i)
		if c.Type() != "type_parameter" {
			continue
		}
		if tb := childByType(c, "type_bound"); tb != nil {
			for j := 0; j < int(tb.NamedChildCount()); j++ {
				b := tb.NamedChild(j)
				if isTypeNode(b.Type()) {
					out = append(out, nodeText(b, src))
				}
			}
		}
	}
	return out
}

// paramDefs reads formal parameters (incl. varargs) into variable definitions.
func paramDefs(params *sitter.Node, src []byte) []java.VariableDefinition {
	if params == nil {
		return nil
	}
	var out []java.VariableDefinition
	for i := 0; i < int(params.NamedChildCount()); i++ {
		c := params.NamedChild(i)
		switch c.Type() {
		case "formal_parameter":
			out = append(out, java.VariableDefinition{
				Name:   nodeText(c.ChildByFieldName("name"), src),
				Typing: formalParamType(c, src),
			})
		case "spread_parameter":
			t := firstTypeChild(c)
			name := ""
			if d := childByType(c, "variable_declarator"); d != nil {
				name = nodeText(d.ChildByFieldName("name"), src)
			}
			out = append(out, java.VariableDefinition{Name: name, Typing: typeText(t, src) + "..."})
		}
	}
	return out
}

// formalParamType returns a formal parameter's type text, including array
// dimensions written C-style after the name: `String s[]` is a `String[]`.
func formalParamType(p *sitter.Node, src []byte) string {
	t := typeText(p.ChildByFieldName("type"), src)
	if dims := p.ChildByFieldName("dimensions"); dims != nil {
		for i := 0; i < int(dims.ChildCount()); i++ {
			if dims.Child(i).Type() == "[" {
				t += "[]"
			}
		}
	}
	return t
}

// firstTypeChild returns the first type-ish named child of a node.
func firstTypeChild(n *sitter.Node) *sitter.Node {
	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(i)
		if isTypeNode(c.Type()) || isPrimitiveType(c.Type()) {
			return c
		}
	}
	return nil
}

// isPrimitiveType reports whether a node type names a Java primitive.
func isPrimitiveType(t string) bool {
	switch t {
	case "integral_type", "floating_point_type", "boolean_type", "void_type":
		return true
	}
	return false
}

// paramSig builds the parenthesized-signature body for a formal_parameters node.
func paramSig(params *sitter.Node, src []byte) string {
	if params == nil {
		return ""
	}
	var parts []string
	for i := 0; i < int(params.NamedChildCount()); i++ {
		c := params.NamedChild(i)
		switch c.Type() {
		case "formal_parameter":
			parts = append(parts, sigTypeText(formalParamType(c, src)))
		case "spread_parameter":
			parts = append(parts, sigTypeText(typeText(firstTypeChild(c), src))+"[]")
		}
	}
	return strings.Join(parts, ",")
}

// defsSig builds a signature body from a slice of variable definitions.
func defsSig(defs []java.VariableDefinition) string {
	var parts []string
	for _, d := range defs {
		parts = append(parts, sigTypeText(d.Typing))
	}
	return strings.Join(parts, ",")
}

// returnDefs reads a method/element return type into a single output definition.
func returnDefs(m *sitter.Node, src []byte) []java.VariableDefinition {
	t := m.ChildByFieldName("type")
	if t == nil {
		return nil
	}
	txt := nodeText(t, src)
	if txt == "" {
		return nil
	}
	return []java.VariableDefinition{{Typing: txt}}
}

// throwsTypes reads a method's `throws` clause type names.
func throwsTypes(m *sitter.Node, src []byte) []string {
	th := childByType(m, "throws")
	if th == nil {
		return nil
	}
	var out []string
	for i := 0; i < int(th.NamedChildCount()); i++ {
		c := th.NamedChild(i)
		if isTypeNode(c.Type()) {
			out = append(out, nodeText(c, src))
		}
	}
	return out
}

// argCount counts the arguments in an argument_list node.
func argCount(args *sitter.Node) int {
	if args == nil {
		return 0
	}
	return int(args.NamedChildCount())
}

// ---- string helpers ----

// sigTypeText normalizes a type's text for use in a method-ID signature: strips
// type arguments, keeps array dims, normalizes varargs T... -> T[], and reduces
// to the simple (unqualified) name.
func sigTypeText(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	varargs := strings.HasSuffix(s, "...")
	if varargs {
		s = strings.TrimSpace(strings.TrimSuffix(s, "..."))
	}
	s = stripAngles(s)
	dims := 0
	for {
		s = strings.TrimSpace(s)
		if strings.HasSuffix(s, "[]") {
			s = strings.TrimSuffix(s, "[]")
			dims++
			continue
		}
		break
	}
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "."); i >= 0 {
		s = s[i+1:]
	}
	for i := 0; i < dims; i++ {
		s += "[]"
	}
	if varargs {
		s += "[]"
	}
	return s
}

// stripTypeText reduces a type's text to a qualifier-keeping base (no generics,
// no array dims, no varargs), used for FQN resolution.
func stripTypeText(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, "...")
	s = stripAngles(s)
	for {
		s = strings.TrimSpace(s)
		if strings.HasSuffix(s, "[]") {
			s = strings.TrimSuffix(s, "[]")
			continue
		}
		break
	}
	return strings.TrimSpace(s)
}

// stripAngles removes all balanced <...> type-argument groups from a string.
func stripAngles(s string) string {
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch r {
		case '<':
			depth++
		case '>':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

// simpleNameOf returns the segment after the last '.'.
func simpleNameOf(s string) string {
	if i := strings.LastIndex(s, "."); i >= 0 {
		return s[i+1:]
	}
	return s
}

// typeTail returns the segment after the last '.' or '$' (so nested/local class
// FQNs resolve by their short name).
func typeTail(s string) string {
	idx := -1
	for i := 0; i < len(s); i++ {
		if s[i] == '.' || s[i] == '$' {
			idx = i
		}
	}
	if idx >= 0 {
		return s[idx+1:]
	}
	return s
}

// lastSegDot returns the segment after the last '.' (the simple name).
func lastSegDot(s string) string { return simpleNameOf(s) }

// lastSegDot2 returns the segment after the last '$' or '.' (for local class
// short names).
func lastSegDot2(s string) string { return typeTail(s) }

// dropLastSeg drops the final dotted segment of a path.
func dropLastSeg(s string) string {
	if i := strings.LastIndex(s, "."); i >= 0 {
		return s[:i]
	}
	return s
}

// cleanDoc strips comment delimiters from a javadoc/block comment.
func cleanDoc(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "/**")
	s = strings.TrimPrefix(s, "/*")
	s = strings.TrimSuffix(s, "*/")
	var lines []string
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimSpace(ln)
		ln = strings.TrimPrefix(ln, "*")
		ln = strings.TrimSpace(ln)
		if ln != "" {
			lines = append(lines, ln)
		}
	}
	return strings.Join(lines, " ")
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
