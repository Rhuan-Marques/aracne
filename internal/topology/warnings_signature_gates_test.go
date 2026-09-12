package topology_test

import (
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// Two ways a signature change that breaks its callers used to go unreported.
//
// A return type is invisible to every call site -- they record what a caller PASSES -- so the
// contract rule, which runs last and is authoritative, withdrew any such warning the moment it
// saw arguments that still fit. And each scanner's "did the signature change" gate decides
// whether a warning exists at all; they compared the parameter count and little else, so a
// default removed, a keyword renamed, a parameter made keyword-only or retyped raised nothing
// for the contract rule to judge.
//
// Every case runs through the default channel, the incremental scan, and ends by putting the
// callee back: a warning that cannot be retracted is a warning an agent learns to ignore.

type gateCase struct {
	name       string
	files      map[string]string
	calleeFile string
	changed    string // the callee after the edit
	// caller is the resource the edit must warn. "" marks a control: an edit every call
	// survives, whose warning the contract rule must retire in the same scan.
	caller string
	// callerFile and callerFixed, when set, are the caller rewritten to fit the change. Fixing
	// the caller has to clear the warning too, or it would outlive correct code.
	callerFile  string
	callerFixed string
}

func runGateCases(t *testing.T, cases []gateCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := newLiveProj(t, c.files)
			p.edit(c.calleeFile, c.changed)
			got := p.stored(domain.WarnSignatureChanged, "", c.caller)
			if c.caller == "" {
				if len(got) != 0 {
					t.Fatalf("every call still fits, yet the edit left %d warning(s): %+v", len(got), got)
				}
				return
			}
			if len(got) == 0 {
				all, _ := p.mgr.ListWarnings("", "", "")
				t.Fatalf("the edit breaks %s and warned nothing; table: %+v", c.caller, all)
			}

			p.edit(c.calleeFile, c.files[c.calleeFile])
			if left := p.stored(domain.WarnSignatureChanged, "", c.caller); len(left) != 0 {
				t.Errorf("putting the callee back must clear the warning, %d left: %+v", len(left), left)
			}

			if c.callerFile == "" {
				return
			}
			p.edit(c.calleeFile, c.changed)
			if len(p.stored(domain.WarnSignatureChanged, "", c.caller)) == 0 {
				t.Fatal("changing the callee again must warn again")
			}
			p.edit(c.callerFile, c.callerFixed)
			if left := p.stored(domain.WarnSignatureChanged, "", c.caller); len(left) != 0 {
				t.Errorf("fixing the caller must clear the warning, %d left: %+v", len(left), left)
			}
		})
	}
}

// WN-2: the return type changes and nothing else does.
func TestReturnTypeOnlyChangeWarnsTheCaller(t *testing.T) {
	runGateCases(t, []gateCase{
		{
			name: "go",
			files: map[string]string{
				"go.mod":     "module ex\n\ngo 1.21\n",
				"lib/lib.go": "package lib\n\nfunc Get(a int) (int, error) { return a, nil }\n",
				"main.go":    "package main\n\nimport \"ex/lib\"\n\nfunc Use() int { x, _ := lib.Get(1); return x }\n\nfunc main() {}\n",
			},
			calleeFile:  "lib/lib.go",
			changed:     "package lib\n\nfunc Get(a int) int { return a }\n",
			caller:      "ex.Use",
			callerFile:  "main.go",
			callerFixed: "package main\n\nimport \"ex/lib\"\n\nfunc Use() int { return lib.Get(1) }\n\nfunc main() {}\n",
		},
		{
			// The single-file edit: the Go partial path, which has to stamp its own baseline.
			name: "go, caller in the same file",
			files: map[string]string{
				"go.mod":     "module ex\n\ngo 1.21\n",
				"lib/lib.go": "package lib\n\nfunc Get(a int) (int, error) { return a, nil }\n\nfunc Use() int { x, _ := Get(1); return x }\n",
			},
			calleeFile: "lib/lib.go",
			changed:    "package lib\n\nfunc Get(a int) int { return a }\n\nfunc Use() int { x, _ := Get(1); return x }\n",
			caller:     "ex/lib.Use",
		},
		{
			name: "python",
			files: map[string]string{
				"lib.py": "def get(a) -> int:\n    return a\n",
				"app.py": "from lib import get\n\n\ndef main():\n    return get(1) + 1\n",
			},
			calleeFile:  "lib.py",
			changed:     "def get(a) -> str:\n    return str(a)\n",
			caller:      "app.main",
			callerFile:  "app.py",
			callerFixed: "from lib import get\n\n\ndef main():\n    return int(get(1)) + 1\n",
		},
		{
			name: "rust",
			files: map[string]string{
				"Cargo.toml": additionCargo,
				"src/lib.rs": "pub mod a;\npub mod b;\n",
				"src/a.rs":   "pub fn get(a: i32) -> i32 { a }\n",
				"src/b.rs":   "use crate::a::get;\npub fn main_fn() -> i32 { get(1) + 1 }\n",
			},
			calleeFile:  "src/a.rs",
			changed:     "pub fn get(a: i32) -> String { a.to_string() }\n",
			caller:      "q::b::main_fn",
			callerFile:  "src/b.rs",
			callerFixed: "use crate::a::get;\npub fn main_fn() -> usize { get(1).len() }\n",
		},
		{
			name: "typescript",
			files: map[string]string{
				"tsconfig.json": "{}",
				"lib.ts":        "export function get(a: number): number { return a; }\n",
				"app.ts":        "import { get } from './lib';\nexport function main(): number { return get(1) + 1; }\n",
			},
			calleeFile:  "lib.ts",
			changed:     "export function get(a: number): string { return String(a); }\n",
			caller:      "app.main",
			callerFile:  "app.ts",
			callerFixed: "import { get } from './lib';\nexport function main(): number { return get(1).length; }\n",
		},
		{
			name: "java",
			files: map[string]string{
				"pom.xml": additionPom,
				"src/main/java/com/ex/Lib.java": "package com.ex;\n\npublic class Lib {\n" +
					"    public static int get(int a) { return a; }\n}\n",
				"src/main/java/com/ex/App.java": "package com.ex;\n\npublic class App {\n" +
					"    public int run() { return Lib.get(1) + 1; }\n}\n",
			},
			calleeFile: "src/main/java/com/ex/Lib.java",
			changed: "package com.ex;\n\npublic class Lib {\n" +
				"    public static String get(int a) { return \"\" + a; }\n}\n",
			caller:     "com.ex.App.run()",
			callerFile: "src/main/java/com/ex/App.java",
			callerFixed: "package com.ex;\n\npublic class App {\n" +
				"    public int run() { return Lib.get(1).length(); }\n}\n",
		},
	})
}

// WN-3: the parameter list changes in a way the old gates did not compare.
func TestSignatureGatesSeeEveryChangeThatBreaksACall(t *testing.T) {
	runGateCases(t, []gateCase{
		{
			name: "python: a default removed",
			files: map[string]string{
				"lib.py": "def opt(x, y=2):\n    return x\n",
				"app.py": "from lib import opt\n\n\ndef main():\n    return opt(3)\n",
			},
			calleeFile: "lib.py",
			changed:    "def opt(x, y):\n    return x\n",
			caller:     "app.main",
		},
		{
			name: "python: a keyword renamed",
			files: map[string]string{
				"lib.py": "def kw(a, b=1):\n    return a\n",
				"app.py": "from lib import kw\n\n\ndef main():\n    return kw(1, b=2)\n",
			},
			calleeFile: "lib.py",
			changed:    "def kw(a, c=1):\n    return a\n",
			caller:     "app.main",
		},
		{
			name: "python: a parameter made keyword-only",
			files: map[string]string{
				"lib.py": "def g(a, b):\n    return a\n",
				"app.py": "from lib import g\n\n\ndef main():\n    return g(1, 2)\n",
			},
			calleeFile: "lib.py",
			changed:    "def g(a, *, b):\n    return a\n",
			caller:     "app.main",
		},
		{
			name: "typescript: an optional parameter made required",
			files: map[string]string{
				"tsconfig.json": "{}",
				"lib.ts":        "export function helper(a: number, b?: number): number { return a; }\n",
				"app.ts":        "import { helper } from './lib';\nexport function main(): number { return helper(1); }\n",
			},
			calleeFile: "lib.ts",
			changed:    "export function helper(a: number, b: number): number { return a; }\n",
			caller:     "app.main",
		},
		{
			name: "typescript: a parameter retyped",
			files: map[string]string{
				"tsconfig.json": "{}",
				"lib.ts":        "export function helper(x: number): number { return 1; }\n",
				"app.ts":        "import { helper } from './lib';\nexport function main(): number { return helper(1); }\n",
			},
			calleeFile: "lib.ts",
			changed:    "export function helper(x: string): number { return 1; }\n",
			caller:     "app.main",
		},
		{
			name: "rust: a parameter retyped",
			files: map[string]string{
				"Cargo.toml": additionCargo,
				"src/lib.rs": "pub mod a;\npub mod b;\n",
				"src/a.rs":   "pub fn helper(a: i32) -> i32 { 1 }\n",
				"src/b.rs":   "use crate::a::helper;\npub fn main_fn() -> i32 { helper(1) }\n",
			},
			calleeFile: "src/a.rs",
			changed:    "pub fn helper(a: String) -> i32 { 1 }\n",
			caller:     "q::b::main_fn",
		},

		// Controls. The gates now fire on these too, and the contract rule has to retire the
		// warning in the same scan because every call still fits.
		{
			name: "python: a parameter renamed under a positional call",
			files: map[string]string{
				"lib.py": "def g(a, b):\n    return a\n",
				"app.py": "from lib import g\n\n\ndef main():\n    return g(1, 2)\n",
			},
			calleeFile: "lib.py",
			changed:    "def g(a, c):\n    return a\n",
		},
		{
			name: "python: a default added",
			files: map[string]string{
				"lib.py": "def opt(x, y):\n    return x\n",
				"app.py": "from lib import opt\n\n\ndef main():\n    return opt(3, 4)\n",
			},
			calleeFile: "lib.py",
			changed:    "def opt(x, y=2):\n    return x\n",
		},
		{
			name: "typescript: a required parameter made optional",
			files: map[string]string{
				"tsconfig.json": "{}",
				"lib.ts":        "export function helper(a: number, b: number): number { return a; }\n",
				"app.ts":        "import { helper } from './lib';\nexport function main(): number { return helper(1, 2); }\n",
			},
			calleeFile: "lib.ts",
			changed:    "export function helper(a: number, b?: number): number { return a; }\n",
		},
		{
			name: "rust: a parameter renamed",
			files: map[string]string{
				"Cargo.toml": additionCargo,
				"src/lib.rs": "pub mod a;\npub mod b;\n",
				"src/a.rs":   "pub fn helper(a: i32) -> i32 { a }\n",
				"src/b.rs":   "use crate::a::helper;\npub fn main_fn() -> i32 { helper(1) }\n",
			},
			calleeFile: "src/a.rs",
			changed:    "pub fn helper(b: i32) -> i32 { b }\n",
		},
		{
			// JavaScript's gate stays at names: a default is never binding there, and its
			// matcher could never retire the warning.
			name: "javascript: a default added",
			files: map[string]string{
				"package.json": `{"name":"x","type":"module"}`,
				"lib.js":       "export function f(a, b) { return a; }\n",
				"app.js":       "import { f } from './lib.js';\nexport function main() { return f(1, 2); }\n",
			},
			calleeFile: "lib.js",
			changed:    "export function f(a, b = 1) { return a; }\n",
		},
	})
}
