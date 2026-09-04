package topology_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner/goscanner"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner/javascanner"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner/jsscanner"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner/pyscanner"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner/rustscanner"
)

// End-to-end warning lifecycle, one table per language.
//
// The unit tables in internal/topology/contract pin the RULES. These pin that the rules are
// actually reached: that each scanner records its call sites, that the records survive the
// database round-trip, and that the warning appears and disappears at the right moments
// through a real scan. A matcher that is never consulted passes every unit test and helps
// nobody.
//
// The sequence each language runs is the one an agent actually produces mid-refactor --
// widen a signature, change it again, put it back, fix the caller instead -- because that
// is where the old event-based rules went wrong.

type contractLang struct {
	name  string
	files map[string]string

	calleeFile string
	// three states of the callee: as written, one parameter wider, two wider.
	calleeOrig  string
	calleeWide  string
	calleeWider string
	// an edit to the callee that changes nothing about its signature.
	calleeNoise string

	callerFile string
	// the caller as written (fits calleeOrig), fixed to fit calleeWide, and touched
	// without being fixed.
	callerOrig    string
	callerFixed   string
	callerTouched string

	// strict is false for JavaScript, where argument count is not binding, so the
	// contract rule declines and only the older callee-history rule applies.
	strict bool
}

func contractLangCases() []contractLang {
	return []contractLang{
		{
			name:   "go",
			strict: true,
			files: map[string]string{
				"go.mod":    "module testproject\n\ngo 1.21\n",
				"target.go": "package testproject\n\nfunc FuncA(x int) int { return x }\n",
				"caller.go": "package testproject\n\nfunc Caller() int { return FuncA(1) }\n",
			},
			calleeFile:  "target.go",
			calleeOrig:  "package testproject\n\nfunc FuncA(x int) int { return x }\n",
			calleeWide:  "package testproject\n\nfunc FuncA(x int, y int) int { return x + y }\n",
			calleeWider: "package testproject\n\nfunc FuncA(x int, y int, z int) int { return x + y + z }\n",
			calleeNoise: "package testproject\n\n// a comment\nfunc FuncA(x int) int { return x }\n",
			callerFile:  "caller.go",
			callerOrig:  "package testproject\n\nfunc Caller() int { return FuncA(1) }\n",
			callerFixed: "package testproject\n\nfunc Caller() int { return FuncA(1, 2) }\n",
			callerTouched: "package testproject\n\n// touched, not fixed\n" +
				"func Caller() int { return FuncA(1) }\n",
		},
		{
			name:   "python",
			strict: true,
			files: map[string]string{
				"a.py": "def add(x):\n    return x\n",
				"b.py": "from a import add\n\n\ndef use():\n    return add(1)\n",
			},
			calleeFile:  "a.py",
			calleeOrig:  "def add(x):\n    return x\n",
			calleeWide:  "def add(x, y):\n    return x + y\n",
			calleeWider: "def add(x, y, z):\n    return x + y + z\n",
			calleeNoise: "# a comment\ndef add(x):\n    return x\n",
			callerFile:  "b.py",
			callerOrig:  "from a import add\n\n\ndef use():\n    return add(1)\n",
			callerFixed: "from a import add\n\n\ndef use():\n    return add(1, 2)\n",
			callerTouched: "from a import add\n\n\n# touched, not fixed\n" +
				"def use():\n    return add(1)\n",
		},
		{
			name:   "rust",
			strict: true,
			files: map[string]string{
				"Cargo.toml": "[package]\nname = \"ctr\"\nversion = \"0.1.0\"\nedition = \"2021\"\n",
				"src/lib.rs": "pub mod a;\npub mod b;\n",
				"src/a.rs":   "pub fn add(x: i32) -> i32 { x }\n",
				"src/b.rs":   "use crate::a::add;\npub fn use_it() -> i32 { add(1) }\n",
			},
			calleeFile:  "src/a.rs",
			calleeOrig:  "pub fn add(x: i32) -> i32 { x }\n",
			calleeWide:  "pub fn add(x: i32, y: i32) -> i32 { x + y }\n",
			calleeWider: "pub fn add(x: i32, y: i32, z: i32) -> i32 { x + y + z }\n",
			calleeNoise: "// a comment\npub fn add(x: i32) -> i32 { x }\n",
			callerFile:  "src/b.rs",
			callerOrig:  "use crate::a::add;\npub fn use_it() -> i32 { add(1) }\n",
			callerFixed: "use crate::a::add;\npub fn use_it() -> i32 { add(1, 2) }\n",
			callerTouched: "use crate::a::add;\n// touched, not fixed\n" +
				"pub fn use_it() -> i32 { add(1) }\n",
		},
		{
			name:   "typescript",
			strict: true,
			files: map[string]string{
				"package.json":  `{ "name": "ctts", "version": "1.0.0" }` + "\n",
				"tsconfig.json": `{ "compilerOptions": { "target": "ES2020" } }` + "\n",
				"a.ts":          "export function add(x: number): number { return x; }\n",
				"b.ts":          "import { add } from './a';\nexport function useIt(): number { return add(1); }\n",
			},
			calleeFile:  "a.ts",
			calleeOrig:  "export function add(x: number): number { return x; }\n",
			calleeWide:  "export function add(x: number, y: number): number { return x + y; }\n",
			calleeWider: "export function add(x: number, y: number, z: number): number { return x + y + z; }\n",
			calleeNoise: "// a comment\nexport function add(x: number): number { return x; }\n",
			callerFile:  "b.ts",
			callerOrig:  "import { add } from './a';\nexport function useIt(): number { return add(1); }\n",
			callerFixed: "import { add } from './a';\nexport function useIt(): number { return add(1, 2); }\n",
			callerTouched: "import { add } from './a';\n// touched, not fixed\n" +
				"export function useIt(): number { return add(1); }\n",
		},
		{
			// JavaScript is deliberately NOT strict: f(1) against function f(a, b) is legal,
			// so the contract rule declines and only the callee-history rule applies.
			name:   "javascript",
			strict: false,
			files: map[string]string{
				"package.json": `{ "name": "ctjs", "version": "1.0.0", "type": "module" }` + "\n",
				"a.js":         "export function add(x) { return x; }\n",
				"b.js":         "import { add } from './a.js';\nexport function useIt() { return add(1); }\n",
			},
			calleeFile:  "a.js",
			calleeOrig:  "export function add(x) { return x; }\n",
			calleeWide:  "export function add(x, y) { return x + y; }\n",
			calleeWider: "export function add(x, y, z) { return x + y + z; }\n",
			calleeNoise: "// a comment\nexport function add(x) { return x; }\n",
			callerFile:  "b.js",
			callerOrig:  "import { add } from './a.js';\nexport function useIt() { return add(1); }\n",
			callerFixed: "import { add } from './a.js';\nexport function useIt() { return add(1, 2); }\n",
			callerTouched: "import { add } from './a.js';\n// touched, not fixed\n" +
				"export function useIt() { return add(1); }\n",
		},
	}
}

func contractRegistry() *scanner.Registry {
	reg := scanner.NewRegistry()
	reg.Register(goscanner.NewGoScanner())
	reg.Register(pyscanner.NewPythonScanner())
	reg.Register(jsscanner.NewJavaScriptScanner())
	reg.Register(jsscanner.NewTypeScriptScanner())
	reg.Register(rustscanner.NewRustScanner())
	reg.Register(javascanner.NewJavaScanner())
	return reg
}

type contractProj struct {
	dir string
	reg *scanner.Registry
	mgr *topology.TopologyManager
	t   *testing.T
}

func newContractProj(t *testing.T, c contractLang) *contractProj {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range c.files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	reg := contractRegistry()
	mgr := topology.New()
	mgr.Load(filepath.Join(dir, "topology.db"))
	if err := mgr.FullScan(dir, reg); err != nil {
		t.Fatalf("FullScan: %v", err)
	}
	return &contractProj{dir: dir, reg: reg, mgr: mgr, t: t}
}

// edit rewrites a file and re-parses it, exactly as the edit hook does.
func (p *contractProj) edit(rel, content string) {
	p.t.Helper()
	path := filepath.Join(p.dir, filepath.FromSlash(rel))
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		p.t.Fatal(err)
	}
	mtime := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(path, mtime, mtime)
	if _, err := p.mgr.UpdateFile(path, p.reg); err != nil {
		p.t.Fatalf("UpdateFile(%s): %v", rel, err)
	}
}

func (p *contractProj) sigWarnings() int {
	p.t.Helper()
	w, err := p.mgr.ListWarnings("", "", domain.WarnSignatureChanged)
	if err != nil {
		p.t.Fatal(err)
	}
	return len(w)
}

// callerWarnings counts warnings that name the caller to go verify, whichever kind they
// are. Java is the reason this is not just signature_changed: its method ids encode the
// parameter list, so widening a signature is an identity change and the graph may report
// the removal of the old id instead. Both name the caller and both must clear.
func (p *contractProj) callerWarnings() int {
	p.t.Helper()
	all, err := p.mgr.ListWarnings("", "", "")
	if err != nil {
		p.t.Fatal(err)
	}
	n := 0
	for _, w := range all {
		if w.Kind == domain.WarnSignatureChanged || w.Kind == domain.WarnNodeRemoved {
			n++
		}
	}
	return n
}

// TestContractLifecyclePerLanguage runs the full break/re-break/revert/fix sequence.
func TestContractLifecyclePerLanguage(t *testing.T) {
	for _, c := range contractLangCases() {
		t.Run(c.name, func(t *testing.T) {
			p := newContractProj(t, c)
			if n := p.callerWarnings(); n != 0 {
				t.Fatalf("a freshly scanned project must be clean, got %d warnings", n)
			}

			// 1. widen the callee: the caller no longer fits.
			p.edit(c.calleeFile, c.calleeWide)
			if n := p.callerWarnings(); n == 0 {
				t.Fatal("widening the signature must warn the caller")
			}

			// 2. an edit to the callee that changes nothing about the signature must not
			//    discharge a warning that is still true.
			p.edit(c.calleeFile, c.calleeWide+"\n")
			if n := p.callerWarnings(); n == 0 {
				t.Error("an unrelated edit to the callee silenced a live warning")
			}

			// 3. widen again. The caller is still written against the ORIGINAL, so the
			//    warning stands.
			p.edit(c.calleeFile, c.calleeWider)
			if n := p.callerWarnings(); n == 0 {
				t.Error("a second widening must not clear the warning")
			}

			// 4. put the callee back exactly as it was: nothing left to verify.
			p.edit(c.calleeFile, c.calleeOrig)
			if n := p.callerWarnings(); n != 0 {
				w, _ := p.mgr.ListWarnings("", "", "")
				t.Errorf("reverting the callee must clear the warning, %d left: %+v", n, w)
			}

			// 5. widen once more, then fix the CALLER instead.
			p.edit(c.calleeFile, c.calleeWide)
			if n := p.callerWarnings(); n == 0 {
				t.Fatal("widening again must warn")
			}
			p.edit(c.callerFile, c.callerFixed)
			if n := p.callerWarnings(); n != 0 {
				w, _ := p.mgr.ListWarnings("", "", "")
				t.Errorf("fixing the caller must clear the warning, %d left: %+v", n, w)
			}
		})
	}
}

// TestContractRejectsATouchThatIsNotAFix is the tightened caller-side clear, per language.
//
// Re-parsing the caller's file used to discharge the warning whatever the edit was, so
// opening a caller and changing a comment silenced a warning that was still true. Where the
// call can be checked, it is; JavaScript keeps the permissive rule because it has no way to
// tell -- passing too few arguments is legal there.
func TestContractRejectsATouchThatIsNotAFix(t *testing.T) {
	for _, c := range contractLangCases() {
		t.Run(c.name, func(t *testing.T) {
			p := newContractProj(t, c)
			p.edit(c.calleeFile, c.calleeWide)
			if n := p.callerWarnings(); n == 0 {
				t.Fatal("widening the signature must warn the caller")
			}

			p.edit(c.callerFile, c.callerTouched)
			got := p.callerWarnings()
			if c.strict && got == 0 {
				t.Error("the call still does not fit; touching the caller must not clear it")
			}
			if !c.strict && got != 0 {
				t.Errorf("JavaScript cannot tell a fix from a touch and must keep the "+
					"permissive rule, got %d warning(s)", got)
			}

			// The real fix clears it in every language.
			p.edit(c.callerFile, c.callerFixed)
			if n := p.callerWarnings(); n != 0 {
				w, _ := p.mgr.ListWarnings("", "", "")
				t.Errorf("fixing the call must clear it, %d left: %+v", n, w)
			}
		})
	}
}

// TestContractDoesNotWarnOnACorrectProject is the false-positive guard. Every language
// scans a project where every call fits, and must produce nothing -- including for the
// constructs a naive matcher gets wrong: defaults, optional parameters, varargs, generics
// and interfaces.
func TestContractDoesNotWarnOnACorrectProject(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
	}{
		{"go: variadic, interface, generic, untyped constants", map[string]string{
			"go.mod": "module tp\n\ngo 1.21\n",
			"a.go": `package tp

import "strings"

type Shape interface{ Area() float64 }
type Sq struct{ S float64 }

func (s Sq) Area() float64 { return s.S * s.S }

func Vari(a int, rest ...string) int      { return a }
func Iface(sh Shape) float64              { return sh.Area() }
func Generic[T any](v T) T                { return v }
func Floaty(f float64) float64            { return f }
func Joins(parts []string) string         { return strings.Join(parts, ",") }
`,
			"b.go": `package tp

func Use() float64 {
	Vari(1)
	Vari(1, "a", "b")
	Iface(Sq{S: 2})
	Generic(5)
	Generic("s")
	Floaty(5)
	Joins([]string{"a"})
	return 0
}
`,
		}},
		{"python: defaults, *args, **kwargs, keyword-only, methods", map[string]string{
			"a.py": `def defaulted(x, y=1, z=2):
    return x


def starred(x, *args, **kwargs):
    return x


def kwonly(x, *, mode=1, req):
    return x


class C:
    def m(self, a, b=2):
        return a

    @classmethod
    def cm(cls, a):
        return a
`,
			"b.py": `from a import defaulted, starred, kwonly, C


def use():
    defaulted(1)
    defaulted(1, 2)
    defaulted(1, 2, 3)
    defaulted(1, z=9)
    starred(1)
    starred(1, 2, 3, extra=4)
    kwonly(1, req=2)
    c = C()
    c.m(1)
    c.m(1, 2)
    C.cm(1)
    return 0
`,
		}},
		{"typescript: optional, default, rest, union, interface, generic", map[string]string{
			"package.json":  `{ "name": "tp", "version": "1.0.0" }` + "\n",
			"tsconfig.json": `{ "compilerOptions": { "target": "ES2020" } }` + "\n",
			"a.ts": `export interface Shape { area(): number }
export class Sq implements Shape { area(): number { return 4; } }
export function opt(a: number, b?: string): number { return a; }
export function def(a: number, b: number = 2): number { return a + b; }
export function rest(a: number, ...more: number[]): number { return a; }
export function uni(a: string | number): number { return 0; }
export function gen<T>(v: T): T { return v; }
export function iface(s: Shape): number { return s.area(); }
`,
			"b.ts": `import { opt, def, rest, uni, gen, iface, Sq } from './a';
export function use(): number {
  opt(1);
  opt(1, "x");
  def(1);
  def(1, 2);
  rest(1);
  rest(1, 2, 3, 4);
  uni("s");
  uni(5);
  gen(1);
  iface(new Sq());
  return 0;
}
`,
		}},
		{"rust: generics, impl Trait, trait objects", map[string]string{
			"Cargo.toml": "[package]\nname = \"tp\"\nversion = \"0.1.0\"\nedition = \"2021\"\n",
			"src/lib.rs": "pub mod a;\npub mod b;\n",
			"src/a.rs": `pub trait Draw { fn draw(&self) -> i32; }
pub struct Circle;
impl Draw for Circle { fn draw(&self) -> i32 { 1 } }

pub fn plain(x: i32, y: f64) -> i32 { x }
pub fn generic<T>(v: T) -> i32 { 0 }
pub fn implish(v: impl Into<String>) -> i32 { 0 }
pub fn dynish(v: &dyn Draw) -> i32 { v.draw() }
pub fn texty(s: &str) -> usize { s.len() }
`,
			"src/b.rs": `use crate::a::{plain, generic, implish, dynish, texty, Circle};
pub fn use_it() -> i32 {
    plain(1, 2.0);
    generic(5);
    generic("s");
    implish("s");
    dynish(&Circle);
    texty("hi");
    0
}
`,
		}},
		{"java: overloads, varargs, boxing, interface", map[string]string{
			"pom.xml": `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.tp</groupId><artifactId>tp</artifactId><version>1.0.0</version>
</project>
`,
			"src/main/java/com/tp/A.java": `package com.tp;
public interface Runner { int run(); }
`,
			"src/main/java/com/tp/B.java": `package com.tp;
public class B implements Runner {
    public int run() { return 1; }
    public int over(int a) { return a; }
    public int over(int a, String b) { return a; }
    public int vari(int a, String... rest) { return a; }
    public int boxed(Long a) { return 1; }
    public int takesRunner(Runner r) { return r.run(); }
}
`,
			"src/main/java/com/tp/C.java": `package com.tp;
public class C {
    public int use() {
        B b = new B();
        b.over(1);
        b.over(1, "x");
        b.vari(1);
        b.vari(1, "a", "b");
        b.takesRunner(b);
        return 0;
    }
}
`,
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			for rel, content := range c.files {
				p := filepath.Join(dir, filepath.FromSlash(rel))
				if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(content), 0644); err != nil {
					t.Fatal(err)
				}
			}
			mgr := topology.New()
			mgr.Load(filepath.Join(dir, "topology.db"))
			if err := mgr.FullScan(dir, contractRegistry()); err != nil {
				t.Fatalf("FullScan: %v", err)
			}
			w, err := mgr.ListWarnings("", "", domain.WarnSignatureChanged)
			if err != nil {
				t.Fatal(err)
			}
			if len(w) != 0 {
				t.Errorf("every call in this project fits; got %d signature warning(s): %+v", len(w), w)
			}
		})
	}
}
