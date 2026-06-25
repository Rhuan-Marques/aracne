package tests_test

// Python edge-case correctness suite.
//
// Each subtest asserts the IDEAL topology the scanner SHOULD produce for a
// Python construct. Subtests that currently FAIL are documented gaps in the
// Python scanner (collapsed annotations, dropped lambda/with bodies, missing
// implements edges, etc.) and are filed as bugs — they are expected to stay red
// until the scanner is fixed. Subtests that PASS lock in behavior that already
// works and guard against regressions.
//
// These tests scan ONLY the testing_ground/python subtree (rooted so the
// resource IDs match the full-corpus "aracne/testing_ground/python/..." scheme).
// This deliberately avoids the other-language trees, whose JS/TS scanner
// currently aborts the multi-language write — an unrelated, pre-existing bug.

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aracne/internal/topology/domain"
)

// P is the resource-ID prefix for every edgecases resource (functions/classes/
// methods/vars). NOTE: file/module resources are keyed by absolute path, so
// imports_module edges must be matched by suffix, not by this prefix.
const P = "aracne/testing_ground/python/edgecases/"

// ---------------------------------------------------------------------------
// Corpus copy + scan helpers (python-only).
// ---------------------------------------------------------------------------

// copyPythonCorpus copies <repo>/testing_ground/python into
// <tmp>/aracne/testing_ground/python and drops a synthetic go.mod, so a scan
// rooted at <tmp>/aracne yields the same "aracne/testing_ground/python/..."
// resource IDs as the full corpus — but WITHOUT the other language trees.
func copyPythonCorpus(t *testing.T) string {
	t.Helper()
	src := filepath.Join(projectRoot(), "testing_ground", "python")
	root := filepath.Join(t.TempDir(), "aracne")
	dst := filepath.Join(root, "testing_ground", "python")
	skip := map[string]bool{".aracne": true, "__pycache__": true, ".git": true}
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		for _, seg := range strings.Split(rel, string(os.PathSeparator)) {
			if skip[seg] {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatalf("copy python corpus: %v", err)
	}
	writeFile(t, filepath.Join(root, "go.mod"), "module aracne\n\ngo 1.25\n")
	return root
}

// scanPythonCorpus copies the python corpus and runs a cold full scan, returning
// the resulting topology.
func scanPythonCorpus(t *testing.T) *domain.Topology {
	t.Helper()
	root := copyPythonCorpus(t)
	db := filepath.Join(t.TempDir(), "py.db")
	mustRun(t, root, "scan", "-all", "-root", root, "-output", db)
	return readTopo(t, db)
}

// ---------------------------------------------------------------------------
// Extra assertion helpers.
// ---------------------------------------------------------------------------

// containmentEdges are structural/ownership edges (a module owning its
// functions, a class owning its methods, an import edge), as opposed to a real
// USE of a symbol. assertReferenced ignores these so that "is this symbol used
// anywhere?" isn't trivially satisfied by the module's has_function edge.
var containmentEdges = map[string]bool{
	"has_function": true, "has_class": true, "has_extvar": true,
	"has_file": true, "methods": true, "constructor": true,
	"imports_module": true, "imports_package": true, "imports_dependency": true,
}

// assertReferenced fails unless some resource has a non-containment connection
// targeting `target` (used to check that a symbol is actually USED somewhere).
func assertReferenced(t *testing.T, topo *domain.Topology, mode, target string) {
	t.Helper()
	for _, r := range topo.Resources {
		for ct, targets := range r.Connections {
			if containmentEdges[ct] {
				continue
			}
			for _, tg := range targets {
				if tg == target {
					return
				}
			}
		}
	}
	t.Errorf("[%s] expected some resource to USE %q, but nothing does", mode, target)
}

// assertConnTargetSuffix fails unless `id` has a `connType` edge whose target
// ends with `suffix` (for edges whose targets are absolute file paths).
func assertConnTargetSuffix(t *testing.T, topo *domain.Topology, mode, id, connType, suffix string) {
	t.Helper()
	r := mustResource(t, topo, mode, id)
	for _, tg := range r.Connections[connType] {
		if strings.HasSuffix(tg, suffix) {
			return
		}
	}
	t.Errorf("[%s] %s: expected %s edge with target ending %q, got %v", mode, id, connType, suffix, r.Connections[connType])
}

// findIDBySuffix returns the single resource ID ending with suffix.
func findIDBySuffix(t *testing.T, topo *domain.Topology, suffix string) string {
	t.Helper()
	var found []string
	for id := range topo.Resources {
		if strings.HasSuffix(id, suffix) {
			found = append(found, id)
		}
	}
	if len(found) != 1 {
		t.Fatalf("expected exactly 1 resource ending %q, got %d: %v", suffix, len(found), found)
	}
	return found[0]
}

func decoratorList(r domain.Resource) []string {
	var out []string
	if d, ok := r.Properties["decorators"].([]any); ok {
		for _, x := range d {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func assertHasDecorator(t *testing.T, topo *domain.Topology, mode, id, dec string) {
	t.Helper()
	r := mustResource(t, topo, mode, id)
	for _, d := range decoratorList(r) {
		if d == dec {
			return
		}
	}
	t.Errorf("[%s] %s: expected decorator %q, got %v", mode, id, dec, decoratorList(r))
}

func assertIsAsync(t *testing.T, topo *domain.Topology, mode, id string) {
	t.Helper()
	r := mustResource(t, topo, mode, id)
	if ia, _ := r.Properties["is_async"].(bool); !ia {
		t.Errorf("[%s] %s: expected is_async=true", mode, id)
	}
}

// ---------------------------------------------------------------------------
// The suite. One cold scan, many themed subtests.
// ---------------------------------------------------------------------------

func TestPythonEdgecases(t *testing.T) {
	t.Parallel()
	topo := scanPythonCorpus(t)
	m := "py"

	// ---- typing & annotations -------------------------------------------
	t.Run("typing", func(t *testing.T) {
		alpha := P + "typing_edge.Alpha"
		beta := P + "typing_edge.Beta"

		// GAP: Optional[Alpha] should resolve the inner class.
		t.Run("optional_inner_resolves", func(t *testing.T) {
			assertHasConn(t, topo, m, P+"typing_edge.takes_optional", connUsesClass, alpha)
		})
		// GAP: Union[Alpha, Beta] should resolve BOTH.
		t.Run("union_resolves_both", func(t *testing.T) {
			assertHasConn(t, topo, m, P+"typing_edge.takes_union", connUsesClass, alpha)
			assertHasConn(t, topo, m, P+"typing_edge.takes_union", connUsesClass, beta)
		})
		// GAP: PEP 604 `Alpha | Beta` should resolve BOTH.
		t.Run("pep604_union_resolves_both", func(t *testing.T) {
			assertHasConn(t, topo, m, P+"typing_edge.takes_pep604", connUsesClass, alpha)
			assertHasConn(t, topo, m, P+"typing_edge.takes_pep604", connUsesClass, beta)
		})
		// GAP: list[Alpha] element type should resolve.
		t.Run("list_element_resolves", func(t *testing.T) {
			assertHasConn(t, topo, m, P+"typing_edge.takes_list", connUsesClass, alpha)
		})
		// GAP: dict[str, Beta] value type should resolve.
		t.Run("dict_value_resolves", func(t *testing.T) {
			assertHasConn(t, topo, m, P+"typing_edge.takes_dict", connUsesClass, beta)
		})
		// GAP: Callable[[Alpha], Beta] arg & return types should resolve.
		t.Run("callable_types_resolve", func(t *testing.T) {
			assertHasConn(t, topo, m, P+"typing_edge.takes_callable", connUsesClass, alpha)
			assertHasConn(t, topo, m, P+"typing_edge.takes_callable", connUsesClass, beta)
		})
		// GAP: quoted forward-ref annotations should resolve.
		t.Run("forward_ref_resolves", func(t *testing.T) {
			assertHasConn(t, topo, m, P+"typing_edge.forward_ref", connUsesClass, alpha)
			assertHasConn(t, topo, m, P+"typing_edge.forward_ref", connUsesClass, beta)
		})
		// GAP: typing.cast(Alpha, x) should resolve the target class.
		t.Run("cast_target_resolves", func(t *testing.T) {
			assertHasConn(t, topo, m, P+"typing_edge.use_cast", connUsesClass, alpha)
		})
		// GAP: PEP 695 `type AlphaList = list[Alpha]` should be a resource.
		t.Run("pep695_type_alias_is_resource", func(t *testing.T) {
			assertResPresent(t, topo, m, P+"typing_edge.AlphaList")
		})
		// GAP: a parameter annotated with an alias should resolve to the aliased class.
		t.Run("alias_param_resolves_target", func(t *testing.T) {
			assertHasConn(t, topo, m, P+"typing_edge.use_alias", connUsesClass, alpha)
		})
		// OK: PEP 695 generic class/func and Generic[T] base parse without crashing.
		t.Run("pep695_generic_class_parsed", func(t *testing.T) {
			assertResPresent(t, topo, m, P+"typing_edge.Stack")
			assertResPresent(t, topo, m, P+"typing_edge.Stack.push")
		})
		t.Run("pep695_generic_func_parsed", func(t *testing.T) {
			assertResPresent(t, topo, m, P+"typing_edge.first")
		})
		t.Run("generic_base_present_no_bogus_inherit", func(t *testing.T) {
			assertResPresent(t, topo, m, P+"typing_edge.Box")
			assertNoConn(t, topo, m, P+"typing_edge.Box", connInherits, "Generic")
		})
	})

	// ---- scope: lambdas, comprehensions, walrus, closures ----------------
	t.Run("scope", func(t *testing.T) {
		helper := P + "scope_edge.helper"
		widget := P + "scope_edge.Widget"

		// OK: calls inside comprehensions / generator expressions are captured.
		t.Run("listcomp_call_captured", func(t *testing.T) {
			assertHasConn(t, topo, m, P+"scope_edge.uses_listcomp", connCalls, helper)
		})
		t.Run("dictcomp_call_captured", func(t *testing.T) {
			assertHasConn(t, topo, m, P+"scope_edge.uses_dictcomp", connCalls, helper)
		})
		t.Run("setcomp_call_captured", func(t *testing.T) {
			assertHasConn(t, topo, m, P+"scope_edge.uses_setcomp", connCalls, helper)
		})
		t.Run("genexp_call_captured", func(t *testing.T) {
			assertHasConn(t, topo, m, P+"scope_edge.uses_genexp", connCalls, helper)
		})
		// OK: walrus-bound call is captured.
		t.Run("walrus_call_captured", func(t *testing.T) {
			assertHasConn(t, topo, m, P+"scope_edge.uses_walrus", connCalls, helper)
		})
		// GAP: call inside a lambda is dropped.
		t.Run("lambda_call_captured", func(t *testing.T) {
			assertHasConn(t, topo, m, P+"scope_edge.uses_lambda", connCalls, helper)
		})
		// GAP: class instantiated inside a comprehension is not a uses_class.
		t.Run("comprehension_instantiation_uses_class", func(t *testing.T) {
			assertHasConn(t, topo, m, P+"scope_edge.comp_instantiates", connUsesClass, widget)
		})
		// OK (by design): nested inner()'s call is NOT attributed to the parent.
		t.Run("nested_call_not_attributed_to_parent", func(t *testing.T) {
			assertNoConn(t, topo, m, P+"scope_edge.outer_closure", connCalls, helper)
		})
	})

	// ---- decorators ------------------------------------------------------
	t.Run("decorators", func(t *testing.T) {
		// OK: a custom decorator resolves to a calls edge.
		t.Run("custom_decorator_as_call", func(t *testing.T) {
			assertHasConn(t, topo, m, P+"decorators_edge.traced_fn", connCalls, P+"decorators_edge.trace")
		})
		// OK: a decorator factory @repeat(3) resolves to a calls edge.
		t.Run("decorator_factory_resolved", func(t *testing.T) {
			assertHasConn(t, topo, m, P+"decorators_edge.repeated_fn", connCalls, P+"decorators_edge.repeat")
		})
		// OK: stacked decorators both recorded.
		t.Run("stacked_decorators_both", func(t *testing.T) {
			assertHasConn(t, topo, m, P+"decorators_edge.stacked_fn", connCalls, P+"decorators_edge.trace")
			assertHasConn(t, topo, m, P+"decorators_edge.stacked_fn", connCalls, P+"decorators_edge.repeat")
		})
		// OK: a decorator from an external module records the dependency.
		t.Run("functools_dependency_recorded", func(t *testing.T) {
			assertHasConn(t, topo, m, P+"decorators_edge.cached_fn", "uses_dependency", "functools")
		})
		// GAP: attribute-access decorator @registry.register isn't resolved.
		t.Run("attribute_decorator_resolved", func(t *testing.T) {
			assertHasConn(t, topo, m, P+"decorators_edge.registered_fn", "uses_extvar", P+"decorators_edge.registry")
		})
		// GAP: class decorator @decorate_class isn't recorded as an edge.
		t.Run("class_decorator_recorded", func(t *testing.T) {
			assertReferenced(t, topo, m, P+"decorators_edge.decorate_class")
		})
	})

	// ---- OOP -------------------------------------------------------------
	t.Run("oop", func(t *testing.T) {
		vector := P + "oop_edge.Vector"

		// OK: @staticmethod / @classmethod are recorded as decorators.
		t.Run("staticmethod_decorator_recorded", func(t *testing.T) {
			assertHasDecorator(t, topo, m, P+"oop_edge.MathUtil.square", "staticmethod")
		})
		t.Run("classmethod_decorator_recorded", func(t *testing.T) {
			assertHasDecorator(t, topo, m, P+"oop_edge.MathUtil.make", "classmethod")
		})
		// OK: simple inheritance edges.
		t.Run("simple_inheritance", func(t *testing.T) {
			assertHasConn(t, topo, m, P+"oop_edge.Employee", connInherits, P+"oop_edge.Person")
			assertHasConn(t, topo, m, P+"oop_edge.Person", connInheritedBy, P+"oop_edge.Employee")
		})
		// OK: parameter annotation with a local class resolves to uses_class.
		t.Run("param_annotation_uses_class", func(t *testing.T) {
			assertHasConn(t, topo, m, P+"oop_edge.use_operators", connUsesClass, vector)
		})
		// OK: diamond inheritance edges all present.
		t.Run("diamond_inheritance_edges", func(t *testing.T) {
			assertHasConn(t, topo, m, P+"oop_edge.DiamondBottom", connInherits, P+"oop_edge.DiamondLeft")
			assertHasConn(t, topo, m, P+"oop_edge.DiamondBottom", connInherits, P+"oop_edge.DiamondRight")
			assertHasConn(t, topo, m, P+"oop_edge.DiamondLeft", connInherits, P+"oop_edge.DiamondTop")
			assertHasConn(t, topo, m, P+"oop_edge.DiamondRight", connInherits, P+"oop_edge.DiamondTop")
		})
		// OK: dataclass instantiation resolves the class.
		t.Run("dataclass_instantiation_uses_class", func(t *testing.T) {
			assertHasConn(t, topo, m, P+"oop_edge.open_account", connUsesClass, P+"oop_edge.Account")
		})

		// GAP: super().__init__() should resolve to the parent constructor.
		t.Run("super_init_call_resolved", func(t *testing.T) {
			assertHasConn(t, topo, m, P+"oop_edge.Employee.__init__", connCalls, P+"oop_edge.Person.__init__")
		})
		// GAP: operator dunders are not resolved as calls.
		t.Run("operator_dunder_add_resolved", func(t *testing.T) {
			assertHasConn(t, topo, m, P+"oop_edge.use_operators", connCalls, P+"oop_edge.Vector.__add__")
		})
		t.Run("operator_dunder_getitem_resolved", func(t *testing.T) {
			assertHasConn(t, topo, m, P+"oop_edge.use_operators", connCalls, P+"oop_edge.Vector.__getitem__")
		})
		// GAP: nested class is not extracted as its own resource.
		t.Run("nested_class_is_resource", func(t *testing.T) {
			assertResPresent(t, topo, m, P+"oop_edge.Outer.Inner")
		})
		t.Run("nested_class_instantiation_uses_class", func(t *testing.T) {
			assertHasConn(t, topo, m, P+"oop_edge.Outer.make_inner", connUsesClass, P+"oop_edge.Outer.Inner")
		})
		// GAP: metaclass=UpperMeta is not recorded.
		t.Run("metaclass_recorded", func(t *testing.T) {
			assertHasConn(t, topo, m, P+"oop_edge.WithMeta", connUsesClass, P+"oop_edge.UpperMeta")
		})
		// GAP: enum members are not modeled as resources.
		t.Run("enum_members_modeled", func(t *testing.T) {
			assertResPresent(t, topo, m, P+"oop_edge.Color.RED")
		})
		// GAP: NamedTuple fields are not modeled.
		t.Run("namedtuple_fields_modeled", func(t *testing.T) {
			assertResPresent(t, topo, m, P+"oop_edge.PointNT.x")
		})
		// GAP: dataclass synthesized constructor not modeled.
		t.Run("dataclass_constructor_modeled", func(t *testing.T) {
			assertResPresent(t, topo, m, P+"oop_edge.Account.__init__")
		})
		// GAP: Protocol structural conformance produces no implements edge.
		t.Run("protocol_structural_implements", func(t *testing.T) {
			assertHasConn(t, topo, m, P+"oop_edge.Button", connImplements, P+"oop_edge.Renderable")
			assertHasConn(t, topo, m, P+"oop_edge.Renderable", connImplBy, P+"oop_edge.Button")
		})
	})

	// ---- async & generators ---------------------------------------------
	t.Run("async_gen", func(t *testing.T) {
		// OK: await / async-for / yield-from calls captured.
		t.Run("await_call_captured", func(t *testing.T) {
			assertHasConn(t, topo, m, P+"async_gen_edge.consume_await", connCalls, P+"async_gen_edge.fetch")
		})
		t.Run("async_for_call_captured", func(t *testing.T) {
			assertHasConn(t, topo, m, P+"async_gen_edge.consume_async_for", connCalls, P+"async_gen_edge.agen")
		})
		t.Run("yield_from_call_captured", func(t *testing.T) {
			assertHasConn(t, topo, m, P+"async_gen_edge.delegating_gen", connCalls, P+"async_gen_edge.number_gen")
		})
		// OK: async functions/methods flagged is_async.
		t.Run("async_function_flagged", func(t *testing.T) {
			assertIsAsync(t, topo, m, P+"async_gen_edge.fetch")
			assertIsAsync(t, topo, m, P+"async_gen_edge.consume_await")
		})
		t.Run("async_method_flagged", func(t *testing.T) {
			assertIsAsync(t, topo, m, P+"async_gen_edge.Resource.__aenter__")
		})
		// GAP: async with Resource() doesn't record uses_class / method call.
		t.Run("async_with_uses_class", func(t *testing.T) {
			assertHasConn(t, topo, m, P+"async_gen_edge.consume_async_with", connUsesClass, P+"async_gen_edge.Resource")
		})
		t.Run("async_with_method_call", func(t *testing.T) {
			assertHasConn(t, topo, m, P+"async_gen_edge.consume_async_with", connCalls, P+"async_gen_edge.Resource.close")
		})
	})

	// ---- control flow: match / with -------------------------------------
	t.Run("control_flow", func(t *testing.T) {
		handler := P + "control_flow_edge.Handler"
		// OK: a call inside a match/case body is captured.
		t.Run("match_case_body_call_captured", func(t *testing.T) {
			assertHasConn(t, topo, m, P+"control_flow_edge.dispatch", connCalls, P+"control_flow_edge.run_action")
		})
		// GAP: class pattern `case Shape3D()` doesn't record uses_class.
		t.Run("match_class_pattern_uses_class", func(t *testing.T) {
			assertHasConn(t, topo, m, P+"control_flow_edge.match_class", connUsesClass, P+"control_flow_edge.Shape3D")
		})
		// GAP: with-statement binding doesn't record uses_class / method call.
		t.Run("with_stmt_uses_class", func(t *testing.T) {
			assertHasConn(t, topo, m, P+"control_flow_edge.use_with", connUsesClass, handler)
		})
		t.Run("with_stmt_method_call", func(t *testing.T) {
			assertHasConn(t, topo, m, P+"control_flow_edge.use_with", connCalls, P+"control_flow_edge.Handler.handle")
		})
		t.Run("nested_with_uses_class", func(t *testing.T) {
			assertHasConn(t, topo, m, P+"control_flow_edge.nested_with", connUsesClass, handler)
		})
	})

	// ---- dynamic / exotic ------------------------------------------------
	t.Run("dynamic", func(t *testing.T) {
		// OK: a normal in-body call is captured.
		t.Run("inbody_call_captured", func(t *testing.T) {
			assertHasConn(t, topo, m, P+"dynamic_edge.module_level_caller", connCalls, P+"dynamic_edge.helper_fn")
		})
		// GAP: monkey-patch `Plugin.run = external_method` records no edge.
		t.Run("monkeypatch_recorded", func(t *testing.T) {
			assertReferenced(t, topo, m, P+"dynamic_edge.external_method")
		})
		// GAP: functools.partial(partial_target, ...) records no use of the target.
		t.Run("partial_target_referenced", func(t *testing.T) {
			assertReferenced(t, topo, m, P+"dynamic_edge.partial_target")
		})
		// GAP: a module-level call (RESULT = module_level_caller()) is not an edge.
		t.Run("module_level_call_referenced", func(t *testing.T) {
			assertReferenced(t, topo, m, P+"dynamic_edge.module_level_caller")
		})
	})

	// ---- modules & imports ----------------------------------------------
	t.Run("imports", func(t *testing.T) {
		modB := findIDBySuffix(t, topo, "edgecases/pkg/mod_b.py")
		pkgInit := findIDBySuffix(t, topo, "edgecases/pkg/__init__.py")

		// OK: relative module import -> imports_module edge.
		t.Run("relative_module_import_edge", func(t *testing.T) {
			assertConnTargetSuffix(t, topo, m, modB, "imports_module", "edgecases/pkg/mod_a.py")
		})
		// OK: re-export in __init__ -> imports_module edge.
		t.Run("reexport_import_edge", func(t *testing.T) {
			assertConnTargetSuffix(t, topo, m, pkgInit, "imports_module", "edgecases/pkg/mod_a.py")
		})
		// OK: aliased symbol import resolves the call.
		t.Run("aliased_symbol_import_resolves_call", func(t *testing.T) {
			assertHasConn(t, topo, m, P+"pkg/mod_b.via_alias", connCalls, P+"pkg/mod_a.provide")
		})
		// OK: cross-module return annotation resolves uses_class.
		t.Run("cross_module_return_uses_class", func(t *testing.T) {
			assertHasConn(t, topo, m, P+"pkg/mod_a.provide", connUsesClass, P+"pkg/mod_a.Service")
		})
		// GAP: `from . import mod_a; mod_a.provide()` attribute call not resolved.
		t.Run("relative_attribute_call_resolved", func(t *testing.T) {
			assertHasConn(t, topo, m, P+"pkg/mod_b.via_relative", connCalls, P+"pkg/mod_a.provide")
		})
	})
}
