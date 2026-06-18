package tests_test

// At-scale JavaScript scenarios (JS + JSX + CommonJS): predictable edits to the
// testing_ground/jsfamily corpus, asserted under incremental / full / hard with
// strict cross-mode equality (see atscale_harness_test.go).
//
// JS resource IDs are path-based: "aracne/testing_ground/jsfamily/<file>.<Name>".
//
// Core scenarios setup-drop the react component files (dropReactComponents) so
// the cross-language react-dependency bug (..._4) does not mask the mechanic;
// the JSX scenario keeps jsfamily/components.jsx and drops only the TS one.
//
// KNOWN-RED (deterministic): structural edges (inherits/inherited_by) are
// global, so JS3/JS4/JS5 stay consistent; self-contained body edits
// (JS1/JS6/JS7/JS8/JS9) stay consistent; the cross-file rename JS2 diverges
// (same root cause as bug _2 intra-scan ordering / dependent-not-reparsed).

import (
	"testing"

	"aracne/internal/topology/domain"
)

const (
	jsShape      = "aracne/testing_ground/jsfamily/shapes.Shape"
	jsCircle     = "aracne/testing_ground/jsfamily/shapes.Circle"
	jsCircleArea = "aracne/testing_ground/jsfamily/shapes.Circle.area"
	jsRectangle  = "aracne/testing_ground/jsfamily/shapes.Rectangle"
	jsRectArea   = "aracne/testing_ground/jsfamily/shapes.Rectangle.area"
	jsArc        = "aracne/testing_ground/jsfamily/shapes.Arc"
	jsArcArea    = "aracne/testing_ground/jsfamily/shapes.Arc.area"

	jsCircleAreaFn = "aracne/testing_ground/jsfamily/factory.circleArea"
	jsReport       = "aracne/testing_ground/jsfamily/consumer.report"
	jsFactMakeCirc = "aracne/testing_ground/jsfamily/factory.makeCircle"
	jsFactMakeRect = "aracne/testing_ground/jsfamily/factory.makeRect"

	jsPentagon    = "aracne/testing_ground/jsfamily/extra.Pentagon"
	jsBundleSumm  = "aracne/testing_ground/jsfamily/bundle.summary"
	jsCjsBuild    = "aracne/testing_ground/jsfamily/commonjs.buildCircle"
	jsCjsRectInfo = "aracne/testing_ground/jsfamily/commonjs.rectInfo"
	jsCompRender  = "aracne/testing_ground/jsfamily/components.CircleView.render"
)

const pentagonJs = `import { Shape } from "./shapes.js";

export class Pentagon extends Shape {
  area() {
    return 0;
  }
}
`

// JS1: new-based resolution swap inside circleArea (self-contained).
func TestAtScaleJs_JS1_NewResolution(t *testing.T) {
	runScenario(t, scenario{
		name:  "JS1_new_resolution",
		setup: dropReactComponents,
		mutate: func(t *testing.T, root string) {
			replaceInCorpusFile(t, root, "jsfamily/factory.js",
				[2]string{"const c = new Circle(radius);", "const c = new Rectangle(radius);"})
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertHasConn(t, topo, mode, jsCircleAreaFn, connCalls, jsRectArea)
			assertNoConn(t, topo, mode, jsCircleAreaFn, connCalls, jsCircleArea)
		},
	})
}

// JS2: rename exported class Circle -> Arc across importers (cross-file body
// resolution diverges; structural inherited_by stays consistent).
func TestAtScaleJs_JS2_ClassRenameAcrossFiles(t *testing.T) {
	runScenario(t, scenario{
		name:  "JS2_class_rename",
		setup: dropReactComponents,
		mutate: func(t *testing.T, root string) {
			replaceInCorpusFile(t, root, "jsfamily/shapes.js", [2]string{"Circle", "Arc"})
			replaceInCorpusFile(t, root, "jsfamily/factory.js",
				[2]string{"{ Circle, Rectangle }", "{ Arc, Rectangle }"},
				[2]string{"new Circle(", "new Arc("})
			replaceInCorpusFile(t, root, "jsfamily/consumer.js",
				[2]string{"{ Circle as Disc, Rectangle }", "{ Arc as Disc, Rectangle }"})
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertResAbsent(t, topo, mode, jsCircle)
			assertResPresent(t, topo, mode, jsArc)
			assertNoReferences(t, topo, mode, jsCircle)
			assertHasConn(t, topo, mode, jsShape, connInheritedBy, jsArc)
			assertHasConn(t, topo, mode, jsReport, connCalls, jsArcArea)
		},
	})
}

// JS3: change class inheritance Circle extends Shape -> extends Rectangle.
func TestAtScaleJs_JS3_InheritanceChange(t *testing.T) {
	runScenario(t, scenario{
		name:  "JS3_inheritance_change",
		setup: dropReactComponents,
		mutate: func(t *testing.T, root string) {
			replaceInCorpusFile(t, root, "jsfamily/shapes.js",
				[2]string{"export class Circle extends Shape {", "export class Circle extends Rectangle {"})
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertHasConn(t, topo, mode, jsCircle, connInherits, jsRectangle)
			assertNoConn(t, topo, mode, jsCircle, connInherits, jsShape)
			assertNoConn(t, topo, mode, jsShape, connInheritedBy, jsCircle)
			assertHasConn(t, topo, mode, jsRectangle, connInheritedBy, jsCircle)
		},
	})
}

// JS4: add a new file with a subclass extending Shape (reverse-edge growth).
func TestAtScaleJs_JS4_AddSubclass(t *testing.T) {
	runScenario(t, scenario{
		name:  "JS4_add_subclass",
		setup: dropReactComponents,
		mutate: func(t *testing.T, root string) {
			writeCorpusFile(t, root, "jsfamily/extra.js", pentagonJs)
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertResPresent(t, topo, mode, jsPentagon)
			assertHasConn(t, topo, mode, jsShape, connInheritedBy, jsPentagon)
			assertHasConn(t, topo, mode, jsPentagon, connInherits, jsShape)
		},
	})
}

// JS5: remove the subclass added in setup (reverse-edge shrink).
func TestAtScaleJs_JS5_RemoveSubclass(t *testing.T) {
	runScenario(t, scenario{
		name: "JS5_remove_subclass",
		setup: func(t *testing.T, root string) {
			dropReactComponents(t, root)
			writeCorpusFile(t, root, "jsfamily/extra.js", pentagonJs)
		},
		mutate: func(t *testing.T, root string) {
			removeCorpusFile(t, root, "jsfamily/extra.js")
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertResAbsent(t, topo, mode, jsPentagon)
			assertNoReferences(t, topo, mode, jsPentagon)
			assertNoConn(t, topo, mode, jsShape, connInheritedBy, jsPentagon)
		},
	})
}

// JS6: drop an import alias { Circle as Disc } -> { Circle } and update usage.
func TestAtScaleJs_JS6_ImportAliasChange(t *testing.T) {
	runScenario(t, scenario{
		name:  "JS6_import_alias_change",
		setup: dropReactComponents,
		mutate: func(t *testing.T, root string) {
			replaceInCorpusFile(t, root, "jsfamily/consumer.js",
				[2]string{"{ Circle as Disc, Rectangle }", "{ Circle, Rectangle }"},
				[2]string{"const c = new Disc(2);", "const c = new Circle(2);"})
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertHasConn(t, topo, mode, jsReport, connCalls, jsCircleArea)
		},
	})
}

// JS7: namespace-import member-call resolution factory.makeCircle -> factory.makeRect.
func TestAtScaleJs_JS7_NamespaceImportResolution(t *testing.T) {
	runScenario(t, scenario{
		name:  "JS7_namespace_import",
		setup: dropReactComponents,
		mutate: func(t *testing.T, root string) {
			replaceInCorpusFile(t, root, "jsfamily/consumer.js",
				[2]string{"factory.makeCircle(3)", "factory.makeRect(3, 3)"})
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertHasConn(t, topo, mode, jsReport, connCalls, jsFactMakeRect)
			assertNoConn(t, topo, mode, jsReport, connCalls, jsFactMakeCirc)
		},
	})
}

// JS8: CommonJS whole-module require + member-call resolution
// (helper.buildCircle -> helper.rectInfo).
func TestAtScaleJs_JS8_CommonJSResolution(t *testing.T) {
	runScenario(t, scenario{
		name:  "JS8_commonjs_resolution",
		setup: dropReactComponents,
		mutate: func(t *testing.T, root string) {
			replaceInCorpusFile(t, root, "jsfamily/bundle.cjs",
				[2]string{"helper.buildCircle(2)", "helper.rectInfo(5, 5)"})
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertHasConn(t, topo, mode, jsBundleSumm, connCalls, jsCjsRectInfo)
			assertNoConn(t, topo, mode, jsBundleSumm, connCalls, jsCjsBuild)
		},
	})
}

// JS9 (JSX): new-based resolution inside a JSX class component. Keeps
// components.jsx (edited here) and drops only the TS component.
func TestAtScaleJs_JS9_JsxNewResolution(t *testing.T) {
	runScenario(t, scenario{
		name: "JS9_jsx_new_resolution",
		setup: func(t *testing.T, root string) {
			dropCorpusFiles(t, root, "tsfamily/components.tsx")
		},
		mutate: func(t *testing.T, root string) {
			replaceInCorpusFile(t, root, "jsfamily/components.jsx",
				[2]string{`import { Circle } from "./shapes.js";`, `import { Circle, Rectangle } from "./shapes.js";`},
				[2]string{"const c = new Circle(this.props.radius);", "const c = new Rectangle(this.props.radius);"})
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertHasConn(t, topo, mode, jsCompRender, connCalls, jsRectArea)
			assertNoConn(t, topo, mode, jsCompRender, connCalls, jsCircleArea)
		},
	})
}
