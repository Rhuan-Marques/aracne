package topology_test

import (
	"testing"
)

// Full warning lifecycles for the medium Python findings PY-01..PY-05.
//
// Every one of them was a call the matcher could not judge correctly, and the symptom was
// always the same shape: a warning that appeared on a correct call and could never be
// retired, or a genuinely broken call nobody was told about. So each case runs the whole
// sequence an agent produces -- a compatible edit that must stay silent, a breaking edit
// that must warn, and a fix that must clear it -- rather than only the moment of the bug.

func pyProj(t *testing.T, files map[string]string) *contractProj {
	t.Helper()
	return newContractProj(t, contractLang{files: files})
}

// PY-01: `int | None` was stored as the literal text "expr" and `typing.Optional[int]` as
// "typing.Optional", so the matcher compared a literal argument against a type name that
// names nothing and reported every correct call. The warning could never be retired,
// because no edit to the caller could make 5 equal "expr".
func TestPythonCompositeHintsDoNotWarnForever(t *testing.T) {
	p := pyProj(t, map[string]string{
		"a.py": "import typing\n\n\n" +
			"def f(x: int | None, y=1):\n    return x\n\n\n" +
			"def g(x: typing.Optional[int], y=1):\n    return x\n",
		"b.py": "from a import f, g\n\n\ndef use():\n    return f(5) or g(5)\n",
	})
	if n := p.callerWarnings(); n != 0 {
		t.Fatalf("a freshly scanned project must be clean, got %d", n)
	}

	// A trailing optional parameter keeps every existing call valid.
	p.edit("a.py", "import typing\n\n\n"+
		"def f(x: int | None, y=1, z=0):\n    return x\n\n\n"+
		"def g(x: typing.Optional[int], y=1, z=0):\n    return x\n")
	if n := p.callerWarnings(); n != 0 {
		w, _ := p.mgr.ListWarnings("", "", "")
		t.Fatalf("a compatible widening must not warn, got %d: %+v", n, w)
	}

	// A required parameter does break the call, and must still be reported.
	p.edit("a.py", "import typing\n\n\n"+
		"def f(x: int | None, y, z):\n    return x\n\n\n"+
		"def g(x: typing.Optional[int], y, z):\n    return x\n")
	if n := p.callerWarnings(); n == 0 {
		t.Fatal("a required parameter breaks the call and must warn")
	}

	// And the fix retires it.
	p.edit("b.py", "from a import f, g\n\n\ndef use():\n    return f(5, 1, 2) or g(5, 1, 2)\n")
	if n := p.callerWarnings(); n != 0 {
		w, _ := p.mgr.ListWarnings("", "", "")
		t.Errorf("fixing the caller must clear the warning, %d left: %+v", n, w)
	}
}

// PY-02: the operator pseudo-call recorded no argument list, and the absent count decoded
// as zero, so `a + b` claimed to pass nothing to __add__. Any edit to __add__ re-judged
// that call and reported the operand as missing.
func TestPythonOperatorCallIsJudgedByItsRealShape(t *testing.T) {
	p := pyProj(t, map[string]string{
		"v.py": "class V:\n    def __add__(self, other: int) -> 'V':\n        return self\n",
		"u.py": "from v import V\n\n\ndef use(a: V, b: V):\n    return a + b\n",
	})
	if n := p.callerWarnings(); n != 0 {
		t.Fatalf("a freshly scanned project must be clean, got %d", n)
	}

	// Changing only the annotation leaves `a + b` exactly as valid as it was.
	p.edit("v.py", "class V:\n    def __add__(self, other: float) -> 'V':\n        return self\n")
	if n := p.callerWarnings(); n != 0 {
		w, _ := p.mgr.ListWarnings("", "", "")
		t.Fatalf("re-annotating an operator must not warn its callers, got %d: %+v", n, w)
	}

	// A second required parameter genuinely cannot be supplied by `a + b`, so it must be
	// reported: the shape is recorded, not merely ignored.
	p.edit("v.py", "class V:\n    def __add__(self, other: float, extra) -> 'V':\n        return self\n")
	if n := p.callerWarnings(); n == 0 {
		t.Error("an operator overload the expression cannot satisfy must warn")
	}

	p.edit("v.py", "class V:\n    def __add__(self, other: float) -> 'V':\n        return self\n")
	if n := p.callerWarnings(); n != 0 {
		w, _ := p.mgr.ListWarnings("", "", "")
		t.Errorf("reverting must clear the warning, %d left: %+v", n, w)
	}
}

// PY-03: `Base.m(self, x)` passes the receiver explicitly while the stored signature has it
// stripped, so the call always counted one argument too many and the warning stood forever.
func TestPythonClassQualifiedCallClears(t *testing.T) {
	p := pyProj(t, map[string]string{
		"base.py": "class Base:\n    def m(self, x: int) -> int:\n        return x\n",
		"derived.py": "from base import Base\n\n\nclass Derived(Base):\n" +
			"    def m(self, x: int) -> int:\n        return Base.m(self, x)\n",
	})
	if n := p.callerWarnings(); n != 0 {
		t.Fatalf("a freshly scanned project must be clean, got %d", n)
	}

	// Only the parameter annotation moves: a changed RETURN type is deliberately not
	// judgeable from a call site, and would keep the warning standing for its own reason.
	p.edit("base.py", "class Base:\n    def m(self, x: float) -> int:\n        return x\n")
	if n := p.callerWarnings(); n != 0 {
		w, _ := p.mgr.ListWarnings("", "", "")
		t.Fatalf("re-annotating must not warn an unbound call that still fits, got %d: %+v", n, w)
	}

	p.edit("base.py", "class Base:\n    def m(self, x: float, y: int) -> int:\n        return x\n")
	if n := p.callerWarnings(); n == 0 {
		t.Fatal("a required parameter breaks Base.m(self, x) and must warn")
	}

	p.edit("derived.py", "from base import Base\n\n\nclass Derived(Base):\n"+
		"    def m(self, x: int) -> int:\n        return Base.m(self, x, 0)\n")
	if n := p.callerWarnings(); n != 0 {
		w, _ := p.mgr.ListWarnings("", "", "")
		t.Errorf("fixing the unbound call must clear the warning, %d left: %+v", n, w)
	}
}

// PY-04: a parameter named cls was dropped from every signature, wherever it stood. On a
// module-level function that invented a keyword the callee "does not have"; on a method it
// hid a break the interpreter raises.
func TestPythonClsParameterIsNotDroppedFromAFunction(t *testing.T) {
	p := pyProj(t, map[string]string{
		"a.py": "def dumps(obj, cls=None):\n    return obj\n",
		"b.py": "from a import dumps\n\n\ndef use():\n    return dumps(1, cls=int)\n",
	})
	p.edit("a.py", "def dumps(obj, cls=None, indent=0):\n    return obj\n")
	if n := p.callerWarnings(); n != 0 {
		w, _ := p.mgr.ListWarnings("", "", "")
		t.Fatalf("cls is a real parameter; the keyword call is correct, got %d: %+v", n, w)
	}

	// Removing it is a real break, and must be reported.
	p.edit("a.py", "def dumps(obj, indent=0):\n    return obj\n")
	if n := p.callerWarnings(); n == 0 {
		t.Error("dropping the parameter the call names by keyword must warn")
	}

	p.edit("b.py", "from a import dumps\n\n\ndef use():\n    return dumps(1, indent=2)\n")
	if n := p.callerWarnings(); n != 0 {
		w, _ := p.mgr.ListWarnings("", "", "")
		t.Errorf("fixing the keyword must clear the warning, %d left: %+v", n, w)
	}
}

// PY-04, the other direction: a second parameter named cls was stripped from a method, so a
// call that the interpreter rejects with TypeError was reported as fitting.
func TestPythonSecondParameterNamedClsIsChecked(t *testing.T) {
	p := pyProj(t, map[string]string{
		"reg.py": "class Reg:\n    def register(self, cls):\n        return cls\n",
		"u.py":   "from reg import Reg\n\n\ndef use(r: Reg):\n    return r.register(int)\n",
	})
	p.edit("reg.py", "class Reg:\n    def register(self, cls, name):\n        return cls\n")
	if n := p.callerWarnings(); n == 0 {
		t.Fatal("r.register(Foo) no longer fits and must warn")
	}
	p.edit("u.py", "from reg import Reg\n\n\ndef use(r: Reg):\n    return r.register(int, 'i')\n")
	if n := p.callerWarnings(); n != 0 {
		w, _ := p.mgr.ListWarnings("", "", "")
		t.Errorf("fixing the caller must clear the warning, %d left: %+v", n, w)
	}
}

// PY-05: a call to a method the receiver's class INHERITS drew no edge, so the base
// method's callers were invisible -- and changing it warned nobody.
func TestPythonInheritedMethodCallerIsWarned(t *testing.T) {
	p := pyProj(t, map[string]string{
		"base.py":  "class Base:\n    def greet(self, who: str) -> str:\n        return who\n",
		"child.py": "from base import Base\n\n\nclass Child(Base):\n    pass\n",
		"u.py":     "from child import Child\n\n\ndef use(c: Child) -> str:\n    return c.greet('x')\n",
	})
	if n := p.callerWarnings(); n != 0 {
		t.Fatalf("a freshly scanned project must be clean, got %d", n)
	}

	p.edit("base.py", "class Base:\n    def greet(self, who: str, loud: bool = False) -> str:\n        return who\n")
	if n := p.callerWarnings(); n != 0 {
		w, _ := p.mgr.ListWarnings("", "", "")
		t.Fatalf("an optional parameter keeps the call valid, got %d: %+v", n, w)
	}

	p.edit("base.py", "class Base:\n    def greet(self, who: str, loud: bool) -> str:\n        return who\n")
	if n := p.callerWarnings(); n == 0 {
		t.Fatal("a required parameter on the inherited method must warn its caller")
	}

	p.edit("u.py", "from child import Child\n\n\ndef use(c: Child) -> str:\n    return c.greet('x', True)\n")
	if n := p.callerWarnings(); n != 0 {
		w, _ := p.mgr.ListWarnings("", "", "")
		t.Errorf("fixing the caller must clear the warning, %d left: %+v", n, w)
	}
}
