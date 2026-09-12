package jsscanner

import (
	"path/filepath"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// Members reached through an imported namespace.
//
// `import {Geo} from './ns'; Geo.dist()` binds a namespace object by name, so the import
// carries ImportedName "Geo" and NOT the Namespace marker `import * as` sets -- and the
// namespace branch of resolveMethodCall keyed on exactly that marker. The scope walk that
// follows only reaches namespaces declared in the caller's own file, so nothing resolved
// `Geo.dist` and the callee ended up with no incoming reference at all: no calls edge, no
// caller in a read, and no signature_changed warning when its signature moved.

func TestTSCallThroughAnImportedNamespace(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "ns.ts"), `
export namespace Geo {
  export function dist(a: number, b: number): number { return a + b; }
}
`)
	writeFile(t, filepath.Join(dir, "app.ts"), `
import { Geo } from './ns';
export function useIt(): number { return Geo.dist(1, 2); }
`)
	topo := scanTS(t, dir)

	caller := findByName(topo, domain.ResourceFunction, "useIt")
	if caller == nil {
		t.Fatal("expected function useIt")
	}
	if !connHasSuffix(caller, "calls", "ns.Geo.dist") {
		t.Errorf("expected useIt to call ns.Geo.dist, calls=%v", caller.Connections["calls"])
	}
}

// TestTSCallThroughANamespaceReExport: `export * as ns from './lib'` binds one namespace
// object rather than flattening the module, so it is deliberately not a whole-module
// re-export -- and it was recorded nowhere at all, leaving an importer of `ns` with nothing
// to resolve against.
func TestTSCallThroughANamespaceReExport(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "lib.ts"), "export function f(x: number): number { return x; }\n")
	writeFile(t, filepath.Join(dir, "barrel.ts"), "export * as ns from './lib';\n")
	writeFile(t, filepath.Join(dir, "app.ts"), `
import { ns } from './barrel';
export function useIt(): number { return ns.f(1); }
`)
	topo := scanTS(t, dir)

	caller := findByName(topo, domain.ResourceFunction, "useIt")
	if caller == nil {
		t.Fatal("expected function useIt")
	}
	if !connHasSuffix(caller, "calls", "lib.f") {
		t.Errorf("expected useIt to call lib.f, calls=%v", caller.Connections["calls"])
	}
}

// TestTSImportedStaticMethodStillUsesItsClass is the counter-case: an imported CLASS whose
// static method is called must keep recording the class as used, not just the call. It is
// the same `Name.member()` shape, resolved a different way.
func TestTSImportedStaticMethodStillUsesItsClass(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "shape.ts"), `
export class Circle {
  static unit(): number { return 1; }
}
`)
	writeFile(t, filepath.Join(dir, "app.ts"), `
import { Circle } from './shape';
export function useIt(): number { return Circle.unit(); }
`)
	topo := scanTS(t, dir)

	caller := findByName(topo, domain.ResourceFunction, "useIt")
	if caller == nil {
		t.Fatal("expected function useIt")
	}
	if !connHasSuffix(caller, "calls", "shape.Circle.unit") {
		t.Errorf("expected useIt to call Circle.unit, calls=%v", caller.Connections["calls"])
	}
	if !connHasSuffix(caller, "uses_class", "shape.Circle") {
		t.Errorf("expected useIt to use Circle, uses_class=%v", caller.Connections["uses_class"])
	}
}
