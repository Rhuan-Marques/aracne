package pyscanner

import (
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/contract"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// Regression tests for the medium findings PY-01..PY-06 of the fresh-eyes audit. Each one
// pins the fact the resolver or the matcher reads, at the level it is produced: what the
// parse script stores for an annotation, what shape an operator records, which parameters
// survive receiver stripping, which method an inherited call resolves to, and where a
// decorated declaration starts.

func paramsOf(t *testing.T, topo *domain.Topology, id string) []contract.Param {
	t.Helper()
	res, ok := topo.Resources[id]
	if !ok {
		t.Fatalf("no resource %s", id)
	}
	return contract.Params(res)
}

func siteTo(t *testing.T, topo *domain.Topology, caller, callee string) contract.CallSite {
	t.Helper()
	sites := contract.CallSitesOf(topo.Resources[caller], callee)
	if len(sites) != 1 {
		t.Fatalf("want exactly one recorded call %s -> %s, got %d (%v)",
			caller, callee, len(sites), topo.Resources[caller].Connections)
	}
	return sites[0]
}

// PY-01: expr_str collapsed a subscript to its head and a PEP 604 union to the literal
// text "expr", so `int | None` was stored as "expr" and `typing.Sequence[str]` as
// "typing.Sequence". The matcher then compared those against the class of a literal and
// reported every correct call as a mismatch.
func TestAnnotationTextIsStoredVerbatim(t *testing.T) {
	_, topo := scanPyTree(t, map[string]string{
		"m.py": "import typing\n\n\n" +
			"def f604(x: int | None, y=1):\n    return x\n\n\n" +
			"def ftyping(x: typing.Optional[int], y=1):\n    return x\n\n\n" +
			"def fseq(x: typing.Sequence[str], y=1):\n    return x\n\n\n" +
			"def flist(x: list[int]) -> dict[str, int]:\n    return {}\n",
	})
	want := map[string]string{
		"m.f604":    "int | None",
		"m.ftyping": "typing.Optional[int]",
		"m.fseq":    "typing.Sequence[str]",
		"m.flist":   "list[int]",
	}
	for id, typing := range want {
		ps := paramsOf(t, topo, id)
		if len(ps) == 0 {
			t.Fatalf("%s has no parameters", id)
		}
		if ps[0].Typing != typing {
			t.Errorf("%s: parameter x is stored as %q, want %q", id, ps[0].Typing, typing)
		}
	}
	res := topo.Resources["m.flist"]
	out := contract.Params(domain.Resource{Properties: map[string]any{"input": res.Properties["output"]}})
	if len(out) != 1 || out[0].Typing != "dict[str, int]" {
		t.Errorf("return annotation is stored as %v, want dict[str, int]", out)
	}
}

// PY-01: a union or generic annotation is not a class the matcher can compare a literal
// against, so it must decline rather than report the call.
func TestCompositeAnnotationsAcceptLiterals(t *testing.T) {
	_, topo := scanPyTree(t, map[string]string{
		"m.py": "import typing\n\n\n" +
			"def f604(x: int | None, y=1):\n    return x\n\n\n" +
			"def ftyping(x: typing.Optional[int], y=1):\n    return x\n\n\n" +
			"def fseq(x: typing.Sequence[str], y=1):\n    return x\n\n\n" +
			"def use():\n    f604(5)\n    f604(None)\n    ftyping(5)\n    fseq('abc')\n",
	})
	caller := topo.Resources["m.use"]
	for _, callee := range []string{"m.f604", "m.ftyping", "m.fseq"} {
		for _, site := range contract.CallSitesOf(caller, callee) {
			v, why := contract.For("python").Match(contract.Env{}, topo.Resources[callee], site)
			if v == contract.Mismatch {
				t.Errorf("%s: correct call reported as a mismatch: %s", callee, why)
			}
		}
	}
}

// PY-02: the operator pseudo-call carried no argument list, and the missing "argc" decoded
// as 0 -- so `a + b` claimed to pass nothing and __add__ was permanently reported as
// requiring an argument the call does not pass.
func TestOperatorCallRecordsItsRealShape(t *testing.T) {
	_, topo := scanPyTree(t, map[string]string{
		"m.py": "class V:\n" +
			"    def __add__(self, other: 'V') -> 'V':\n        return self\n\n" +
			"    def __getitem__(self, i: int) -> int:\n        return i\n\n\n" +
			"def use(a: V, b: V):\n    c = a + b\n    return a[0]\n",
	})
	for _, callee := range []string{"m.V.__add__", "m.V.__getitem__"} {
		site := siteTo(t, topo, "m.use", callee)
		if site.N != 1 {
			t.Errorf("%s: operator call records N=%d, want 1", callee, site.N)
		}
		v, why := contract.For("python").Match(contract.Env{}, topo.Resources[callee], site)
		if v == contract.Mismatch {
			t.Errorf("%s: operator call reported as a mismatch: %s", callee, why)
		}
	}
}

// PY-03: `Base.m(self, x)` passes the receiver explicitly, but the stored signature has it
// stripped, so the call counted one argument too many and never fitted.
func TestClassQualifiedCallDoesNotCountTheReceiver(t *testing.T) {
	_, topo := scanPyTree(t, map[string]string{
		"m.py": "class Base:\n" +
			"    def __init__(self, a: int):\n        self.a = a\n\n" +
			"    def m(self, x: int) -> int:\n        return x\n\n" +
			"    @staticmethod\n    def s(x: int) -> int:\n        return x\n\n\n" +
			"class Derived(Base):\n" +
			"    def __init__(self, a: int):\n        Base.__init__(self, a)\n\n" +
			"    def m(self, x: int) -> int:\n        return Base.m(self, x)\n\n" +
			"    def viaclass(self, x: int) -> int:\n        return Base.s(x)\n",
	})
	for caller, callee := range map[string]string{
		"m.Derived.m":        "m.Base.m",
		"m.Derived.__init__": "m.Base.__init__",
	} {
		site := siteTo(t, topo, caller, callee)
		if site.N != 1 {
			t.Errorf("%s -> %s records N=%d, want 1 (the receiver is not an argument)",
				caller, callee, site.N)
		}
		if v, why := contract.For("python").Match(contract.Env{}, topo.Resources[callee], site); v == contract.Mismatch {
			t.Errorf("%s -> %s reported as a mismatch: %s", caller, callee, why)
		}
	}
	// A staticmethod declares no receiver, so nothing may be shifted away from its call.
	if site := siteTo(t, topo, "m.Derived.viaclass", "m.Base.s"); site.N != 1 {
		t.Errorf("Base.s(x) records N=%d, want 1", site.N)
	}
}

// PY-04: every parameter named self or cls was dropped, wherever it appeared. A
// module-level `dumps(obj, cls=None)` lost a real parameter, and a method's second
// parameter named cls vanished with it, hiding a genuinely broken call.
func TestReceiverStrippingOnlyTakesTheFirstParameter(t *testing.T) {
	_, topo := scanPyTree(t, map[string]string{
		"m.py": "def dumps(obj, cls=None):\n    return obj\n\n\n" +
			"class Reg:\n" +
			"    def register(self, cls):\n        return cls\n\n" +
			"    @staticmethod\n    def build(cls, x):\n        return x\n\n" +
			"    @classmethod\n    def make(cls, x):\n        return x\n",
	})
	want := map[string][]string{
		"m.dumps":        {"obj", "cls"},
		"m.Reg.register": {"cls"},
		"m.Reg.build":    {"cls", "x"},
		"m.Reg.make":     {"x"},
	}
	for id, names := range want {
		ps := paramsOf(t, topo, id)
		got := make([]string, 0, len(ps))
		for _, p := range ps {
			got = append(got, p.Name)
		}
		if len(got) != len(names) {
			t.Errorf("%s: parameters %v, want %v", id, got, names)
			continue
		}
		for i := range names {
			if got[i] != names[i] {
				t.Errorf("%s: parameters %v, want %v", id, got, names)
				break
			}
		}
	}
}

// PY-05: a receiver was typed to its class and the method looked up only in that class's
// own methods, so the ordinary OOP case -- calling a method the class inherits -- drew no
// edge at all, from the subclass or from an outside caller.
func TestInheritedMethodCallsDrawAnEdge(t *testing.T) {
	_, topo := scanPyTree(t, map[string]string{
		"m.py": "class Base:\n" +
			"    def greet(self, who: str) -> str:\n        return who\n\n\n" +
			"class Child(Base):\n" +
			"    def hello(self) -> str:\n        return self.greet('x')\n\n\n" +
			"def use(c: Child) -> str:\n    return c.greet('y')\n",
	})
	wantEdge(t, topo, "m.Child.hello", "calls", "m.Base.greet")
	wantEdge(t, topo, "m.use", "calls", "m.Base.greet")
	if site := siteTo(t, topo, "m.use", "m.Base.greet"); site.N != 1 {
		t.Errorf("inherited call records N=%d, want 1", site.N)
	}
	// An override still wins over the inherited definition.
	_, topo2 := scanPyTree(t, map[string]string{
		"m.py": "class Base:\n    def greet(self) -> str:\n        return 'b'\n\n\n" +
			"class Child(Base):\n" +
			"    def greet(self) -> str:\n        return 'c'\n\n" +
			"    def hello(self) -> str:\n        return self.greet()\n",
	})
	wantEdge(t, topo2, "m.Child.hello", "calls", "m.Child.greet")
	wantNoEdge(t, topo2, "m.Child.hello", "calls", "m.Base.greet")
}

// PY-06: node.lineno is the `def`/`class` line, which is BELOW the decorators, so every
// read of a decorated declaration dropped the decorator that gives it its meaning --
// @dataclass, @property, @staticmethod, a route.
func TestDeclarationsStartAtTheirFirstDecorator(t *testing.T) {
	_, topo := scanPyTree(t, map[string]string{
		"m.py": "import dataclasses\n" + // 1
			"\n" + // 2
			"\n" + // 3
			"def deco(f):\n" + // 4
			"    return f\n" + // 5
			"\n" + // 6
			"\n" + // 7
			"@dataclasses.dataclass\n" + // 8
			"class Point:\n" + // 9
			"    x: int = 0\n" + // 10
			"\n" + // 11
			"    @property\n" + // 12
			"    def double(self) -> int:\n" + // 13
			"        return self.x * 2\n" + // 14
			"\n" + // 15
			"\n" + // 16
			"@deco\n" + // 17
			"@deco\n" + // 18
			"def routed():\n" + // 19
			"    return 1\n", // 20
	})
	want := map[string]int{"m.Point": 8, "m.Point.double": 12, "m.routed": 17, "m.deco": 4}
	for id, line := range want {
		res, ok := topo.Resources[id]
		if !ok {
			t.Fatalf("no resource %s", id)
		}
		if res.Location.StartsAt != line {
			t.Errorf("%s starts at line %d, want %d", id, res.Location.StartsAt, line)
		}
	}
}
