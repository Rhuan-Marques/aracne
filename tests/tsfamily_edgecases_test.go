package tests_test

// TypeScript-family edge-case correctness suite.
//
// Same convention as jsfamily_edgecases_test.go: `// OK` subtests lock in
// working behavior (green), `// GAP (bug …_NN)` subtests assert the ideal
// behavior and are EXPECTED TO STAY RED until the scanner is fixed (each maps to
// a filed bug). Scanned in-memory over testing_ground/tsfamily, so all matches
// are by SUFFIX (resource IDs are prefixed with "tsfamily/"). Shared helpers
// (jtScan/jtFind/jtWantConn/…) live in jsfamily_edgecases_test.go.

import (
	"testing"

	"aracne/internal/topology/domain"
)

func TestTSFamilyEdgecases(t *testing.T) {
	t.Parallel()
	topo := jtScan(t, "tsfamily")

	// OK: every fixture parses without recording a scanner error.
	t.Run("no_parse_errors", func(t *testing.T) {
		if len(topo.Errors) != 0 {
			t.Errorf("expected no parse errors, got %v", topo.Errors)
		}
	})

	// ---- declaration merging --------------------------------------------
	t.Run("declaration_merging", func(t *testing.T) {
		// OK: function/method overloads (N signatures + 1 impl) collapse to a
		// single resource without aborting the write.
		t.Run("function_overloads_single_resource", func(t *testing.T) {
			jtWantKind(t, topo, "overloads.area", domain.ResourceFunction)
		})
		t.Run("method_overloads_single_resource", func(t *testing.T) {
			jtWantRes(t, topo, "overloads.Calc.add")
		})
		// OK: a function/class survives merging with a same-name namespace.
		t.Run("function_namespace_merge_keeps_function", func(t *testing.T) {
			jtWantKind(t, topo, "merge_ns.widget", domain.ResourceFunction)
		})
		t.Run("class_namespace_merge_keeps_class", func(t *testing.T) {
			jtWantKind(t, topo, "merge_ns.Holder", domain.ResourceType)
			jtWantRes(t, topo, "merge_ns.Holder.value")
		})
		// GAP (bug …_20): a class merged with a same-name interface is OVERWRITTEN
		// by the interface — the class (and its methods) are lost.
		t.Run("class_interface_merge_keeps_class", func(t *testing.T) {
			jtWantKind(t, topo, "merge_iface.Combo", domain.ResourceType)
		})
	})

	// ---- annotation-driven resolution boundaries ------------------------
	t.Run("resolution", func(t *testing.T) {
		// GAP (bug …_21): inline `f().method()` does not follow f's return type.
		t.Run("inline_return_chain_resolves_method", func(t *testing.T) {
			jtWantConn(t, topo, "resolution.viaReturn", "calls", "shapes.Circle.area")
		})
		// GAP (bug …_22): a type alias of a class is not followed.
		t.Run("type_alias_indirection_resolves", func(t *testing.T) {
			jtWantConn(t, topo, "resolution.aliasArea", "calls", "shapes.Circle.area")
		})
		// GAP (bug …_23): a union-typed parameter resolves no member.
		t.Run("union_param_resolves_member", func(t *testing.T) {
			jtWantConn(t, topo, "resolution.unionArea", "calls", "shapes.Circle.area")
		})
		// GAP (bug …_24): an `as` cast does not drive resolution.
		t.Run("as_cast_resolves_method", func(t *testing.T) {
			jtWantConn(t, topo, "resolution.castArea", "calls", "shapes.Circle.area")
		})
	})

	// ---- generics --------------------------------------------------------
	t.Run("generics", func(t *testing.T) {
		// OK: generic instantiation resolves the class and the (annotated) .get().
		t.Run("generic_instantiation_uses_classes", func(t *testing.T) {
			jtWantConn(t, topo, "generics_adv.unwrap", "uses_class", "shapes.Box")
			jtWantConn(t, topo, "generics_adv.unwrap", "uses_class", "shapes.Circle")
		})
		t.Run("generic_get_method_resolved", func(t *testing.T) {
			jtWantAnyConn(t, topo, "generics_adv.unwrap", "shapes.Box.get")
		})
		// GAP (bug …_25): the generic type argument (Box<Circle>.get() -> Circle)
		// is not propagated, so the chained `.area()` is unresolved.
		t.Run("generic_return_type_propagated", func(t *testing.T) {
			jtWantConn(t, topo, "generics_adv.unwrap", "calls", "shapes.Circle.area")
		})
	})

	// ---- type-only imports ----------------------------------------------
	t.Run("type_only", func(t *testing.T) {
		// OK: a type-only import still drives annotation-based resolution.
		t.Run("type_only_import_drives_resolution", func(t *testing.T) {
			jtWantAnyConn(t, topo, "typeonly.describe", "models.Shape")
		})
	})

	// ---- decorators ------------------------------------------------------
	t.Run("decorators", func(t *testing.T) {
		// OK: a method decorator does not block constructor resolution.
		t.Run("method_decorator_keeps_uses_class", func(t *testing.T) {
			jtWantConn(t, topo, "decorators.Service.build", "uses_class", "shapes.Circle")
		})
		// OK: a parameter decorator does not block param-type method resolution.
		t.Run("param_decorator_keeps_param_resolution", func(t *testing.T) {
			jtWantConn(t, topo, "decorators.Service.run", "calls", "shapes.Circle.area")
		})
	})

	// ---- interfaces ------------------------------------------------------
	t.Run("interfaces", func(t *testing.T) {
		// OK: a class implementing MULTIPLE interfaces links to all of them.
		t.Run("multi_implements_links_all", func(t *testing.T) {
			jtWantConn(t, topo, "interfaces_adv.IA", "implemented_by", "interfaces_adv.Impl")
			jtWantConn(t, topo, "interfaces_adv.IB", "implemented_by", "interfaces_adv.Impl")
		})
		// OK: an interface extending MULTIPLE interfaces references all bases.
		t.Run("interface_extends_multiple", func(t *testing.T) {
			jtWantAnyConn(t, topo, "interfaces_adv.IAB", "interfaces_adv.IA")
			jtWantAnyConn(t, topo, "interfaces_adv.IAB", "interfaces_adv.IB")
		})
	})

	// ---- enums -----------------------------------------------------------
	t.Run("enums", func(t *testing.T) {
		// OK: a string enum is extracted as a resource.
		t.Run("string_enum_extracted", func(t *testing.T) {
			jtWantRes(t, topo, "enums_adv.Status")
		})
	})

	// ---- namespaces ------------------------------------------------------
	t.Run("namespaces", func(t *testing.T) {
		// GAP (bug …_27): a namespace-qualified type annotation (Geo.Point) is not
		// resolved, so the method call on it is unresolved.
		t.Run("qualified_type_annotation_resolves", func(t *testing.T) {
			jtWantConn(t, topo, "namespaces_adv.useQualified", "calls", "Geo.Point.dist")
		})
		// GAP (bug …_28): a qualified call into a nested namespace is not resolved.
		t.Run("nested_namespace_call_resolves", func(t *testing.T) {
			jtWantConn(t, topo, "namespaces_adv.A.viaNested", "calls", "A.B.deep")
		})
	})

	// ---- TS import-equals + ambient -------------------------------------
	t.Run("import_equals_and_ambient", func(t *testing.T) {
		// GAP (bug …_29): `import x = require(...)` binding is not tracked.
		t.Run("import_equals_resolves_member", func(t *testing.T) {
			jtWantConn(t, topo, "importeq.viaEquals", "uses_class", "shapes.Circle")
		})
		// OK: a `declare module "..."` extracts its exported declarations.
		t.Run("ambient_module_class_extracted", func(t *testing.T) {
			jtWantRes(t, topo, "VirtualCircle")
		})
		// GAP (bug …_30): members of `declare global { ... }` are dropped.
		t.Run("declare_global_members_extracted", func(t *testing.T) {
			jtWantRes(t, topo, "globalHelper")
		})
	})

	// ---- type guards -----------------------------------------------------
	t.Run("type_guards", func(t *testing.T) {
		// OK: calling a user-defined type guard resolves the call.
		t.Run("type_guard_call_resolved", func(t *testing.T) {
			jtWantConn(t, topo, "predicates.areaIfCircle", "calls", "predicates.isCircle")
		})
		// GAP (bug …_23): after narrowing via the guard, the method call on the
		// narrowed union value is not resolved.
		t.Run("narrowed_method_call_resolved", func(t *testing.T) {
			jtWantConn(t, topo, "predicates.areaIfCircle", "calls", "shapes.Circle.area")
		})
	})
}
