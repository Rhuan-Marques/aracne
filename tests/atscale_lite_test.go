package tests_test

// At-scale representative scenarios for Python, JS, and TS. Each is a
// self-contained single-file edit that changes how a method call resolves, then
// asserts the new resolution under all three scan modes with strict cross-mode
// equality. (JS/TS require the tree-sitter scanner, which the test binary built
// by TestMain links in via CGO.)

import (
	"testing"

	"aracne/internal/topology/domain"
)

// P1 (Python): change a parameter type hint Shape -> Circle. The body call
// shape.describe() must re-resolve from the abstract Shape.describe to the
// concrete Circle.describe.
func TestAtScalePy_P1_AnnotationResolution(t *testing.T) {
	const (
		render     = "aracne/testing_ground/python/consumer.render"
		shapeDesc  = "aracne/testing_ground/python/shapes.Shape.describe"
		circleDesc = "aracne/testing_ground/python/shapes.Circle.describe"
	)
	runScenario(t, scenario{
		name: "P1_python_annotation_resolution",
		mutate: func(t *testing.T, root string) {
			replaceInCorpusFile(t, root, "python/consumer.py",
				[2]string{"def render(shape: Shape) -> str:", "def render(shape: Circle) -> str:"})
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertHasConn(t, topo, mode, render, connCalls, circleDesc)
			assertNoConn(t, topo, mode, render, connCalls, shapeDesc)
		},
	})
}

// J1 (JS): change `new Circle` to `new Rectangle` inside circleArea. `new`-based
// resolution must swap the resolved method from Circle.area to Rectangle.area.
//
// KNOWN-RED (bug ..._4): the targeted JS assertions pass, but the strict
// cross-mode check fails because incrementally re-scanning this JS file drops
// the `imports_dependency -> react` edge from the unrelated TS file
// components.tsx (cross-language dependency loss). P1/T1 pass (self-contained).
func TestAtScaleJs_J1_NewResolution(t *testing.T) {
	const (
		circleArea = "aracne/testing_ground/jsfamily/factory.circleArea"
		jsCircle   = "aracne/testing_ground/jsfamily/shapes.Circle.area"
		jsRect     = "aracne/testing_ground/jsfamily/shapes.Rectangle.area"
	)
	runScenario(t, scenario{
		name: "J1_js_new_resolution",
		mutate: func(t *testing.T, root string) {
			replaceInCorpusFile(t, root, "jsfamily/factory.js",
				[2]string{"const c = new Circle(radius);", "const c = new Rectangle(radius);"})
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertHasConn(t, topo, mode, circleArea, connCalls, jsRect)
			assertNoConn(t, topo, mode, circleArea, connCalls, jsCircle)
		},
	})
}

// T1 (TS): change the local annotation `const c: Circle = makeCircle(...)` to
// `const c: Rectangle = makeRectangle(...)`. Annotation-driven resolution must
// swap both the resolved method (Circle.area -> Rectangle.area) and the factory
// call (makeCircle -> makeRectangle).
func TestAtScaleTs_T1_AnnotationResolution(t *testing.T) {
	const (
		areaOf        = "aracne/testing_ground/tsfamily/factory.areaOf"
		tsCircle      = "aracne/testing_ground/tsfamily/shapes.Circle.area"
		tsRect        = "aracne/testing_ground/tsfamily/shapes.Rectangle.area"
		makeCircle    = "aracne/testing_ground/tsfamily/factory.makeCircle"
		makeRectangle = "aracne/testing_ground/tsfamily/factory.makeRectangle"
	)
	runScenario(t, scenario{
		name: "T1_ts_annotation_resolution",
		mutate: func(t *testing.T, root string) {
			replaceInCorpusFile(t, root, "tsfamily/factory.ts",
				[2]string{"const c: Circle = makeCircle(radius);", "const c: Rectangle = makeRectangle(radius);"})
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertHasConn(t, topo, mode, areaOf, connCalls, tsRect)
			assertNoConn(t, topo, mode, areaOf, connCalls, tsCircle)
			assertHasConn(t, topo, mode, areaOf, connCalls, makeRectangle)
			assertNoConn(t, topo, mode, areaOf, connCalls, makeCircle)
		},
	})
}
