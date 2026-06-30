package java

import (
	"testing"

	"aracne/internal/topology/domain"
)

// Locks in the wire-format strings of the Java connection kinds. These are
// persisted to SQLite and consumed by the shared viz/context-graph, so they must
// stay aligned with the Go/JS/Rust spellings.
func TestConnectionKindStrings(t *testing.T) {
	cases := map[ConnectionKind]string{
		ConnCalls:         "calls",
		ConnUsesStruct:    "uses_struct",
		ConnUsesNamedType: "uses_named_type",
		ConnUsesTrait:     "uses_interface",
		ConnUsesVar:       "uses_extvar",
		ConnUsesDep:       "uses_dependency",
		ConnHasMethod:     "methods",
		ConnImplements:    "implements",
		ConnImplementedBy: "implemented_by",
		ConnInherits:      "inherits",
		ConnInheritedBy:   "inherited_by",
		ConnConstructor:   "constructor",
		ConnHasFunc:       "has_function",
		ConnHasStruct:     "has_struct",
		ConnHasNamedType:  "has_named_type",
		ConnHasTrait:      "has_interface",
		ConnHasVar:        "has_extvar",
		ConnImportsModule: "imports_module",
		ConnImportsDep:    "imports_dependency",
	}
	for k, want := range cases {
		if string(k) != want {
			t.Errorf("ConnectionKind = %q, want %q", string(k), want)
		}
	}
}

// Verifies the typed connection accessors read the right edge lists.
func TestConnectionAccessors(t *testing.T) {
	m := JavaMethod{Connections: map[ConnectionKind][]string{
		ConnCalls:      {"a", "b"},
		ConnUsesStruct: {"S"},
		ConnUsesTrait:  {"T"},
		ConnUsesDep:    {"java.util"},
	}}
	if got := m.Calls(); len(got) != 2 || got[0] != "a" {
		t.Errorf("Calls() = %v", got)
	}
	if got := m.UsesStruct(); len(got) != 1 || got[0] != "S" {
		t.Errorf("UsesStruct() = %v", got)
	}
	if got := m.UsesInterface(); len(got) != 1 || got[0] != "T" {
		t.Errorf("UsesInterface() = %v", got)
	}
	if got := m.UsesDep(); len(got) != 1 || got[0] != "java.util" {
		t.Errorf("UsesDep() = %v", got)
	}

	s := JavaClass{Connections: map[ConnectionKind][]string{
		ConnHasMethod:   {"com.x.S.m()"},
		ConnImplements:  {"com.x.T"},
		ConnInherits:    {"com.x.Base"},
		ConnInheritedBy: {"com.x.Sub"},
		ConnUsesStruct:  {"com.x.Other"},
		ConnUsesTrait:   {"com.x.Iface"},
	}}
	if got := s.Methods(); len(got) != 1 || got[0] != "com.x.S.m()" {
		t.Errorf("Methods() = %v", got)
	}
	if got := s.Implements(); len(got) != 1 || got[0] != "com.x.T" {
		t.Errorf("Implements() = %v", got)
	}
	if got := s.Inherits(); len(got) != 1 || got[0] != "com.x.Base" {
		t.Errorf("Inherits() = %v", got)
	}
	if got := s.InheritedBy(); len(got) != 1 || got[0] != "com.x.Sub" {
		t.Errorf("InheritedBy() = %v", got)
	}
	if got := s.UsesStruct(); len(got) != 1 || got[0] != "com.x.Other" {
		t.Errorf("class UsesStruct() = %v", got)
	}
	if got := s.UsesInterface(); len(got) != 1 || got[0] != "com.x.Iface" {
		t.Errorf("class UsesInterface() = %v", got)
	}

	iface := JavaInterface{Connections: map[ConnectionKind][]string{
		ConnImplementedBy: {"com.x.Impl"},
		ConnInherits:      {"com.x.Super"},
		ConnInheritedBy:   {"com.x.SubIface"},
	}}
	if got := iface.ImplementedBy(); len(got) != 1 || got[0] != "com.x.Impl" {
		t.Errorf("ImplementedBy() = %v", got)
	}
	if got := iface.Inherits(); len(got) != 1 || got[0] != "com.x.Super" {
		t.Errorf("iface Inherits() = %v", got)
	}
	if got := iface.InheritedBy(); len(got) != 1 || got[0] != "com.x.SubIface" {
		t.Errorf("iface InheritedBy() = %v", got)
	}

	mod := JavaModule{Connections: map[ConnectionKind][]string{
		ConnHasFunc:       {"com.x.S.m()"},
		ConnHasStruct:     {"com.x.S"},
		ConnHasTrait:      {"com.x.T"},
		ConnImportsModule: {"/abs/other.java"},
		ConnImportsDep:    {"java.util"},
	}}
	if got := mod.Functions(); len(got) != 1 || got[0] != "com.x.S.m()" {
		t.Errorf("Functions() = %v", got)
	}
	if got := mod.Structs(); len(got) != 1 || got[0] != "com.x.S" {
		t.Errorf("Structs() = %v", got)
	}
	if got := mod.Interfaces(); len(got) != 1 || got[0] != "com.x.T" {
		t.Errorf("Interfaces() = %v", got)
	}
	if got := mod.ModulesImported(); len(got) != 1 || got[0] != "/abs/other.java" {
		t.Errorf("ModulesImported() = %v", got)
	}
	if got := mod.DependenciesImported(); len(got) != 1 || got[0].Coordinate != "java.util" {
		t.Errorf("DependenciesImported() = %v", got)
	}
}

// Round-trips a representative Java topology through ToGeneric and FromGeneric and
// checks that the kind-defining flags, properties, and connections survive. This
// is the contract the scanner and manager both depend on. It also exercises the
// module-owned structural records (__extends_records / __impl_records), which are
// unrecognized ConnectionKinds that must round-trip untouched.
func TestMapperRoundTrip(t *testing.T) {
	const (
		connExtends = "__extends_records"
		connImpl    = "__impl_records"
	)

	circleID := StructID("com.x.shapes.Circle")
	ctorID := FunctionID("com.x.shapes.Circle.<init>(double)")
	methodFrom := circleID
	enumID := StructID("com.x.enums.Op")
	recordID := StructID("com.x.records.Point")
	annoID := InterfaceID("com.x.ann.Marker")
	shapeID := InterfaceID("com.x.shapes.Shape")
	modID := ModuleID("/abs/src/main/java/com/x/shapes/Circle.java")
	clinitID := FunctionID("com.x.config.Settings.<clinit>()")

	gt := &JavaTopology{
		Root: "/abs",
		Methods: map[FunctionID]JavaMethod{
			ctorID: {
				ID:            ctorID,
				Name:          "<init>",
				MethodFrom:    &methodFrom,
				Input:         []VariableDefinition{{Name: "radius", Typing: "double"}},
				Throws:        []string{"IllegalArgumentException"},
				IsConstructor: true,
				Visibility:    "public",
				Exported:      true,
			},
			"com.x.factory.Factory.makeCircle(double)": {
				ID:         "com.x.factory.Factory.makeCircle(double)",
				Name:       "makeCircle",
				MethodFrom: ptr(StructID("com.x.factory.Factory")),
				Output:     []VariableDefinition{{Typing: "Shape", TypingID: string(shapeID)}},
				IsStatic:   true,
				Visibility: "public",
				Exported:   true,
				Connections: map[ConnectionKind][]string{
					ConnUsesStruct: {string(circleID)},
				},
			},
			clinitID: {
				ID:          clinitID,
				Name:        "<clinit>",
				MethodFrom:  ptr(StructID("com.x.config.Settings")),
				IsSynthetic: true,
				Visibility:  "private",
			},
			"com.x.inh.Base.rank()": {
				ID:         "com.x.inh.Base.rank()",
				Name:       "rank",
				MethodFrom: ptr(StructID("com.x.inh.Base")),
				IsAbstract: true,
				Visibility: "public",
				Exported:   true,
			},
		},
		Classes: map[StructID]JavaClass{
			circleID: {
				ID:          circleID,
				Name:        "Circle",
				Fields:      []VariableDefinition{{Name: "radius", Typing: "double"}},
				Constructor: &ctorID,
				Generics:    []string{"T"},
				Visibility:  "public",
				Exported:    true,
				Connections: map[ConnectionKind][]string{
					ConnHasMethod:  {string(ctorID)},
					ConnImplements: {string(shapeID)},
				},
			},
			enumID: {
				ID:       enumID,
				Name:     "Op",
				IsEnum:   true,
				Variants: []string{"PLUS", "MINUS"},
			},
			recordID: {
				ID:         recordID,
				Name:       "Point",
				IsRecord:   true,
				Components: []VariableDefinition{{Name: "x", Typing: "int"}, {Name: "y", Typing: "int"}},
			},
			"com.x.inh.Base": {
				ID:         "com.x.inh.Base",
				Name:       "Base",
				IsAbstract: true,
			},
			"com.x.sealed.Expr": {
				ID:       "com.x.sealed.Expr",
				Name:     "Expr",
				IsSealed: true,
				IsStatic: true,
				IsFinal:  true,
				Permits:  []string{"com.x.sealed.Lit", "com.x.sealed.Neg"},
			},
			"com.x.nested.Outer$anon1": {
				ID:          "com.x.nested.Outer$anon1",
				Name:        "anon1",
				IsAnonymous: true,
			},
			"com.x.nested.Outer$Helper": {
				ID:      "com.x.nested.Outer$Helper",
				Name:    "Helper",
				IsLocal: true,
			},
		},
		Interfaces: map[InterfaceID]JavaInterface{
			shapeID: {
				ID:      string(shapeID),
				Name:    "Shape",
				Methods: []FunctionDefinition{{Name: "area", IsAbstract: true}},
				Connections: map[ConnectionKind][]string{
					ConnImplementedBy: {string(circleID)},
				},
			},
			annoID: {
				ID:           string(annoID),
				Name:         "Marker",
				IsAnnotation: true,
				Generics:     []string{},
			},
		},
		Modules: map[ModuleID]JavaModule{
			modID: {
				ID:      modID,
				Name:    "Circle.java",
				Package: "com.x.shapes",
				Connections: map[ConnectionKind][]string{
					ConnHasStruct: {string(circleID)},
					// Structural records owned by the module: these are
					// unrecognized ConnectionKinds and must survive the round
					// trip untouched (they drive whole-graph hierarchy rebuild).
					connExtends: {"com.x.inh.Derived=>>com.x.inh.Base:class"},
					connImpl:    {"com.x.shapes.Circle=>>com.x.shapes.Shape"},
				},
			},
		},
		Dependencies: []JavaDependency{{Coordinate: "java.util"}},
		Errors:       map[string]string{},
	}

	generic := ToGeneric(gt, "java")
	if generic.Language != "java" {
		t.Fatalf("ToGeneric language = %q, want java", generic.Language)
	}
	// Spot-check kinds in the generic graph.
	if generic.Resources[string(ctorID)].Kind != domain.ResourceMethod {
		t.Errorf("constructor kind = %v, want method", generic.Resources[string(ctorID)].Kind)
	}
	if generic.Resources[string(shapeID)].Kind != domain.ResourceInterface {
		t.Errorf("interface kind = %v, want interface", generic.Resources[string(shapeID)].Kind)
	}
	if generic.Resources[string(enumID)].Kind != domain.ResourceStruct {
		t.Errorf("enum kind = %v, want struct", generic.Resources[string(enumID)].Kind)
	}
	if generic.Resources[string(recordID)].Kind != domain.ResourceStruct {
		t.Errorf("record kind = %v, want struct", generic.Resources[string(recordID)].Kind)
	}
	if generic.Resources[string(modID)].Kind != domain.ResourceFile {
		t.Errorf("module kind = %v, want file", generic.Resources[string(modID)].Kind)
	}
	if generic.Resources["java.util"].Kind != domain.ResourceDependency {
		t.Errorf("dependency kind = %v, want dependency", generic.Resources["java.util"].Kind)
	}

	back := FromGeneric(generic)

	// Constructor flags + MethodFrom + throws survive.
	ctor := back.Methods[ctorID]
	if !ctor.IsConstructor || ctor.MethodFrom == nil || *ctor.MethodFrom != methodFrom {
		t.Errorf("constructor flags/method_from lost: %+v", ctor)
	}
	if len(ctor.Throws) != 1 || ctor.Throws[0] != "IllegalArgumentException" {
		t.Errorf("constructor throws lost: %v", ctor.Throws)
	}
	if len(ctor.Input) != 1 || ctor.Input[0].Name != "radius" || ctor.Input[0].Typing != "double" {
		t.Errorf("constructor input lost: %+v", ctor.Input)
	}

	// Static flag + return TypingID survive (drives factory-return inference).
	mc := back.Methods["com.x.factory.Factory.makeCircle(double)"]
	if !mc.IsStatic {
		t.Error("method IsStatic lost")
	}
	if len(mc.Output) != 1 || mc.Output[0].TypingID != string(shapeID) {
		t.Errorf("output TypingID lost: %+v", mc.Output)
	}

	// Synthetic + abstract method flags survive.
	if !back.Methods[clinitID].IsSynthetic {
		t.Error("method IsSynthetic lost")
	}
	if !back.Methods["com.x.inh.Base.rank()"].IsAbstract {
		t.Error("method IsAbstract lost")
	}

	// Class constructor link + implements + generics survive.
	circle := back.Classes[circleID]
	if circle.Constructor == nil || *circle.Constructor != ctorID {
		t.Errorf("class constructor lost: %+v", circle.Constructor)
	}
	if got := circle.Implements(); len(got) != 1 || got[0] != string(shapeID) {
		t.Errorf("class implements lost: %v", got)
	}
	if len(circle.Generics) != 1 || circle.Generics[0] != "T" {
		t.Errorf("class generics lost: %v", circle.Generics)
	}

	// Enum flag + variants survive.
	op := back.Classes[enumID]
	if !op.IsEnum || len(op.Variants) != 2 || op.Variants[0] != "PLUS" {
		t.Errorf("enum flag/variants lost: %+v", op)
	}
	// Record flag + components survive.
	pt := back.Classes[recordID]
	if !pt.IsRecord || len(pt.Components) != 2 || pt.Components[0].Name != "x" {
		t.Errorf("record flag/components lost: %+v", pt)
	}
	// Abstract / sealed / final / static / anonymous / local flags survive.
	if !back.Classes["com.x.inh.Base"].IsAbstract {
		t.Error("class IsAbstract lost")
	}
	sealed := back.Classes["com.x.sealed.Expr"]
	if !sealed.IsSealed || !sealed.IsFinal || !sealed.IsStatic {
		t.Errorf("class sealed/final/static flags lost: %+v", sealed)
	}
	if len(sealed.Permits) != 2 || sealed.Permits[0] != "com.x.sealed.Lit" {
		t.Errorf("class permits lost: %v", sealed.Permits)
	}
	if !back.Classes["com.x.nested.Outer$anon1"].IsAnonymous {
		t.Error("class IsAnonymous lost")
	}
	if !back.Classes["com.x.nested.Outer$Helper"].IsLocal {
		t.Error("class IsLocal lost")
	}

	// Interface implemented_by + annotation flag + method summaries survive.
	shape := back.Interfaces[shapeID]
	if got := shape.ImplementedBy(); len(got) != 1 || got[0] != string(circleID) {
		t.Errorf("interface implemented_by lost: %v", got)
	}
	if len(shape.Methods) != 1 || shape.Methods[0].Name != "area" {
		t.Errorf("interface method summaries lost: %+v", shape.Methods)
	}
	if !back.Interfaces[annoID].IsAnnotation {
		t.Error("interface IsAnnotation lost")
	}

	// Module package + the module-owned structural records survive untouched.
	mod := back.Modules[modID]
	if mod.Package != "com.x.shapes" {
		t.Errorf("module package lost: %q", mod.Package)
	}
	if got := mod.Connections[connExtends]; len(got) != 1 || got[0] != "com.x.inh.Derived=>>com.x.inh.Base:class" {
		t.Errorf("__extends_records not preserved: %v", got)
	}
	if got := mod.Connections[connImpl]; len(got) != 1 || got[0] != "com.x.shapes.Circle=>>com.x.shapes.Shape" {
		t.Errorf("__impl_records not preserved: %v", got)
	}

	// Dependency survives.
	if len(back.Dependencies) != 1 || back.Dependencies[0].Coordinate != "java.util" {
		t.Errorf("dependency lost: %v", back.Dependencies)
	}
}

// Ensures FromGeneric drops resources tagged as other languages (so a
// multi-language graph does not bleed non-Java nodes into the Java view).
func TestFromGenericLanguageFilter(t *testing.T) {
	topo := &domain.Topology{
		Language: "multi",
		Resources: map[string]domain.Resource{
			"com.x.shapes.Circle": {ID: "com.x.shapes.Circle", Kind: domain.ResourceStruct, Name: "Circle", Language: "java"},
			"pkg.GoStruct":        {ID: "pkg.GoStruct", Kind: domain.ResourceStruct, Name: "GoStruct", Language: "go"},
			"app/x.JsClass":       {ID: "app/x.JsClass", Kind: domain.ResourceStruct, Name: "JsClass", Language: "javascript"},
		},
	}
	gt := FromGeneric(topo)
	if len(gt.Classes) != 1 {
		t.Fatalf("expected 1 java class, got %d: %v", len(gt.Classes), gt.Classes)
	}
	if _, ok := gt.Classes["com.x.shapes.Circle"]; !ok {
		t.Errorf("java class missing after filter")
	}
}

func ptr[T any](v T) *T { return &v }
