package jsscanner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

func span(topo *domain.Topology, id string) (int, int) {
	res := topo.Resources[id]
	return res.Location.StartsAt, res.Location.EndsAt
}

// inputNames reads a function resource's parameter names back through JSON, the shape the
// database holds.
func inputNames(t *testing.T, topo *domain.Topology, id string) []string {
	t.Helper()
	res, ok := topo.Resources[id]
	if !ok {
		t.Fatalf("missing %s", id)
	}
	blob, _ := json.Marshal(res.Properties["input"])
	var params []struct{ Name string }
	_ = json.Unmarshal(blob, &params)
	var out []string
	for _, p := range params {
		out = append(out, p.Name)
	}
	return out
}

func conflictMessages(topo *domain.Topology) []string {
	var out []string
	for _, w := range helper.InterfaceConflictWarnings(topo) {
		out = append(out, w.Message)
	}
	return out
}

// JS-5: a class-field arrow is a method -- it is extracted, called, and satisfies an interface
// -- and an optional interface method is not a requirement.
func TestClassFieldArrowsAndOptionalInterfaceMethods(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.ts"), `export interface H { handle(e: number): void; }
export interface I { run(): void; maybe?(): void; }
export class FieldImpl implements H {
  handle = (e: number) => { helper(); };
  static make = () => new FieldImpl();
  count = 0;
}
export class OptImpl implements I {
  run() {}
}
function helper() {}
`)
	topo := scanTS(t, dir)
	if msgs := conflictMessages(topo); len(msgs) != 0 {
		t.Errorf("correct implementers reported as broken: %v", msgs)
	}
	handle, ok := topo.Resources["a.FieldImpl.handle"]
	if !ok {
		t.Fatal("the class-field arrow a.FieldImpl.handle is not a resource")
	}
	if handle.Kind != domain.ResourceMethod || handle.Properties["method_from"] != "a.FieldImpl" {
		t.Errorf("handle: kind %s, method_from %v; want a method of FieldImpl", handle.Kind, handle.Properties["method_from"])
	}
	if !hasConn(topo, "a.FieldImpl.handle", "calls", "a.helper") {
		t.Errorf("handle's body: calls %v", conns(topo, "a.FieldImpl.handle", "calls"))
	}
	if make, ok := topo.Resources["a.FieldImpl.make"]; !ok || make.Properties["is_static"] != true {
		t.Errorf("static field arrow: %+v", make)
	}
	if _, ok := topo.Resources["a.FieldImpl.count"]; ok {
		t.Error("a field holding a number must not become a resource")
	}
}

// JS-5 (fence): a required method that really is missing still warns, and so does an
// optional one's sibling.
func TestMissingRequiredInterfaceMethodStillWarns(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.ts"), `export interface I { run(): void; maybe?(): void; }
export class Broken implements I {
  maybe() {}
}
`)
	msgs := conflictMessages(scanTS(t, dir))
	if len(msgs) != 1 || !strings.Contains(msgs[0], "does not provide run") {
		t.Errorf("want exactly the missing run() reported, got %v", msgs)
	}
}

// JS-6: TypeScript's `this` parameter is not part of the signature a caller sees, while the
// body still reads its annotation.
func TestTSThisParameterNotInSignature(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.ts"), `export class C {
  m(this: C, x: number) { return this.n(); }
  n() { return 1; }
}
export function free(this: C, y: string) { return this.n(); }
export interface I { f(this: I, z: number): void; }
`)
	topo := scanTS(t, dir)
	for id, want := range map[string]string{"a.C.m": "x", "a.free": "y"} {
		if got := inputNames(t, topo, id); len(got) != 1 || got[0] != want {
			t.Errorf("%s input %v, want [%s]", id, got, want)
		}
	}
	if !hasConn(topo, "a.free", "calls", "a.C.n") {
		t.Errorf("this: C still types `this` in the body: free calls %v", conns(topo, "a.free", "calls"))
	}
	blob, _ := json.Marshal(topo.Resources["a.I"].Properties["methods"])
	if strings.Contains(string(blob), `"this"`) {
		t.Errorf("interface method kept the this parameter: %s", blob)
	}
}

// JS-7: files sharing a stem keep distinct IDs; the one an extension-less import reaches keeps
// the plain namespace, and a file with no such sibling is untouched.
func TestStemCollisionKeepsBothFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "util.js"), "export function fmt(x) { return String(x); }\n")
	writeFile(t, filepath.Join(dir, "util.cjs"), "function fmt(x) { return '' + x; }\nmodule.exports = { fmt };\n")
	writeFile(t, filepath.Join(dir, "solo.js"), "export function one() {}\n")
	writeFile(t, filepath.Join(dir, "main.js"), `import { fmt } from './util';
const u = require('./util.cjs');
export function go() { fmt(1); u.fmt(2); }
`)
	topo := scanProject(t, dir)
	for id, file := range map[string]string{"util.fmt": "util.js", "util.cjs.fmt": "util.cjs", "solo.one": "solo.js"} {
		res, ok := topo.Resources[id]
		if !ok || filepath.Base(res.Location.Path) != file {
			t.Errorf("%s: want a resource declared in %s, got %+v", id, file, res.Location)
		}
	}
	for _, target := range []string{"util.fmt", "util.cjs.fmt"} {
		if !hasConn(topo, "main.go", "calls", target) {
			t.Errorf("main.go should call %s, calls %v", target, conns(topo, "main.go", "calls"))
		}
	}

	// Editing util.cjs no longer deletes util.js's fmt.
	writeFile(t, filepath.Join(dir, "util.cjs"), "function other() {}\nmodule.exports = { other };\n")
	if _, err := NewJavaScriptScanner().UpdateFile(topo, filepath.Join(dir, "util.cjs")); err != nil {
		t.Fatal(err)
	}
	if _, ok := topo.Resources["util.fmt"]; !ok {
		t.Error("editing util.cjs removed util.js's fmt")
	}
}

// JS-7: adding the higher-ranked sibling to a lone file moves the lone file's IDs aside in
// the same update, so the result matches a cold scan.
func TestStemSiblingAddedIncrementally(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "util.cjs"), "function fmt(x) { return '' + x; }\nmodule.exports = { fmt };\n")
	topo := scanProject(t, dir)
	res, ok := topo.Resources["util.fmt"]
	if !ok {
		t.Fatal("a lone util.cjs keeps the plain namespace")
	}
	res.Description = "formats x"
	topo.Resources["util.fmt"] = res
	writeFile(t, filepath.Join(dir, "util.js"), "export function fmt(x) { return String(x); }\n")
	if _, err := NewJavaScriptScanner().UpdateFile(topo, filepath.Join(dir, "util.js")); err != nil {
		t.Fatal(err)
	}
	requireSameGraph(t, topo, scanProject(t, dir))
	if got := topo.Resources["util.cjs.fmt"].Description; got != "formats x" {
		t.Errorf("the moved declaration lost its description: %q", got)
	}
}

// JS-7: a JavaScript file compiled next to its TypeScript source yields the namespace to it.
func TestCompiledJSBesideTSSource(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "lib.ts"), "export function fmt(x: number): string { return String(x); }\n")
	writeFile(t, filepath.Join(dir, "lib.js"), "export function fmt(x) { return String(x); }\n")
	if _, ok := scanTS(t, dir).Resources["lib.fmt"]; !ok {
		t.Error("lib.ts keeps lib.fmt")
	}
	if _, ok := scanProject(t, dir).Resources["lib.js.fmt"]; !ok {
		t.Error("lib.js beside lib.ts should mint lib.js.fmt")
	}
}

// JS-9: decorators on an exported class and on TypeScript members are recorded, and the spans
// include them.
func TestDecoratorsOnExportedClassesAndTSMembers(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "w.ts"), `function Component(x: any) { return (c: any) => c; }
function Log(t: any, k: string) {}
@Component({
  selector: 'w',
})
export class Widget {
  @Log
  render() { return 1; }
  @Log
  @Log
  static make() { return 2; }
  @Log handler = () => 1;
  plain() { return 3; }
}
`)
	topo := scanTS(t, dir)
	for id, want := range map[string][2]int{
		"w.Widget":         {3, 14},
		"w.Widget.render":  {7, 8},
		"w.Widget.make":    {9, 11},
		"w.Widget.handler": {12, 12},
		"w.Widget.plain":   {13, 13},
	} {
		if s, e := span(topo, id); s != want[0] || e != want[1] {
			t.Errorf("%s spans %d-%d, want %d-%d", id, s, e, want[0], want[1])
		}
	}
	for id, want := range map[string]string{
		"w.Widget": `["Component"]`, "w.Widget.render": `["Log"]`, "w.Widget.make": `["Log","Log"]`,
		"w.Widget.handler": `["Log"]`, "w.Widget.plain": `null`,
	} {
		got, _ := json.Marshal(topo.Resources[id].Properties["decorators"])
		if string(got) != want {
			t.Errorf("%s decorators %s, want %s", id, got, want)
		}
	}
}

// JS-11: a getter and a setter of one name are one resource covering both.
func TestAccessorPairIsOneResource(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "acc.js"), `import { log } from './log.js';
export class T {
  get size() {
    return this._s;
  }
  other() {}
  set size(v) {
    log(v);
  }
}
`)
	writeFile(t, filepath.Join(dir, "log.js"), "export function log(v) {}\n")
	topo := scanProject(t, dir)
	if s, e := span(topo, "acc.T.size"); s != 3 || e != 9 {
		t.Errorf("accessor pair spans %d-%d, want 3-9 (getter through setter)", s, e)
	}
	if kind := topo.Resources["acc.T.size"].Properties["kind"]; kind != "getter" {
		t.Errorf("accessor pair kind %v, want getter", kind)
	}
	if !hasConn(topo, "acc.T.size", "calls", "log.log") {
		t.Errorf("the setter body's call is kept: calls %v", conns(topo, "acc.T.size", "calls"))
	}
}

// JS-11: a file the parser had to recover from is recorded as a scan error on every path, and
// the error goes away once the file parses cleanly. A clean file records none.
func TestSyntaxErrorIsRecorded(t *testing.T) {
	dir := t.TempDir()
	broken := filepath.Join(dir, "broken.js")
	writeFile(t, broken, "function good1() { return 1; }\nclass Half {\n  m() { return 1;\nfunction good2() { return 2; }\n")
	writeFile(t, filepath.Join(dir, "clean.js"), "export function ok() {}\n")
	topo := scanProject(t, dir)
	msg, ok := topo.Errors[broken]
	if !ok || !strings.Contains(msg, "line 2") {
		t.Errorf("broken.js: want a parse error at line 2, got %q (errors %v)", msg, topo.Errors)
	}
	if _, ok := topo.Errors[filepath.Join(dir, "clean.js")]; ok {
		t.Error("a clean file recorded an error")
	}
	if _, ok := topo.Resources["broken.good1"]; !ok {
		t.Error("declarations the parser recovered are kept")
	}

	if err := os.WriteFile(broken, []byte("function good1() { return 1; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewJavaScriptScanner().UpdateFile(topo, broken); err != nil {
		t.Fatal(err)
	}
	if msg, ok := topo.Errors[broken]; ok {
		t.Errorf("fixed file still carries %q", msg)
	}
}
