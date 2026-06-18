package tests_test

// At-scale Go scenarios: predictable edits to the testing_ground Go corpus,
// each asserted under incremental / full / hard and checked for strict
// cross-mode equality (see atscale_harness_test.go).
//
// KNOWN-RED (deterministic): the strict cross-mode check currently surfaces real
// incremental-scanner bugs (filed via bug_report). The per-scenario targeted
// assertions pass (the mechanic works in a cold scan); the cross-mode equality
// fails because the incremental path diverges. Mapping:
//   bug ..._1 spurious uses_interface->Shape on re-parse : G1, G4a, G4b, G7,
//             G8 (and the isolated repro GhostUsesInterfaceOnReparse)
//   bug ..._2 intra-scan ordering staleness             : G2, G3, G3b, G8
//   bug ..._3 dropped uses_package on re-parse           : G2
// G3 is an intentional divergence probe (dependent not re-parsed); G3b/G8 show
// re-parsing the dependent is not enough (ordering). G5, G6 pass: structural
// reverse-edges (implemented_by) are recomputed globally and stay consistent.

import (
	"testing"

	"aracne/internal/topology/domain"
)

// Stable resource IDs in the Go corpus (module path: aracne).
const (
	idConsumerReport = "aracne/testing_ground/go/consumer.Report"
	idConsumerTotal  = "aracne/testing_ground/go/consumer.Total"
	idGeomMakeCircle = "aracne/testing_ground/go/geometry.MakeCircle"
	idGeomMintCircle = "aracne/testing_ground/go/geometry.MintCircle"

	idShape       = "aracne/testing_ground/go/shapes.Shape"
	idCircle      = "aracne/testing_ground/go/shapes.Circle"
	idCircleArea  = "aracne/testing_ground/go/shapes.(Circle).Area"
	idCirclePerim = "aracne/testing_ground/go/shapes.(Circle).Perimeter"
	idCircleScale = "aracne/testing_ground/go/shapes.(Circle).Scale"
	idDisc        = "aracne/testing_ground/go/shapes.Disc"
	idDiscArea    = "aracne/testing_ground/go/shapes.(Disc).Area"
	idRectArea    = "aracne/testing_ground/go/shapes.(Rectangle).Area"
	idTriangle    = "aracne/testing_ground/go/shapes.Triangle"
	idTriDescribe = "aracne/testing_ground/go/shapes.(Triangle).Describe"

	idHexagon     = "aracne/testing_ground/go/shapes.Hexagon"
	idHexagonArea = "aracne/testing_ground/go/shapes.(Hexagon).Area"
)

// A new Shape implementer used by the add/remove implementer scenarios.
const hexagonGo = `package shapes

// Hexagon is a NEW Shape implementer added at runtime by the at-scale suite.
type Hexagon struct {
	Side float64
}

func (h Hexagon) Area() float64      { return 2.598076 * h.Side * h.Side }
func (h Hexagon) Perimeter() float64 { return 6 * h.Side }
func (h Hexagon) Describe() string   { return "hexagon" }
`

// G1: rename a cross-package function (geometry.MakeCircle -> MintCircle) and
// update its single caller. The consumer's cross-package return inference
// (c := MintCircle(); c.Area() -> shapes.(Circle).Area) must survive, the old
// id must be gone everywhere, and all three modes must agree.
func TestAtScaleGo_G1_CrossPkgFuncRename(t *testing.T) {
	runScenario(t, scenario{
		name: "G1_cross_pkg_func_rename",
		mutate: func(t *testing.T, root string) {
			replaceInCorpusFile(t, root, "go/geometry/factory.go",
				[2]string{"MakeCircle", "MintCircle"})
			replaceInCorpusFile(t, root, "go/consumer/consumer.go",
				[2]string{"geometry.MakeCircle", "geometry.MintCircle"})
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertResPresent(t, topo, mode, idGeomMintCircle)
			assertResAbsent(t, topo, mode, idGeomMakeCircle)
			assertNoReferences(t, topo, mode, idGeomMakeCircle)
			assertHasConn(t, topo, mode, idConsumerReport, connCalls, idGeomMintCircle)
			assertHasConn(t, topo, mode, idConsumerReport, connCalls, idCircleArea)
			assertHasConn(t, topo, mode, idConsumerReport, connUsesStruct, idCircle)
		},
	})
}

// G2: rename the struct shapes.Circle -> Disc across packages (definition,
// constructor return, method receivers, geometry's return type). The consumer
// references the type only through comments, so its comment is rewritten to
// force a re-parse (fair compare). Old ids must be fully gone, the interface's
// implemented_by must point at Disc, and the consumer must resolve to Disc's
// method via the returned value.
func TestAtScaleGo_G2_StructRenameAcrossPkgs(t *testing.T) {
	runScenario(t, scenario{
		name: "G2_struct_rename_across_pkgs",
		mutate: func(t *testing.T, root string) {
			replaceInCorpusFile(t, root, "go/shapes/shape.go",
				[2]string{"type Circle struct {", "type Disc struct {"},
				[2]string{"func NewCircle(r float64) *Circle {", "func NewCircle(r float64) *Disc {"},
				[2]string{"return &Circle{Radius: r, Kind: KindCircle}", "return &Disc{Radius: r, Kind: KindCircle}"},
				[2]string{"func (c Circle) Area() float64", "func (c Disc) Area() float64"},
				[2]string{"func (c Circle) Perimeter() float64", "func (c Disc) Perimeter() float64"},
				[2]string{"func (c Circle) Describe() string", "func (c Disc) Describe() string"},
			)
			replaceInCorpusFile(t, root, "go/geometry/factory.go",
				[2]string{"func MakeCircle(r float64) shapes.Circle {", "func MakeCircle(r float64) shapes.Disc {"},
				[2]string{"return shapes.Circle{Radius: r, Kind: shapes.KindCircle}", "return shapes.Disc{Radius: r, Kind: shapes.KindCircle}"},
			)
			replaceInCorpusFile(t, root, "go/consumer/consumer.go",
				[2]string{"shapes.Circle", "shapes.Disc"})
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertResAbsent(t, topo, mode, idCircle)
			assertResAbsent(t, topo, mode, idCircleArea)
			assertResPresent(t, topo, mode, idDisc)
			assertResPresent(t, topo, mode, idDiscArea)
			assertNoReferences(t, topo, mode, idCircle)
			assertNoReferences(t, topo, mode, idCircleArea)
			assertHasConn(t, topo, mode, idShape, connImplBy, idDisc)
			assertHasConn(t, topo, mode, idConsumerReport, connCalls, idDiscArea)
			assertHasConn(t, topo, mode, idConsumerReport, connUsesStruct, idDisc)
		},
	})
}

// G3 (divergence probe): change geometry.MakeCircle's return type to Rectangle
// WITHOUT editing the consumer. The cold full/hard scans re-resolve
// consumer.Report's c.Area() to shapes.(Rectangle).Area; the incremental scan
// does not re-parse the consumer, so strict equality is expected to catch the
// staleness (this test surfaces a real incremental limitation).
func TestAtScaleGo_G3_ReturnTypeChange_DivergenceProbe(t *testing.T) {
	runScenario(t, scenario{
		name: "G3_return_type_change_geometry_only",
		mutate: func(t *testing.T, root string) {
			replaceInCorpusFile(t, root, "go/geometry/factory.go",
				[2]string{"func MakeCircle(r float64) shapes.Circle {", "func MakeCircle(r float64) shapes.Rectangle {"},
				[2]string{"return shapes.Circle{Radius: r, Kind: shapes.KindCircle}", "return shapes.Rectangle{Width: r, Height: r}"},
			)
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertHasConn(t, topo, mode, idConsumerReport, connCalls, idRectArea)
			assertNoConn(t, topo, mode, idConsumerReport, connCalls, idCircleArea)
		},
	})
}

// G3b: same return-type change, but the consumer is also re-saved so the
// incremental scan re-parses it. This should converge -> all three modes agree.
// Pairing G3 (red) with G3b (green) isolates the cause as "dependent not
// re-parsed".
func TestAtScaleGo_G3b_ReturnTypeChange_ConsumerReparsed(t *testing.T) {
	runScenario(t, scenario{
		name: "G3b_return_type_change_consumer_touched",
		mutate: func(t *testing.T, root string) {
			replaceInCorpusFile(t, root, "go/geometry/factory.go",
				[2]string{"func MakeCircle(r float64) shapes.Circle {", "func MakeCircle(r float64) shapes.Rectangle {"},
				[2]string{"return shapes.Circle{Radius: r, Kind: shapes.KindCircle}", "return shapes.Rectangle{Width: r, Height: r}"},
			)
			touchCorpusFile(t, root, "go/consumer/consumer.go")
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertHasConn(t, topo, mode, idConsumerReport, connCalls, idRectArea)
			assertNoConn(t, topo, mode, idConsumerReport, connCalls, idCircleArea)
		},
	})
}

// G4a: add a local variable that receives an inferred shapes.Circle and call a
// method on it; the function must gain the resolved call edge. Self-contained
// (one file) -> all three modes agree.
func TestAtScaleGo_G4a_LocalVarAdd(t *testing.T) {
	runScenario(t, scenario{
		name: "G4a_local_var_add",
		mutate: func(t *testing.T, root string) {
			replaceInCorpusFile(t, root, "go/consumer/consumer.go",
				[2]string{"a := geometry.MakeCircle(1)", "a := geometry.MakeCircle(1)\n\t_ = a.Perimeter()"})
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertHasConn(t, topo, mode, idConsumerTotal, connCalls, idCirclePerim)
		},
	})
}

// G4b: remove an existing local method call; the function must lose that edge.
// Self-contained -> all three modes agree.
func TestAtScaleGo_G4b_LocalVarRemove(t *testing.T) {
	runScenario(t, scenario{
		name: "G4b_local_var_remove",
		mutate: func(t *testing.T, root string) {
			replaceInCorpusFile(t, root, "go/consumer/consumer.go",
				[2]string{"_ = circ.Perimeter()", "_ = circ"})
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertNoConn(t, topo, mode, idConsumerReport, connCalls, idCirclePerim)
		},
	})
}

// G5: add a brand-new file with a struct that satisfies the existing Shape
// interface. The reverse edge implemented_by must grow (it is recomputed
// globally), so incremental matches full/hard.
func TestAtScaleGo_G5_AddInterfaceImplementer(t *testing.T) {
	runScenario(t, scenario{
		name: "G5_add_interface_implementer",
		mutate: func(t *testing.T, root string) {
			writeCorpusFile(t, root, "go/shapes/hexagon.go", hexagonGo)
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertResPresent(t, topo, mode, idHexagon)
			assertHasConn(t, topo, mode, idShape, connImplBy, idHexagon)
			assertHasConn(t, topo, mode, idHexagon, connImplements, idShape)
		},
	})
}

// G6: delete the implementer added in setup. Its resources must disappear and
// the interface's implemented_by must shrink back, identically across modes.
func TestAtScaleGo_G6_RemoveInterfaceImplementer(t *testing.T) {
	runScenario(t, scenario{
		name: "G6_remove_interface_implementer",
		setup: func(t *testing.T, root string) {
			writeCorpusFile(t, root, "go/shapes/hexagon.go", hexagonGo)
		},
		mutate: func(t *testing.T, root string) {
			removeCorpusFile(t, root, "go/shapes/hexagon.go")
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertResAbsent(t, topo, mode, idHexagon)
			assertResAbsent(t, topo, mode, idHexagonArea)
			assertNoReferences(t, topo, mode, idHexagon)
			assertNoConn(t, topo, mode, idShape, connImplBy, idHexagon)
			assertHasConn(t, topo, mode, idShape, connImplBy, idCircle)
		},
	})
}

// G7: break an implementation by removing Triangle.Describe so Triangle no
// longer satisfies Shape. The interface's implemented_by must drop Triangle.
// Files that fan out over Shape are re-saved so their body edges to the removed
// method are recomputed (fair compare).
func TestAtScaleGo_G7_BreakImplementation(t *testing.T) {
	runScenario(t, scenario{
		name: "G7_break_implementation",
		mutate: func(t *testing.T, root string) {
			replaceInCorpusFile(t, root, "go/shapes/shape.go",
				[2]string{"\nfunc (t Triangle) Describe() string { return \"triangle\" }\n", "\n"})
			touchCorpusFile(t, root, "go/geometry/factory.go")
			touchCorpusFile(t, root, "go/consumer/consumer.go")
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertResAbsent(t, topo, mode, idTriDescribe)
			assertNoReferences(t, topo, mode, idTriDescribe)
			assertNoConn(t, topo, mode, idShape, connImplBy, idTriangle)
			assertNoConn(t, topo, mode, idTriangle, connImplements, idShape)
			assertHasConn(t, topo, mode, idShape, connImplBy, idCircle)
		},
	})
}

// G8: add a NON-interface method to shapes.Circle and call it from the consumer
// through the value returned by geometry.MakeCircle. The consumer never imports
// shapes, yet must resolve the new method via cross-package return inference.
// Both files are edited -> fair, all three modes agree.
func TestAtScaleGo_G8_AddMethodOnReturnedStruct(t *testing.T) {
	runScenario(t, scenario{
		name: "G8_add_method_on_returned_struct",
		mutate: func(t *testing.T, root string) {
			replaceInCorpusFile(t, root, "go/shapes/shape.go",
				[2]string{
					"// Rectangle implements Shape with POINTER receivers.",
					"func (c Circle) Scale(f float64) float64 { return c.Radius * f }\n\n// Rectangle implements Shape with POINTER receivers.",
				})
			replaceInCorpusFile(t, root, "go/consumer/consumer.go",
				[2]string{"desc := c.Describe()", "desc := c.Describe()\n\t_ = c.Scale(2)"})
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertResPresent(t, topo, mode, idCircleScale)
			assertHasConn(t, topo, mode, idConsumerReport, connCalls, idCircleScale)
			assertHasConn(t, topo, mode, idConsumerReport, connCalls, idCircleArea)
		},
	})
}
