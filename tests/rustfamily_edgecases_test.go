package tests_test

// Rust-family edge-case correctness suite.
//
// Each subtest asserts the IDEAL topology the scanner SHOULD produce for a Rust
// construct in testing_ground/rustfamily. Subtests commented `// OK` lock in
// behavior that already works (regression guard, green). Subtests commented
// `// GAP (rust_NN)` assert the ideal behavior and are EXPECTED TO STAY RED
// until the scanner is fixed — they document a confirmed gap (filed as a bug,
// see redprobes.rs). This mirrors the JS/Python edge-case suites.
//
// The corpus is scanned IN-MEMORY via rustscanner.NewRustScanner().Scan, so no
// SQLite write happens. Resource IDs are rooted at the Cargo package name
// ("rustfamily"), so every assertion matches by SUFFIX (shared jt* helpers).

import (
	"path/filepath"
	"testing"

	"aracne/internal/topology/domain"
	"aracne/internal/topology/scanner/rustscanner"
)

// rtScan scans testing_ground/rustfamily in-memory and returns the topology.
func rtScan(t *testing.T) *domain.Topology {
	t.Helper()
	dir := filepath.Join(projectRoot(), "testing_ground", "rustfamily")
	topo, err := rustscanner.NewRustScanner().Scan(dir)
	if err != nil {
		t.Fatalf("scan rustfamily: %v", err)
	}
	return topo
}

func TestRustFamilyEdgecases(t *testing.T) {
	t.Parallel()
	topo := rtScan(t)

	// ---- Resource kinds (OK) ----

	t.Run("trait_is_interface_kind", func(t *testing.T) { // OK
		jtWantKind(t, topo, "::shapes::Shape", domain.ResourceInterface)
	})
	t.Run("struct_is_struct_kind", func(t *testing.T) { // OK
		jtWantKind(t, topo, "::shapes::Circle", domain.ResourceStruct)
	})
	t.Run("enum_is_struct_kind_with_variants", func(t *testing.T) { // OK
		jtWantKind(t, topo, "::geometry::Geometry", domain.ResourceStruct)
		geo, ok := jtFind(topo, "::geometry::Geometry")
		if !ok {
			t.Fatal("Geometry not found")
		}
		if ie, _ := geo.Properties["is_enum"].(bool); !ie {
			t.Errorf("Geometry should have is_enum=true, got %v", geo.Properties["is_enum"])
		}
		// Variants modeled as a property list (per the decided enum model). The
		// in-memory scan keeps the native []string (a DB round-trip would yield []any).
		vs, _ := geo.Properties["variants"].([]string)
		if len(vs) != 2 {
			t.Errorf("Geometry should have 2 variants, got %v", geo.Properties["variants"])
		}
	})
	t.Run("tuple_unit_newtype_structs", func(t *testing.T) { // OK
		jtWantKind(t, topo, "::shapes::Meters", domain.ResourceStruct)
		jtWantKind(t, topo, "::shapes::Origin", domain.ResourceStruct)
		jtWantKind(t, topo, "::shapes::Disk", domain.ResourceStruct)
	})
	t.Run("type_alias_is_named_type", func(t *testing.T) { // OK
		jtWantKind(t, topo, "::shapes::Radius", domain.ResourceNamedType)
		jtWantKind(t, topo, "::factory::ShapeFactory", domain.ResourceNamedType)
	})
	t.Run("const_is_variable", func(t *testing.T) { // OK
		jtWantKind(t, topo, "::errors::NOT_FOUND", domain.ResourceVariable)
	})

	// ---- Methods & impls (OK) ----

	t.Run("inherent_methods_attach", func(t *testing.T) { // OK
		jtWantKind(t, topo, "::shapes::Circle::new", domain.ResourceMethod)
		jtWantConn(t, topo, "::shapes::Circle", "methods", "::shapes::Circle::new")
		jtWantConn(t, topo, "::shapes::Circle", "methods", "::shapes::Circle::diameter")
	})
	t.Run("constructor_detected", func(t *testing.T) { // OK
		c, ok := jtFind(topo, "::shapes::Circle")
		if !ok {
			t.Fatal("Circle not found")
		}
		if ctor, _ := c.Properties["constructor"].(string); ctor == "" {
			t.Error("Circle should record a constructor (Circle::new)")
		}
	})
	t.Run("inherent_and_trait_method_dedup", func(t *testing.T) { // OK
		// Circle has BOTH an inherent `area` and a trait-impl `area`; they share
		// the same ID and de-dup to a single node (no UNIQUE-constraint clash).
		jtWantKind(t, topo, "::shapes::Circle::area", domain.ResourceMethod)
	})
	t.Run("trait_impl_same_file", func(t *testing.T) { // OK
		jtWantConn(t, topo, "::shapes::Circle", "implements", "::shapes::Shape")
		jtWantConn(t, topo, "::shapes::Shape", "implemented_by", "::shapes::Circle")
	})
	t.Run("trait_impl_cross_module", func(t *testing.T) { // OK
		// impl Shape for Geometry lives in geometry.rs; Shape in shapes.rs.
		jtWantConn(t, topo, "::geometry::Geometry", "implements", "::shapes::Shape")
		jtWantConn(t, topo, "::shapes::Shape", "implemented_by", "::geometry::Geometry")
	})
	t.Run("supertrait_inherits", func(t *testing.T) { // OK
		// trait Drawable: Shape  -> Drawable inherits Shape (cross-module).
		jtWantConn(t, topo, "::traits::Drawable", "inherits", "::shapes::Shape")
		jtWantConn(t, topo, "::shapes::Shape", "inherited_by", "::traits::Drawable")
	})

	// ---- Calls, imports, deps (OK) ----

	t.Run("flagship_consumer_resolves", func(t *testing.T) { // OK
		jtWantConn(t, topo, "::consumer::report", "calls", "::shapes::Circle::new")
		jtWantConn(t, topo, "::consumer::report", "calls", "::factory::make_circle")
		jtWantConn(t, topo, "::consumer::report", "calls", "::shapes::Circle::area")
		jtWantConn(t, topo, "::consumer::report", "uses_struct", "::shapes::Circle")
	})
	t.Run("factory_constructor_chain", func(t *testing.T) { // OK
		jtWantConn(t, topo, "::factory::make_circle", "calls", "::shapes::Circle::new")
	})
	t.Run("enum_assoc_fn_calls_ctor", func(t *testing.T) { // OK
		jtWantConn(t, topo, "::geometry::Geometry::round", "calls", "::shapes::Circle::new")
	})
	t.Run("error_propagation_call", func(t *testing.T) { // OK
		jtWantConn(t, topo, "::errors::load", "calls", "::errors::validate")
		jtWantConn(t, topo, "::errors::validate", "uses_struct", "::shapes::Circle")
	})
	t.Run("internal_use_imports_module", func(t *testing.T) { // OK
		jtWantConn(t, topo, "consumer.rs", "imports_module", "shapes.rs")
		jtWantConn(t, topo, "consumer.rs", "imports_module", "factory.rs")
	})
	t.Run("external_use_is_dependency", func(t *testing.T) { // OK
		jtWantRes(t, topo, "serde")
		jtWantConn(t, topo, "errors.rs", "imports_dependency", "serde")
		// Must NOT become a false internal module import.
		if jtConnTo(topo, "errors.rs", "imports_module", "serde") {
			t.Error("serde must not be an internal imports_module edge")
		}
	})
	t.Run("redprobe_partial_resolution_ok", func(t *testing.T) { // OK
		// The direct calls in the red-probe file DO resolve, even though the
		// chained method calls do not.
		jtWantConn(t, topo, "::redprobes::via_trait_object", "calls", "::factory::make_shape")
		jtWantConn(t, topo, "::redprobes::via_question", "calls", "::errors::load")
	})

	// ---- GAPs (RED, expected to fail until the scanner improves) ----

	t.Run("trait_object_method_resolves", func(t *testing.T) { // GAP (rust_01)
		// `let s: Box<dyn Shape> = make_shape(); s.area()` — concrete type
		// behind the trait object is unknown, so area() does not resolve.
		jtWantConn(t, topo, "::redprobes::via_trait_object", "calls", "::shapes::Circle::area")
	})
	t.Run("generic_param_method_resolves", func(t *testing.T) { // GAP (rust_02)
		// `fn render<T: Shape>(t: T) { t.area() }` — T is a type parameter.
		jtWantConn(t, topo, "::redprobes::render", "calls", "::shapes::Circle::area")
	})
	t.Run("question_unwrap_method_resolves", func(t *testing.T) { // GAP (rust_03)
		// `let c = load()?; c.area()` — the `?` result type is not tracked.
		jtWantConn(t, topo, "::redprobes::via_question", "calls", "::shapes::Circle::area")
	})
	t.Run("closure_param_method_resolves", func(t *testing.T) { // GAP (rust_04)
		// `.map(|x| x.area())` — the closure param carries no tracked type.
		jtWantConn(t, topo, "::redprobes::via_iterator", "calls", "::shapes::Circle::area")
	})
	t.Run("macro_tokentree_call_resolves", func(t *testing.T) { // GAP (rust_05)
		// `vec![Circle::new(1.0)]` — calls inside a macro token-tree are unparsed.
		jtWantConn(t, topo, "::redprobes::via_iterator", "calls", "::shapes::Circle::new")
	})
}
