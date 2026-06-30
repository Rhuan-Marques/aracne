package rust

import (
	"testing"

	"aracne/internal/topology/domain"
)

// Locks in the wire-format strings of the Rust connection kinds. These are
// persisted to SQLite and consumed by the shared viz/context-graph, so they must
// stay aligned with the Go/JS spellings.
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
	fn := RustFunction{Connections: map[ConnectionKind][]string{
		ConnCalls:      {"a", "b"},
		ConnUsesStruct: {"S"},
		ConnUsesTrait:  {"T"},
		ConnUsesVar:    {"V"},
		ConnUsesDep:    {"serde"},
	}}
	if got := fn.Calls(); len(got) != 2 || got[0] != "a" {
		t.Errorf("Calls() = %v", got)
	}
	if got := fn.UsesStruct(); len(got) != 1 || got[0] != "S" {
		t.Errorf("UsesStruct() = %v", got)
	}
	if got := fn.UsesTrait(); len(got) != 1 || got[0] != "T" {
		t.Errorf("UsesTrait() = %v", got)
	}
	if got := fn.UsesVar(); len(got) != 1 || got[0] != "V" {
		t.Errorf("UsesVar() = %v", got)
	}
	if got := fn.UsesDep(); len(got) != 1 || got[0] != "serde" {
		t.Errorf("UsesDep() = %v", got)
	}

	s := RustStruct{Connections: map[ConnectionKind][]string{
		ConnHasMethod:  {"S::m"},
		ConnImplements: {"T"},
	}}
	if got := s.Methods(); len(got) != 1 || got[0] != "S::m" {
		t.Errorf("Methods() = %v", got)
	}
	if got := s.Implements(); len(got) != 1 || got[0] != "T" {
		t.Errorf("Implements() = %v", got)
	}

	tr := RustTrait{Connections: map[ConnectionKind][]string{
		ConnImplementedBy: {"S"},
		ConnInherits:      {"Base"},
		ConnInheritedBy:   {"Sub"},
	}}
	if got := tr.ImplementedBy(); len(got) != 1 || got[0] != "S" {
		t.Errorf("ImplementedBy() = %v", got)
	}
	if got := tr.Inherits(); len(got) != 1 || got[0] != "Base" {
		t.Errorf("Inherits() = %v", got)
	}
	if got := tr.InheritedBy(); len(got) != 1 || got[0] != "Sub" {
		t.Errorf("InheritedBy() = %v", got)
	}

	mod := RustModule{Connections: map[ConnectionKind][]string{
		ConnHasFunc:       {"f"},
		ConnHasStruct:     {"S"},
		ConnHasTrait:      {"T"},
		ConnImportsModule: {"/abs/other.rs"},
		ConnImportsDep:    {"serde"},
	}}
	if got := mod.Functions(); len(got) != 1 || got[0] != "f" {
		t.Errorf("Functions() = %v", got)
	}
	if got := mod.Structs(); len(got) != 1 || got[0] != "S" {
		t.Errorf("Structs() = %v", got)
	}
	if got := mod.Traits(); len(got) != 1 || got[0] != "T" {
		t.Errorf("Traits() = %v", got)
	}
	if got := mod.ModulesImported(); len(got) != 1 || got[0] != "/abs/other.rs" {
		t.Errorf("ModulesImported() = %v", got)
	}
	if got := mod.DependenciesImported(); len(got) != 1 || got[0].CratePath != "serde" {
		t.Errorf("DependenciesImported() = %v", got)
	}
}

// Round-trips a representative topology through ToGeneric and FromGeneric and
// checks that the kind-defining flags, properties, and connections survive. This
// is the contract the scanner and manager both depend on.
func TestMapperRoundTrip(t *testing.T) {
	ctorID := "rustfamily::shapes::Circle::new"
	structID := StructID("rustfamily::shapes::Circle")
	methodFrom := structID
	gt := &RustTopology{
		Root: "/abs",
		Functions: map[FunctionID]RustFunction{
			"rustfamily::factory::make_circle": {
				ID:         "rustfamily::factory::make_circle",
				Name:       "make_circle",
				Output:     []VariableDefinition{{Typing: "Circle", TypingID: string(structID)}},
				IsConst:    false,
				Visibility: "public",
				Exported:   true,
				Connections: map[ConnectionKind][]string{
					ConnUsesStruct: {string(structID)},
				},
			},
			ctorID: {
				ID:           ctorID,
				Name:         "new",
				MethodFrom:   &methodFrom,
				IsAssociated: true,
				Receiver:     "",
				Output:       []VariableDefinition{{Typing: "Self", TypingID: string(structID)}},
				Visibility:   "public",
				Exported:     true,
			},
			"rustfamily::macros::make_thing": {
				ID:      "rustfamily::macros::make_thing",
				Name:    "make_thing",
				IsMacro: true,
			},
		},
		Structs: map[StructID]RustStruct{
			structID: {
				ID:          structID,
				Name:        "Circle",
				Fields:      []VariableDefinition{{Name: "r", Typing: "f64"}},
				Constructor: &ctorID,
				Visibility:  "public",
				Exported:    true,
				Connections: map[ConnectionKind][]string{
					ConnHasMethod:  {ctorID},
					ConnImplements: {"rustfamily::shapes::Shape"},
				},
			},
			"rustfamily::geometry::Geometry": {
				ID:       "rustfamily::geometry::Geometry",
				Name:     "Geometry",
				IsEnum:   true,
				Variants: []string{"Round", "Empty"},
			},
		},
		Traits: map[TraitID]RustTrait{
			"rustfamily::shapes::Shape": {
				ID:      "rustfamily::shapes::Shape",
				Name:    "Shape",
				Methods: []FunctionDefinition{{Name: "area", HasDefault: false}},
				Bounds:  []string{},
				Connections: map[ConnectionKind][]string{
					ConnImplementedBy: {string(structID)},
				},
			},
		},
		NamedTypes: map[NamedTypeID]RustNamedType{
			"rustfamily::util::Pair": {ID: "rustfamily::util::Pair", Name: "Pair", Underlying: "(f64, f64)"},
		},
		Variables: map[VariableID]RustVariable{
			"rustfamily::util::math::PI": {ID: "rustfamily::util::math::PI", Name: "PI", IsConst: true, Typing: "f64", Visibility: "crate"},
		},
		Modules: map[ModuleID]RustModule{
			"/abs/src/shapes.rs": {
				ID:         "/abs/src/shapes.rs",
				Name:       "shapes.rs",
				ModulePath: "rustfamily::shapes",
				Connections: map[ConnectionKind][]string{
					ConnHasStruct: {string(structID)},
				},
			},
		},
		Dependencies: []RustDependency{{CratePath: "serde"}},
		Errors:       map[string]string{},
	}

	generic := ToGeneric(gt, "rust")
	if generic.Language != "rust" {
		t.Fatalf("ToGeneric language = %q, want rust", generic.Language)
	}
	// Spot-check kinds in the generic graph.
	if generic.Resources[ctorID].Kind != domain.ResourceMethod {
		t.Errorf("constructor kind = %v, want method", generic.Resources[ctorID].Kind)
	}
	if generic.Resources["rustfamily::macros::make_thing"].Kind != domain.ResourceFunction {
		t.Errorf("macro kind = %v, want function", generic.Resources["rustfamily::macros::make_thing"].Kind)
	}
	if generic.Resources["rustfamily::shapes::Shape"].Kind != domain.ResourceInterface {
		t.Errorf("trait kind = %v, want interface", generic.Resources["rustfamily::shapes::Shape"].Kind)
	}
	if generic.Resources["rustfamily::geometry::Geometry"].Kind != domain.ResourceStruct {
		t.Errorf("enum kind = %v, want struct", generic.Resources["rustfamily::geometry::Geometry"].Kind)
	}
	if generic.Resources["/abs/src/shapes.rs"].Kind != domain.ResourceFile {
		t.Errorf("module kind = %v, want file", generic.Resources["/abs/src/shapes.rs"].Kind)
	}

	back := FromGeneric(generic)

	// Macro flag survives.
	if !back.Functions["rustfamily::macros::make_thing"].IsMacro {
		t.Error("IsMacro lost in round-trip")
	}
	// Associated-fn flag + MethodFrom survive.
	ctor := back.Functions[ctorID]
	if !ctor.IsAssociated || ctor.MethodFrom == nil || *ctor.MethodFrom != methodFrom {
		t.Errorf("constructor associated/method_from lost: %+v", ctor)
	}
	// Return TypingID survives (drives constructor inference).
	mc := back.Functions["rustfamily::factory::make_circle"]
	if len(mc.Output) != 1 || mc.Output[0].TypingID != string(structID) {
		t.Errorf("output TypingID lost: %+v", mc.Output)
	}
	// Struct constructor + implements survive.
	circle := back.Structs[structID]
	if circle.Constructor == nil || *circle.Constructor != ctorID {
		t.Errorf("struct constructor lost: %+v", circle.Constructor)
	}
	if got := circle.Implements(); len(got) != 1 || got[0] != "rustfamily::shapes::Shape" {
		t.Errorf("struct implements lost: %v", got)
	}
	// Enum flag + variants survive.
	geo := back.Structs["rustfamily::geometry::Geometry"]
	if !geo.IsEnum || len(geo.Variants) != 2 || geo.Variants[0] != "Round" {
		t.Errorf("enum flag/variants lost: %+v", geo)
	}
	// Trait implemented_by survives.
	shape := back.Traits["rustfamily::shapes::Shape"]
	if got := shape.ImplementedBy(); len(got) != 1 || got[0] != string(structID) {
		t.Errorf("trait implemented_by lost: %v", got)
	}
	// Named type underlying survives.
	if back.NamedTypes["rustfamily::util::Pair"].Underlying != "(f64, f64)" {
		t.Errorf("named type underlying lost")
	}
	// Variable const flag + module path survive.
	if !back.Variables["rustfamily::util::math::PI"].IsConst {
		t.Errorf("variable const flag lost")
	}
	if back.Modules["/abs/src/shapes.rs"].ModulePath != "rustfamily::shapes" {
		t.Errorf("module path lost")
	}
	// Dependency survives.
	if len(back.Dependencies) != 1 || back.Dependencies[0].CratePath != "serde" {
		t.Errorf("dependency lost: %v", back.Dependencies)
	}
}

// Ensures FromGeneric drops resources tagged as other languages (so a
// multi-language graph doesn't bleed non-Rust nodes into the Rust view).
func TestFromGenericLanguageFilter(t *testing.T) {
	topo := &domain.Topology{
		Language: "multi",
		Resources: map[string]domain.Resource{
			"rustfamily::shapes::Circle": {ID: "rustfamily::shapes::Circle", Kind: domain.ResourceStruct, Name: "Circle", Language: "rust"},
			"pkg.GoStruct":               {ID: "pkg.GoStruct", Kind: domain.ResourceStruct, Name: "GoStruct", Language: "go"},
			"app/x.JsClass":              {ID: "app/x.JsClass", Kind: domain.ResourceStruct, Name: "JsClass", Language: "javascript"},
		},
	}
	gt := FromGeneric(topo)
	if len(gt.Structs) != 1 {
		t.Fatalf("expected 1 rust struct, got %d: %v", len(gt.Structs), gt.Structs)
	}
	if _, ok := gt.Structs["rustfamily::shapes::Circle"]; !ok {
		t.Errorf("rust struct missing after filter")
	}
}
