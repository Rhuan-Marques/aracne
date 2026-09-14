package jsscanner

import (
	"path/filepath"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	js "github.com/Rhuan-Marques/aracne/internal/topology/javascript"
)

// TypeScript under NodeNext/Node16 module resolution must spell a relative
// import with the JavaScript extension of the emitted file: `./shape.js` names
// the source `shape.ts`. The resolver has to map it back to the TS module.
func TestTSImportWithJSExtensionResolvesToTSSource(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "models", "shape.ts"), `export class Circle { area(): number { return 1; } }
`)
	writeFile(t, filepath.Join(dir, "main.ts"), `import { Circle } from './models/shape.js';
export function run(): number { return new Circle().area(); }
`)
	writeFile(t, filepath.Join(dir, "mod.mts"), `export function fromMts(): number { return 1; }
`)
	writeFile(t, filepath.Join(dir, "a.mts"), `import { fromMts } from './mod.mjs';
export function useMts(): number { return fromMts(); }
`)
	writeFile(t, filepath.Join(dir, "c.cts"), `export function fromCts(): number { return 1; }
`)
	writeFile(t, filepath.Join(dir, "b.cts"), `import { fromCts } from './c.cjs';
export function useCts(): number { return fromCts(); }
`)
	writeFile(t, filepath.Join(dir, "lib", "index.ts"), `export function fromIndex(): number { return 1; }
`)
	writeFile(t, filepath.Join(dir, "useindex.ts"), `import { fromIndex } from './lib/index.js';
export function useIndex(): number { return fromIndex(); }
`)
	writeFile(t, filepath.Join(dir, "button.tsx"), `export function Button(): number { return 1; }
`)
	writeFile(t, filepath.Join(dir, "app.tsx"), `import { Button } from './button.js';
export function App(): number { return Button(); }
`)

	topo := scanTS(t, dir)

	mainMod := findByName(topo, domain.ResourceFile, "main.ts")
	if !connHasSuffix(mainMod, string(js.ConnImportsModule), "models/shape.ts") {
		t.Errorf("main.ts should import models/shape.ts, imports_module=%v", mainMod.Connections[string(js.ConnImportsModule)])
	}
	run := findByName(topo, domain.ResourceFunction, "run")
	if !connHasSuffix(run, "uses_class", "models/shape.Circle") {
		t.Errorf("run should use class Circle, uses_class=%v", run.Connections["uses_class"])
	}
	if !connHasSuffix(run, "calls", "models/shape.Circle.area") {
		t.Errorf("run should call Circle.area, calls=%v", run.Connections["calls"])
	}

	for _, tc := range []struct{ fn, callee string }{
		{"useMts", "mod.fromMts"},           // './mod.mjs' -> mod.mts
		{"useCts", "c.fromCts"},             // './c.cjs'   -> c.cts
		{"useIndex", "lib/index.fromIndex"}, // './lib/index.js' -> lib/index.ts
		{"App", "button.Button"},            // './button.js' -> button.tsx
	} {
		f := findByName(topo, domain.ResourceFunction, tc.fn)
		if !connHasSuffix(f, "calls", tc.callee) {
			var got []string
			if f != nil {
				got = f.Connections["calls"]
			}
			t.Errorf("%s should call %s, calls=%v", tc.fn, tc.callee, got)
		}
	}
}

// A `.js` specifier naming a file that really exists resolves to that file; the
// TypeScript fallback only applies when it does not. JS and TS are independent
// topologies, so a JavaScript project never reaches a `.ts` file through it.
func TestResolveSpecifierPrefersExistingJSFile(t *testing.T) {
	// An absolute root valid on every platform: resolveSpecifier joins the importer's
	// directory with the specifier through path/filepath, so a module keyed "/p/x.js" -- rooted
	// but with no VOLUME -- is unreachable on Windows, where that join yields a drive-qualified
	// path and nothing in this map matched.
	root, err := filepath.Abs(filepath.FromSlash("/p"))
	if err != nil {
		t.Fatal(err)
	}
	mod := func(name string) string { return filepath.Join(root, name) }
	gt := newTopology(root)
	for _, name := range []string{"x.js", "x.ts", "y.ts"} {
		gt.Modules[mod(name)] = js.JavaScriptModule{ID: mod(name)}
	}

	cases := []struct {
		spec, want string
		ok         bool
	}{
		{"./x.js", mod("x.js"), true}, // the real .js file wins
		{"./x", mod("x.ts"), true},    // extensionless: unchanged TS-first order
		{"./y.js", mod("y.ts"), true}, // no y.js: its TS source
		{"./y.mjs", "", false},        // .mjs maps to .mts only
		{"./z.js", "", false},         // nothing to resolve to
	}
	for _, c := range cases {
		got, ok := resolveSpecifier(mod("main.ts"), c.spec, gt)
		if got != c.want || ok != c.ok {
			t.Errorf("resolveSpecifier(%q) = (%q, %v), want (%q, %v)", c.spec, got, ok, c.want, c.ok)
		}
	}
}

func TestJSImportWithJSExtensionUnchanged(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "lib.js"), `export function fromJS() { return 1; }
`)
	writeFile(t, filepath.Join(dir, "only.ts"), `export function fromTS(): number { return 1; }
`)
	writeFile(t, filepath.Join(dir, "main.js"), `import { fromJS } from './lib.js';
import { fromTS } from './only.js';
export function go() { fromJS(); fromTS(); }
`)

	topo := scanProject(t, dir)

	goFn := findByName(topo, domain.ResourceFunction, "go")
	if !connHasSuffix(goFn, "calls", "lib.fromJS") {
		t.Errorf("go should call lib.fromJS, calls=%v", goFn.Connections["calls"])
	}
	// only.ts belongs to the TypeScript topology, not this one.
	if connHasSuffix(goFn, "calls", "only.fromTS") {
		t.Errorf("a JS import must not resolve into the TS topology, calls=%v", goFn.Connections["calls"])
	}
}
