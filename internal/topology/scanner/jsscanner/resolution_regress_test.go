package jsscanner

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// conns returns res's targets of one edge kind, sorted, or nil when res is missing.
func conns(topo *domain.Topology, id, kind string) []string {
	res, ok := topo.Resources[id]
	if !ok {
		return nil
	}
	out := append([]string(nil), res.Connections[kind]...)
	sort.Strings(out)
	return out
}

func hasConn(topo *domain.Topology, id, kind, target string) bool {
	for _, t := range conns(topo, id, kind) {
		if t == target {
			return true
		}
	}
	return false
}

// graphFingerprint renders every resource (identity, span, properties) and every edge, so two
// topologies of the same tree can be compared for equality. Descriptions are left out.
func graphFingerprint(t *testing.T, topo *domain.Topology) string {
	t.Helper()
	var lines []string
	for id, res := range topo.Resources {
		props, _ := json.Marshal(res.Properties)
		var norm any
		_ = json.Unmarshal(props, &norm)
		props, _ = json.Marshal(norm)
		lines = append(lines, strings.Join([]string{"R", id, string(res.Kind), res.Name, res.Location.Path,
			itoa(res.Location.StartsAt) + "-" + itoa(res.Location.EndsAt), string(props)}, "|"))
		for kind, targets := range res.Connections {
			for _, target := range targets {
				lines = append(lines, "C|"+id+"|"+kind+"|"+target)
			}
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// requireSameGraph fails when an incrementally updated topology differs from a cold scan.
func requireSameGraph(t *testing.T, incremental, cold *domain.Topology) {
	t.Helper()
	a, b := graphFingerprint(t, incremental), graphFingerprint(t, cold)
	if a == b {
		return
	}
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	inA, inB := map[string]bool{}, map[string]bool{}
	for _, l := range al {
		inA[l] = true
	}
	for _, l := range bl {
		inB[l] = true
	}
	for _, l := range al {
		if !inB[l] {
			t.Errorf("only after the incremental update: %s", l)
		}
	}
	for _, l := range bl {
		if !inA[l] {
			t.Errorf("only in the cold scan: %s", l)
		}
	}
}

// JS-3: a base class resolves through the declaring file's imports -- including a renamed
// import -- and never by whichever same-named class Go's map iteration yields first.
func TestHeritageResolvesThroughImportsDeterministically(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "shapes.js"), "export class Base { area() { return 0; } }\n")
	writeFile(t, filepath.Join(dir, "other.js"), "export class Base { area() { return 1; } }\n")
	writeFile(t, filepath.Join(dir, "sub.js"), `import { Base } from './shapes.js';
import { Base as B } from './other.js';
import * as ns from './other.js';
export class Sub extends Base {}
export class Sub2 extends B {}
export class Sub3 extends ns.Base {}
`)
	for i := 0; i < 12; i++ { // the old fallback picked a different Base about half the time
		topo := scanProject(t, dir)
		if got := conns(topo, "sub.Sub", "inherits"); len(got) != 1 || got[0] != "shapes.Base" {
			t.Fatalf("scan %d: sub.Sub inherits %v, want [shapes.Base]", i, got)
		}
		if got := conns(topo, "sub.Sub2", "inherits"); len(got) != 1 || got[0] != "other.Base" {
			t.Fatalf("scan %d: sub.Sub2 (extends the renamed import B) inherits %v, want [other.Base]", i, got)
		}
		if got := conns(topo, "sub.Sub3", "inherits"); len(got) != 1 || got[0] != "other.Base" {
			t.Fatalf("scan %d: sub.Sub3 (extends ns.Base) inherits %v, want [other.Base]", i, got)
		}
	}
}

// JS-3: a package import is never pinned on an unrelated project class sharing its name, and
// a name the file neither declares nor imports resolves only when exactly one class has it.
func TestHeritageFallbackIsUniqueOnly(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "component.js"), "export class Component {}\n")
	writeFile(t, filepath.Join(dir, "widget.js"), `import { Component } from 'react';
import React from 'react';
export class Widget extends Component {}
export class Widget2 extends React.Component {}
`)
	// The React wrapper pattern: the same-named local class is not its own base.
	writeFile(t, filepath.Join(dir, "wrap.js"), `import React from 'react';
export class Component extends React.Component {}
`)
	// Script-style globals: no import, one declaration -> the edge stays (intended behaviour).
	writeFile(t, filepath.Join(dir, "g1.js"), "class Animal { speak() {} }\n")
	writeFile(t, filepath.Join(dir, "g2.js"), "class Dog extends Animal {}\n")
	// Two declarations of the name and no import to choose between them -> no edge.
	writeFile(t, filepath.Join(dir, "g3.js"), "class Thing {}\n")
	writeFile(t, filepath.Join(dir, "g4.js"), "class Thing {}\n")
	writeFile(t, filepath.Join(dir, "g5.js"), "class Gadget extends Thing {}\n")

	topo := scanProject(t, dir)
	for _, id := range []string{"widget.Widget", "widget.Widget2", "wrap.Component"} {
		if got := conns(topo, id, "inherits"); len(got) != 0 {
			t.Errorf("%s extends react's Component, not the project's: inherits %v", id, got)
		}
	}
	if !hasConn(topo, "g2.Dog", "inherits", "g1.Animal") {
		t.Errorf("an unimported, uniquely named base must still resolve: Dog inherits %v", conns(topo, "g2.Dog", "inherits"))
	}
	if got := conns(topo, "g5.Gadget", "inherits"); len(got) != 0 {
		t.Errorf("an ambiguous unimported base must not be guessed: Gadget inherits %v", got)
	}
}

// JS-3: `implements` and interface `extends` resolve through imports too, and an incremental
// re-parse of the declaring file reproduces the cold scan.
func TestTSImplementsResolvesThroughImports(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.ts"), "export interface Shape { area(): number; }\n")
	writeFile(t, filepath.Join(dir, "b.ts"), "export interface Shape { perimeter(): number; }\n")
	writeFile(t, filepath.Join(dir, "c.ts"), `import { Shape as S } from './a';
import { Shape } from './b';
export class C implements S { area() { return 1; } }
export interface Solid extends Shape { volume(): number; }
`)
	topo := scanTS(t, dir)
	if got := conns(topo, "c.C", "implements"); len(got) != 1 || got[0] != "a.Shape" {
		t.Errorf("C implements %v, want [a.Shape]", got)
	}
	if got := conns(topo, "c.Solid", "inherits"); len(got) != 1 || got[0] != "b.Shape" {
		t.Errorf("Solid inherits %v, want [b.Shape]", got)
	}
	if _, err := NewTypeScriptScanner().UpdateFile(topo, filepath.Join(dir, "a.ts")); err != nil {
		t.Fatal(err)
	}
	requireSameGraph(t, topo, scanTS(t, dir))
}

// JS-4: a static method called on an imported class resolves, and a type annotation imported
// through a barrel (`export *`, `export {X} from`) resolves to its declaration.
func TestTSStaticCallAndBarrelTypes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "shapes/circle.ts"), `export class Circle {
  static unit(): Circle { return new Circle(); }
  area(): number { return 1; }
}
`)
	writeFile(t, filepath.Join(dir, "shapes/shape.ts"), "export interface Shape { area(): number; }\n")
	writeFile(t, filepath.Join(dir, "shapes/index.ts"), "export * from './circle';\nexport { Shape } from './shape';\n")
	writeFile(t, filepath.Join(dir, "app.ts"), `import { Circle } from './shapes/circle';
import { Circle as C2, Shape } from './shapes';
export function make() { return Circle.unit(); }
export function f(c: C2, s: Shape) { return c.area(); }
`)
	topo := scanTS(t, dir)
	if !hasConn(topo, "app.make", "calls", "shapes/circle.Circle.unit") {
		t.Errorf("Circle.unit() on the imported class: make calls %v", conns(topo, "app.make", "calls"))
	}
	if !hasConn(topo, "app.f", "uses_class", "shapes/circle.Circle") {
		t.Errorf("c: C2 through the barrel: f uses_class %v", conns(topo, "app.f", "uses_class"))
	}
	if !hasConn(topo, "app.f", "uses_interface", "shapes/shape.Shape") {
		t.Errorf("s: Shape through the barrel: f uses_interface %v", conns(topo, "app.f", "uses_interface"))
	}
	if !hasConn(topo, "app.f", "calls", "shapes/circle.Circle.area") {
		t.Errorf("c.area() on a barrel-typed parameter: f calls %v", conns(topo, "app.f", "calls"))
	}
}

// JS-8: inside a namespace a bare name means the namespace's own member before the file's,
// and a non-exported top-level namespace is extracted at all.
func TestTSNamespaceScopedResolution(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "ns.ts"), `function helper() { return 0; }
class Inner { go() {} }
namespace A {
  export function helper() { return 1; }
  export class Inner { go() {} }
  export function g() {
    helper();
    const i = new Inner();
    i.go();
  }
  export function t(x: Inner) { x.go(); }
}
export function top() { helper(); return new Inner(); }
`)
	topo := scanTS(t, dir)
	for _, want := range []struct{ id, kind, target string }{
		{"ns.A.g", "calls", "ns.A.helper"},
		{"ns.A.g", "uses_class", "ns.A.Inner"},
		{"ns.A.g", "calls", "ns.A.Inner.go"},
		{"ns.A.t", "uses_class", "ns.A.Inner"},
		{"ns.A.t", "calls", "ns.A.Inner.go"},
		// The file scope is unchanged for code outside the namespace.
		{"ns.top", "calls", "ns.helper"},
		{"ns.top", "uses_class", "ns.Inner"},
	} {
		if !hasConn(topo, want.id, want.kind, want.target) {
			t.Errorf("%s should %s %s, got %v", want.id, want.kind, want.target, conns(topo, want.id, want.kind))
		}
	}
	for _, wrong := range []string{"ns.helper", "ns.Inner.go"} {
		if hasConn(topo, "ns.A.g", "calls", wrong) {
			t.Errorf("ns.A.g must not call the shadowed file-level %s", wrong)
		}
	}
}

// JS-8 (fence): an object literal is not a scope a bare name can see into. `helper()` inside
// `obj.m` is the module's helper, not the sibling member `obj.helper`.
func TestObjectLiteralMemberIsNotAScope(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "o.js"), `function helper() { return 0; }
export const obj = {
  m() { return helper(); },
  helper() { return 1; },
};
`)
	topo := scanProject(t, dir)
	if got := conns(topo, "o.obj.m", "calls"); len(got) != 1 || got[0] != "o.helper" {
		t.Errorf("obj.m calls %v, want [o.helper]", got)
	}
}

// JS-10: resolution through a local export rename, a CommonJS object alias, a default-imported
// package, and a renamed require binding.
func TestJSExportAliasesAndPackageMembers(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "lib.js"), "function impl() { return 1; }\nexport { impl as renamed };\n")
	writeFile(t, filepath.Join(dir, "cjs.cjs"), "function inner() {}\nmodule.exports = { outer: inner };\n")
	writeFile(t, filepath.Join(dir, "foo.cjs"), "class Foo { bar() { return 1; } static make() {} }\nmodule.exports = Foo;\n")
	writeFile(t, filepath.Join(dir, "use.js"), `import { renamed } from './lib.js';
import axios from 'axios';
import { Client } from 'pkg';
const { outer } = require('./cjs.cjs');
const Bar = require('./foo.cjs');
export function go() {
  renamed();
  outer();
  axios.get('/x');
  new Client();
  new Bar().bar();
  Bar.make();
}
`)
	topo := scanProject(t, dir)
	for _, want := range []struct{ kind, target string }{
		{"calls", "lib.impl"},
		{"calls", "cjs.inner"},
		{"uses_dependency", "axios"},
		{"uses_dependency", "pkg"},
		{"uses_class", "foo.Foo"},
		{"calls", "foo.Foo.bar"},
		{"calls", "foo.Foo.make"}, // a static on the class a require binding holds
	} {
		if !hasConn(topo, "use.go", want.kind, want.target) {
			t.Errorf("use.go should %s %s, got %v", want.kind, want.target, conns(topo, "use.go", want.kind))
		}
	}
}

// JS-10: a tsconfig `paths` alias is the project file it maps to, not a package; a catch-all
// pattern and a redirect into node_modules still name packages.
func TestTSConfigPathAliases(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "tsconfig.json"), `{
  // comments and trailing commas are legal in tsconfig
  "compilerOptions": {
    "baseUrl": ".",
    "paths": { "@app/*": ["src/*"], "lodash": ["node_modules/lodash-es"], },
  },
}
`)
	writeFile(t, filepath.Join(dir, "node_modules/lodash-es/index.js"), "export const x = 1;\n")
	writeFile(t, filepath.Join(dir, "src/models/shape.ts"), "export class Shape { area() { return 0; } }\n")
	writeFile(t, filepath.Join(dir, "src/app.ts"), `import { Shape } from '@app/models/shape';
import _ from 'lodash';
export function mk() { _.map(); return new Shape().area(); }
`)
	topo := scanTS(t, dir)
	if _, fake := topo.Resources["@app/models/shape"]; fake {
		t.Error("the alias became a dependency node")
	}
	if !hasConn(topo, "src/app.mk", "uses_class", "src/models/shape.Shape") {
		t.Errorf("new Shape() through the alias: mk uses_class %v", conns(topo, "src/app.mk", "uses_class"))
	}
	if !hasConn(topo, filepath.Join(dir, "src/app.ts"), "imports_module", filepath.Join(dir, "src/models/shape.ts")) {
		t.Errorf("app.ts should import the aliased module, got %v", conns(topo, filepath.Join(dir, "src/app.ts"), "imports_module"))
	}
	if !hasConn(topo, "src/app.mk", "uses_dependency", "lodash") {
		t.Errorf("a paths redirect into node_modules is still the package: mk uses_dependency %v", conns(topo, "src/app.mk", "uses_dependency"))
	}
}
