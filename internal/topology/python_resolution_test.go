package topology_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/topology"
)

// WN-8: a Python constructor call recorded only a class use, so changing __init__ never warned
// the code that calls the class -- among the most common Python breaks. It now records the
// call to __init__ with its arguments, and the ordinary signature lifecycle judges it: a
// breaking change warns, a revert or a fixed caller clears it, and a change every call still
// fits never surfaces.
func TestPythonConstructorSignatureChangeWarnsTheCaller(t *testing.T) {
	calc := "class Calc:\n    def __init__(self, base):\n        self.base = base\n"
	use := "from calc import Calc\n\n\ndef make():\n    return Calc(1)\n"
	runGateCases(t, []gateCase{
		{
			name:        "python: __init__ gains a required parameter",
			files:       map[string]string{"calc.py": calc, "use.py": use},
			calleeFile:  "calc.py",
			changed:     "class Calc:\n    def __init__(self, base, extra):\n        self.base = base\n",
			caller:      "use.make",
			callerFile:  "use.py",
			callerFixed: "from calc import Calc\n\n\ndef make():\n    return Calc(1, 2)\n",
		},
		{
			name: "python: a module-qualified constructor call",
			files: map[string]string{"calc.py": calc,
				"use.py": "import calc\n\n\ndef make():\n    return calc.Calc(base=1)\n"},
			calleeFile: "calc.py",
			changed:    "class Calc:\n    def __init__(self, start):\n        self.base = start\n",
			caller:     "use.make",
		},
		{
			// Control: an optional parameter breaks no call.
			name:       "python: __init__ gains an optional parameter",
			files:      map[string]string{"calc.py": calc, "use.py": use},
			calleeFile: "calc.py",
			changed:    "class Calc:\n    def __init__(self, base, extra=0):\n        self.base = base\n",
		},
		{
			// Control: the dataclass constructor is synthesized from every class-level name
			// without defaults, so it is never linked; a defaulted field must not warn Pt(1).
			name: "python: a dataclass gains a defaulted field",
			files: map[string]string{
				"pt.py":  "from dataclasses import dataclass\n\n\n@dataclass\nclass Pt:\n    x: int\n    y: int = 0\n",
				"use.py": "from pt import Pt\n\n\ndef make():\n    return Pt(1)\n",
			},
			calleeFile: "pt.py",
			changed:    "from dataclasses import dataclass\n\n\n@dataclass\nclass Pt:\n    x: int\n    y: int = 0\n    z: int = 5\n",
		},
		{
			// Control: a class without its own __init__ links no constructor, so an edit to
			// the class raises nothing against the code that instantiates it.
			name: "python: a class without __init__ changes",
			files: map[string]string{
				"k.py":   "class NoInit:\n    def run(self):\n        return 1\n",
				"use.py": "from k import NoInit\n\n\ndef make():\n    return NoInit()\n",
			},
			calleeFile: "k.py",
			changed:    "class NoInit:\n    def run(self, x):\n        return x\n\n    def other(self, y):\n        return y\n",
		},
	})
}

// Every Python resolution change made for PY-3..PY-7 and WN-8 keeps the incremental scan equal
// to a cold scan of the same tree, across a sequence of ordinary edits.
func TestPythonResolutionIncrementalMatchesColdScan(t *testing.T) {
	type seqCase struct {
		name  string
		files map[string]string
		steps []map[string]string
		want  []string // edges the cold scan must have, as "src -kind-> tgt"
	}
	m := "def deco(fn):\n    return fn\n\n\nclass Circle:\n    def area(self):\n        return 1\n\n\n" +
		"def handler():\n    return 1\n\n\n"
	inTry := "def in_try():\n    try:\n        c = Circle()\n    except Exception:\n        return 0\n    return c.area()\n"
	cases := []seqCase{
		{
			name:  "PY-3: a file gains a BOM, a latin-1 file appears",
			files: map[string]string{"bom.py": "def bom_fn():\n    return 1\n", "use.py": "from bom import bom_fn\n\n\ndef u():\n    return bom_fn()\n"},
			steps: []map[string]string{
				{"bom.py": "\xef\xbb\xbfdef bom_fn():\n    return 1\n"},
				{"latin.py": "# -*- coding: latin-1 -*-\nfrom bom import bom_fn\n\n\ndef latin_fn():\n    return bom_fn(), \"caf\xe9\"\n"},
			},
			want: []string{"use.u -calls-> bom.bom_fn", "latin.latin_fn -calls-> bom.bom_fn"},
		},
		{
			name: "PY-4: a Protocol appears beside a ProtocolError hierarchy",
			files: map[string]string{
				"errs.py": "class ProtocolError(Exception):\n    def run(self):\n        pass\n\n\nclass HandshakeFailed(ProtocolError):\n    def run(self):\n        pass\n",
				"impl.py": "class Unrelated:\n    def run(self):\n        pass\n",
			},
			steps: []map[string]string{
				{"proto.py": "from typing import Protocol\n\n\nclass Runner(Protocol):\n    def run(self): ...\n"},
				{"impl.py": "class Unrelated:\n    def run(self):\n        pass\n\n\nclass Second:\n    def run(self):\n        pass\n"},
			},
			want: []string{"impl.Unrelated -implements-> proto.Runner", "impl.Second -implements-> proto.Runner"},
		},
		{
			name: "PY-5: an imported base appears, is re-exported, renamed and restored",
			files: map[string]string{
				"pkg/__init__.py": "",
				"pkg/a.py":        "class Other:\n    pass\n",
				"pkg/b.py":        "class Base:\n    def stop(self):\n        pass\n",
				"pkg/c.py":        "from .a import Base\nfrom pkg import b as bmod\n\n\nclass Child(Base):\n    pass\n\n\nclass Other2(bmod.Base):\n    pass\n",
				"pkg/d.py":        "from pkg import Base\n\n\nclass D(Base):\n    pass\n",
			},
			steps: []map[string]string{
				{"pkg/a.py": "class Base:\n    def go(self):\n        pass\n"},
				{"pkg/__init__.py": "from .b import Base\n"},
				{"pkg/b.py": "class Base2:\n    def stop(self):\n        pass\n"},
				{"pkg/b.py": "class Base:\n    def stop(self):\n        pass\n"},
			},
			want: []string{"pkg/c.Child -inherits-> pkg/a.Base", "pkg/c.Other2 -inherits-> pkg/b.Base", "pkg/d.D -inherits-> pkg/b.Base"},
		},
		{
			name: "PY-6: dotted calls into a package and a namespace package",
			files: map[string]string{
				"pkg/__init__.py": "def helper():\n    return 0\n",
				"pkg/sub.py":      "def subf():\n    return 1\n",
				"nspkg/mod.py":    "def nsf():\n    return 2\n",
				"app.py":          "import pkg.sub\n\n\ndef caller():\n    return pkg.sub.subf()\n",
			},
			steps: []map[string]string{
				{"app.py": "import pkg.sub\nimport nspkg.mod\n\n\ndef caller():\n    pkg.sub.subf()\n    nspkg.mod.nsf()\n    return pkg.helper()\n"},
				{"pkg/sub.py": "def subf(x=0):\n    return 1\n"},
			},
			want: []string{"app.caller -calls-> pkg/sub.subf", "app.caller -calls-> nspkg/mod.nsf", "app.caller -calls-> pkg/__init__.helper"},
		},
		{
			name: "PY-7: a function rebound by hand, and a local bound inside try",
			files: map[string]string{
				"m.py": m + inTry,
				"u.py": "from m import handler\n\n\ndef uses():\n    return handler()\n",
			},
			steps: []map[string]string{
				{"m.py": m + "handler = deco(handler)\n\n\n" + inTry},
				{"u.py": "from m import handler\n\n\ndef uses():\n    return handler() + 1\n"},
			},
			want: []string{"m.handler -calls-> m.deco", "u.uses -calls-> m.handler", "m.in_try -calls-> m.Circle.area"},
		},
		{
			name: "WN-8: __init__ changed, removed and restored",
			files: map[string]string{
				"calc.py": "class Calc:\n    def __init__(self, base):\n        self.base = base\n",
				"use.py":  "from calc import Calc\n\n\ndef make():\n    return Calc(1)\n",
			},
			steps: []map[string]string{
				{"calc.py": "class Calc:\n    def __init__(self, base, extra):\n        self.base = base\n"},
				{"calc.py": "class Calc:\n    pass\n"},
				{"calc.py": "class Calc:\n    def __init__(self, base=0):\n        self.base = base\n"},
			},
			want: []string{"use.make -calls-> calc.Calc.__init__"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			additionWrite(t, dir, c.files, time.Time{})
			reg := contractRegistry()
			mgr := topology.New()
			mgr.Load(filepath.Join(dir, "topology.db"))
			if err := mgr.FullScan(dir, reg); err != nil {
				t.Fatalf("FullScan: %v", err)
			}
			for i, step := range c.steps {
				additionWrite(t, dir, step, time.Now().Add(time.Duration(3*(i+1))*time.Second))
				if _, err := mgr.IncrementalScan(dir, reg); err != nil {
					t.Fatalf("IncrementalScan after step %d: %v", i+1, err)
				}
			}
			incremental, err := mgr.ReadAll()
			if err != nil {
				t.Fatal(err)
			}

			cold := topology.New()
			cold.Load(filepath.Join(t.TempDir(), "cold.db"))
			if err := cold.FullScan(dir, reg); err != nil {
				t.Fatalf("cold FullScan: %v", err)
			}
			fresh, err := cold.ReadAll()
			if err != nil {
				t.Fatal(err)
			}
			if len(fresh.Errors) != 0 {
				t.Fatalf("cold scan reported errors: %v", fresh.Errors)
			}
			coldEdges := additionEdges(fresh)
			for _, edge := range c.want {
				if !coldEdges[edge] {
					t.Errorf("the cold scan has no %q", edge)
				}
			}
			if diff := additionDiff(incremental, fresh); len(diff) > 0 {
				t.Errorf("incremental scan differs from a cold scan (- incremental only, + cold only):\n  %s",
					strings.Join(diff, "\n  "))
			}
		})
	}
}
