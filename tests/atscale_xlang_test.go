package tests_test

// Cross-language scenarios: re-scanning a file in one JS-based language must not
// corrupt the OTHER language's topology. These keep the full corpus (react
// components present) and isolate the cross-language dependency bug (..._4).

import (
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// assertFileImportsDep finds the file resource whose ID ends with suffix and
// asserts it carries an imports_dependency edge to dep.
func assertFileImportsDep(t *testing.T, topo *domain.Topology, mode, suffix, dep string) {
	t.Helper()
	for id, r := range topo.Resources {
		if r.Kind == domain.ResourceFile && strings.HasSuffix(id, suffix) {
			if hasTarget(r, connImportsDep, dep) {
				return
			}
			t.Errorf("[%s] file %s missing imports_dependency -> %q (got %v)", mode, id, dep, r.Connections[connImportsDep])
			return
		}
	}
	t.Errorf("[%s] file resource ending %q not found", mode, suffix)
}

// XL1: re-parsing a JS file must not drop the react dependency from an unrelated
// TS file. Isolated repro for bug ..._4 (expected FAIL incrementally).
func TestAtScaleXlang_XL1_JsEditKeepsTsReactDep(t *testing.T) {
	runScenario(t, scenario{
		name: "XL1_js_edit_keeps_ts_react",
		mutate: func(t *testing.T, root string) {
			touchCorpusFile(t, root, "jsfamily/factory.js")
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertFileImportsDep(t, topo, mode, "tsfamily/components.tsx", "react")
		},
	})
}

// XL2: the symmetric direction — re-parsing a TS file must not drop the react
// dependency from an unrelated JS file. Probe (FAIL would be a new finding).
func TestAtScaleXlang_XL2_TsEditKeepsJsReactDep(t *testing.T) {
	runScenario(t, scenario{
		name: "XL2_ts_edit_keeps_js_react",
		mutate: func(t *testing.T, root string) {
			touchCorpusFile(t, root, "tsfamily/factory.ts")
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertFileImportsDep(t, topo, mode, "jsfamily/components.jsx", "react")
		},
	})
}
