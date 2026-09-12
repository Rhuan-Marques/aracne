package jsscanner

import (
	"path/filepath"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// resByIDSuffix finds the resource whose ID ends with suffix.
func resByIDSuffix(topo *domain.Topology, suffix string) *domain.Resource {
	for id := range topo.Resources {
		if len(id) >= len(suffix) && id[len(id)-len(suffix):] == suffix {
			res := topo.Resources[id]
			return &res
		}
	}
	return nil
}

// `module.exports = class …` / `= function …` / `= () => …` export the
// declaration itself as the module's value. It must be extracted (with its body
// calls) and recorded as the module's default export, so a consumer that
// requires the module and calls or constructs the binding resolves to it.
func TestCommonJSModuleExportsDeclaration(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "widget.cjs"), `module.exports = class Widget {
  render() { return helper(); }
};
function helper() { return 1; }
`)
	writeFile(t, filepath.Join(dir, "build.cjs"), `module.exports = function build(a) { return inner(a); };
function inner(x) { return x; }
`)
	writeFile(t, filepath.Join(dir, "anoncls.cjs"), `module.exports = class {
  go() { return 1; }
};
`)
	writeFile(t, filepath.Join(dir, "anonfn.cjs"), `module.exports = function () { return 1; };
`)
	writeFile(t, filepath.Join(dir, "arrow.cjs"), `module.exports = (x) => x * 2;
`)
	writeFile(t, filepath.Join(dir, "expdef.cjs"), `exports.default = class Thing {
  run() { return 1; }
};
`)
	writeFile(t, filepath.Join(dir, "consumer.cjs"), `const W = require('./widget.cjs');
const make = require('./build.cjs');
const Anon = require('./anoncls.cjs');
const af = require('./anonfn.cjs');
const dbl = require('./arrow.cjs');
function go() {
  const w = new W();
  w.render();
  make(1);
  new Anon().go();
  af();
  return dbl(2);
}
module.exports = { go };
`)

	topo := scanProject(t, dir)

	// Extraction: named forms keep their own name, anonymous ones are "default"
	// (as an anonymous `export default class {}` / `function(){}` is).
	for _, c := range []struct {
		suffix string
		kind   domain.ResourceKind
	}{
		{"widget.Widget", domain.ResourceStruct},
		{"widget.Widget.render", domain.ResourceMethod},
		{"build.build", domain.ResourceFunction},
		{"anoncls.default", domain.ResourceStruct},
		{"anoncls.default.go", domain.ResourceMethod},
		{"anonfn.default", domain.ResourceFunction},
		{"arrow.default", domain.ResourceFunction},
		{"expdef.Thing", domain.ResourceStruct},
	} {
		r := resByIDSuffix(topo, c.suffix)
		if r == nil {
			t.Errorf("expected resource %s", c.suffix)
			continue
		}
		if r.Kind != c.kind {
			t.Errorf("%s: kind %s, want %s", c.suffix, r.Kind, c.kind)
		}
	}
	if r := resByIDSuffix(topo, "arrow.default"); r != nil && r.Properties["kind"] != "arrow" {
		t.Errorf("arrow.default kind property = %v, want arrow", r.Properties["kind"])
	}

	// Body calls inside the exported declarations are kept.
	if r := resByIDSuffix(topo, "widget.Widget.render"); !connHasSuffix(r, "calls", "widget.helper") {
		t.Errorf("Widget.render should call helper, got %v", r)
	}
	if r := resByIDSuffix(topo, "build.build"); !connHasSuffix(r, "calls", "build.inner") {
		t.Errorf("build should call inner, got %v", r)
	}

	// Recorded as the module's default export.
	for file, want := range map[string]string{
		"widget.cjs": "Widget", "build.cjs": "build", "anoncls.cjs": "default",
		"anonfn.cjs": "default", "arrow.cjs": "default", "expdef.cjs": "Thing",
	} {
		mod := findByName(topo, domain.ResourceFile, file)
		if mod == nil {
			t.Errorf("expected module %s", file)
			continue
		}
		if got := mod.Properties["default_export"]; got != want {
			t.Errorf("%s default_export = %v, want %q", file, got, want)
		}
	}

	checkConsumer := func(t *testing.T, topo *domain.Topology) {
		t.Helper()
		goFn := resByIDSuffix(topo, "consumer.go")
		if goFn == nil {
			t.Fatal("expected consumer.go")
		}
		for _, want := range []struct{ conn, suffix string }{
			{"uses_class", "widget.Widget"},
			{"calls", "widget.Widget.render"},
			{"calls", "build.build"},
			{"uses_class", "anoncls.default"},
			{"calls", "anoncls.default.go"},
			{"calls", "anonfn.default"},
			{"calls", "arrow.default"},
		} {
			if !connHasSuffix(goFn, want.conn, want.suffix) {
				t.Errorf("consumer.go should have %s -> %s, got %v", want.conn, want.suffix, goFn.Connections[want.conn])
			}
		}
	}
	checkConsumer(t, topo)

	// An incremental re-parse of the consumer resolves identically.
	if _, err := NewJavaScriptScanner().UpdateFile(topo, filepath.Join(dir, "consumer.cjs")); err != nil {
		t.Fatalf("UpdateFile: %v", err)
	}
	checkConsumer(t, topo)
}

// A whole-module require binding still resolves a same-named export when the
// module has no default: `module.exports = { helper }` + `helper()`.
func TestCommonJSRequireBindingFallsBackToName(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "util.js"), `function helper() { return 7; }
module.exports = { helper };
`)
	writeFile(t, filepath.Join(dir, "app.js"), `const helper = require('./util');
function run() { return helper(); }
module.exports = run;
`)

	topo := scanProject(t, dir)

	run := findByName(topo, domain.ResourceFunction, "run")
	if !connHasSuffix(run, "calls", "util.helper") {
		t.Errorf("run should call util.helper, calls=%v", run.Connections["calls"])
	}
}
