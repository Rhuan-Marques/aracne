package tests_test

// At-scale Python scenarios: predictable edits to the testing_ground/python
// corpus, asserted under incremental / full / hard with strict cross-mode
// equality (see atscale_harness_test.go).
//
// Python resource IDs are package-based and OMIT the filename:
// "aracne.testing_ground.python.<Name>" and "....<Class>.<method>".
//
// KNOWN-RED (deterministic): structural edges (inherits/inherited_by/methods)
// are recomputed globally every UpdateFile, so PY1/PY4-PY10 stay consistent;
// cross-file body edges are per-file, so PY2/PY3/PY3b diverge (same root causes
// as Go bug _2 intra-scan ordering / dependent-not-reparsed). Targeted asserts
// encode the correct (cold-scan) behavior; the cross-mode check catches the
// incremental divergence.

import (
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

const (
	pyRender       = "testing_ground/python/consumer.render"
	pyFromFactory  = "testing_ground/python/consumer.from_factory"
	pyBuildMeasure = "testing_ground/python/consumer.build_and_measure"

	pyShape         = "testing_ground/python/shapes.Shape"
	pyShapeDesc     = "testing_ground/python/shapes.Shape.describe"
	pyShapePerim    = "testing_ground/python/shapes.Shape.perimeter"
	pyCircle        = "testing_ground/python/shapes.Circle"
	pyCircleArea    = "testing_ground/python/shapes.Circle.area"
	pyCircleDesc    = "testing_ground/python/shapes.Circle.describe"
	pyCirclePerim   = "testing_ground/python/shapes.Circle.perimeter"
	pyRectangle     = "testing_ground/python/shapes.Rectangle"
	pyRectArea      = "testing_ground/python/shapes.Rectangle.area"
	pyRectPerim     = "testing_ground/python/shapes.Rectangle.perimeter"
	pyDisc          = "testing_ground/python/shapes.Disc"
	pyDiscArea      = "testing_ground/python/shapes.Disc.area"
	pyLabeled       = "testing_ground/python/shapes.LabeledCircle"
	pyLabeledDisc   = "testing_ground/python/shapes.LabeledDisc"
	pyNamed         = "testing_ground/python/shapes.Named"
	pyPentagon      = "testing_ground/python/extra.Pentagon"
	pyMakeRect      = "testing_ground/python/factory.make_rectangle"
	pyDefaultCircle = "testing_ground/python/consumer.DEFAULT_CIRCLE"
)

const pentagonPy = `from .shapes import Shape


class Pentagon(Shape):
    """A new Shape subclass added at runtime by the at-scale suite."""

    def area(self) -> float:
        return 0.0

    def describe(self) -> str:
        return "pentagon"
`

// PY1: param type-hint Shape -> Circle re-resolves the method call.
func TestAtScalePy_PY1_AnnotationResolution(t *testing.T) {
	runScenario(t, scenario{
		name: "PY1_param_annotation",
		mutate: func(t *testing.T, root string) {
			replaceInCorpusFile(t, root, "python/consumer.py",
				[2]string{"def render(shape: Shape) -> str:", "def render(shape: Circle) -> str:"})
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertHasConn(t, topo, mode, pyRender, connCalls, pyCircleDesc)
			assertNoConn(t, topo, mode, pyRender, connCalls, pyShapeDesc)
		},
	})
}

// PY2: rename class Circle -> Disc across modules (renames LabeledCircle too).
// Structural inheritance stays consistent; cross-file body resolution diverges.
func TestAtScalePy_PY2_ClassRenameAcrossModules(t *testing.T) {
	runScenario(t, scenario{
		name: "PY2_class_rename",
		mutate: func(t *testing.T, root string) {
			replaceInCorpusFile(t, root, "python/shapes.py", [2]string{"Circle", "Disc"})
			replaceInCorpusFile(t, root, "python/factory.py", [2]string{"Circle", "Disc"})
			replaceInCorpusFile(t, root, "python/consumer.py", [2]string{"Circle", "Disc"})
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertResAbsent(t, topo, mode, pyCircle)
			assertResAbsent(t, topo, mode, pyLabeled)
			assertResPresent(t, topo, mode, pyDisc)
			assertResPresent(t, topo, mode, pyLabeledDisc)
			assertNoReferences(t, topo, mode, pyCircle)
			assertHasConn(t, topo, mode, pyShape, connInheritedBy, pyDisc)
			assertHasConn(t, topo, mode, pyFromFactory, connCalls, pyDiscArea)
		},
	})
}

// PY3 (divergence probe): change make_circle's return type without editing the
// consumer; from_factory's c.area() should re-resolve to Rectangle.area.
func TestAtScalePy_PY3_ReturnTypeChange_DivergenceProbe(t *testing.T) {
	runScenario(t, scenario{
		name: "PY3_return_type_change",
		mutate: func(t *testing.T, root string) {
			replaceInCorpusFile(t, root, "python/factory.py", [2]string{
				"def make_circle(radius: float) -> Circle:\n    \"\"\"Return a Circle (cross-module type return).\"\"\"\n    return Circle(radius)",
				"def make_circle(radius: float) -> Rectangle:\n    \"\"\"Return a Rectangle (cross-module type return).\"\"\"\n    return Rectangle(radius)",
			})
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertHasConn(t, topo, mode, pyFromFactory, connCalls, pyRectArea)
			assertNoConn(t, topo, mode, pyFromFactory, connCalls, pyCircleArea)
		},
	})
}

// PY3b: PY3 + re-saving the consumer. Shows re-parsing the dependent is not
// enough (intra-scan ordering).
func TestAtScalePy_PY3b_ReturnTypeChange_ConsumerReparsed(t *testing.T) {
	runScenario(t, scenario{
		name: "PY3b_return_type_change_consumer_touched",
		mutate: func(t *testing.T, root string) {
			replaceInCorpusFile(t, root, "python/factory.py", [2]string{
				"def make_circle(radius: float) -> Circle:\n    \"\"\"Return a Circle (cross-module type return).\"\"\"\n    return Circle(radius)",
				"def make_circle(radius: float) -> Rectangle:\n    \"\"\"Return a Rectangle (cross-module type return).\"\"\"\n    return Rectangle(radius)",
			})
			touchCorpusFile(t, root, "python/consumer.py")
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertHasConn(t, topo, mode, pyFromFactory, connCalls, pyRectArea)
			assertNoConn(t, topo, mode, pyFromFactory, connCalls, pyCircleArea)
		},
	})
}

// PY4: local instantiation Circle(2.0) -> Rectangle(2.0) (self-contained).
func TestAtScalePy_PY4_LocalInstantiation(t *testing.T) {
	runScenario(t, scenario{
		name: "PY4_local_instantiation",
		mutate: func(t *testing.T, root string) {
			replaceInCorpusFile(t, root, "python/consumer.py",
				[2]string{"from .shapes import Circle, Shape", "from .shapes import Circle, Rectangle, Shape"},
				[2]string{"c = Circle(2.0)", "c = Rectangle(2.0)"})
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertHasConn(t, topo, mode, pyBuildMeasure, connCalls, pyRectArea)
			assertNoConn(t, topo, mode, pyBuildMeasure, connCalls, pyCircleArea)
		},
	})
}

// PY5: factory-return chaining make_circle -> make_rectangle (self-contained;
// reads make_rectangle's existing return TypingID).
func TestAtScalePy_PY5_FactoryChainSwap(t *testing.T) {
	runScenario(t, scenario{
		name: "PY5_factory_chain_swap",
		mutate: func(t *testing.T, root string) {
			replaceInCorpusFile(t, root, "python/consumer.py",
				[2]string{"from .factory import make_circle, total_area", "from .factory import make_circle, make_rectangle, total_area"},
				[2]string{"c = make_circle(3.0)", "c = make_rectangle(3.0)"})
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertHasConn(t, topo, mode, pyFromFactory, connCalls, pyMakeRect)
			assertHasConn(t, topo, mode, pyFromFactory, connCalls, pyRectArea)
			assertNoConn(t, topo, mode, pyFromFactory, connCalls, pyCircleArea)
		},
	})
}

// PY6: add a new file with a subclass of the Shape ABC (reverse-edge growth).
func TestAtScalePy_PY6_AddSubclass(t *testing.T) {
	runScenario(t, scenario{
		name: "PY6_add_subclass",
		mutate: func(t *testing.T, root string) {
			writeCorpusFile(t, root, "python/extra.py", pentagonPy)
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertResPresent(t, topo, mode, pyPentagon)
			assertHasConn(t, topo, mode, pyShape, connInheritedBy, pyPentagon)
			assertHasConn(t, topo, mode, pyPentagon, connInherits, pyShape)
		},
	})
}

// PY7: remove the subclass added in setup (reverse-edge shrink).
func TestAtScalePy_PY7_RemoveSubclass(t *testing.T) {
	runScenario(t, scenario{
		name: "PY7_remove_subclass",
		setup: func(t *testing.T, root string) {
			writeCorpusFile(t, root, "python/extra.py", pentagonPy)
		},
		mutate: func(t *testing.T, root string) {
			removeCorpusFile(t, root, "python/extra.py")
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertResAbsent(t, topo, mode, pyPentagon)
			assertNoReferences(t, topo, mode, pyPentagon)
			assertNoConn(t, topo, mode, pyShape, connInheritedBy, pyPentagon)
		},
	})
}

// PY8: drop a base from multiple inheritance LabeledCircle(Circle, Named) -> (Circle).
func TestAtScalePy_PY8_DropBaseClass(t *testing.T) {
	runScenario(t, scenario{
		name: "PY8_drop_base_class",
		mutate: func(t *testing.T, root string) {
			replaceInCorpusFile(t, root, "python/shapes.py",
				[2]string{"class LabeledCircle(Circle, Named):", "class LabeledCircle(Circle):"})
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertNoConn(t, topo, mode, pyNamed, connInheritedBy, pyLabeled)
			assertNoConn(t, topo, mode, pyLabeled, connInherits, pyNamed)
			assertHasConn(t, topo, mode, pyLabeled, connInherits, pyCircle)
		},
	})
}

// PY9: add an abstract method to the Shape ABC plus impls in Circle/Rectangle
// (methods is a global pass; new method resources land in the re-parsed file).
func TestAtScalePy_PY9_AddAbstractMethod(t *testing.T) {
	runScenario(t, scenario{
		name: "PY9_add_abstract_method",
		mutate: func(t *testing.T, root string) {
			replaceInCorpusFile(t, root, "python/shapes.py",
				[2]string{
					"    @abstractmethod\n    def describe(self) -> str:\n        ...",
					"    @abstractmethod\n    def describe(self) -> str:\n        ...\n\n    @abstractmethod\n    def perimeter(self) -> float:\n        ...",
				},
				[2]string{
					"    @property\n    def diameter(self) -> float:",
					"    def perimeter(self) -> float:\n        return 2 * PI * self.radius\n\n    @property\n    def diameter(self) -> float:",
				},
				[2]string{
					"        return f\"rect {self.width}x{self.height}\"",
					"        return f\"rect {self.width}x{self.height}\"\n\n    def perimeter(self) -> float:\n        return 2 * (self.width + self.height)",
				})
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertResPresent(t, topo, mode, pyShapePerim)
			assertResPresent(t, topo, mode, pyCirclePerim)
			assertResPresent(t, topo, mode, pyRectPerim)
			assertHasConn(t, topo, mode, pyShape, connMethods, pyShapePerim)
		},
	})
}

// PY10: change a module-level var initializer (cross-call -> direct
// instantiation). Module-var edge modeling is uncertain, so this asserts
// presence + relies on strict cross-mode equality.
func TestAtScalePy_PY10_ModuleVarInitializer(t *testing.T) {
	runScenario(t, scenario{
		name: "PY10_module_var_initializer",
		mutate: func(t *testing.T, root string) {
			replaceInCorpusFile(t, root, "python/consumer.py",
				[2]string{"DEFAULT_CIRCLE = make_circle(1.0)", "DEFAULT_CIRCLE = Circle(2.0)"})
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertResPresent(t, topo, mode, pyDefaultCircle)
		},
	})
}
