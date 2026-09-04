package tests_test

// At-scale Go scenarios: predictable edits to the testing_ground Go corpus,
// each asserted under incremental / full / hard and checked for strict
// cross-mode equality (see atscale_harness_test.go).
//
// Status (current working tree): G4a/G4b/G5/G6/G7 and GhostUsesInterfaceOnReparse
// PASS — the spurious-uses_interface ghost (bug ..._1) no longer reproduces, so
// the Ghost repro now acts as a regression guard. The remaining red tests are
// cross-file edits where the incremental path diverges from a cold scan:
//   G1  cross-package fn rename + consumer call update  -> stale (bug ..._2)
//   G2  struct rename across packages                   -> stale body edges + dropped
//                                                          uses_package (bugs ..._2/..._3)
//   G3  return-type change, consumer NOT re-parsed       -> divergence probe (..._2)
//   G3b same change, consumer re-saved                   -> still stale (intra-scan order)
//   G8  add method on returned struct, both files edited -> stale (intra-scan order)
// Structural reverse-edges (implemented_by) are recomputed globally and stay
// consistent (G5/G6/G7). Per-scenario targeted asserts encode the correct
// cold-scan behavior; the cross-mode check catches the incremental divergence.
//
// G1–G8 use runScenario (full multi-language corpus) and are therefore CURRENTLY
// BLOCKED by the pre-existing JS/TS full-scan crash (a duplicate-connection
// write-abort in the untracked jsfamily/tsfamily WIP that aborts copyCorpus's
// scan). That crash is unrelated to Go and is tracked separately.
//
// G9–G19 cover additional Go edge cases and use runGoScenario (a GO-ONLY corpus
// copy), so they run independently of that crash — mirroring the python-only
// isolation trick. GREEN: G9 (mutual recursion calls), G10 (error interface +
// sentinel), G11 (cross-pkg embedding / promotion-not-modeled), G14 (defer/go
// calls), G15 (unexported interface matching + cross-visibility calls), G16
// (init + package-init var), G18 (iota-expr/typed consts/struct tags), G19 (dot
// import — no false internal edge). RED PROBES (assert the correct edge the
// scanner does not yet produce; cross-mode-consistent, fail only on the targeted
// assert): G12 method value/expression (bug ..._1), G13 type assertion/switch
// (bug ..._2), G17 generic-interface satisfaction (bug ..._3). The same-file
// duplicate-init write-abort (bug ..._4) is documented, not added to the corpus.

import (
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// Stable resource IDs in the Go corpus (module path: aracne).
const (
	idConsumerReport = "github.com/Rhuan-Marques/aracne/testing_ground/go/consumer.Report"
	idConsumerTotal  = "github.com/Rhuan-Marques/aracne/testing_ground/go/consumer.Total"
	idGeomMakeCircle = "github.com/Rhuan-Marques/aracne/testing_ground/go/geometry.MakeCircle"
	idGeomMintCircle = "github.com/Rhuan-Marques/aracne/testing_ground/go/geometry.MintCircle"

	idShape       = "github.com/Rhuan-Marques/aracne/testing_ground/go/shapes.Shape"
	idCircle      = "github.com/Rhuan-Marques/aracne/testing_ground/go/shapes.Circle"
	idCircleArea  = "github.com/Rhuan-Marques/aracne/testing_ground/go/shapes.(Circle).Area"
	idCirclePerim = "github.com/Rhuan-Marques/aracne/testing_ground/go/shapes.(Circle).Perimeter"
	idCircleScale = "github.com/Rhuan-Marques/aracne/testing_ground/go/shapes.(Circle).Scale"
	idDisc        = "github.com/Rhuan-Marques/aracne/testing_ground/go/shapes.Disc"
	idDiscArea    = "github.com/Rhuan-Marques/aracne/testing_ground/go/shapes.(Disc).Area"
	idRectArea    = "github.com/Rhuan-Marques/aracne/testing_ground/go/shapes.(Rectangle).Area"
	idTriangle    = "github.com/Rhuan-Marques/aracne/testing_ground/go/shapes.Triangle"
	idTriDescribe = "github.com/Rhuan-Marques/aracne/testing_ground/go/shapes.(Triangle).Describe"

	idHexagon     = "github.com/Rhuan-Marques/aracne/testing_ground/go/shapes.Hexagon"
	idHexagonArea = "github.com/Rhuan-Marques/aracne/testing_ground/go/shapes.(Hexagon).Area"
)

// Stable resource IDs for the edge-case packages exercised by G9–G19.
const (
	// recursive
	idRecPing    = "github.com/Rhuan-Marques/aracne/testing_ground/go/recursive.ping"
	idRecPong    = "github.com/Rhuan-Marques/aracne/testing_ground/go/recursive.pong"
	idRecBounce  = "github.com/Rhuan-Marques/aracne/testing_ground/go/recursive.Bounce"
	idRecNodeLen = "github.com/Rhuan-Marques/aracne/testing_ground/go/recursive.(Node).Length"

	// failure (error interface + sentinel)
	idFailValidator = "github.com/Rhuan-Marques/aracne/testing_ground/go/failure.Validator"
	idFailForm      = "github.com/Rhuan-Marques/aracne/testing_ground/go/failure.Form"
	idFailValidate  = "github.com/Rhuan-Marques/aracne/testing_ground/go/failure.(Form).Validate"
	idFailParseErr  = "github.com/Rhuan-Marques/aracne/testing_ground/go/failure.ParseError"
	idFailErrEmpty  = "github.com/Rhuan-Marques/aracne/testing_ground/go/failure.ErrEmpty"
	idFailCheck     = "github.com/Rhuan-Marques/aracne/testing_ground/go/failure.Check"
	idFailWrap      = "github.com/Rhuan-Marques/aracne/testing_ground/go/failure.wrap"

	// embedding
	idEmbRecursivePkg = "github.com/Rhuan-Marques/aracne/testing_ground/go/recursive"
	idEmbDecorated    = "github.com/Rhuan-Marques/aracne/testing_ground/go/embedding.Decorated"
	idEmbDerived      = "github.com/Rhuan-Marques/aracne/testing_ground/go/embedding.Derived"
	idEmbGreet        = "github.com/Rhuan-Marques/aracne/testing_ground/go/embedding.(Derived).Greet"
	idEmbBaseHello    = "github.com/Rhuan-Marques/aracne/testing_ground/go/embedding.(Base).Hello"

	// dispatch (method value/expr, type assertion/switch)
	idDispDogSound = "github.com/Rhuan-Marques/aracne/testing_ground/go/dispatch.(Dog).Sound"
	idDispCatSound = "github.com/Rhuan-Marques/aracne/testing_ground/go/dispatch.(Cat).Sound"
	idDispMValue   = "github.com/Rhuan-Marques/aracne/testing_ground/go/dispatch.UseMethodValue"
	idDispMExpr    = "github.com/Rhuan-Marques/aracne/testing_ground/go/dispatch.UseMethodExpr"
	idDispAssert   = "github.com/Rhuan-Marques/aracne/testing_ground/go/dispatch.AssertAnimal"
	idDispSwitch   = "github.com/Rhuan-Marques/aracne/testing_ground/go/dispatch.SwitchAnimal"

	// concurrency (defer / go)
	idConcWork    = "github.com/Rhuan-Marques/aracne/testing_ground/go/concurrency.Work"
	idConcSpawn   = "github.com/Rhuan-Marques/aracne/testing_ground/go/concurrency.Spawn"
	idConcSelect  = "github.com/Rhuan-Marques/aracne/testing_ground/go/concurrency.Select"
	idConcProcess = "github.com/Rhuan-Marques/aracne/testing_ground/go/concurrency.process"
	idConcCleanup = "github.com/Rhuan-Marques/aracne/testing_ground/go/concurrency.cleanup"

	// visibility (unexported resources)
	idVisReader      = "github.com/Rhuan-Marques/aracne/testing_ground/go/visibility.reader"
	idVisFileReader  = "github.com/Rhuan-Marques/aracne/testing_ground/go/visibility.fileReader"
	idVisNewReader   = "github.com/Rhuan-Marques/aracne/testing_ground/go/visibility.newReader"
	idVisLoad        = "github.com/Rhuan-Marques/aracne/testing_ground/go/visibility.Load"
	idVisDefaultPath = "github.com/Rhuan-Marques/aracne/testing_ground/go/visibility.defaultPath"

	// inits
	idInitInit     = "github.com/Rhuan-Marques/aracne/testing_ground/go/inits.init"
	idInitConfig   = "github.com/Rhuan-Marques/aracne/testing_ground/go/inits.Config"
	idInitReady    = "github.com/Rhuan-Marques/aracne/testing_ground/go/inits.Ready"
	idInitReadyVar = "github.com/Rhuan-Marques/aracne/testing_ground/go/inits.ready"

	// dotimport
	idDotShout   = "github.com/Rhuan-Marques/aracne/testing_ground/go/dotimport.Shout"
	idDotLoud    = "github.com/Rhuan-Marques/aracne/testing_ground/go/dotimport.Loud"
	idDotToUpper = "github.com/Rhuan-Marques/aracne/testing_ground/go/dotimport.ToUpper" // phantom; must NOT exist as an edge

	// generics (generic interface)
	idGenContainer = "github.com/Rhuan-Marques/aracne/testing_ground/go/generics.Container"
	idGenBox       = "github.com/Rhuan-Marques/aracne/testing_ground/go/generics.Box"

	// edge (funcvars.go: package-level func-typed vars called through the var)
	idEdgeColorize        = "github.com/Rhuan-Marques/aracne/testing_ground/go/edge.Colorize"
	idEdgeShout           = "github.com/Rhuan-Marques/aracne/testing_ground/go/edge.Shout"
	idEdgeHandleVar       = "github.com/Rhuan-Marques/aracne/testing_ground/go/edge.Handle"
	idEdgeDecorate        = "github.com/Rhuan-Marques/aracne/testing_ground/go/edge.Decorate"
	idEdgeDecorateChecked = "github.com/Rhuan-Marques/aracne/testing_ground/go/edge.DecorateChecked"
	idConsumerDecorated   = "github.com/Rhuan-Marques/aracne/testing_ground/go/consumer.Decorated"

	// edge (more.go: iota expr / typed consts / struct tags)
	idEdgeKB       = "github.com/Rhuan-Marques/aracne/testing_ground/go/edge.KB"
	idEdgePriority = "github.com/Rhuan-Marques/aracne/testing_ground/go/edge.Priority"
	idEdgeTagged   = "github.com/Rhuan-Marques/aracne/testing_ground/go/edge.Tagged"
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

// ---------------------------------------------------------------------------
// G9–G19: edge-case packages added to the Go corpus. Each touches a single
// self-contained file so the construct is validated under incremental AND the
// two cold modes, with strict cross-mode equality. Assertions encode the
// authoritative cold-scan behavior. Scenarios marked "(red probe)" assert the
// CORRECT behavior that the scanner does NOT yet produce — they fail on their
// targeted assert (cross-mode equality still holds, since all modes agree on
// the absence) and are traceable to a filed bug, mirroring G1–G3.
// ---------------------------------------------------------------------------

// G9: mutual recursion. ping<->pong and Bounce->ping each carry a calls edge.
// A self-recursive method call through a struct field ((Node).Length ->
// n.Next.Length()) is NOT resolved; asserted absent to document the limit.
func TestAtScaleGo_G9_Recursion(t *testing.T) {
	runGoScenario(t, scenario{
		name:   "G9_recursion",
		mutate: func(t *testing.T, root string) { touchCorpusFile(t, root, "go/recursive/recursive.go") },
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertHasConn(t, topo, mode, idRecBounce, connCalls, idRecPing)
			assertHasConn(t, topo, mode, idRecPing, connCalls, idRecPong)
			assertHasConn(t, topo, mode, idRecPong, connCalls, idRecPing)
			assertNoConn(t, topo, mode, idRecNodeLen, connCalls, idRecNodeLen) // field-method recursion not modeled
		},
	})
}

// G10: error-handling. A struct implementing a LOCAL interface whose method
// returns the builtin error; a method using a sentinel error var; constructor
// chaining. The struct implementing only the BUILTIN error must NOT be wired to
// the local Validator interface.
func TestAtScaleGo_G10_ErrorInterface(t *testing.T) {
	runGoScenario(t, scenario{
		name:   "G10_error_interface",
		mutate: func(t *testing.T, root string) { touchCorpusFile(t, root, "go/failure/failure.go") },
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertResPresent(t, topo, mode, idFailParseErr)
			assertHasConn(t, topo, mode, idFailValidator, connImplBy, idFailForm)
			assertHasConn(t, topo, mode, idFailForm, connImplements, idFailValidator)
			assertHasConn(t, topo, mode, idFailValidate, connUsesExtvar, idFailErrEmpty)
			assertHasConn(t, topo, mode, idFailValidate, connUsesStruct, idFailParseErr)
			assertHasConn(t, topo, mode, idFailCheck, connCalls, idFailValidate)
			assertHasConn(t, topo, mode, idFailCheck, connCalls, idFailWrap)
			assertNoConn(t, topo, mode, idFailValidator, connImplBy, idFailParseErr) // builtin-error boundary
		},
	})
}

// G11: embedding. Cross-package embedding records uses_package (NOT uses_struct
// to the embedded type), and promoted methods are not modeled (Greet's call to
// the promoted Base.Hello does not resolve) — both asserted to lock current
// behavior.
func TestAtScaleGo_G11_Embedding(t *testing.T) {
	runGoScenario(t, scenario{
		name:   "G11_embedding",
		mutate: func(t *testing.T, root string) { touchCorpusFile(t, root, "go/embedding/embedding.go") },
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertResPresent(t, topo, mode, idEmbDerived)
			assertHasConn(t, topo, mode, idEmbDecorated, connUsesPkg, idEmbRecursivePkg)
			assertNoConn(t, topo, mode, idEmbGreet, connCalls, idEmbBaseHello) // promotion not modeled
		},
	})
}

// G12 (red probe): a method VALUE (f := d.Sound) and a method EXPRESSION
// (Dog.Sound) bound to a var and called do NOT resolve to a calls edge today
// (filed: bug_1782267868402936812_1).
func TestAtScaleGo_G12_MethodValueExpr(t *testing.T) {
	runGoScenario(t, scenario{
		name:   "G12_method_value_expr",
		mutate: func(t *testing.T, root string) { touchCorpusFile(t, root, "go/dispatch/dispatch.go") },
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertHasConn(t, topo, mode, idDispMValue, connCalls, idDispDogSound)
			assertHasConn(t, topo, mode, idDispMExpr, connCalls, idDispDogSound)
		},
	})
}

// G13 (red probe): a method call on a variable bound by a TYPE ASSERTION
// (a.(Dog)) or a TYPE SWITCH (switch a.(type)) does NOT resolve to a calls edge
// (filed: bug_1782267872456641805_2).
func TestAtScaleGo_G13_TypeAssertSwitch(t *testing.T) {
	runGoScenario(t, scenario{
		name:   "G13_type_assert_switch",
		mutate: func(t *testing.T, root string) { touchCorpusFile(t, root, "go/dispatch/dispatch.go") },
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertHasConn(t, topo, mode, idDispAssert, connCalls, idDispDogSound)
			assertHasConn(t, topo, mode, idDispSwitch, connCalls, idDispDogSound)
			assertHasConn(t, topo, mode, idDispSwitch, connCalls, idDispCatSound)
		},
	})
}

// G14: concurrency. A DEFERRED call, a GOROUTINE on a named function, an
// ANONYMOUS goroutine, and a SELECT case all produce ordinary calls edges.
func TestAtScaleGo_G14_DeferGoroutine(t *testing.T) {
	runGoScenario(t, scenario{
		name:   "G14_defer_goroutine",
		mutate: func(t *testing.T, root string) { touchCorpusFile(t, root, "go/concurrency/concurrency.go") },
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertHasConn(t, topo, mode, idConcWork, connCalls, idConcCleanup)  // defer cleanup()
			assertHasConn(t, topo, mode, idConcWork, connCalls, idConcProcess)  // go process(j)
			assertHasConn(t, topo, mode, idConcSpawn, connCalls, idConcProcess) // go func(){ process() }()
			assertHasConn(t, topo, mode, idConcSelect, connCalls, idConcProcess)
		},
	})
}

// G15: visibility. An unexported struct implements an unexported interface, and
// an exported entry point resolves calls to the unexported constructor + method
// across the visibility boundary.
func TestAtScaleGo_G15_Visibility(t *testing.T) {
	runGoScenario(t, scenario{
		name:   "G15_visibility",
		mutate: func(t *testing.T, root string) { touchCorpusFile(t, root, "go/visibility/visibility.go") },
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertResPresent(t, topo, mode, idVisReader)
			assertResPresent(t, topo, mode, idVisFileReader)
			assertHasConn(t, topo, mode, idVisReader, connImplBy, idVisFileReader)
			assertHasConn(t, topo, mode, idVisFileReader, connImplements, idVisReader)
			assertHasConn(t, topo, mode, idVisLoad, connCalls, idVisNewReader)
			assertHasConn(t, topo, mode, idVisLoad, connUsesInterface, idVisReader)
			assertHasConn(t, topo, mode, idVisNewReader, connUsesExtvar, idVisDefaultPath)
		},
	})
}

// G16: init function + a package-level var initialized by a local call. The
// single init resource (id ...inits.init) and the Config var are present, and
// init/Ready both reference the package var. (NOTE: two init() in the SAME file
// collide on this id and abort the scan write — bug_1782267882609196041_4,
// documented in the corpus README; deliberately NOT added to the corpus here.)
func TestAtScaleGo_G16_InitAndPackageVar(t *testing.T) {
	runGoScenario(t, scenario{
		name:   "G16_init_and_package_var",
		mutate: func(t *testing.T, root string) { touchCorpusFile(t, root, "go/inits/inits.go") },
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertResPresent(t, topo, mode, idInitInit)
			assertResPresent(t, topo, mode, idInitConfig)
			assertHasConn(t, topo, mode, idInitInit, connUsesExtvar, idInitReadyVar)
			assertHasConn(t, topo, mode, idInitReady, connUsesExtvar, idInitReadyVar)
		},
	})
}

// G17 (red probe): Box[T] structurally satisfies the generic interface
// Container[T] (Add(T)/Get(int)T), but generic-interface satisfaction matching
// is not implemented, so the implements/implemented_by edges are missing
// (filed: bug_1782267877103805981_3).
func TestAtScaleGo_G17_GenericInterfaceImpl(t *testing.T) {
	runGoScenario(t, scenario{
		name:   "G17_generic_interface_impl",
		mutate: func(t *testing.T, root string) { touchCorpusFile(t, root, "go/generics/generics.go") },
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertResPresent(t, topo, mode, idGenContainer)
			assertResPresent(t, topo, mode, idGenBox)
			assertHasConn(t, topo, mode, idGenContainer, connImplBy, idGenBox)
			assertHasConn(t, topo, mode, idGenBox, connImplements, idGenContainer)
		},
	})
}

// G18: iota-EXPRESSION constants, a TYPED const block, and a STRUCT-TAGGED type
// all become resources, and struct tags do not produce spurious edges.
func TestAtScaleGo_G18_IotaTypedConstsTags(t *testing.T) {
	runGoScenario(t, scenario{
		name:   "G18_iota_typed_consts_tags",
		mutate: func(t *testing.T, root string) { touchCorpusFile(t, root, "go/edge/more.go") },
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertResPresent(t, topo, mode, idEdgeKB)       // 1 << (10 * iota)
			assertResPresent(t, topo, mode, idEdgePriority) // typed-const defined type
			assertResPresent(t, topo, mode, idEdgeTagged)   // struct with json/xml tags
		},
	})
}

// G19: a DOT IMPORT (import . "strings"). The unqualified ToUpper must NOT
// become a spurious internal edge; Loud's call to the internal Shout still
// resolves. (A use_missing_node warning for ToUpper is emitted but warnings are
// excluded from the cross-mode compare.)
func TestAtScaleGo_G19_DotImport(t *testing.T) {
	runGoScenario(t, scenario{
		name:   "G19_dot_import",
		mutate: func(t *testing.T, root string) { touchCorpusFile(t, root, "go/dotimport/dotimport.go") },
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertResPresent(t, topo, mode, idDotShout)
			assertHasConn(t, topo, mode, idDotLoud, connCalls, idDotShout)
			assertNoConn(t, topo, mode, idDotShout, connCalls, idDotToUpper)
		},
	})
}

// G20: package-level func-typed vars are call targets, not missing nodes.
//
// Regression cover for the defect that shipped 145 false `use_missing_node`
// warnings in the cli/cli fixture: calls through a package var of function type
// (`var Yellow = makeColorFunc(...)`, called as `utils.Yellow(s)`) resolved
// against functions/structs/named types only, so a node that existed in the
// database was reported as nonexistent and the caller lost the edge.
//
// Pinning the edge in all three scan modes is what makes the false warning
// impossible: the warning was only ever emitted on the path that failed to
// produce this edge.
func TestAtScaleGo_G20_PackageFuncVars(t *testing.T) {
	runGoScenario(t, scenario{
		name:   "G20_package_func_vars",
		mutate: func(t *testing.T, root string) { touchCorpusFile(t, root, "go/edge/funcvars.go") },
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertResPresent(t, topo, mode, idEdgeColorize)
			assertResPresent(t, topo, mode, idEdgeShout)
			assertResPresent(t, topo, mode, idEdgeHandleVar)

			// same-package calls through the vars
			assertHasConn(t, topo, mode, idEdgeDecorate, connUsesExtvar, idEdgeColorize)
			assertHasConn(t, topo, mode, idEdgeDecorate, connUsesExtvar, idEdgeShout)
			assertHasConn(t, topo, mode, idEdgeDecorateChecked, connUsesExtvar, idEdgeHandleVar)

			// cross-package call through the var: the cli/cli shape
			assertHasConn(t, topo, mode, idConsumerDecorated, connUsesExtvar, idEdgeColorize)

			// The extvar arm deliberately emits no uses_package edge, mirroring
			// resolveUseMissingWarning's extvar case. Cold and incremental must
			// agree here or assertSameGraph3 fails.
			assertNoConn(t, topo, mode, idConsumerDecorated, connUsesPkg, "github.com/Rhuan-Marques/aracne/testing_ground/go/edge")

			// and none of it is reported as missing
			for _, w := range topo.Warnings {
				if w.Kind == domain.WarnUseMissingNode &&
					(w.TargetID == idEdgeColorize || w.TargetID == idEdgeShout || w.TargetID == idEdgeHandleVar) {
					t.Errorf("[%s] func-typed package var reported missing: %s", mode, w.Message)
				}
			}
		},
	})
}
