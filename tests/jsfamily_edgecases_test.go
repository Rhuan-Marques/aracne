package tests_test

// JavaScript-family edge-case correctness suite.
//
// Each subtest asserts the IDEAL topology the scanner SHOULD produce for a JS
// construct in testing_ground/jsfamily. Subtests commented `// OK` lock in
// behavior that already works (regression guard, green). Subtests commented
// `// GAP (bug …_NN)` assert the ideal behavior and are EXPECTED TO STAY RED
// until the scanner is fixed — they document a confirmed gap that was filed as a
// bug (see the `_NN` suffix of the bug ID). This mirrors the Python edge-case
// suite (tests/python_edgecases_test.go), which is also deliberately left red.
//
// The corpus is scanned IN-MEMORY via jsscanner.NewJavaScriptScanner().Scan, so
// no SQLite write happens and the unrelated, pre-existing multi-language
// scan-write abort cannot interfere. Resource IDs are therefore prefixed with
// the scanned dir base name ("jsfamily/"), so every assertion matches by SUFFIX.

import (
	"path/filepath"
	"strings"
	"testing"

	"aracne/internal/topology/domain"
	"aracne/internal/topology/scanner/jsscanner"
)

// ---------------------------------------------------------------------------
// Shared in-memory scan + suffix-matching helpers (used by both the JS and TS
// edge-case suites; defined once here).
// ---------------------------------------------------------------------------

// jtScan scans testing_ground/<family> in-memory and returns the topology.
func jtScan(t *testing.T, family string) *domain.Topology {
	t.Helper()
	dir := filepath.Join(projectRoot(), "testing_ground", family)
	// The shared ECMAScript scanner walks a fixed extension set per language, so
	// the TS family needs the TypeScript constructor (.ts/.tsx/.mts/.cts) — the
	// JS one only sees .js/.jsx/.cjs and would return an empty topology.
	scanner := jsscanner.NewJavaScriptScanner()
	if family == "tsfamily" {
		scanner = jsscanner.NewTypeScriptScanner()
	}
	topo, err := scanner.Scan(dir)
	if err != nil {
		t.Fatalf("scan %s: %v", family, err)
	}
	return topo
}

// jtFind returns the unique resource whose ID ends with idSuffix.
func jtFind(topo *domain.Topology, idSuffix string) (domain.Resource, bool) {
	var hit domain.Resource
	count := 0
	for id := range topo.Resources {
		if strings.HasSuffix(id, idSuffix) {
			hit = topo.Resources[id]
			count++
		}
	}
	return hit, count == 1
}

// jtConnTo reports whether the resource ending idSuffix has a `conn` edge whose
// target ends with targetSuffix.
func jtConnTo(topo *domain.Topology, idSuffix, conn, targetSuffix string) bool {
	r, ok := jtFind(topo, idSuffix)
	if !ok {
		return false
	}
	for _, tg := range r.Connections[conn] {
		if strings.HasSuffix(tg, targetSuffix) {
			return true
		}
	}
	return false
}

// jtAnyConnTo reports whether the resource ending idSuffix has ANY edge (of any
// kind) whose target ends with targetSuffix.
func jtAnyConnTo(topo *domain.Topology, idSuffix, targetSuffix string) bool {
	r, ok := jtFind(topo, idSuffix)
	if !ok {
		return false
	}
	for _, targets := range r.Connections {
		for _, tg := range targets {
			if strings.HasSuffix(tg, targetSuffix) {
				return true
			}
		}
	}
	return false
}

// jtCountKindInFile counts resources of `kind` whose ID contains fileToken.
func jtCountKindInFile(topo *domain.Topology, fileToken string, kind domain.ResourceKind) int {
	n := 0
	for id := range topo.Resources {
		if topo.Resources[id].Kind == kind && strings.Contains(id, fileToken) {
			n++
		}
	}
	return n
}

// jtWantConn asserts the edge exists (green lock-in, or red gap).
func jtWantConn(t *testing.T, topo *domain.Topology, idSuffix, conn, targetSuffix string) {
	t.Helper()
	if jtConnTo(topo, idSuffix, conn, targetSuffix) {
		return
	}
	if r, ok := jtFind(topo, idSuffix); ok {
		t.Errorf("want %s --%s--> *%q; got %s=%v", idSuffix, conn, targetSuffix, conn, r.Connections[conn])
	} else {
		t.Errorf("want %s --%s--> *%q; source resource not uniquely found", idSuffix, conn, targetSuffix)
	}
}

// jtWantAnyConn asserts SOME edge to the target exists (kind-agnostic).
func jtWantAnyConn(t *testing.T, topo *domain.Topology, idSuffix, targetSuffix string) {
	t.Helper()
	if !jtAnyConnTo(topo, idSuffix, targetSuffix) {
		t.Errorf("want %s to reference *%q via some edge; none found", idSuffix, targetSuffix)
	}
}

// jtWantRes asserts a unique resource ending idSuffix exists.
func jtWantRes(t *testing.T, topo *domain.Topology, idSuffix string) {
	t.Helper()
	if _, ok := jtFind(topo, idSuffix); !ok {
		t.Errorf("want a unique resource ending %q; not found", idSuffix)
	}
}

// jtWantKind asserts a unique resource ending idSuffix exists with the given kind.
func jtWantKind(t *testing.T, topo *domain.Topology, idSuffix string, kind domain.ResourceKind) {
	t.Helper()
	r, ok := jtFind(topo, idSuffix)
	if !ok {
		t.Errorf("want resource ending %q (kind %s); not uniquely found", idSuffix, kind)
		return
	}
	if r.Kind != kind {
		t.Errorf("want %q kind=%s; got kind=%s", idSuffix, kind, r.Kind)
	}
}

// jtWantAtLeast asserts at least `min` resources of `kind` exist in fileToken.
func jtWantAtLeast(t *testing.T, topo *domain.Topology, fileToken string, kind domain.ResourceKind, min int) {
	t.Helper()
	if got := jtCountKindInFile(topo, fileToken, kind); got < min {
		t.Errorf("want >=%d %s resources in %q; got %d", min, kind, fileToken, got)
	}
}

// ---------------------------------------------------------------------------
// The JS suite. One cold in-memory scan, many themed subtests.
// ---------------------------------------------------------------------------

func TestJSFamilyEdgecases(t *testing.T) {
	t.Parallel()
	topo := jtScan(t, "jsfamily")

	// OK: every fixture parses without recording a scanner error.
	t.Run("no_parse_errors", func(t *testing.T) {
		if len(topo.Errors) != 0 {
			t.Errorf("expected no parse errors, got %v", topo.Errors)
		}
	})

	// ---- imports / exports ----------------------------------------------
	t.Run("imports_exports", func(t *testing.T) {
		// GAP (bug …_5): re-export chain through a barrel does not forward the
		// binding, so the consumer resolves nothing.
		t.Run("reexport_chain_resolves_class", func(t *testing.T) {
			jtWantConn(t, topo, "barrel_consumer.useBarrel", "uses_class", "shapes.Circle")
		})
		t.Run("reexport_chain_resolves_default", func(t *testing.T) {
			jtWantConn(t, topo, "barrel_consumer.useBarrel", "calls", "shapes.makeSquare")
		})
		// GAP (bug …_13): anonymous `export default function` is dropped.
		t.Run("anon_default_function_extracted", func(t *testing.T) {
			jtWantAtLeast(t, topo, "anon_default_fn", domain.ResourceFunction, 1)
		})
		// GAP (bug …_13): anonymous `export default class` is dropped.
		t.Run("anon_default_class_extracted", func(t *testing.T) {
			jtWantAtLeast(t, topo, "anon_default_class", domain.ResourceStruct, 1)
		})
		// OK: an export list with renames still extracts the underlying decls.
		t.Run("renamed_exports_extracted", func(t *testing.T) {
			jtWantKind(t, topo, "renames.localHelper", domain.ResourceFunction)
			jtWantKind(t, topo, "renames.Widget", domain.ResourceStruct)
			jtWantRes(t, topo, "renames.Widget.build")
			jtWantKind(t, topo, "renames.LOCAL", domain.ResourceVariable)
		})
		// OK: a circular import resolves cross-module calls both ways.
		t.Run("circular_import_a_to_b", func(t *testing.T) {
			jtWantConn(t, topo, "circular_a.useB", "calls", "circular_b.fromB")
		})
		t.Run("circular_import_b_to_a", func(t *testing.T) {
			jtWantConn(t, topo, "circular_b.fromB", "calls", "circular_a.fromA")
		})
	})

	// ---- CommonJS --------------------------------------------------------
	t.Run("commonjs", func(t *testing.T) {
		// GAP (bug …_14): arrow/function/class expressions assigned to exports.x
		// are not extracted as resources.
		t.Run("exports_assignment_extracts_function", func(t *testing.T) {
			jtWantAtLeast(t, topo, "cjs_assign", domain.ResourceFunction, 1)
		})
		t.Run("exports_assignment_extracts_class", func(t *testing.T) {
			jtWantAtLeast(t, topo, "cjs_assign", domain.ResourceStruct, 1)
		})
	})

	// ---- dynamic import --------------------------------------------------
	t.Run("dynamic_import", func(t *testing.T) {
		// GAP (bug …_8): dynamic import() is never parsed.
		t.Run("dynamic_import_uses_class", func(t *testing.T) {
			jtWantConn(t, topo, "dynamic.lazyCircle", "uses_class", "shapes.Circle")
		})
	})

	// ---- object literals -------------------------------------------------
	t.Run("object_literals", func(t *testing.T) {
		// GAP (bug …_7): object-literal methods are not resources.
		t.Run("object_method_is_resource", func(t *testing.T) {
			jtWantRes(t, topo, "objects.geometryOps.makeCircle")
		})
	})

	// ---- nested functions ------------------------------------------------
	t.Run("nested_functions", func(t *testing.T) {
		// GAP (bug …_6): a nested function declaration is not its own resource.
		t.Run("nested_function_is_resource", func(t *testing.T) {
			jtWantRes(t, topo, "nested.inner")
		})
		// OK: a function expression assigned to a const IS extracted.
		t.Run("function_expr_const_extracted", func(t *testing.T) {
			jtWantKind(t, topo, "nested.localFactory", domain.ResourceFunction)
		})
		// OK (current behavior): the nested body's `new` folds into the parent.
		t.Run("nested_body_new_folds_into_parent", func(t *testing.T) {
			jtWantConn(t, topo, "nested.outer", "uses_class", "shapes.Circle")
		})
	})

	// ---- method-call resolution -----------------------------------------
	t.Run("call_resolution", func(t *testing.T) {
		// OK: `new X()` + intermediate variable resolves the chained method.
		t.Run("new_via_variable_resolves_method", func(t *testing.T) {
			jtWantConn(t, topo, "factory.circleArea", "calls", "shapes.Circle.area")
		})
		// OK: `new X()` on its own records uses_class + the constructor.
		t.Run("inline_new_records_uses_class", func(t *testing.T) {
			jtWantConn(t, topo, "calls.inlineArea", "uses_class", "shapes.Circle")
		})
		// GAP (bug …_11): a method chained directly on a `new` expression
		// (`new Circle(r).area()`) does not resolve the method.
		t.Run("inline_new_chain_resolves_method", func(t *testing.T) {
			jtWantConn(t, topo, "calls.inlineArea", "calls", "shapes.Circle.area")
		})
		// OK: intra-class `this.method()` resolves to the sibling method in a
		// full scan. (NOTE: bug …_9 was filed for this from an MCP read that
		// showed an empty CONTEXT, but read_function simply omits same-class
		// sibling edges from its render — the edge DOES exist. Dismiss bug …_9.)
		t.Run("this_call_resolves_sibling_method", func(t *testing.T) {
			jtWantConn(t, topo, "calls.Helper.run", "calls", "calls.Helper.compute")
		})
		// GAP (bug …_10): `super.method()` is not resolved.
		t.Run("super_call_resolves_parent_method", func(t *testing.T) {
			jtWantConn(t, topo, "calls.LoudCircle.area", "calls", "shapes.Circle.area")
		})
		// GAP (bug …_12): a call in a default-parameter initializer is not tracked.
		t.Run("default_param_initializer_call_tracked", func(t *testing.T) {
			jtWantConn(t, topo, "calls.withDefault", "calls", "factory.makeCircle")
		})
	})

	// ---- class features --------------------------------------------------
	t.Run("class_features", func(t *testing.T) {
		// OK: private #methods and async generator methods are extracted.
		t.Run("private_method_extracted", func(t *testing.T) {
			jtWantRes(t, topo, "classfeatures.Featured.#hidden")
		})
		t.Run("async_generator_method_extracted", func(t *testing.T) {
			jtWantRes(t, topo, "classfeatures.Featured.stream")
		})
		// GAP (bug …_17): a computed method name keeps the literal bracket syntax
		// in its ID (`Featured.["dynamic"]`) instead of the clean property name.
		t.Run("computed_method_clean_id", func(t *testing.T) {
			jtWantRes(t, topo, "classfeatures.Featured.dynamic")
		})
		// OK: a getter+setter with the SAME name yields ONE accessor and does NOT
		// abort the write (regression guard for a previously-known abort bug).
		t.Run("getter_setter_same_name_no_abort", func(t *testing.T) {
			jtWantKind(t, topo, "getset_bug.Thermostat", domain.ResourceStruct)
			jtWantRes(t, topo, "getset_bug.Thermostat.temp")
		})
		// GAP (bug …_15): a class EXPRESSION assigned to a const is not extracted.
		t.Run("class_expression_extracted", func(t *testing.T) {
			jtWantKind(t, topo, "classexpr.Anon", domain.ResourceStruct)
		})
		// GAP (bug …_16): a mixin/call-expression base does not resolve inheritance.
		t.Run("mixin_base_resolves_root_class", func(t *testing.T) {
			jtWantConn(t, topo, "classexpr.Decorated", "inherits", "shapes.Circle")
		})
	})
}
