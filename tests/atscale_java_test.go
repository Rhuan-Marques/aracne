package tests_test

// At-scale Java scenarios: predictable edits to the testing_ground/javafamily
// corpus (Maven layout, package com.aracne.*), each asserted under incremental /
// full / hard and checked for strict cross-mode equality (see
// atscale_harness_test.go). Java symbol IDs are FQN-based and path-independent,
// so the hardcoded IDs below hold regardless of the temp scan root.
//
// All J1-J9 use runJavaScenario (a JAVA-ONLY corpus copy), so they run
// independently of the pre-existing JS/TS multi-language scan-write abort. The
// Java scanner rebuilds structural edges (inherits/implements + reverse edges)
// whole-graph from module-owned hierarchy records and derives IDs purely at
// parse time, so full == incremental == hard; every scenario is expected GREEN.

import (
	"os"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// Stable FQN resource IDs in the Java corpus (from the authoritative scan dump).
const (
	jShape       = "com.aracne.shapes.Shape"
	jShapeArea   = "com.aracne.shapes.Shape.area()"
	jCircle      = "com.aracne.shapes.Circle"
	jCircleInit  = "com.aracne.shapes.Circle.<init>(double)"
	jCircleArea  = "com.aracne.shapes.Circle.area()"
	jCircleAreaI = "com.aracne.shapes.Circle.area(int)"
	jCircleAreaD = "com.aracne.shapes.Circle.area(double)" // J6: added overload
	jRectangle   = "com.aracne.shapes.Rectangle"
	jRectInit    = "com.aracne.shapes.Rectangle.<init>(double,double)"
	jRectArea    = "com.aracne.shapes.Rectangle.area()"
	jTriangle    = "com.aracne.shapes.Triangle" // J4/J5: added implementer
	jTriangleAr  = "com.aracne.shapes.Triangle.area()"
	jDisk        = "com.aracne.shapes.Disk" // J2: Circle renamed to Disk
	jDiskInit    = "com.aracne.shapes.Disk.<init>(double)"
	jDiskArea    = "com.aracne.shapes.Disk.area()"

	jFactory    = "com.aracne.factory.Factory"
	jMakeCircle = "com.aracne.factory.Factory.makeCircle(double)"
	jMakeRect   = "com.aracne.factory.Factory.makeRect(double,double)"

	jConsumer    = "com.aracne.consumer.Consumer"
	jConsumerTot = "com.aracne.consumer.Consumer.total()"

	jBase         = "com.aracne.inheritance.Base"
	jBaseInit     = "com.aracne.inheritance.Base.<init>(String)"
	jDerived      = "com.aracne.inheritance.Derived"
	jDerivedCtor0 = "com.aracne.inheritance.Derived.<init>()"
	jDerivedCtor1 = "com.aracne.inheritance.Derived.<init>(int)"
	jWidget       = "com.aracne.inheritance.Widget"

	jEvents      = "com.aracne.lambdas.Events"
	jEvWire      = "com.aracne.lambdas.Events.wire()"
	jEvIncrement = "com.aracne.lambdas.Events.increment()"
	jEvAreaOf    = "com.aracne.lambdas.Events.areaOf(Circle)"
	jEvMaker     = "com.aracne.lambdas.Events.maker()"
	jEvMakerRef  = "com.aracne.lambdas.Events.makerRef()"
)

// Relative corpus paths (under <root>/testing_ground/).
const (
	javaSrc   = "javafamily/src/main/java/com/aracne/"
	fFactory  = javaSrc + "factory/Factory.java"
	fCircle   = javaSrc + "shapes/Circle.java"
	fDisk     = javaSrc + "shapes/Disk.java"
	fTriangle = javaSrc + "shapes/Triangle.java"
	fDerived  = javaSrc + "inheritance/Derived.java"
	fConsumer = javaSrc + "consumer/Consumer.java"
	fEvents   = javaSrc + "lambdas/Events.java"
)

// triangleJava is a NEW Shape implementer added/removed by J4/J5.
const triangleJava = `package com.aracne.shapes;

// A NEW Shape implementer added at runtime by the at-scale suite.
public class Triangle implements Shape {
    private final double base;
    private final double height;

    public Triangle(double base, double height) {
        this.base = base;
        this.height = height;
    }

    @Override
    public double area() {
        return 0.5 * base * height;
    }
}
`

// diskJava is the renamed Circle class written by J2 (class + file rename).
const diskJava = `package com.aracne.shapes;

// Renamed from Circle by the J2 at-scale scenario (class + file rename).
public class Disk implements Shape {
    private final double radius;

    public Disk(double radius) {
        this.radius = radius;
    }

    @Override
    public double area() {
        return Math.PI * radius * radius;
    }

    public double area(int scale) {
        return area() * scale;
    }
}
`

// J1: in factory/Factory.java swap `new Circle(r)` -> `new Rectangle(r, r)` in
// makeCircle's body. makeCircle must now construct Rectangle and drop every edge
// to Circle. Single-file edit -> all three modes agree.
func TestAtScaleJava_J1_FactorySwapCircleToRectangle(t *testing.T) {
	runJavaScenario(t, scenario{
		name: "J1_factory_new_circle_to_rectangle",
		mutate: func(t *testing.T, root string) {
			replaceInCorpusFile(t, root, fFactory,
				[2]string{"return new Circle(r);", "return new Rectangle(r, r);"})
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertHasConn(t, topo, mode, jMakeCircle, connCalls, jRectInit)
			assertHasConn(t, topo, mode, jMakeCircle, connUsesStruct, jRectangle)
			assertNoConn(t, topo, mode, jMakeCircle, connCalls, jCircleInit)
			assertNoConn(t, topo, mode, jMakeCircle, connUsesStruct, jCircle)
		},
	})
}

// J2: rename class Circle -> Disk (file Circle.java -> Disk.java) and its
// importers (Factory + Events; Consumer reaches Circle only through Factory's
// Shape return, so it is untouched). The old symbol must vanish everywhere, the
// interface's implemented_by must re-point at Disk (structural whole-graph
// rebuild), and the importers' body edges must follow to Disk.
func TestAtScaleJava_J2_RenameCircleToDisk(t *testing.T) {
	runJavaScenario(t, scenario{
		name: "J2_rename_circle_to_disk",
		mutate: func(t *testing.T, root string) {
			removeCorpusFile(t, root, fCircle)
			writeCorpusFile(t, root, fDisk, diskJava)
			replaceInCorpusFile(t, root, fFactory,
				[2]string{"import com.aracne.shapes.Circle;", "import com.aracne.shapes.Disk;"},
				[2]string{"return new Circle(r);", "return new Disk(r);"})
			replaceInCorpusFile(t, root, fEvents,
				[2]string{"import com.aracne.shapes.Circle;", "import com.aracne.shapes.Disk;"},
				[2]string{"Function<Circle, Double> f = Circle::area;", "Function<Disk, Double> f = Disk::area;"},
				[2]string{"public double areaOf(Circle c)", "public double areaOf(Disk c)"},
				[2]string{"return () -> new Circle(1.0);", "return () -> new Disk(1.0);"},
				[2]string{"public Supplier<Circle> maker()", "public Supplier<Disk> maker()"},
				[2]string{"public Function<Double, Circle> makerRef()", "public Function<Double, Disk> makerRef()"},
				[2]string{"Function<Double, Circle> ctor = Circle::new;", "Function<Double, Disk> ctor = Disk::new;"},
			)
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertResPresent(t, topo, mode, jDisk)
			assertResPresent(t, topo, mode, jDiskInit)
			assertResPresent(t, topo, mode, jDiskArea)
			assertResAbsent(t, topo, mode, jCircle)
			assertResAbsent(t, topo, mode, jCircleInit)
			assertNoReferences(t, topo, mode, jCircle)
			assertNoReferences(t, topo, mode, jCircleInit)
			assertHasConn(t, topo, mode, jShape, connImplBy, jDisk)
			assertHasConn(t, topo, mode, jDisk, connImplements, jShape)
			assertHasConn(t, topo, mode, jMakeCircle, connUsesStruct, jDisk)
			assertHasConn(t, topo, mode, jMakeCircle, connCalls, jDiskInit)
		},
	})
}

// J3: in inheritance/Derived.java change `extends Base` -> `extends Widget`. The
// struct<->struct inherits edge must flip: Derived inherits Widget / Widget
// inherited_by Derived, and NO Derived inherits Base / Base inherited_by Derived.
// Structural edges are rebuilt whole-graph from Derived.java's record.
func TestAtScaleJava_J3_DerivedExtendsWidget(t *testing.T) {
	runJavaScenario(t, scenario{
		name: "J3_derived_extends_widget",
		mutate: func(t *testing.T, root string) {
			replaceInCorpusFile(t, root, fDerived,
				[2]string{"public class Derived extends Base {", "public class Derived extends Widget {"})
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertHasConn(t, topo, mode, jDerived, connInherits, jWidget)
			assertHasConn(t, topo, mode, jWidget, connInheritedBy, jDerived)
			assertNoConn(t, topo, mode, jDerived, connInherits, jBase)
			assertNoConn(t, topo, mode, jBase, connInheritedBy, jDerived)
		},
	})
}

// J4: add a new shapes/Triangle.java implementing Shape. The reverse edge is
// recomputed whole-graph, so Shape implemented_by Triangle / Triangle implements
// Shape grow identically across modes.
func TestAtScaleJava_J4_AddTriangleImplementer(t *testing.T) {
	runJavaScenario(t, scenario{
		name: "J4_add_triangle_implementer",
		mutate: func(t *testing.T, root string) {
			writeCorpusFile(t, root, fTriangle, triangleJava)
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertResPresent(t, topo, mode, jTriangle)
			assertResPresent(t, topo, mode, jTriangleAr)
			assertHasConn(t, topo, mode, jShape, connImplBy, jTriangle)
			assertHasConn(t, topo, mode, jTriangle, connImplements, jShape)
			assertHasConn(t, topo, mode, jShape, connImplBy, jCircle) // existing intact
		},
	})
}

// J5: remove the Triangle.java added in setup. Its resources vanish, all
// references are swept, and the interface's implemented_by shrinks back.
func TestAtScaleJava_J5_RemoveTriangleImplementer(t *testing.T) {
	runJavaScenario(t, scenario{
		name: "J5_remove_triangle_implementer",
		setup: func(t *testing.T, root string) {
			writeCorpusFile(t, root, fTriangle, triangleJava)
		},
		mutate: func(t *testing.T, root string) {
			removeCorpusFile(t, root, fTriangle)
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertResAbsent(t, topo, mode, jTriangle)
			assertResAbsent(t, topo, mode, jTriangleAr)
			assertNoReferences(t, topo, mode, jTriangle)
			assertNoConn(t, topo, mode, jShape, connImplBy, jTriangle)
			assertHasConn(t, topo, mode, jShape, connImplBy, jCircle) // others remain
		},
	})
}

// J6: add an overload `double area(double k){return area();}` to Circle. All
// three area overloads must coexist as distinct IDs and ALL be has_method of
// Circle (overloading lock-in). Events.areaOf's `Circle::area` method reference
// fans out by NAME over Circle's overloads, so adding area(double) re-points
// that reference in a cold scan; Events.java is touched so the incremental scan
// re-resolves it too (a FAIR cross-mode compare — the overload lock-in itself is
// what this scenario asserts).
func TestAtScaleJava_J6_AddAreaDoubleOverload(t *testing.T) {
	runJavaScenario(t, scenario{
		name: "J6_add_area_double_overload",
		mutate: func(t *testing.T, root string) {
			replaceInCorpusFile(t, root, fCircle,
				[2]string{
					"    public double area(int scale) {\n        return area() * scale;\n    }\n}",
					"    public double area(int scale) {\n        return area() * scale;\n    }\n\n    public double area(double k) {\n        return area();\n    }\n}",
				})
			touchCorpusFile(t, root, fEvents)
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertResPresent(t, topo, mode, jCircleArea)
			assertResPresent(t, topo, mode, jCircleAreaI)
			assertResPresent(t, topo, mode, jCircleAreaD)
			assertHasConn(t, topo, mode, jCircle, connMethods, jCircleArea)
			assertHasConn(t, topo, mode, jCircle, connMethods, jCircleAreaI)
			assertHasConn(t, topo, mode, jCircle, connMethods, jCircleAreaD)
		},
	})
}

// J7: constructor-chaining lock-in on the UNMUTATED corpus (empty mutate). The
// no-arg ctor chains through this() to Derived.<init>(int), which chains through
// super() to Base.<init>(String).
func TestAtScaleJava_J7_CtorChainingLockIn(t *testing.T) {
	runJavaScenario(t, scenario{
		name:   "J7_ctor_chaining_lockin",
		mutate: func(t *testing.T, root string) {},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertHasConn(t, topo, mode, jDerivedCtor0, connCalls, jDerivedCtor1)
			assertHasConn(t, topo, mode, jDerivedCtor1, connCalls, jBaseInit)
		},
	})
}

// J8: in consumer/Consumer.java swap the factory call makeCircle -> makeRect.
// total() must now call Factory.makeRect(double,double) and NOT makeCircle, while
// the Shape.area() call (through the Shape-typed local) still resolves.
func TestAtScaleJava_J8_ConsumerSwapFactoryCall(t *testing.T) {
	runJavaScenario(t, scenario{
		name: "J8_consumer_makecircle_to_makerect",
		mutate: func(t *testing.T, root string) {
			replaceInCorpusFile(t, root, fConsumer,
				[2]string{"Shape s = Factory.makeCircle(2.0);", "Shape s = Factory.makeRect(2.0, 3.0);"})
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertHasConn(t, topo, mode, jConsumerTot, connCalls, jMakeRect)
			assertHasConn(t, topo, mode, jConsumerTot, connCalls, jShapeArea)
			assertNoConn(t, topo, mode, jConsumerTot, connCalls, jMakeCircle)
			assertHasConn(t, topo, mode, jConsumerTot, connUsesStruct, jFactory)
			assertHasConn(t, topo, mode, jConsumerTot, connUsesInterface, jShape)
		},
	})
}

// J9: lambda / method-reference lock-in (touch Events.java to force a re-parse).
// Calls inside lambda bodies attribute to the enclosing method; an instance
// method reference Circle::area resolves BOTH overloads by name; a lambda that
// constructs a Circle and the constructor reference Circle::new both record the
// ctor edge.
func TestAtScaleJava_J9_LambdaMethodRefLockIn(t *testing.T) {
	runJavaScenario(t, scenario{
		name:   "J9_lambda_method_ref_lockin",
		mutate: func(t *testing.T, root string) { touchCorpusFile(t, root, fEvents) },
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertHasConn(t, topo, mode, jEvWire, connCalls, jEvIncrement)
			assertHasConn(t, topo, mode, jEvAreaOf, connCalls, jCircleArea)
			assertHasConn(t, topo, mode, jEvAreaOf, connCalls, jCircleAreaI)
			assertHasConn(t, topo, mode, jEvAreaOf, connUsesStruct, jCircle)
			assertHasConn(t, topo, mode, jEvMaker, connCalls, jCircleInit)
			assertHasConn(t, topo, mode, jEvMakerRef, connCalls, jCircleInit)
		},
	})
}

// J10 (JV-1): an internal type sharing its simple name with java.util.List must
// not capture the corpus's `import java.util.List` users. The setup seeds
// shapes/List.java (the copy only); the importers are touched so the incremental
// scan re-resolves them too, and every mode must leave them unbound.
func TestAtScaleJava_J10_InternalListDoesNotCaptureJavaUtilList(t *testing.T) {
	const (
		jShapesList    = "com.aracne.shapes.List"
		jShapesListAdd = "com.aracne.shapes.List.add(Object)"
		jExtNames      = "com.aracne.external.ExternalUser.names()"
		jBoxTotal      = "com.aracne.generics.Box.total(List)"
		fShapesList    = javaSrc + "shapes/List.java"
		fExternalUser  = javaSrc + "external/ExternalUser.java"
		fBox           = javaSrc + "generics/Box.java"
	)
	runJavaScenario(t, scenario{
		name: "J10_internal_list_vs_java_util_list",
		setup: func(t *testing.T, root string) {
			writeCorpusFile(t, root, fShapesList,
				"package com.aracne.shapes;\n\npublic class List {\n    public void add(Object o) {\n    }\n}\n")
		},
		mutate: func(t *testing.T, root string) {
			touchCorpusFile(t, root, fExternalUser)
			touchCorpusFile(t, root, fBox)
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertResPresent(t, topo, mode, jShapesList)
			for _, id := range []string{jExtNames, jBoxTotal} {
				assertResPresent(t, topo, mode, id)
				assertNoConn(t, topo, mode, id, connUsesStruct, jShapesList)
				assertNoConn(t, topo, mode, id, connCalls, jShapesListAdd)
			}
			// The JDK import still yields its dependency edge.
			assertHasConn(t, topo, mode, jExtNames, connUsesDep, "java.util")
		},
	})
}

// J11 (JV-7): a method added to Base binds an unqualified call in a class two
// levels down. setup seeds inheritance/Leaf.java (Leaf extends Derived extends
// Base), which has no edge into Base.java: the incremental scan reaches it
// through Derived, whose file depends on Base.java.
func TestAtScaleJava_J11_GrandparentMethodBindsUnqualifiedCall(t *testing.T) {
	const (
		jLeafGo    = "com.aracne.inheritance.Leaf.go()"
		jBaseFresh = "com.aracne.inheritance.Base.fresh()"
		jLeafRank  = "com.aracne.inheritance.Leaf.ranked()"
		jDerivRank = "com.aracne.inheritance.Derived.rank()"
		jBaseRank  = "com.aracne.inheritance.Base.rank()"
		fLeaf      = javaSrc + "inheritance/Leaf.java"
		fBase      = javaSrc + "inheritance/Base.java"
	)
	runJavaScenario(t, scenario{
		name: "J11_grandparent_method_binds_unqualified_call",
		setup: func(t *testing.T, root string) {
			writeCorpusFile(t, root, fLeaf, "package com.aracne.inheritance;\n\n"+
				"public class Leaf extends Derived {\n"+
				"    public String go() {\n        return fresh();\n    }\n\n"+
				"    public int ranked() {\n        return rank();\n    }\n}\n")
		},
		mutate: func(t *testing.T, root string) {
			replaceInCorpusFile(t, root, fBase, [2]string{
				"    public String label() {",
				"    public String fresh() {\n        return name;\n    }\n\n    public String label() {",
			})
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertHasConn(t, topo, mode, jLeafGo, connCalls, jBaseFresh)
			// The nearest declaration wins: Derived's override, not Base's abstract rank().
			assertHasConn(t, topo, mode, jLeafRank, connCalls, jDerivRank)
			assertNoConn(t, topo, mode, jLeafRank, connCalls, jBaseRank)
		},
	})
}

// J12 (JV-6): Circle also declared under src/main/java11 (the copy the graph
// keeps: last in path order). Deleting that copy must leave src/main/java's
// Circle in place, with its implements and callers, as a cold scan has it.
func TestAtScaleJava_J12_DeleteOneOfTwoSourceSetCopies(t *testing.T) {
	const fCircle11 = "javafamily/src/main/java11/com/aracne/shapes/Circle.java"
	runJavaScenario(t, scenario{
		name: "J12_delete_one_of_two_source_set_copies",
		setup: func(t *testing.T, root string) {
			data, err := os.ReadFile(corpusFile(root, fCircle))
			if err != nil {
				t.Fatal(err)
			}
			writeCorpusFile(t, root, fCircle11, string(data))
		},
		mutate: func(t *testing.T, root string) {
			removeCorpusFile(t, root, fCircle11)
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertResPresent(t, topo, mode, jCircle)
			assertResPresent(t, topo, mode, jCircleInit)
			assertHasConn(t, topo, mode, jShape, connImplBy, jCircle)
			assertHasConn(t, topo, mode, jMakeCircle, connCalls, jCircleInit)
		},
	})
}
