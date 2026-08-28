package tests_test

// At-scale TypeScript scenarios (TS + TSX): predictable edits to the
// testing_ground/tsfamily corpus, asserted under incremental / full / hard with
// strict cross-mode equality (see atscale_harness_test.go).
//
// TS resource IDs are path-based: "testing_ground/tsfamily/<file>.<Name>".
// TS-only edges: implements/implemented_by, uses_interface, uses_named_type.
//
// Core scenarios setup-drop the react components (dropReactComponents); the TSX
// scenario keeps tsfamily/components.tsx and drops only the JS one.
//
// KNOWN-RED (deterministic): structural edges (implements/implemented_by/inherits)
// are global, so TS2/TS3/TS5 stay consistent; self-contained edits
// (TS1/TS6/TS7/TS8/TS9) stay consistent; the cross-file interface rename TS4
// diverges (same root cause as bug _2).

import (
	"testing"

	"aracne/internal/topology/domain"
)

const (
	tsAreaOf         = "testing_ground/tsfamily/factory.areaOf"
	tsRender         = "testing_ground/tsfamily/factory.render"
	tsFactMakeCirc   = "testing_ground/tsfamily/factory.makeCircle"
	tsFactMakeRect   = "testing_ground/tsfamily/factory.makeRectangle"
	tsShapesCircArea = "testing_ground/tsfamily/shapes.Circle.area"
	tsShapesRectArea = "testing_ground/tsfamily/shapes.Rectangle.area"
	tsShapesBase     = "testing_ground/tsfamily/shapes.Base"
	tsShapesRect     = "testing_ground/tsfamily/shapes.Rectangle"

	tsModelsShape  = "testing_ground/tsfamily/models.Shape"
	tsModelsForm   = "testing_ground/tsfamily/models.Form"
	tsModelsSolid  = "testing_ground/tsfamily/models.Solid"
	tsModelsPoly   = "testing_ground/tsfamily/models.Polyhedron"
	tsModelsColor  = "testing_ground/tsfamily/models.Color"
	tsModelsTagged = "testing_ground/tsfamily/models.Tagged"

	tsExtraPentagon = "testing_ground/tsfamily/extra.Pentagon"
	tsConsumerUnit  = "testing_ground/tsfamily/consumer.Geometry.unit"
	tsCompRender    = "testing_ground/tsfamily/components.CircleView.render"
)

const pentagonTs = `import { Shape } from "./models";

export class Pentagon implements Shape {
  area(): number {
    return 0;
  }
  describe(): string {
    return "pentagon";
  }
}
`

// TS1: local annotation-driven resolution swap (self-contained).
func TestAtScaleTs_TS1_AnnotationResolution(t *testing.T) {
	runScenario(t, scenario{
		name:  "TS1_annotation_resolution",
		setup: dropReactComponents,
		mutate: func(t *testing.T, root string) {
			replaceInCorpusFile(t, root, "tsfamily/factory.ts",
				[2]string{"const c: Circle = makeCircle(radius);", "const c: Rectangle = makeRectangle(radius);"})
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertHasConn(t, topo, mode, tsAreaOf, connCalls, tsShapesRectArea)
			assertNoConn(t, topo, mode, tsAreaOf, connCalls, tsShapesCircArea)
			assertHasConn(t, topo, mode, tsAreaOf, connCalls, tsFactMakeRect)
			assertNoConn(t, topo, mode, tsAreaOf, connCalls, tsFactMakeCirc)
		},
	})
}

// TS2: add a new class implementing the Shape interface (implemented_by growth).
func TestAtScaleTs_TS2_AddInterfaceImplementer(t *testing.T) {
	runScenario(t, scenario{
		name:  "TS2_add_implementer",
		setup: dropReactComponents,
		mutate: func(t *testing.T, root string) {
			writeCorpusFile(t, root, "tsfamily/extra.ts", pentagonTs)
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertResPresent(t, topo, mode, tsExtraPentagon)
			assertHasConn(t, topo, mode, tsModelsShape, connImplBy, tsExtraPentagon)
			assertHasConn(t, topo, mode, tsExtraPentagon, connImplements, tsModelsShape)
		},
	})
}

// TS3: remove the implementer added in setup (implemented_by shrink).
func TestAtScaleTs_TS3_RemoveInterfaceImplementer(t *testing.T) {
	runScenario(t, scenario{
		name: "TS3_remove_implementer",
		setup: func(t *testing.T, root string) {
			dropReactComponents(t, root)
			writeCorpusFile(t, root, "tsfamily/extra.ts", pentagonTs)
		},
		mutate: func(t *testing.T, root string) {
			removeCorpusFile(t, root, "tsfamily/extra.ts")
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertResAbsent(t, topo, mode, tsExtraPentagon)
			assertNoReferences(t, topo, mode, tsExtraPentagon)
			assertNoConn(t, topo, mode, tsModelsShape, connImplBy, tsExtraPentagon)
		},
	})
}

// TS4: rename interface Shape -> Form across files. Structural implemented_by
// rewires consistently; cross-file body resolution diverges.
func TestAtScaleTs_TS4_InterfaceRenameAcrossFiles(t *testing.T) {
	runScenario(t, scenario{
		name:  "TS4_interface_rename",
		setup: dropReactComponents,
		mutate: func(t *testing.T, root string) {
			replaceInCorpusFile(t, root, "tsfamily/models.ts", [2]string{"Shape", "Form"})
			replaceInCorpusFile(t, root, "tsfamily/shapes.ts", [2]string{"Shape", "Form"})
			replaceInCorpusFile(t, root, "tsfamily/factory.ts", [2]string{"Shape", "Form"})
			replaceInCorpusFile(t, root, "tsfamily/consumer.ts", [2]string{"Shape", "Form"})
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertResAbsent(t, topo, mode, tsModelsShape)
			assertResPresent(t, topo, mode, tsModelsForm)
			assertNoReferences(t, topo, mode, tsModelsShape)
			assertHasConn(t, topo, mode, tsModelsForm, connImplBy, tsShapesBase)
			assertHasConn(t, topo, mode, tsModelsForm, connImplBy, tsShapesRect)
		},
	})
}

// TS5: add an interface that extends another interface (inherits/inherited_by).
func TestAtScaleTs_TS5_InterfaceExtends(t *testing.T) {
	runScenario(t, scenario{
		name:  "TS5_interface_extends",
		setup: dropReactComponents,
		mutate: func(t *testing.T, root string) {
			replaceInCorpusFile(t, root, "tsfamily/models.ts",
				[2]string{
					"export interface Solid extends Shape {\n  volume(): number;\n}",
					"export interface Solid extends Shape {\n  volume(): number;\n}\n\nexport interface Polyhedron extends Solid {\n  faces(): number;\n}",
				})
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertResPresent(t, topo, mode, tsModelsPoly)
			assertHasConn(t, topo, mode, tsModelsPoly, connInherits, tsModelsSolid)
			assertHasConn(t, topo, mode, tsModelsSolid, connInheritedBy, tsModelsPoly)
		},
	})
}

// TS6: add an enum member (the named-type resource updates; cross-mode equality
// is the real check).
func TestAtScaleTs_TS6_EnumMemberAdd(t *testing.T) {
	runScenario(t, scenario{
		name:  "TS6_enum_member_add",
		setup: dropReactComponents,
		mutate: func(t *testing.T, root string) {
			replaceInCorpusFile(t, root, "tsfamily/models.ts",
				[2]string{"  Blue,\n}", "  Blue,\n  Yellow,\n}"})
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertResPresent(t, topo, mode, tsModelsColor)
		},
	})
}

// TS7: change a type-alias definition (intersection -> union).
func TestAtScaleTs_TS7_TypeAliasChange(t *testing.T) {
	runScenario(t, scenario{
		name:  "TS7_type_alias_change",
		setup: dropReactComponents,
		mutate: func(t *testing.T, root string) {
			replaceInCorpusFile(t, root, "tsfamily/models.ts",
				[2]string{"export type Tagged = Shape & Labeled;", "export type Tagged = Shape | Labeled;"})
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertResPresent(t, topo, mode, tsModelsTagged)
		},
	})
}

// TS8: namespace-member body resolution (Geometry.unit calls makeCircle -> makeRectangle).
func TestAtScaleTs_TS8_NamespaceMemberResolution(t *testing.T) {
	runScenario(t, scenario{
		name:  "TS8_namespace_member",
		setup: dropReactComponents,
		mutate: func(t *testing.T, root string) {
			replaceInCorpusFile(t, root, "tsfamily/consumer.ts",
				[2]string{`import { makeCircle, areaOf } from "./factory";`, `import { makeCircle, makeRectangle, areaOf } from "./factory";`},
				[2]string{"return makeCircle(1);", "return makeRectangle(1, 1);"})
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertHasConn(t, topo, mode, tsConsumerUnit, connCalls, tsFactMakeRect)
			assertNoConn(t, topo, mode, tsConsumerUnit, connCalls, tsFactMakeCirc)
		},
	})
}

// TS9 (TSX): local annotation-driven resolution in a TSX class component. Keeps
// components.tsx (edited here) and drops only the JS component.
func TestAtScaleTs_TS9_TsxAnnotationResolution(t *testing.T) {
	runScenario(t, scenario{
		name: "TS9_tsx_annotation_resolution",
		setup: func(t *testing.T, root string) {
			dropCorpusFiles(t, root, "jsfamily/components.jsx")
		},
		mutate: func(t *testing.T, root string) {
			replaceInCorpusFile(t, root, "tsfamily/components.tsx",
				[2]string{`import { Circle } from "./shapes";`, `import { Circle, Rectangle } from "./shapes";`},
				[2]string{"const c: Circle = this.props.shape;", "const c: Rectangle = this.props.shape;"})
		},
		assert: func(t *testing.T, topo *domain.Topology, mode string) {
			assertHasConn(t, topo, mode, tsCompRender, connCalls, tsShapesRectArea)
			assertNoConn(t, topo, mode, tsCompRender, connCalls, tsShapesCircArea)
		},
	})
}
