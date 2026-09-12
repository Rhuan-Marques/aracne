package topology_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner"
)

// Deleting a function that another file calls is the most breaking edit there is, and on the
// default channel -- the incremental scan the guard runs around every tool call -- it reported
// nothing in five languages of six. The scan re-parses the caller's file, every scanner but
// goscanner drops a reference it can no longer resolve, and the referrer sweep then skipped
// every re-parsed file. Only `arac update-file`, which re-parses the edited file alone, warned.

// liveProj is a project driven the way an agent session drives it: a cold scan, then edits on
// disk picked up by the incremental scan.
type liveProj struct {
	t   *testing.T
	dir string
	reg *scanner.Registry
	mgr *topology.TopologyManager
}

func newLiveProj(t *testing.T, files map[string]string) *liveProj {
	t.Helper()
	p := &liveProj{t: t, dir: t.TempDir(), reg: contractRegistry(), mgr: topology.New()}
	for rel, content := range files {
		p.put(rel, content, time.Time{})
	}
	p.mgr.Load(filepath.Join(p.dir, "topology.db"))
	if err := p.mgr.FullScan(p.dir, p.reg); err != nil {
		t.Fatalf("FullScan: %v", err)
	}
	return p
}

func (p *liveProj) put(rel, content string, mtime time.Time) {
	p.t.Helper()
	path := filepath.Join(p.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		p.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		p.t.Fatal(err)
	}
	if !mtime.IsZero() {
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			p.t.Fatal(err)
		}
	}
}

// edit rewrites one file and runs the incremental scan, returning what the scan reported.
// Each edit is stamped later than the last so the manifest diff always sees it.
func (p *liveProj) edit(rel, content string) []domain.TopologyWarning {
	p.t.Helper()
	p.put(rel, content, time.Now().Add(3*time.Second))
	ws, err := p.mgr.IncrementalScan(p.dir, p.reg)
	if err != nil {
		p.t.Fatalf("IncrementalScan after editing %s: %v", rel, err)
	}
	time.Sleep(10 * time.Millisecond)
	return ws
}

// stored returns the warnings table rows of one kind that name this source and target.
func (p *liveProj) stored(kind domain.WarningKind, source, target string) []domain.TopologyWarning {
	p.t.Helper()
	ws, err := p.mgr.ListWarnings(source, target, kind)
	if err != nil {
		p.t.Fatal(err)
	}
	return ws
}

func reports(ws []domain.TopologyWarning, kind domain.WarningKind, source, target string) bool {
	for _, w := range ws {
		if w.Kind == kind && w.SourceID == source && w.TargetID == target {
			return true
		}
	}
	return false
}

type removalCase struct {
	name       string
	files      map[string]string
	calleeFile string
	without    string // the callee's file with the function deleted
	caller     string // the resource that calls it
	removed    string // the deleted function
}

func removalCases() []removalCase {
	return []removalCase{
		{
			name: "python",
			files: map[string]string{
				"lib.py": "def helper(a):\n    return a\n\n\ndef keep():\n    return 0\n",
				"app.py": "from lib import helper\n\n\ndef main():\n    return helper(1)\n",
			},
			calleeFile: "lib.py",
			without:    "def keep():\n    return 0\n",
			caller:     "app.main",
			removed:    "lib.helper",
		},
		{
			name: "javascript",
			files: map[string]string{
				"package.json": `{"name":"x","type":"module"}`,
				"lib.js":       "export function helper(a) { return a; }\nexport function keep() { return 0; }\n",
				"app.js":       "import { helper } from './lib.js';\nexport function main() { return helper(1); }\n",
			},
			calleeFile: "lib.js",
			without:    "export function keep() { return 0; }\n",
			caller:     "app.main",
			removed:    "lib.helper",
		},
		{
			name: "typescript",
			files: map[string]string{
				"tsconfig.json": "{}",
				"lib.ts":        "export function helper(a: number): number { return a; }\nexport function keep(): number { return 0; }\n",
				"app.ts":        "import { helper } from './lib';\nexport function main(): number { return helper(1); }\n",
			},
			calleeFile: "lib.ts",
			without:    "export function keep(): number { return 0; }\n",
			caller:     "app.main",
			removed:    "lib.helper",
		},
		{
			name: "rust",
			files: map[string]string{
				"Cargo.toml": additionCargo,
				"src/lib.rs": "pub mod a;\npub mod b;\n",
				"src/a.rs":   "pub fn helper(a: i32) -> i32 { a }\npub fn keep() -> i32 { 0 }\n",
				"src/b.rs":   "use crate::a::helper;\npub fn main_fn() -> i32 { helper(1) }\n",
			},
			calleeFile: "src/a.rs",
			without:    "pub fn keep() -> i32 { 0 }\n",
			caller:     "q::b::main_fn",
			removed:    "q::a::helper",
		},
		{
			name: "java",
			files: map[string]string{
				"pom.xml": additionPom,
				"src/main/java/com/ex/Lib.java": "package com.ex;\n\npublic class Lib {\n" +
					"    public static int helper(int a) { return a; }\n    public static int keep() { return 0; }\n}\n",
				"src/main/java/com/ex/App.java": "package com.ex;\n\npublic class App {\n" +
					"    public int run() { return Lib.helper(1); }\n}\n",
			},
			calleeFile: "src/main/java/com/ex/Lib.java",
			without:    "package com.ex;\n\npublic class Lib {\n    public static int keep() { return 0; }\n}\n",
			caller:     "com.ex.App.run()",
			removed:    "com.ex.Lib.helper(int)",
		},
		{
			// The control: goscanner always warned, through its own resolver.
			name: "go",
			files: map[string]string{
				"go.mod":     "module ex\n\ngo 1.21\n",
				"lib/lib.go": "package lib\n\nfunc Helper(a int) int { return a }\n\nfunc Keep() int { return 0 }\n",
				"main.go":    "package main\n\nimport \"ex/lib\"\n\nfunc Use() int { return lib.Helper(1) }\n\nfunc main() {}\n",
			},
			calleeFile: "lib/lib.go",
			without:    "package lib\n\nfunc Keep() int { return 0 }\n",
			caller:     "ex.Use",
			removed:    "ex/lib.Helper",
		},
	}
}

func TestDeletingACalledFunctionWarnsItsCallerOnTheScanChannel(t *testing.T) {
	for _, c := range removalCases() {
		t.Run(c.name, func(t *testing.T) {
			p := newLiveProj(t, c.files)

			reported := p.edit(c.calleeFile, c.without)
			if !reports(reported, domain.WarnNodeRemoved, c.caller, c.removed) {
				t.Errorf("the scan did not report that %s lost %s; reported %+v", c.caller, c.removed, reported)
			}
			if len(p.stored(domain.WarnNodeRemoved, c.caller, c.removed)) == 0 {
				all, _ := p.mgr.ListWarnings("", "", "")
				t.Fatalf("no stored node_removed for %s -> %s; table: %+v", c.caller, c.removed, all)
			}

			// Putting the function back answers the warning.
			p.edit(c.calleeFile, c.files[c.calleeFile])
			if left := p.stored(domain.WarnNodeRemoved, c.caller, c.removed); len(left) != 0 {
				t.Errorf("restoring %s must clear the warning, %d left: %+v", c.removed, len(left), left)
			}
		})
	}
}

// The resolver connects a call it can no longer match exactly to a surviving overload, so the
// re-parsed caller still has a `calls` edge -- to a method the call never meant. Only the
// pre-update graph remembers which overload it did mean.
func TestDeletingAJavaOverloadWarnsTheCallTheResolverReLinked(t *testing.T) {
	p := newLiveProj(t, map[string]string{
		"pom.xml": additionPom,
		"src/main/java/com/ex/Util.java": "package com.ex;\n\npublic class Util {\n" +
			"    public static int add(int a) { return a; }\n" +
			"    public static int add(int a, int b) { return a + b; }\n}\n",
		"src/main/java/com/ex/App.java": "package com.ex;\n\npublic class App {\n" +
			"    public int run() { return Util.add(1) + Util.add(1, 2); }\n}\n",
	})
	p.edit("src/main/java/com/ex/Util.java", "package com.ex;\n\npublic class Util {\n"+
		"    public static int add(int a, int b) { return a + b; }\n}\n")
	if len(p.stored(domain.WarnNodeRemoved, "com.ex.App.run()", "com.ex.Util.add(int)")) == 0 {
		all, _ := p.mgr.ListWarnings("", "", "")
		t.Fatalf("removing add(int) must warn App.run(), whose add(1) no longer compiles; table: %+v", all)
	}
}

// A file re-parsed only because something it depends on changed is not a file its author
// edited, so it is not evidence that the warnings pointing at it were answered. It used to be
// treated as one: every re-resolved file had its referrer warnings cleared, and a scanner that
// silently drops an unresolvable reference has nothing to put back.
func TestRemovalWarningOutlivesAReResolveTheCallerDidNotAskFor(t *testing.T) {
	p := newLiveProj(t, map[string]string{
		"lib.py": "def helper(a):\n    return a\n",
		"app.py": "from lib import helper, later\n\n\ndef main():\n    return helper(1)\n\n\n" +
			"def other():\n    return later()\n",
	})
	p.edit("lib.py", "def keep():\n    return 0\n")
	if len(p.stored(domain.WarnNodeRemoved, "app.main", "lib.helper")) == 0 {
		t.Fatal("removing helper must warn app.main")
	}

	// Writing `later` re-resolves app.py, which names it -- and app.main still calls the
	// function that is gone.
	p.edit("lib.py", "def keep():\n    return 0\n\n\ndef later():\n    return 1\n")
	topo, err := p.mgr.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if !additionEdges(topo)["app.other -calls-> lib.later"] {
		t.Fatal("app.py was not re-resolved against the new function, so this proves nothing")
	}
	if len(p.stored(domain.WarnNodeRemoved, "app.main", "lib.helper")) == 0 {
		t.Error("re-resolving app.py for an unrelated addition cleared a warning that is still true")
	}
}
