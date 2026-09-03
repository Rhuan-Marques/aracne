package topology_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aracne/internal/helper"
	"aracne/internal/topology"
	"aracne/internal/topology/domain"
	"aracne/internal/topology/scanner"
	"aracne/internal/topology/scanner/goscanner"
	"aracne/internal/topology/scanner/javascanner"
	"aracne/internal/topology/scanner/jsscanner"
	"aracne/internal/topology/scanner/pyscanner"
	"aracne/internal/topology/scanner/rustscanner"
)

// Cross-language cover for the breakage-warning channel.
//
// Two defects made this channel silent for five of six languages, and a whole
// 27-task benchmark run made 85 edits and got back the bare string "edit
// succeeded" every single time:
//
//   - TopologyManager.UpdateFile discarded updateFileWithScanner's return
//     value. Only goscanner writes into topo.Warnings directly, so every other
//     scanner's warnings were dropped on the edit path before any attribution
//     logic ran.
//   - The four non-Go scanners attributed node_removed to the symbol that had
//     just been deleted, so CleanupOrphanedWarnings removed the warning before
//     it could reach the database.
//
// Each case below removes a symbol that another FILE calls, and asserts the
// three properties that were broken: the warning reaches the caller of
// UpdateFile, it is attributed to the surviving referrer (not the corpse), and
// it persists into the warnings table. Restoring the symbol must clear it.

func crossLangRegistry() *scanner.Registry {
	reg := scanner.NewRegistry()
	reg.Register(goscanner.NewGoScanner())
	reg.Register(pyscanner.NewPythonScanner())
	reg.Register(jsscanner.NewJavaScriptScanner())
	reg.Register(jsscanner.NewTypeScriptScanner())
	reg.Register(rustscanner.NewRustScanner())
	reg.Register(javascanner.NewJavaScanner())
	return reg
}

type crossLangCase struct {
	lang string
	// files written before the first scan, relative path -> content
	files map[string]string
	// the file that gets edited, and the replacement that removes the symbol
	target  string
	oldText string
	newText string
	// expected attribution
	wantSource string // the surviving caller
	wantTarget string // the removed symbol
}

func (c crossLangCase) writeAll(t *testing.T, dir string) {
	t.Helper()
	for rel, content := range c.files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func (c crossLangCase) replaceInTarget(t *testing.T, dir, from, to string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(c.target))
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, from) {
		t.Fatalf("%s: %q not found in %s", c.lang, from, c.target)
	}
	if err := os.WriteFile(p, []byte(strings.Replace(s, from, to, 1)), 0644); err != nil {
		t.Fatal(err)
	}
}

func crossLangCases() []crossLangCase {
	return []crossLangCase{
		{
			lang: "javascript",
			files: map[string]string{
				"package.json": `{ "name": "xlangjs", "version": "1.0.0", "type": "module" }` + "\n",
				"a.js":         "export const arrowAdd = (x, y) => x + y;\n",
				"b.js":         "import { arrowAdd } from './a.js';\nexport function crossUser(a, b) { return arrowAdd(a, b); }\n",
			},
			target: "a.js", oldText: "arrowAdd", newText: "arrowSum",
			wantSource: "b.crossUser", wantTarget: "a.arrowAdd",
		},
		{
			lang: "typescript",
			files: map[string]string{
				"package.json": `{ "name": "xlangts", "version": "1.0.0" }` + "\n",
				"a.ts":         "export function calc(x: number): number { return x * 2; }\n",
				"b.ts":         "import { calc } from './a';\nexport function useCalc(n: number): number { return calc(n); }\n",
			},
			target: "a.ts", oldText: "function calc", newText: "function compute",
			wantSource: "b.useCalc", wantTarget: "a.calc",
		},
		{
			lang: "python",
			files: map[string]string{
				"a.py": "def add(x, y):\n    return x + y\n",
				"b.py": "from a import add\n\n\ndef use(x, y):\n    return add(x, y)\n",
			},
			target: "a.py", oldText: "def add", newText: "def summed",
			wantSource: "b.use", wantTarget: "a.add",
		},
		{
			lang: "rust",
			files: map[string]string{
				"Cargo.toml": "[package]\nname = \"xlangrs\"\nversion = \"0.1.0\"\nedition = \"2021\"\n",
				"src/lib.rs": "pub mod a;\npub mod b;\n",
				"src/a.rs":   "pub fn add(x: i32, y: i32) -> i32 { x + y }\n",
				"src/b.rs":   "use crate::a::add;\npub fn use_it(x: i32, y: i32) -> i32 { add(x, y) }\n",
			},
			target: "src/a.rs", oldText: "pub fn add", newText: "pub fn summed",
			wantSource: "xlangrs::b::use_it", wantTarget: "xlangrs::a::add",
		},
		{
			lang: "java",
			files: map[string]string{
				"pom.xml": `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.demo</groupId>
  <artifactId>xlangjava</artifactId>
  <version>1.0.0</version>
</project>
`,
				"src/main/java/com/demo/A.java": "package com.demo;\n\npublic class A {\n    public int add(int x, int y) { return x + y; }\n}\n",
				"src/main/java/com/demo/B.java": "package com.demo;\n\npublic class B {\n    public int use(int x, int y) {\n        A a = new A();\n        return a.add(x, y);\n    }\n}\n",
			},
			target: "src/main/java/com/demo/A.java", oldText: "int add(", newText: "int summed(",
			wantSource: "com.demo.B.use(int,int)", wantTarget: "com.demo.A.add(int,int)",
		},
	}
}

// TestCrossLanguageNodeRemovedWarning is the regression for "85 edits, zero
// warnings": every language must report a removed cross-file symbol, attribute
// it to the surviving caller, persist it, and clear it when the symbol returns.
func TestCrossLanguageNodeRemovedWarning(t *testing.T) {
	for _, c := range crossLangCases() {
		t.Run(c.lang, func(t *testing.T) {
			dir := t.TempDir()
			c.writeAll(t, dir)

			reg := crossLangRegistry()
			mgr := topology.New()
			mgr.Load(filepath.Join(dir, "topology.db"))
			if err := mgr.FullScan(dir, reg); err != nil {
				t.Fatalf("FullScan: %v", err)
			}
			if n := countWarns(t, mgr, ""); n != 0 {
				t.Fatalf("expected a clean graph before the edit, got %d warnings", n)
			}

			// remove the symbol the other file calls
			c.replaceInTarget(t, dir, c.oldText, c.newText)
			warnings, err := mgr.UpdateFile(filepath.Join(dir, filepath.FromSlash(c.target)), reg)
			if err != nil {
				t.Fatalf("UpdateFile: %v", err)
			}

			// 1. the warning reaches the caller of UpdateFile at all
			//    (it used to be discarded at manager.go's updateFileWithScanner call)
			var got *domain.TopologyWarning
			for i := range warnings {
				if warnings[i].Kind == domain.WarnNodeRemoved && warnings[i].TargetID == c.wantTarget {
					got = &warnings[i]
					break
				}
			}
			if got == nil {
				t.Fatalf("no node_removed warning for %s; UpdateFile returned %d warnings: %+v",
					c.wantTarget, len(warnings), warnings)
			}

			// 2. attributed to the surviving referrer, not to the deleted symbol
			if got.SourceID != c.wantSource {
				t.Errorf("SourceID = %q, want the surviving caller %q", got.SourceID, c.wantSource)
			}
			if got.TargetID == "" {
				t.Error("TargetID is empty: the agent cannot tell which symbol went missing")
			}
			if !strings.Contains(got.Message, c.wantSource) {
				t.Errorf("message should name the caller to check, got %q", got.Message)
			}

			// 3. it survived CleanupOrphanedWarnings and reached the database
			if n := countWarns(t, mgr, domain.WarnNodeRemoved); n != 1 {
				t.Errorf("persisted node_removed warnings = %d, want 1", n)
			}

			// 4. the dangling edge is gone from the referrer. Leaving it means
			//    the readers silently drop that neighbour from the CONTEXT
			//    block and the agent sees an incomplete context with no signal.
			if refs := callerRefs(t, mgr, c.wantSource); refs[c.wantTarget] {
				t.Errorf("%s still holds a dangling edge to the removed %s", c.wantSource, c.wantTarget)
			}

			// 5. restoring the symbol clears it
			c.replaceInTarget(t, dir, c.newText, c.oldText)
			if _, err := mgr.UpdateFile(filepath.Join(dir, filepath.FromSlash(c.target)), reg); err != nil {
				t.Fatalf("UpdateFile (restore): %v", err)
			}
			if n := countWarns(t, mgr, domain.WarnNodeRemoved); n != 0 {
				w, _ := mgr.ListWarnings("", "", domain.WarnNodeRemoved)
				t.Errorf("restoring the symbol should clear the warning, still have %d: %+v", n, w)
			}

			// 6. and the edge is back. The referrer's own file is never
			//    re-parsed here, so nothing but ResolveReferrerWarnings could
			//    have restored it; without that, stripping in step 4 would lose
			//    the edge until a cold `scan --hard`.
			if refs := callerRefs(t, mgr, c.wantSource); !refs[c.wantTarget] {
				t.Errorf("%s should reference %s again after the symbol was restored", c.wantSource, c.wantTarget)
			}
		})
	}
}

// callerRefs returns the set of ids the given resource references through any
// body-reference edge kind.
func callerRefs(t *testing.T, mgr *topology.TopologyManager, id string) map[string]bool {
	t.Helper()
	topo, err := mgr.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	res, ok := topo.Resources[id]
	if !ok {
		t.Fatalf("resource %q not in topology", id)
	}
	refs := map[string]bool{}
	for connType, targets := range res.Connections {
		if !helper.ReferenceConnTypes[connType] {
			continue
		}
		for _, tgt := range targets {
			refs[tgt] = true
		}
	}
	return refs
}

// TestCrossLanguageWarningsHaveNoSelfAttributedNoise pins the removal of the
// old self-attributed emitters. They named the just-deleted symbol as SourceID
// with an empty TargetID, were garbage collected before they could persist, and
// after the referrer pass landed they were pure duplicate noise in the string
// the agent reads back from `edit`.
func TestCrossLanguageWarningsHaveNoSelfAttributedNoise(t *testing.T) {
	for _, c := range crossLangCases() {
		t.Run(c.lang, func(t *testing.T) {
			dir := t.TempDir()
			c.writeAll(t, dir)

			reg := crossLangRegistry()
			mgr := topology.New()
			mgr.Load(filepath.Join(dir, "topology.db"))
			if err := mgr.FullScan(dir, reg); err != nil {
				t.Fatalf("FullScan: %v", err)
			}

			c.replaceInTarget(t, dir, c.oldText, c.newText)
			warnings, err := mgr.UpdateFile(filepath.Join(dir, filepath.FromSlash(c.target)), reg)
			if err != nil {
				t.Fatalf("UpdateFile: %v", err)
			}

			for _, w := range warnings {
				if w.Kind == domain.WarnNodeRemoved && w.TargetID == "" {
					t.Errorf("self-attributed node_removed warning survived: %+v", w)
				}
				if w.Kind == domain.WarnNodeRemoved && w.SourceID == c.wantTarget {
					t.Errorf("node_removed attributed to the removed symbol itself: %+v", w)
				}
			}
		})
	}
}

// TestReferrerWarningSkipsSameFileReferrer pins the SkipSource predicate: a
// caller in the very file being re-parsed is not warned about, because that
// file was just re-resolved from source (goscanner reports use_missing_node for
// it instead, and the other scanners simply drop the edge).
func TestReferrerWarningSkipsSameFileReferrer(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("package.json", `{ "name": "samefile", "version": "1.0.0", "type": "module" }`+"\n")
	write("a.js", "export const helper = (n) => n * 2;\nexport function local(n) { return helper(n); }\n")

	reg := crossLangRegistry()
	mgr := topology.New()
	mgr.Load(filepath.Join(dir, "topology.db"))
	if err := mgr.FullScan(dir, reg); err != nil {
		t.Fatalf("FullScan: %v", err)
	}

	// remove helper; its only caller lives in the same file
	write("a.js", "export function local(n) { return helper(n); }\n")
	warnings, err := mgr.UpdateFile(filepath.Join(dir, "a.js"), reg)
	if err != nil {
		t.Fatalf("UpdateFile: %v", err)
	}
	for _, w := range warnings {
		if w.Kind == domain.WarnNodeRemoved {
			t.Errorf("same-file referrer should not get a node_removed warning: %+v", w)
		}
	}
}

// TestReferrerWarningGoIDsUnchanged pins that Go keeps its own richer warning.
// goscanner emits the same src@kind@tgt id from its reverse-caller index and
// strips the stale edge, so the shared pass must neither duplicate it nor
// overwrite its message.
func TestReferrerWarningGoIDsUnchanged(t *testing.T) {
	p := newWarnProj(t)
	p.write(t, "target.go", `package testproject

func Add(a, b int) int { return a + b }
`)
	p.write(t, "caller.go", `package testproject

func Use() int { return Add(1, 2) }
`)
	mgr := p.scan(t)

	p.write(t, "target.go", `package testproject

func Sum(a, b int) int { return a + b }
`)
	warnings := p.updateFileWarnings(t, mgr, "target.go")

	var removed []domain.TopologyWarning
	for _, w := range warnings {
		if w.Kind == domain.WarnNodeRemoved {
			removed = append(removed, w)
		}
	}
	if len(removed) != 1 {
		t.Fatalf("expected exactly one node_removed warning (no shared-pass duplicate), got %d: %+v",
			len(removed), removed)
	}
	w := removed[0]
	if w.SourceID != "testproject.Use" || w.TargetID != "testproject.Add" {
		t.Errorf("attribution = (%q -> %q), want (testproject.Use -> testproject.Add)", w.SourceID, w.TargetID)
	}
	if want := "testproject.Use@node_removed@testproject.Add"; w.ID != want {
		t.Errorf("warning id = %q, want %q", w.ID, want)
	}
	// goscanner's message is the more specific of the two emitters and must win
	if !strings.Contains(w.Message, "which was removed from") {
		t.Errorf("goscanner's message should take precedence, got %q", w.Message)
	}
}

// TestReferrerWarningGoExtVarRemoved is a capability Go never had: its
// node_removed sweep covers functions, structs and named types only, so a
// removed package-level func var broke its callers silently. The shared pass
// works off the graph, so it covers every reference edge kind.
func TestReferrerWarningGoExtVarRemoved(t *testing.T) {
	p := newWarnProj(t)
	p.write(t, "hooks.go", `package testproject

var Format = func(s string) string { return s }
`)
	p.write(t, "caller.go", `package testproject

func Use(s string) string { return Format(s) }
`)
	mgr := p.scan(t)

	p.write(t, "hooks.go", `package testproject

var Other = func(s string) string { return s }
`)
	warnings := p.updateFileWarnings(t, mgr, "hooks.go")

	found := false
	for _, w := range warnings {
		if w.Kind == domain.WarnNodeRemoved && w.TargetID == "testproject.Format" {
			found = true
			if w.SourceID != "testproject.Use" {
				t.Errorf("SourceID = %q, want testproject.Use", w.SourceID)
			}
		}
	}
	if !found {
		t.Fatalf("removing a func-typed package var must warn its callers, got %+v", warnings)
	}
}

// TestPartialIncrementalReportsOnlyNewWarnings pins the delta fix for the Go
// fast path.
//
// UpdateFilePartial returns the scanner's whole warnings map — the partial
// working set is seeded with every row of the warnings table — so the fast path
// re-reported the entire backlog on every scan despite its comment claiming it
// surfaced "only newly-added" ones. An agent editing a repo with an existing
// warning got it handed back after every unrelated edit.
func TestPartialIncrementalReportsOnlyNewWarnings(t *testing.T) {
	p := newWarnProj(t)
	p.write(t, "target.go", `package testproject

func Add(a, b int) int { return a + b }
`)
	p.write(t, "caller.go", `package testproject

func Use() int { return Add(1, 2) }
`)
	p.write(t, "unrelated.go", `package testproject

func Untouched() int { return 7 }
`)
	mgr := p.scan(t)

	// create a standing warning by removing Add
	p.writeForIncrementalScan(t, "target.go", `package testproject

func Sum(a, b int) int { return a + b }
`)
	if got := p.incrementalScan(t, mgr); len(got) == 0 {
		t.Fatal("expected the removal to warn")
	}
	if n := countWarns(t, mgr, domain.WarnNodeRemoved); n == 0 {
		t.Fatal("expected the warning to persist")
	}

	// now make a completely unrelated edit; the standing warning must not be
	// reported again
	p.writeForIncrementalScan(t, "unrelated.go", `package testproject

func Untouched() int { return 8 }
`)
	again := p.incrementalScan(t, mgr)
	for _, w := range again {
		if w.Kind == domain.WarnNodeRemoved {
			t.Errorf("an unrelated edit re-reported a standing warning: %+v", w)
		}
	}
}

// --- signature_changed, cross-language ---
//
// The same defect that made node_removed useless for five of six languages was
// left in place for signature_changed: the four non-Go scanners attribute it to
// the symbol whose signature moved and set no TargetID at all. Nothing can
// clear that shape -- the symbol still exists so CleanupOrphanedWarnings keeps
// it, and with no caller named neither ClearReferrerWarningsForFile nor
// goscanner's clearReanalyzedFunctionWarnings has anything to key on. Fixing
// every caller left the row in the database forever, and so did reverting the
// signature. helper.ExpandSignatureWarnings fans the warning out to its callers
// before it is ever persisted, which is both the actionable form and the only
// one the clearers can reach.

type crossLangSigCase struct {
	lang string
	// files written before the first scan, relative path -> content
	files map[string]string
	// the file whose function signature changes
	target  string
	oldText string
	newText string
	// the file holding the caller, and the edit that fixes the call site
	caller    string
	callerOld string
	callerNew string
	// expected attribution: the changed symbol, and the caller to go verify
	wantSource string
	wantTarget string
	// stableID is false for Java alone: its method ids encode the signature, so
	// widening one is an identity change. Which kind of warning that produces
	// depends on when the caller is re-resolved -- UpdateFile leaves the caller
	// pointing at the vanished old id and the referrer pass reports node_removed,
	// while IncrementalScan re-resolves the caller onto the new id and the
	// signature warning survives. Both name the caller and both clear, which is
	// the whole contract; only the kind differs, so Java asserts the contract and
	// not the kind.
	stableID bool
}

// warningKinds returns the kinds this case may legitimately raise. A stable id
// means the caller keeps pointing at the same symbol across the edit, so the
// change is only ever a signature change.
func (c crossLangSigCase) warningKinds() []domain.WarningKind {
	if c.stableID {
		return []domain.WarningKind{domain.WarnSignatureChanged}
	}
	return []domain.WarningKind{domain.WarnSignatureChanged, domain.WarnNodeRemoved}
}

// namedCaller returns the id the warning tells an agent to go check. The two
// kinds point opposite ways round: signature_changed keeps the changed symbol in
// SourceID and names the caller in TargetID, node_removed is attributed to the
// caller itself.
func namedCaller(w domain.TopologyWarning) string {
	if w.Kind == domain.WarnNodeRemoved {
		return w.SourceID
	}
	return w.TargetID
}

func crossLangSigCases() []crossLangSigCase {
	return []crossLangSigCase{
		{
			lang: "javascript",
			files: map[string]string{
				"package.json": `{ "name": "sigjs", "version": "1.0.0", "type": "module" }` + "\n",
				"a.js":         "export const arrowAdd = (x, y) => x + y;\n",
				"b.js":         "import { arrowAdd } from './a.js';\nexport function crossUser(a, b) { return arrowAdd(a, b); }\n",
			},
			target: "a.js", oldText: "(x, y) => x + y", newText: "(x, y, z) => x + y + z",
			caller: "b.js", callerOld: "arrowAdd(a, b)", callerNew: "arrowAdd(a, b, 0)",
			wantSource: "a.arrowAdd", wantTarget: "b.crossUser",
			stableID: true,
		},
		{
			lang: "typescript",
			files: map[string]string{
				"package.json": `{ "name": "sigts", "version": "1.0.0" }` + "\n",
				"a.ts":         "export function calc(x: number): number { return x * 2; }\n",
				"b.ts":         "import { calc } from './a';\nexport function useCalc(n: number): number { return calc(n); }\n",
			},
			target: "a.ts", oldText: "calc(x: number)", newText: "calc(x: number, k: number)",
			caller: "b.ts", callerOld: "calc(n)", callerNew: "calc(n, 1)",
			wantSource: "a.calc", wantTarget: "b.useCalc",
			stableID: true,
		},
		{
			lang: "python",
			files: map[string]string{
				"a.py": "def add(x, y):\n    return x + y\n",
				"b.py": "from a import add\n\n\ndef use(x, y):\n    return add(x, y)\n",
			},
			target: "a.py", oldText: "def add(x, y):", newText: "def add(x, y, z):",
			caller: "b.py", callerOld: "return add(x, y)", callerNew: "return add(x, y, 0)",
			wantSource: "a.add", wantTarget: "b.use",
			stableID: true,
		},
		{
			lang: "rust",
			files: map[string]string{
				"Cargo.toml": "[package]\nname = \"sigrs\"\nversion = \"0.1.0\"\nedition = \"2021\"\n",
				"src/lib.rs": "pub mod a;\npub mod b;\n",
				"src/a.rs":   "pub fn add(x: i32, y: i32) -> i32 { x + y }\n",
				"src/b.rs":   "use crate::a::add;\npub fn use_it(x: i32, y: i32) -> i32 { add(x, y) }\n",
			},
			target: "src/a.rs", oldText: "add(x: i32, y: i32)", newText: "add(x: i32, y: i32, z: i32)",
			caller: "src/b.rs", callerOld: "add(x, y) }", callerNew: "add(x, y, 0) }",
			wantSource: "sigrs::a::add", wantTarget: "sigrs::b::use_it",
			stableID: true,
		},
		{
			lang: "java",
			files: map[string]string{
				"pom.xml": `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.demo</groupId>
  <artifactId>sigjava</artifactId>
  <version>1.0.0</version>
</project>
`,
				"src/main/java/com/demo/A.java": "package com.demo;\n\npublic class A {\n    public int add(int x, int y) { return x + y; }\n}\n",
				"src/main/java/com/demo/B.java": "package com.demo;\n\npublic class B {\n    public int use(int x, int y) {\n        A a = new A();\n        return a.add(x, y);\n    }\n}\n",
			},
			target: "src/main/java/com/demo/A.java", oldText: "int add(int x, int y)", newText: "int add(int x, int y, int z)",
			caller: "src/main/java/com/demo/B.java", callerOld: "a.add(x, y)", callerNew: "a.add(x, y, 0)",
			wantSource: "com.demo.A.add(int,int)", wantTarget: "com.demo.B.use(int,int)",
			stableID: false,
		},
	}
}

func (c crossLangSigCase) writeAll(t *testing.T, dir string) {
	t.Helper()
	for rel, content := range c.files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

// replace rewrites rel, swapping the first occurrence of from for to, and pushes
// the mtime forward so DiffScanFiles sees the change even when the manifest was
// stamped in the same second.
func (c crossLangSigCase) replace(t *testing.T, dir, rel, from, to string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, from) {
		t.Fatalf("%s: %q not found in %s", c.lang, from, rel)
	}
	if err := os.WriteFile(p, []byte(strings.Replace(s, from, to, 1)), 0644); err != nil {
		t.Fatal(err)
	}
	mtime := time.Now().Add(3 * time.Second)
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

// TestCrossLanguageSignatureChangedClearsWhenCallerIsFixed is the regression for
// "the warning never goes away": changing a signature must name the caller that
// needs verifying, and fixing that caller must clear the row from the database.
// Go already behaved this way (TestIncrementalScanClearsSignatureChangedWhenCallerIsEdited);
// before ExpandSignatureWarnings the other five languages never cleared at all.
//
// Both write paths are covered: UpdateFile is what the MCP edit/write tools call,
// IncrementalScan is what `arac scan` and the watcher call.
func TestCrossLanguageSignatureChangedClearsWhenCallerIsFixed(t *testing.T) {
	paths := []struct {
		name  string
		apply func(t *testing.T, mgr *topology.TopologyManager, reg *scanner.Registry, dir, rel string) []domain.TopologyWarning
	}{
		{
			name: "UpdateFile",
			apply: func(t *testing.T, mgr *topology.TopologyManager, reg *scanner.Registry, dir, rel string) []domain.TopologyWarning {
				t.Helper()
				w, err := mgr.UpdateFile(filepath.Join(dir, filepath.FromSlash(rel)), reg)
				if err != nil {
					t.Fatalf("UpdateFile(%s): %v", rel, err)
				}
				return w
			},
		},
		{
			name: "IncrementalScan",
			apply: func(t *testing.T, mgr *topology.TopologyManager, reg *scanner.Registry, dir, _ string) []domain.TopologyWarning {
				t.Helper()
				w, err := mgr.IncrementalScan(dir, reg)
				if err != nil {
					t.Fatalf("IncrementalScan: %v", err)
				}
				return w
			},
		},
	}

	for _, path := range paths {
		t.Run(path.name, func(t *testing.T) {
			for _, c := range crossLangSigCases() {
				t.Run(c.lang, func(t *testing.T) {
					dir := t.TempDir()
					c.writeAll(t, dir)

					reg := crossLangRegistry()
					mgr := topology.New()
					mgr.Load(filepath.Join(dir, "topology.db"))
					if err := mgr.FullScan(dir, reg); err != nil {
						t.Fatalf("FullScan: %v", err)
					}
					if n := countWarns(t, mgr, ""); n != 0 {
						t.Fatalf("expected a clean graph before the edit, got %d warnings", n)
					}

					// widen the signature the other file calls
					c.replace(t, dir, c.target, c.oldText, c.newText)
					warnings := path.apply(t, mgr, reg, dir, c.target)

					// 1. the warning reaches the caller of the update at all
					var got *domain.TopologyWarning
					for i := range warnings {
						for _, k := range c.warningKinds() {
							if warnings[i].Kind == k {
								got = &warnings[i]
								break
							}
						}
						if got != nil {
							break
						}
					}
					if got == nil {
						t.Fatalf("no %v warning; the update returned %d warnings: %+v",
							c.warningKinds(), len(warnings), warnings)
					}

					// 2. it names a caller to go verify, not just the symbol that
					//    changed. This is the whole fix: a warning with no caller in
					//    it is both unactionable and unclearable.
					if caller := namedCaller(*got); caller != c.wantTarget {
						t.Errorf("warning names %q as the code to check, want the caller %q (%+v)",
							caller, c.wantTarget, *got)
					}
					if !strings.Contains(got.Message, c.wantTarget) {
						t.Errorf("message should name the caller to check, got %q", got.Message)
					}
					if got.Kind == domain.WarnSignatureChanged && c.stableID && got.SourceID != c.wantSource {
						t.Errorf("SourceID = %q, want the changed symbol %q", got.SourceID, c.wantSource)
					}

					// 3. it survived the cleanup passes and reached the database
					if n := countWarns(t, mgr, got.Kind); n != 1 {
						w, _ := mgr.ListWarnings("", "", got.Kind)
						t.Errorf("persisted %s warnings = %d, want 1: %+v", got.Kind, n, w)
					}

					// 4. fixing the caller clears it. Before the fix this stayed
					//    in the warnings table forever for every non-Go language.
					c.replace(t, dir, c.caller, c.callerOld, c.callerNew)
					path.apply(t, mgr, reg, dir, c.caller)

					if n := countWarns(t, mgr, ""); n != 0 {
						w, _ := mgr.ListWarnings("", "", "")
						t.Errorf("fixing the caller should clear the warning, still have %d: %+v", n, w)
					}
				})
			}
		})
	}
}

// TestCrossLanguageSignatureChangedClearsWhenCallerIsDeleted covers the other
// way a signature warning stops being actionable: the caller it points at is
// gone, so there is nothing left to verify. CleanupOrphanedWarnings only tested
// SourceID, which for signature_changed is the symbol that changed and is still
// very much present.
func TestCrossLanguageSignatureChangedClearsWhenCallerIsDeleted(t *testing.T) {
	for _, c := range crossLangSigCases() {
		if !c.stableID {
			continue
		}
		t.Run(c.lang, func(t *testing.T) {
			dir := t.TempDir()
			c.writeAll(t, dir)

			reg := crossLangRegistry()
			mgr := topology.New()
			mgr.Load(filepath.Join(dir, "topology.db"))
			if err := mgr.FullScan(dir, reg); err != nil {
				t.Fatalf("FullScan: %v", err)
			}

			c.replace(t, dir, c.target, c.oldText, c.newText)
			if _, err := mgr.UpdateFile(filepath.Join(dir, filepath.FromSlash(c.target)), reg); err != nil {
				t.Fatalf("UpdateFile: %v", err)
			}
			if n := countWarns(t, mgr, domain.WarnSignatureChanged); n != 1 {
				t.Fatalf("expected 1 signature_changed before deleting the caller, got %d", n)
			}

			// delete the caller outright
			callerPath := filepath.Join(dir, filepath.FromSlash(c.caller))
			if err := os.Remove(callerPath); err != nil {
				t.Fatal(err)
			}
			if _, err := mgr.UpdateFile(callerPath, reg); err != nil {
				t.Fatalf("UpdateFile (delete caller): %v", err)
			}

			if n := countWarns(t, mgr, domain.WarnSignatureChanged); n != 0 {
				w, _ := mgr.ListWarnings("", "", domain.WarnSignatureChanged)
				t.Errorf("removing the caller should clear the warning, still have %d: %+v", n, w)
			}
		})
	}
}

// TestPartialIncrementalDropsWarningsItOrphans pins the scoped fast path's own
// orphan sweep. WriteScopedResources truncates and rewrites the whole warnings
// table from the map the scanner hands back, and that map is seeded with every
// row in the table, so a warning the delta just orphaned is re-inserted and
// outlives the code it points at. goscanner's resolveWarnings covers the
// node_removed case but not use_missing_node, and the whole-graph
// CleanupOrphanedWarnings cannot run on a working set -- an id absent from a
// working set only means "not loaded".
func TestPartialIncrementalDropsWarningsItOrphans(t *testing.T) {
	p := newWarnProj(t)
	p.write(t, "caller.go", `package testproject

func Broken() int { return NoSuchFunc() }

func Untouched() int { return 7 }
`)
	mgr := p.scan(t)
	if n := countWarns(t, mgr, domain.WarnUseMissingNode); n == 0 {
		t.Fatal("expected use_missing_node for the undefined call")
	}

	// Delete Broken. Nothing outside this file references it, so the edit stays
	// on the scoped fast path -- the only path that rewrites the warnings table
	// without a whole-graph orphan sweep.
	before := topology.PartialIncrementalCount()
	p.writeForIncrementalScan(t, "caller.go", `package testproject

func Untouched() int { return 7 }
`)
	p.incrementalScan(t, mgr)

	// Asserted for whichever path ran: the two must agree, and the full path's
	// CleanupOrphanedWarnings has always done this. The skip below only reports
	// that the scoped sweep itself went unexercised.
	if n := countWarns(t, mgr, ""); n != 0 {
		w, _ := mgr.ListWarnings("", "", "")
		t.Errorf("%d warning(s) left pointing at deleted code: %+v", n, w)
	}
	if topology.PartialIncrementalCount() == before {
		t.Skip("the edit fell back to the full path (ARAC_NO_PARTIAL?), so the scoped orphan sweep was not exercised")
	}
}

// writeFile overwrites the case's target file with arbitrary content.
func (c crossLangCase) writeFile(t *testing.T, dir, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(c.target))
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
