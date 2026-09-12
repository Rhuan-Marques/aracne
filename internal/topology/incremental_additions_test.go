package topology_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// An incremental scan must equal a cold one for edits that ADD something.
//
// The incremental path re-resolves the files that changed, plus the files whose edges point at
// something that went away or changed shape -- the pre-update graph names those. An addition
// leaves no such trail: a call written against a function that did not exist yet resolved to
// nothing, so nothing recorded that it was waiting, and nothing re-parsed it when the function
// appeared. Each case is an ordinary step of the edit loop -- the caller written before the
// callee -- that used to leave the graph missing an edge, or pointing at the wrong symbol,
// until someone ran `scan --all`. Further plain scans never caught up.
//
// Each case compares the whole graph against a cold scan of the same tree, and also names the
// edge the edit is about, so a comparison that passes because BOTH scans missed it fails.

type additionCase struct {
	name  string
	files map[string]string // the tree before the edit
	edit  map[string]string // files the edit writes, new or rewritten
	want  []string          // edges the cold scan must have, as "src -kind-> tgt"
}

const additionPom = `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.ex</groupId><artifactId>ex</artifactId><version>1.0.0</version>
</project>
`

const additionGoMod = "module ex\n\ngo 1.21\n"

const additionCargo = "[package]\nname = \"q\"\nversion = \"0.1.0\"\n"

func additionCases() []additionCase {
	return []additionCase{
		{
			name: "javascript: a callee written after its caller",
			files: map[string]string{
				"package.json": `{"name":"x"}`,
				"a.js":         "export function existing() { return 1; }\n",
				"b.js":         "import { existing, later } from './a.js';\nexport function caller() { existing(); later(); }\n",
			},
			edit: map[string]string{
				"a.js": "export function existing() { return 1; }\nexport function later() { return 2; }\n",
			},
			want: []string{"b.caller -calls-> a.later"},
		},
		{
			name: "javascript: a method written after its call",
			files: map[string]string{
				"package.json": `{"name":"x"}`,
				"a.js":         "export class C {\n  one() { return 1; }\n}\n",
				"b.js":         "import { C } from './a.js';\nexport function caller() { const c = new C(); c.one(); c.two(); }\n",
			},
			edit: map[string]string{
				"a.js": "export class C {\n  one() { return 1; }\n  two() { return 2; }\n}\n",
			},
			want: []string{"b.caller -calls-> a.C.two"},
		},
		{
			name: "javascript: a barrel gains export *",
			files: map[string]string{
				"package.json": `{"name":"x"}`,
				"lib/a.js":     "export function fa() { return 1; }\n",
				"lib/b.js":     "export function fb() { return 1; }\n",
				"lib/index.js": "export * from './a.js';\n",
				"use.js":       "import { fa, fb } from './lib/index.js';\nexport function caller() { fa(); fb(); }\n",
			},
			edit: map[string]string{
				"lib/index.js": "export * from './a.js';\nexport * from './b.js';\n",
			},
			want: []string{"use.caller -calls-> lib/b.fb"},
		},
		{
			name: "javascript: a new file satisfies a dangling import",
			files: map[string]string{
				"package.json": `{"name":"x"}`,
				"use.js":       "import { fn } from './later.js';\nexport function caller() { fn(); }\n",
			},
			edit: map[string]string{
				"later.js": "export function fn() { return 1; }\n",
			},
			want: []string{"use.caller -calls-> later.fn"},
		},
		{
			name: "javascript: export default re-pointed",
			files: map[string]string{
				"package.json": `{"name":"x"}`,
				"a.js":         "function one() {}\nfunction two() {}\nexport default one;\n",
				"b.js":         "import def from './a.js';\nexport function caller() { def(); }\n",
			},
			edit: map[string]string{
				"a.js": "function one() {}\nfunction two() {}\nexport default two;\n",
			},
			want: []string{"b.caller -calls-> a.two"},
		},
		{
			name: "javascript: a named re-export retargeted",
			files: map[string]string{
				"package.json": `{"name":"x"}`,
				"a.js":         "export function fa() {}\n",
				"b.js":         "export function fb() {}\n",
				"barrel.js":    "export { fa as x } from './a.js';\n",
				"use.js":       "import { x } from './barrel.js';\nexport function caller() { x(); }\n",
			},
			edit: map[string]string{
				"barrel.js": "export { fb as x } from './b.js';\n",
			},
			want: []string{"use.caller -calls-> b.fb"},
		},
		{
			name: "typescript: an interface written after the parameter typed with it",
			files: map[string]string{
				"tsconfig.json": "{}",
				"a.ts":          "export class Real { go(): void {} }\n",
				"b.ts":          "import { Real, Later } from './a';\nexport function use(r: Real, l: Later): void { r.go(); }\n",
			},
			edit: map[string]string{
				"a.ts": "export class Real { go(): void {} }\nexport interface Later { x(): void; }\n",
			},
			want: []string{"b.use -uses_interface-> a.Later"},
		},
		{
			name: "typescript: a constructor written after its call",
			files: map[string]string{
				"tsconfig.json": "{}",
				"a.ts":          "export class A { run(): void {} }\n",
				"b.ts":          "import { A } from './a';\nexport function use(): void { const x = new A(); }\n",
			},
			edit: map[string]string{
				"a.ts": "export class A {\n  constructor() {}\n  run(): void {}\n}\n",
			},
			want: []string{"b.use -calls-> a.A.constructor"},
		},
		{
			name: "typescript: the default-exported class swapped",
			files: map[string]string{
				"tsconfig.json": "{}",
				"a.ts":          "class One { m(): void {} }\nclass Two { m(): void {} }\nexport default One;\n",
				"b.ts":          "import Def from './a';\nexport function use(): void { const d = new Def(); d.m(); }\n",
			},
			edit: map[string]string{
				"a.ts": "class One { m(): void {} }\nclass Two { m(): void {} }\nexport default Two;\n",
			},
			want: []string{"b.use -calls-> a.Two.m"},
		},
		{
			name: "python: a callee written after its caller",
			files: map[string]string{
				"a.py": "def existing():\n    return 1\n",
				"b.py": "from a import existing, later\n\n\ndef caller():\n    existing()\n    later()\n",
			},
			edit: map[string]string{
				"a.py": "def existing():\n    return 1\n\n\ndef later():\n    return 2\n",
			},
			want: []string{"b.caller -calls-> a.later"},
		},
		{
			name: "python: a new module satisfies a dangling import",
			files: map[string]string{
				"use.py":   "from later import fn\n\n\ndef caller():\n    fn()\n",
				"other.py": "def o():\n    return 1\n",
			},
			edit: map[string]string{
				"later.py": "def fn():\n    return 1\n",
			},
			want: []string{"use.caller -calls-> later.fn"},
		},
		{
			name: "rust: a new module file",
			files: map[string]string{
				"Cargo.toml":  additionCargo,
				"src/lib.rs":  "pub mod user;\npub mod helpers;\n",
				"src/user.rs": "use crate::helpers::assist;\npub fn go() { assist(); }\n",
			},
			edit: map[string]string{
				"src/helpers.rs": "pub fn assist() {}\n",
			},
			want: []string{"q::user::go -calls-> q::helpers::assist"},
		},
		{
			name: "rust: a callee written after its caller",
			files: map[string]string{
				"Cargo.toml": additionCargo,
				"src/lib.rs": "pub mod a;\npub mod b;\n",
				"src/a.rs":   "pub fn existing() {}\n",
				"src/b.rs":   "use crate::a::{existing, later};\npub fn caller() { existing(); later(); }\n",
			},
			edit: map[string]string{
				"src/a.rs": "pub fn existing() {}\npub fn later() {}\n",
			},
			want: []string{"q::b::caller -calls-> q::a::later"},
		},
		{
			name: "java: a new class file",
			files: map[string]string{
				"pom.xml": additionPom,
				"src/main/java/com/ex/Kid.java": "package com.ex;\n\npublic class Kid extends Late {\n" +
					"    public int own() { return 1; }\n}\n",
				"src/main/java/com/ex/User.java": "package com.ex;\n\npublic class User {\n" +
					"    public int run() { return Late.go(); }\n}\n",
			},
			edit: map[string]string{
				"src/main/java/com/ex/Late.java": "package com.ex;\n\npublic class Late {\n" +
					"    public static int go() { return 1; }\n}\n",
			},
			want: []string{
				"com.ex.Kid -inherits-> com.ex.Late",
				"com.ex.User.run() -calls-> com.ex.Late.go()",
			},
		},
		{
			name: "java: a method written after its call",
			files: map[string]string{
				"pom.xml": additionPom,
				"src/main/java/com/ex/Late.java": "package com.ex;\n\npublic class Late {\n" +
					"    public static int one() { return 1; }\n}\n",
				"src/main/java/com/ex/User.java": "package com.ex;\n\npublic class User {\n" +
					"    public int run() { return Late.one() + Late.two(); }\n}\n",
			},
			edit: map[string]string{
				"src/main/java/com/ex/Late.java": "package com.ex;\n\npublic class Late {\n" +
					"    public static int one() { return 1; }\n    public static int two() { return 2; }\n}\n",
			},
			want: []string{"com.ex.User.run() -calls-> com.ex.Late.two()"},
		},
		{
			// One new file: the partial fast path. `c.Diameter()` on a Circle-typed
			// variable resolved to nothing and left no warning behind, so no trail
			// pointed back at use.go when the method appeared.
			name: "go: a method written after its call",
			files: map[string]string{
				"go.mod":           additionGoMod,
				"shapes/circle.go": "package shapes\n\ntype Circle struct{ R int }\n",
				"shapes/use.go":    "package shapes\n\nfunc Use(c Circle) int { return c.Diameter() }\n",
			},
			edit: map[string]string{
				"shapes/diam.go": "package shapes\n\nfunc (c Circle) Diameter() int { return c.R * 2 }\n",
			},
			want: []string{"ex/shapes.Use -calls-> ex/shapes.(Circle).Diameter"},
		},
		{
			// Two changed files: the two-phase full path, same missing edge.
			name: "go: a method written after its call, two files changed",
			files: map[string]string{
				"go.mod":           additionGoMod,
				"shapes/circle.go": "package shapes\n\ntype Circle struct{ R int }\n",
				"shapes/use.go":    "package shapes\n\nfunc Use(c Circle) int { return c.Diameter() }\n",
			},
			edit: map[string]string{
				"shapes/diam.go":  "package shapes\n\nfunc (c Circle) Diameter() int { return c.R * 2 }\n",
				"shapes/other.go": "package shapes\n\nfunc Other() int { return 1 }\n",
			},
			want: []string{"ex/shapes.Use -calls-> ex/shapes.(Circle).Diameter"},
		},
		{
			// The method lands in an EXISTING file, and its caller is in another package.
			name: "go: a method added to an existing file, called from an importer",
			files: map[string]string{
				"go.mod":           additionGoMod,
				"shapes/circle.go": "package shapes\n\ntype Circle struct{ R int }\n\nfunc (c Circle) Area() int { return c.R }\n",
				"use/use.go":       "package use\n\nimport \"ex/shapes\"\n\nfunc Use(c shapes.Circle) int { return c.Area() + c.Diameter() }\n",
			},
			edit: map[string]string{
				"shapes/circle.go": "package shapes\n\ntype Circle struct{ R int }\n\nfunc (c Circle) Area() int { return c.R }\n\nfunc (c Circle) Diameter() int { return c.R * 2 }\n",
			},
			want: []string{"ex/use.Use -calls-> ex/shapes.(Circle).Diameter"},
		},
		{
			name: "go: a package var written after its use",
			files: map[string]string{
				"go.mod":      additionGoMod,
				"vars/use.go": "package vars\n\nfunc Use() int { return Count }\n",
			},
			edit: map[string]string{
				"vars/count.go": "package vars\n\nvar Count = 3\n",
			},
			want: []string{"ex/vars.Use -uses_extvar-> ex/vars.Count"},
		},
		{
			name: "go: a named type written after a body spells it",
			files: map[string]string{
				"go.mod":       additionGoMod,
				"units/use.go": "package units\n\nfunc Use() float64 {\n\tvar m Meter\n\treturn float64(m)\n}\n",
			},
			edit: map[string]string{
				"units/meter.go": "package units\n\ntype Meter float64\n",
			},
			want: []string{"ex/units.Use -uses_named_type-> ex/units.Meter"},
		},
		{
			// The implements edge comes from the methods Outer gets by embedding Base, which
			// live in a package the interface's file never mentions.
			name: "go: an interface satisfied through an embedded struct's methods",
			files: map[string]string{
				"go.mod":         additionGoMod,
				"base/base.go":   "package base\n\ntype Base struct{}\n\nfunc (b Base) Hello() string { return \"hi\" }\n",
				"mid/outer.go":   "package mid\n\nimport \"ex/base\"\n\ntype Outer struct{ base.Base }\n",
				"mid/greeter.go": "package mid\n\ntype Greeter interface {\n\tHello() string\n}\n",
			},
			edit: map[string]string{
				"mid/outer.go": "package mid\n\nimport \"ex/base\"\n\n// Outer greets through Base.\ntype Outer struct{ base.Base }\n",
			},
			want: []string{"ex/mid.Greeter -implemented_by-> ex/mid.Outer"},
		},
		{
			name: "go: a method added to a non-struct named type, called from an importer",
			files: map[string]string{
				"go.mod":           additionGoMod,
				"units/celsius.go": "package units\n\ntype Celsius float64\n",
				"app/app.go": "package app\n\nimport \"ex/units\"\n\n" +
					"func Use(c units.Celsius) string { return c.String() }\n",
			},
			edit: map[string]string{
				"units/celsius.go": "package units\n\ntype Celsius float64\n\nfunc (c Celsius) String() string { return \"c\" }\n",
			},
			want: []string{"ex/app.Use -calls-> ex/units.(Celsius).String"},
		},
		{
			name: "go: a package var written after another package reads it",
			files: map[string]string{
				"go.mod":   additionGoMod,
				"b/use.go": "package b\n\nimport \"ex/a\"\n\nfunc Use() int { return a.MaxRetries }\n",
				"a/a.go":   "package a\n\nvar Other = 1\n",
			},
			edit: map[string]string{
				"a/a.go": "package a\n\nvar Other = 1\n\nconst MaxRetries = 3\n",
			},
			want: []string{"ex/b.Use -uses_extvar-> ex/a.MaxRetries"},
		},
		{
			name: "go: a struct written after a body spells it",
			files: map[string]string{
				"go.mod":       additionGoMod,
				"boxes/use.go": "package boxes\n\nfunc Use() int {\n\tvar b Box\n\t_ = b\n\treturn 1\n}\n",
			},
			edit: map[string]string{
				"boxes/box.go": "package boxes\n\ntype Box struct{}\n",
			},
			want: []string{"ex/boxes.Use -uses_struct-> ex/boxes.Box"},
		},
	}
}

func TestIncrementalScanMatchesColdScanAfterAnAddition(t *testing.T) {
	for _, c := range additionCases() {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			additionWrite(t, dir, c.files, time.Time{})
			reg := contractRegistry()
			mgr := topology.New()
			mgr.Load(filepath.Join(dir, "topology.db"))
			if err := mgr.FullScan(dir, reg); err != nil {
				t.Fatalf("FullScan: %v", err)
			}

			additionWrite(t, dir, c.edit, time.Now().Add(3*time.Second))
			if _, err := mgr.IncrementalScan(dir, reg); err != nil {
				t.Fatalf("IncrementalScan: %v", err)
			}
			incremental, err := mgr.ReadAll()
			if err != nil {
				t.Fatal(err)
			}

			// A second database outside the tree, so the cold scan indexes the same paths.
			cold := topology.New()
			cold.Load(filepath.Join(t.TempDir(), "cold.db"))
			if err := cold.FullScan(dir, reg); err != nil {
				t.Fatalf("cold FullScan: %v", err)
			}
			fresh, err := cold.ReadAll()
			if err != nil {
				t.Fatal(err)
			}

			coldEdges := additionEdges(fresh)
			for _, edge := range c.want {
				if !coldEdges[edge] {
					t.Fatalf("the cold scan has no %q, so this case proves nothing", edge)
				}
			}
			if diff := additionDiff(incremental, fresh); len(diff) > 0 {
				t.Errorf("incremental scan differs from a cold scan (- incremental only, + cold only):\n  %s",
					strings.Join(diff, "\n  "))
			}
		})
	}
}

// additionWrite writes files under dir, stamping them with mtime when it is not zero so the
// incremental scan sees them as changed.
func additionWrite(t *testing.T, dir string, files map[string]string, mtime time.Time) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		if !mtime.IsZero() {
			if err := os.Chtimes(p, mtime, mtime); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func additionEdges(topo *domain.Topology) map[string]bool {
	out := map[string]bool{}
	for id, res := range topo.Resources {
		for kind, targets := range res.Connections {
			for _, target := range targets {
				out[id+" -"+kind+"-> "+target] = true
			}
		}
	}
	return out
}

// additionDiff lists every resource, property set and edge that only one side has.
func additionDiff(incremental, cold *domain.Topology) []string {
	var diff []string
	describe := func(res domain.Resource) string {
		props, _ := json.Marshal(res.Properties)
		loc := res.Location
		return string(res.Kind) + " " + res.Name + " " + loc.Path + " " + string(props) +
			" @" + strings.Join([]string{itoa(loc.StartsAt), itoa(loc.EndsAt)}, "-")
	}
	for id, res := range incremental.Resources {
		other, ok := cold.Resources[id]
		switch {
		case !ok:
			diff = append(diff, "- resource "+id)
		case describe(res) != describe(other):
			diff = append(diff, "- resource "+id+": "+describe(res), "+ resource "+id+": "+describe(other))
		}
	}
	for id := range cold.Resources {
		if _, ok := incremental.Resources[id]; !ok {
			diff = append(diff, "+ resource "+id)
		}
	}
	inc, fresh := additionEdges(incremental), additionEdges(cold)
	for edge := range inc {
		if !fresh[edge] {
			diff = append(diff, "- edge "+edge)
		}
	}
	for edge := range fresh {
		if !inc[edge] {
			diff = append(diff, "+ edge "+edge)
		}
	}
	sort.Strings(diff)
	return diff
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
