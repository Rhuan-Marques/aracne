package pyscanner

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/contract"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// Regression tests for the Python scanner findings PY-3..PY-7 and WN-8 of the 1.0 release
// audit. Each scans a small tree and asserts on the graph a reader would rely on, including
// the cases next to each fix that must keep behaving as before.

func scanPyTree(t *testing.T, files map[string]string) (string, *domain.Topology) {
	t.Helper()
	if !hasPython() {
		t.Skip("python not available")
	}
	dir := t.TempDir()
	writePyTree(t, dir, files)
	topo, err := NewPythonScanner().Scan(dir)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(topo.Errors) != 0 {
		t.Fatalf("scan reported errors: %v", topo.Errors)
	}
	return dir, topo
}

func hasEdge(topo *domain.Topology, src, kind, tgt string) bool {
	return slices.Contains(topo.Resources[src].Connections[kind], tgt)
}

func wantEdge(t *testing.T, topo *domain.Topology, src, kind, tgt string) {
	t.Helper()
	if !hasEdge(topo, src, kind, tgt) {
		t.Errorf("missing %s -%s-> %s; %s has %v", src, kind, tgt, src, topo.Resources[src].Connections)
	}
}

func wantNoEdge(t *testing.T, topo *domain.Topology, src, kind, tgt string) {
	t.Helper()
	if hasEdge(topo, src, kind, tgt) {
		t.Errorf("unexpected %s -%s-> %s", src, kind, tgt)
	}
}

func classFlag(t *testing.T, topo *domain.Topology, id, prop string) bool {
	t.Helper()
	res, ok := topo.Resources[id]
	if !ok || res.Kind != domain.ResourceStruct {
		t.Fatalf("no class %s", id)
	}
	v, _ := res.Properties[prop].(bool)
	return v
}

// PY-3: the file was read as text before ast.parse, so a UTF-8 BOM or a PEP 263 coding
// cookie for a non-UTF-8 encoding made the whole file fail to parse and vanish.
func TestParseAcceptsBOMAndCodingCookie(t *testing.T) {
	if !hasPython() {
		t.Skip("python not available")
	}
	dir := t.TempDir()
	files := map[string][]byte{
		"bom.py":   []byte("\xef\xbb\xbfdef bom_fn():\n    return 1\n"),
		"latin.py": []byte("# -*- coding: latin-1 -*-\n\n\ndef latin_fn():\n    return \"caf\xe9\"\n"),
	}
	for rel, body := range files {
		if err := os.WriteFile(filepath.Join(dir, rel), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for rel, want := range map[string][2]any{"bom.py": {"bom_fn", 1}, "latin.py": {"latin_fn", 4}} {
		pr, err := ParseFile(filepath.Join(dir, rel), ".", dir)
		if err != nil {
			t.Fatalf("%s must parse: %v", rel, err)
		}
		if len(pr.Functions) != 1 || pr.Functions[0].Function.Name != want[0] {
			t.Fatalf("%s: want one function %v, got %+v", rel, want[0], pr.Functions)
		}
		// Line numbers stay those of the file on disk: the BOM sits on line 1, and decoding
		// must not shift anything a later Cut addresses by line.
		if got := pr.Functions[0].Function.Loc.StartsAt; got != want[1] {
			t.Errorf("%s: %v starts at line %d, want %d", rel, want[0], got, want[1])
		}
	}
	// A cookie that lies about the bytes is still a parse error, as it is for Python.
	bad := filepath.Join(dir, "bad.py")
	if err := os.WriteFile(bad, []byte("# coding: ascii\nx = \"caf\xc3\xa9\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseFile(bad, ".", dir); err == nil {
		t.Error("a file its own coding cookie cannot decode must still fail to parse")
	}
}

// PY-4: Protocol and ABC were detected by substring over the base names, so an exception
// named ProtocolError became a structural interface every class with a matching method
// "implemented", and ABCMetaThing made its subclasses abstract.
func TestProtocolAndABCAreMatchedByWhatTheBaseIs(t *testing.T) {
	_, topo := scanPyTree(t, map[string]string{
		"errs.py": "class ProtocolError(Exception):\n    def run(self):\n        pass\n\n\n" +
			"class HandshakeFailed(ProtocolError):\n    def run(self):\n        pass\n\n\n" +
			"class ABCMetaThing:\n    pass\n\n\n" +
			"class MyABCs(ABCMetaThing):\n    def run(self):\n        pass\n\n\n" +
			"class Protocol:\n    def run(self):\n        pass\n\n\n" +
			"class LocalProtocolKid(Protocol):\n    def run(self):\n        pass\n",
		"impl.py": "class Unrelated:\n    def run(self):\n        pass\n",
		"protos.py": "import abc\nimport typing as t\nfrom abc import ABC, ABCMeta\nfrom typing import Generic, Protocol, TypeVar\n\n" +
			"T = TypeVar('T')\n\n\n" +
			"class Runner(Protocol):\n    def run(self): ...\n\n\n" +
			"class Walker(t.Protocol):\n    def walk(self): ...\n\n\n" +
			"class Box(Protocol[T]):\n    def get(self) -> T: ...\n\n\n" +
			"class Shape(ABC):\n    @abc.abstractmethod\n    def area(self): ...\n\n\n" +
			"class Shape2(abc.ABC):\n    pass\n\n\n" +
			"class Meta(metaclass=ABCMeta):\n    pass\n\n\n" +
			"class MetaSub(ABCMeta):\n    pass\n",
		"ext.py":  "from typing_extensions import Protocol as P\n\n\nclass Ext(P):\n    def ext(self): ...\n",
		"star.py": "from typing import *\n\n\nclass Starred(Protocol):\n    def star(self): ...\n",
	})
	for id, want := range map[string]bool{
		"protos.Runner": true, "protos.Walker": true, "protos.Box": true, "ext.Ext": true, "star.Starred": true,
		"errs.ProtocolError": false, "errs.HandshakeFailed": false, "errs.Protocol": false,
		"errs.LocalProtocolKid": false, "protos.Shape": false,
	} {
		if got := classFlag(t, topo, id, "is_protocol"); got != want {
			t.Errorf("%s: is_protocol = %v, want %v", id, got, want)
		}
	}
	for id, want := range map[string]bool{
		"protos.Shape": true, "protos.Shape2": true, "protos.Meta": true,
		"errs.MyABCs": false, "errs.ABCMetaThing": false, "protos.MetaSub": false, "protos.Runner": false,
	} {
		if got := classFlag(t, topo, id, "is_abc"); got != want {
			t.Errorf("%s: is_abc = %v, want %v", id, got, want)
		}
	}
	// The effect the substring match had: a false structural interface.
	wantNoEdge(t, topo, "impl.Unrelated", "implements", "errs.HandshakeFailed")
	wantNoEdge(t, topo, "impl.Unrelated", "implements", "errs.LocalProtocolKid")
	// The real Protocol keeps its structural conformance.
	wantEdge(t, topo, "impl.Unrelated", "implements", "protos.Runner")
}

// PY-5: base classes were resolved by simple name across the project, ignoring the file's
// imports: a relative import lost its edge when two modules defined the name, a module-
// qualified base never matched, and a third-party base was bound to a project class that
// happened to share its name.
func TestBaseClassesResolveThroughImports(t *testing.T) {
	_, topo := scanPyTree(t, map[string]string{
		"pkg/__init__.py": "from .reex import Exported\n",
		"pkg/a.py":        "class Base:\n    def go(self):\n        pass\n",
		"pkg/b.py":        "class Base:\n    def stop(self):\n        pass\n",
		"pkg/reex.py":     "class Exported:\n    pass\n",
		"pkg/shapes.py":   "class Shape:\n    pass\n",
		"pkg/w.py":        "class Widget:\n    pass\n",
		"pkg/c.py": "from .a import Base\nfrom pkg import b as bmod\nfrom pkg import shapes\nimport pkg.shapes\n" +
			"from tkinter import Widget\nfrom pkg import Exported\n\n\n" +
			"class Child(Base):\n    pass\n\n\n" +
			"class Other(bmod.Base):\n    pass\n\n\n" +
			"class D(shapes.Shape):\n    pass\n\n\n" +
			"class D2(pkg.shapes.Shape):\n    pass\n\n\n" +
			"class MyWidget(Widget):\n    pass\n\n\n" +
			"class ViaInit(Exported):\n    pass\n",
		// `class Base(Base)` extends the imported class; the same-module name is itself.
		"pkg/wrap.py": "from .a import Base\n\n\nclass Base(Base):\n    pass\n",
	})
	wantEdge(t, topo, "pkg/c.Child", "inherits", "pkg/a.Base")
	wantNoEdge(t, topo, "pkg/c.Child", "inherits", "pkg/b.Base")
	wantEdge(t, topo, "pkg/c.Other", "inherits", "pkg/b.Base")
	wantEdge(t, topo, "pkg/c.D", "inherits", "pkg/shapes.Shape")
	wantEdge(t, topo, "pkg/c.D2", "inherits", "pkg/shapes.Shape")
	wantEdge(t, topo, "pkg/c.ViaInit", "inherits", "pkg/reex.Exported")
	wantEdge(t, topo, "pkg/a.Base", "inherited_by", "pkg/c.Child")
	wantEdge(t, topo, "pkg/wrap.Base", "inherits", "pkg/a.Base")
	wantNoEdge(t, topo, "pkg/wrap.Base", "inherits", "pkg/wrap.Base")
	if got := topo.Resources["pkg/c.MyWidget"].Connections["inherits"]; len(got) != 0 {
		t.Errorf("a base imported from tkinter is not the project's Widget, got inherits %v", got)
	}
}

// The by-name rules still hold for a base no import binds: same module first, then a class
// unique in the project (a star import, or a name the parser cannot trace).
func TestUnboundBaseClassesKeepTheByNameRules(t *testing.T) {
	_, topo := scanPyTree(t, map[string]string{
		"pkg/__init__.py": "",
		"pkg/a.py":        "class Unique:\n    pass\n\n\nclass Twin:\n    pass\n",
		"pkg/b.py":        "class Twin:\n    pass\n",
		"pkg/c.py": "from .a import *\n\n\nclass Local:\n    pass\n\n\n" +
			"class Kid(Local):\n    pass\n\n\nclass Star(Unique):\n    pass\n\n\nclass Amb(Twin):\n    pass\n",
	})
	wantEdge(t, topo, "pkg/c.Kid", "inherits", "pkg/c.Local")
	wantEdge(t, topo, "pkg/c.Star", "inherits", "pkg/a.Unique")
	if got := topo.Resources["pkg/c.Amb"].Connections["inherits"]; len(got) != 0 {
		t.Errorf("an ambiguous unbound name must stay unresolved, got %v", got)
	}
}

// PY-6: an attribute call on a dotted receiver (`pkg.sub.f()`) recorded nothing, `import
// pkg.sub` bound `pkg` to the submodule, and a directory of code without __init__.py (a PEP
// 420 namespace package) was classified as a third-party dependency.
func TestDottedCallsAndNamespacePackages(t *testing.T) {
	dir, topo := scanPyTree(t, map[string]string{
		"pkg/__init__.py":  "def helper():\n    return 0\n",
		"pkg/sub.py":       "def subf():\n    return 1\n\n\ndef helper():\n    return 9\n\n\nclass Cls:\n    @staticmethod\n    def make():\n        return 2\n",
		"nspkg/mod.py":     "def nsf():\n    return 2\n",
		"nspkg/deep/m2.py": "def deepf():\n    return 3\n",
		"data/readme.txt":  "not code\n",
		"app.py": "import pkg.sub\nimport nspkg.mod\nimport nspkg.deep.m2\nfrom nspkg import mod as m2\nimport os.path\n\n\n" +
			"def caller():\n    pkg.sub.subf()\n    pkg.helper()\n    pkg.sub.Cls.make()\n    nspkg.mod.nsf()\n" +
			"    nspkg.deep.m2.deepf()\n    m2.nsf()\n    os.path.join('a', 'b')\n",
	})
	wantEdge(t, topo, "app.caller", "calls", "pkg/sub.subf")
	wantEdge(t, topo, "app.caller", "calls", "pkg/sub.Cls.make")
	wantEdge(t, topo, "app.caller", "calls", "nspkg/mod.nsf")
	wantEdge(t, topo, "app.caller", "calls", "nspkg/deep/m2.deepf")
	// `import pkg.sub` binds pkg to the package: pkg.helper is __init__'s, not sub's.
	wantEdge(t, topo, "app.caller", "calls", "pkg/__init__.helper")
	wantNoEdge(t, topo, "app.caller", "calls", "pkg/sub.helper")
	// The dotted call is recorded as a call, with its shape.
	if sites := contract.CallSitesOf(topo.Resources["app.caller"], "pkg/sub.subf"); len(sites) != 1 || sites[0].N != 0 {
		t.Errorf("want one recorded call site for pkg/sub.subf, got %+v", sites)
	}
	appFile := filepath.Join(dir, "app.py")
	wantEdge(t, topo, appFile, "imports_module", filepath.Join(dir, "nspkg", "mod.py"))
	for _, dep := range []string{"nspkg.mod", "nspkg.deep.m2"} {
		if _, ok := topo.Resources[dep]; ok {
			t.Errorf("%s is project code, not a dependency node", dep)
		}
	}
	// Genuinely third-party imports stay dependencies, and a directory with no Python in it
	// is not a package.
	if res, ok := topo.Resources["os.path"]; !ok || res.Kind != domain.ResourceDependency {
		t.Errorf("os.path must stay a dependency")
	}
	if isInternal("data", dir) || isInternal("os", dir) {
		t.Error("only directories holding Python code are namespace packages")
	}
	if !isInternal("nspkg.mod", dir) || !isInternal("nspkg", dir) {
		t.Error("a directory of Python code without __init__.py is a namespace package")
	}
}

// PY-7 (locals): assignments were collected from a function's top-level statements only, so
// a local bound inside try/if/for/while/with/match lost its method calls.
func TestLocalsBoundInsideBlocksKeepTheirMethodCalls(t *testing.T) {
	_, topo := scanPyTree(t, map[string]string{
		"m.py": "class Circle:\n    def area(self):\n        return 1\n\n\n" +
			"class Res:\n    def __enter__(self):\n        return self\n\n    def __exit__(self, *a):\n        return False\n\n    def use(self):\n        return 2\n\n\n" +
			"def in_try():\n    try:\n        c = Circle()\n    except Exception:\n        return 0\n    return c.area()\n\n\n" +
			"def in_if(flag):\n    if flag:\n        c = Circle()\n        return c.area()\n    return 0\n\n\n" +
			"def in_for(xs):\n    for _ in xs:\n        c = Circle()\n        c.area()\n\n\n" +
			"def in_while(n):\n    while n:\n        c = Circle()\n        n -= c.area()\n\n\n" +
			"def in_with_if(flag):\n    if flag:\n        with Res() as r:\n            return r.use()\n\n\n" +
			"def in_match(v):\n    match v:\n        case 1:\n            c = Circle()\n            return c.area()\n    return 0\n\n\n" +
			"def nested_scope_stays_out():\n    def inner():\n        c = Circle()\n        return c\n    return c.area()\n",
	})
	for _, fn := range []string{"m.in_try", "m.in_if", "m.in_for", "m.in_while", "m.in_match"} {
		wantEdge(t, topo, fn, "calls", "m.Circle.area")
	}
	wantEdge(t, topo, "m.in_with_if", "calls", "m.Res.use")
	// A local of a nested function is not a local of its parent.
	wantNoEdge(t, topo, "m.nested_scope_stays_out", "calls", "m.Circle.area")
}

// PY-7 (rebinding): `handler = deco(handler)` minted a variable with the function's ID, which
// replaced the function -- it became unreadable, and callers' calls edges pointed at a
// variable. The function is what a reader means; the rebinding is a decorator application.
func TestRebindingKeepsTheFunctionAndRecordsTheWrapper(t *testing.T) {
	_, topo := scanPyTree(t, map[string]string{
		"m.py": "def deco(fn):\n    return fn\n\n\ndef route(path):\n    return deco\n\n\n" +
			"@deco\ndef handler(a):\n    return a\n\n\nhandler = deco(handler)\n\n\n" +
			"def view():\n    return 1\n\n\nview = route('/x')(view)\n\n\n" +
			"def dropped():\n    return 1\n\n\ndropped = None\n\n\n" +
			"class Kls:\n    pass\n\n\nKls = deco(Kls)\n\n\n" +
			"plain = deco(1)\n",
		"u.py": "from m import handler\n\n\ndef uses():\n    return handler(1)\n",
	})
	for id, kind := range map[string]domain.ResourceKind{
		"m.handler": domain.ResourceFunction, "m.view": domain.ResourceFunction,
		"m.dropped": domain.ResourceFunction, "m.Kls": domain.ResourceStruct,
		"m.plain": domain.ResourceVariable,
	} {
		if got := topo.Resources[id].Kind; got != kind {
			t.Errorf("%s is a %q, want %q", id, got, kind)
		}
	}
	// The declaration's own span, not the rebinding's (line 13). It starts at the decorator
	// on line 9, which is where a decorated declaration now begins -- see PY-06.
	if got := topo.Resources["m.handler"].Location.StartsAt; got != 9 {
		t.Errorf("m.handler must keep the declaration's span (line 9), got line %d", got)
	}
	wantEdge(t, topo, "m.handler", "calls", "m.deco")
	wantEdge(t, topo, "m.view", "calls", "m.route")
	wantEdge(t, topo, "u.uses", "calls", "m.handler")
	if sites := contract.CallSitesOf(topo.Resources["u.uses"], "m.handler"); len(sites) != 1 || sites[0].N != 1 {
		t.Errorf("the caller's call site must still be judged against the function, got %+v", sites)
	}
	decs, _ := topo.Resources["m.handler"].Properties["decorators"].([]string)
	if !slices.Equal(decs, []string{"deco", "deco"}) {
		t.Errorf("the rebinding is the outermost decorator, want [deco deco], got %v", decs)
	}
	if decs, _ := topo.Resources["m.dropped"].Properties["decorators"].([]string); len(decs) != 0 {
		t.Errorf("a rebinding that does not wrap the function is no decorator, got %v", decs)
	}
}

// WN-8: `Calc(1)` recorded only uses_class, so an __init__ signature change never reached the
// caller. A class call is now a call to the __init__ the class declares, with its call site --
// and to nothing else: no inherited or implicit constructor, no dataclass-synthesized one,
// and no class pattern, each of which would be a guessed callee.
func TestClassCallsLinkTheDeclaredInit(t *testing.T) {
	_, topo := scanPyTree(t, map[string]string{
		"calc.py": "from dataclasses import dataclass\n\n\n" +
			"class Calc:\n    def __init__(self, base):\n        self.base = base\n\n    @classmethod\n    def default(cls):\n        return Calc(0)\n\n\n" +
			"class NoInit:\n    def run(self):\n        return 1\n\n\n" +
			"class Kid(Calc):\n    pass\n\n\n" +
			"@dataclass\nclass Pt:\n    x: int\n    y: int = 0\n",
		"use.py": "import calc\nfrom calc import Calc, Kid, NoInit, Pt\n\n\n" +
			"def make():\n    return Calc(1)\n\n\ndef dotted():\n    return calc.Calc(base=2)\n\n\n" +
			"def many(xs):\n    return [Calc(x) for x in xs]\n\n\n" +
			"def implicit():\n    return NoInit(), Kid(1), Pt(1)\n\n\n" +
			"def pattern(v):\n    match v:\n        case Calc(base=1):\n            return 1\n    return 0\n",
	})
	const init = "calc.Calc.__init__"
	for _, fn := range []string{"use.make", "use.dotted", "use.many", "calc.Calc.default"} {
		wantEdge(t, topo, fn, "calls", init)
	}
	if sites := contract.CallSitesOf(topo.Resources["use.make"], init); len(sites) != 1 || sites[0].N != 1 {
		t.Errorf("Calc(1) must record one call site passing 1 argument, got %+v", sites)
	}
	if sites := contract.CallSitesOf(topo.Resources["use.dotted"], init); len(sites) != 1 || !slices.Equal(sites[0].Kwargs, []string{"base"}) {
		t.Errorf("calc.Calc(base=2) must record its keyword, got %+v", sites)
	}
	// The class use is still recorded alongside the call.
	wantEdge(t, topo, "use.make", "uses_class", "calc.Calc")

	if calls := topo.Resources["use.implicit"].Connections["calls"]; len(calls) != 0 {
		t.Errorf("classes without a declared __init__ (and a dataclass) must link no constructor, got %v", calls)
	}
	if _, ok := topo.Resources["calc.Pt.__init__"]; !ok {
		t.Fatal("the dataclass keeps its synthesized constructor resource")
	}
	wantNoEdge(t, topo, "use.pattern", "calls", init)
	wantEdge(t, topo, "use.pattern", "uses_class", "calc.Calc")
	for _, s := range topo.Resources["use.pattern"].Connections[contract.CallSitesConn] {
		if strings.HasPrefix(s, init) {
			t.Errorf("a class pattern runs no constructor, yet recorded %s", s)
		}
	}
}
