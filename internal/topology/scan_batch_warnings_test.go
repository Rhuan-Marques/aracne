package topology_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// A warning describes the state a scan LEAVES, not the steps it took to get there.
//
// One scan can delete, add and rewrite several files. Judged step by step, those intermediate
// states are full of dangling references that the same scan resolves: a caller deleted in the file
// processed second is still alive while the first file's removal is judged, and a callee added in
// a new file does not exist yet while the first new file's calls are resolved. Judged by the
// result, the rule is one line: warn a caller that SURVIVES the scan and references something that
// does NOT exist after it. Everything else is noise the agent would have to read past.
//
//	file1.fun1 calls file2.fun2; fun2 gone, fun1 survives    -> warn
//	file1.fun1 calls file2.fun2; fun2 gone, fun1 gone too    -> nothing
//	fun1 added, calls fun2 that exists nowhere               -> warn
//	fun1 added, calls fun2 added in the same scan            -> nothing
//
// Every case below applies ALL of its changes, then runs ONE IncrementalScan, in every language
// the scanners support.

// batchLang writes the same logical project in one language: a callee module and a caller module,
// each a file of top-level functions.
type batchLang struct {
	name     string
	scaffold map[string]string
	// file returns the relative path of logical module m ("a", "b", "c").
	file func(m string) string
	// module renders module m declaring funcs, and calling calls[i] from funcs[i] when calls[i]
	// is non-empty. imports are the logical modules the calls come from.
	module func(m string, funcs, calls, imports []string) string
	// index is rewritten whenever the module set changes (Rust's lib.rs); nil when unneeded.
	index func(mods []string) map[string]string
	// callerNamed reports whether a warning source id is function fn of module m.
	callerNamed func(sourceID, m, fn string) bool
}

func batchLangs() []batchLang {
	lower := func(s string) string { return strings.ToLower(s) }
	return []batchLang{
		{
			name:     "go",
			scaffold: map[string]string{"go.mod": "module batchgo\n\ngo 1.21\n"},
			file:     func(m string) string { return m + ".go" },
			module: func(m string, funcs, calls, _ []string) string {
				var b strings.Builder
				b.WriteString("package batchgo\n\n")
				for i, f := range funcs {
					body := "1"
					if calls[i] != "" {
						body = calls[i] + "()"
					}
					fmt.Fprintf(&b, "func %s() int { return %s }\n\n", f, body)
				}
				return b.String()
			},
			callerNamed: func(id, _ string, fn string) bool { return strings.HasSuffix(id, "."+fn) },
		},
		{
			name: "python",
			file: func(m string) string { return m + ".py" },
			module: func(m string, funcs, calls, imports []string) string {
				var b strings.Builder
				for _, imp := range imports {
					var names []string
					for _, c := range calls {
						if c != "" {
							names = append(names, c)
						}
					}
					fmt.Fprintf(&b, "from %s import %s\n", imp, strings.Join(names, ", "))
				}
				b.WriteString("\n\n")
				for i, f := range funcs {
					body := "1"
					if calls[i] != "" {
						body = calls[i] + "()"
					}
					fmt.Fprintf(&b, "def %s():\n    return %s\n\n\n", f, body)
				}
				return b.String()
			},
			callerNamed: func(id, m, fn string) bool { return id == m+"."+fn },
		},
		{
			name:        "typescript",
			scaffold:    map[string]string{"package.json": `{ "name": "batchts", "version": "1.0.0" }` + "\n"},
			file:        func(m string) string { return m + ".ts" },
			module:      jsModule("'./%s'"),
			callerNamed: func(id, m, fn string) bool { return id == m+"."+fn },
		},
		{
			name:        "javascript",
			scaffold:    map[string]string{"package.json": `{ "name": "batchjs", "version": "1.0.0", "type": "module" }` + "\n"},
			file:        func(m string) string { return m + ".js" },
			module:      jsModule("'./%s.js'"),
			callerNamed: func(id, m, fn string) bool { return id == m+"."+fn },
		},
		{
			name:     "rust",
			scaffold: map[string]string{"Cargo.toml": "[package]\nname = \"batchrs\"\nversion = \"0.1.0\"\nedition = \"2021\"\n"},
			file:     func(m string) string { return "src/" + m + ".rs" },
			module: func(m string, funcs, calls, imports []string) string {
				var b strings.Builder
				for _, imp := range imports {
					for _, c := range calls {
						if c != "" {
							fmt.Fprintf(&b, "use crate::%s::%s;\n", imp, c)
						}
					}
				}
				for i, f := range funcs {
					body := "1"
					if calls[i] != "" {
						body = calls[i] + "()"
					}
					fmt.Fprintf(&b, "pub fn %s() -> i32 { %s }\n", f, body)
				}
				return b.String()
			},
			index: func(mods []string) map[string]string {
				var b strings.Builder
				for _, m := range mods {
					fmt.Fprintf(&b, "pub mod %s;\n", m)
				}
				return map[string]string{"src/lib.rs": b.String()}
			},
			callerNamed: func(id, m, fn string) bool { return id == "batchrs::"+m+"::"+fn },
		},
		{
			name: "java",
			scaffold: map[string]string{"pom.xml": `<?xml version="1.0" encoding="UTF-8"?>
<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.batch</groupId>
  <artifactId>batchjava</artifactId>
  <version>1.0.0</version>
</project>
`},
			file: func(m string) string { return "src/main/java/com/batch/" + strings.ToUpper(m) + ".java" },
			module: func(m string, funcs, calls, imports []string) string {
				var b strings.Builder
				fmt.Fprintf(&b, "package com.batch;\n\npublic class %s {\n", strings.ToUpper(m))
				for i, f := range funcs {
					body := "return 1;"
					if calls[i] != "" {
						owner := strings.ToUpper(m)
						if len(imports) > 0 {
							owner = strings.ToUpper(imports[0])
						}
						body = fmt.Sprintf("%s o = new %s();\n        return o.%s();", owner, owner, calls[i])
					}
					fmt.Fprintf(&b, "    public int %s() {\n        %s\n    }\n", f, body)
				}
				b.WriteString("}\n")
				return b.String()
			},
			callerNamed: func(id, m, fn string) bool {
				return strings.HasPrefix(lower(id), lower("com.batch."+strings.ToUpper(m)+"."+fn+"("))
			},
		},
	}
}

func jsModule(importFmt string) func(m string, funcs, calls, imports []string) string {
	return func(m string, funcs, calls, imports []string) string {
		var b strings.Builder
		for _, imp := range imports {
			var names []string
			for _, c := range calls {
				if c != "" {
					names = append(names, c)
				}
			}
			fmt.Fprintf(&b, "import { %s } from %s;\n", strings.Join(names, ", "), fmt.Sprintf(importFmt, imp))
		}
		for i, f := range funcs {
			body := "1"
			if calls[i] != "" {
				body = calls[i] + "()"
			}
			fmt.Fprintf(&b, "export function %s() { return %s; }\n", f, body)
		}
		return b.String()
	}
}

// modSpec is one logical module: its functions, what each calls, and the modules it imports.
type modSpec struct {
	funcs, calls, imports []string
}

// state is a whole project: logical module name -> spec.
type state map[string]modSpec

func (l batchLang) render(s state) map[string]string {
	out := map[string]string{}
	for k, v := range l.scaffold {
		out[k] = v
	}
	var mods []string
	for m, spec := range s {
		mods = append(mods, m)
		out[l.file(m)] = l.module(m, spec.funcs, spec.calls, spec.imports)
	}
	sort.Strings(mods)
	if l.index != nil {
		for k, v := range l.index(mods) {
			out[k] = v
		}
	}
	return out
}

type batchCase struct {
	name       string
	before     state
	after      state
	wantCaller string // "m.fn" that must be warned; "" means NO warning of any breakage kind
}

// Shorthands: a module of one function calling nothing, and of one caller calling into another.
func leaf(fns ...string) modSpec {
	return modSpec{funcs: fns, calls: make([]string, len(fns))}
}
func caller(fn, callee, from string) modSpec {
	s := modSpec{funcs: []string{fn, "keepCaller"}, calls: []string{callee, ""}}
	if from != "" {
		s.imports = []string{from}
	}
	return s
}

func batchCases() []batchCase {
	return []batchCase{
		// ---- removals --------------------------------------------------------------------
		{
			name:       "callee file deleted, caller survives",
			before:     state{"a": leaf("fun2"), "b": caller("fun1", "fun2", "a")},
			after:      state{"b": caller("fun1", "fun2", "a")},
			wantCaller: "b.fun1",
		},
		{
			name:       "callee removed by an edit, caller survives",
			before:     state{"a": leaf("fun2", "keepCallee"), "b": caller("fun1", "fun2", "a")},
			after:      state{"a": leaf("keepCallee"), "b": caller("fun1", "fun2", "a")},
			wantCaller: "b.fun1",
		},
		{
			name: "caller and callee in the same file, file deleted",
			before: state{"a": {funcs: []string{"fun1", "fun2"}, calls: []string{"fun2", ""}},
				"c": leaf("other")},
			after: state{"c": leaf("other")},
		},
		{
			name:   "caller file and callee file both deleted",
			before: state{"a": leaf("fun2"), "b": caller("fun1", "fun2", "a"), "c": leaf("other")},
			after:  state{"c": leaf("other")},
		},
		{
			name:   "callee file deleted, caller removed by an edit",
			before: state{"a": leaf("fun2"), "b": caller("fun1", "fun2", "a")},
			after:  state{"b": leaf("keepCaller")},
		},
		{
			name:   "callee removed by an edit, caller file deleted",
			before: state{"a": leaf("fun2", "keepCallee"), "b": caller("fun1", "fun2", "a")},
			after:  state{"a": leaf("keepCallee")},
		},
		{
			name:   "callee and caller both removed by edits",
			before: state{"a": leaf("fun2", "keepCallee"), "b": caller("fun1", "fun2", "a")},
			after:  state{"a": leaf("keepCallee"), "b": leaf("keepCaller")},
		},
		// ---- additions -------------------------------------------------------------------
		{
			name:       "new caller file calls a function that exists nowhere",
			before:     state{"a": leaf("keepCallee")},
			after:      state{"a": leaf("keepCallee"), "b": caller("fun1", "fun2", "a")},
			wantCaller: "b.fun1",
		},
		{
			name:   "new caller file and new callee file in the same scan",
			before: state{"c": leaf("other")},
			after:  state{"c": leaf("other"), "a": leaf("fun2"), "b": caller("fun1", "fun2", "a")},
		},
		{
			name:   "new file holding both caller and callee",
			before: state{"c": leaf("other")},
			after: state{"c": leaf("other"),
				"a": {funcs: []string{"fun1", "fun2"}, calls: []string{"fun2", ""}}},
		},
		{
			name:   "new caller file, callee added to an existing file in the same scan",
			before: state{"a": leaf("keepCallee")},
			after:  state{"a": leaf("keepCallee", "fun2"), "b": caller("fun1", "fun2", "a")},
		},
		{
			name:   "existing caller edited to call a callee added in a new file",
			before: state{"b": leaf("fun1", "keepCaller")},
			after:  state{"a": leaf("fun2"), "b": caller("fun1", "fun2", "a")},
		},
		{
			name:       "existing caller edited to call a function that exists nowhere",
			before:     state{"a": leaf("keepCallee"), "b": leaf("fun1", "keepCaller")},
			after:      state{"a": leaf("keepCallee"), "b": caller("fun1", "fun2", "a")},
			wantCaller: "b.fun1",
		},
	}
}

var breakageKinds = map[domain.WarningKind]bool{
	domain.WarnNodeRemoved:      true,
	domain.WarnUseMissingNode:   true,
	domain.WarnSignatureChanged: true,
}

func TestOneScanWarnsOnlyWhatItsResultLeavesBroken(t *testing.T) {
	for _, lang := range batchLangs() {
		for _, c := range batchCases() {
			t.Run(lang.name+"/"+c.name, func(t *testing.T) {
				dir := t.TempDir()
				before, after := lang.render(c.before), lang.render(c.after)
				additionWrite(t, dir, before, time.Time{})

				reg := contractRegistry()
				mgr := topology.New()
				mgr.Load(filepath.Join(dir, "topology.db"))
				if err := mgr.FullScan(dir, reg); err != nil {
					t.Fatalf("FullScan: %v", err)
				}
				if pre := breakage(t, mgr, nil); len(pre) != 0 {
					t.Fatalf("the starting project must be clean, got %v", pre)
				}

				// Every change of the case, then one scan.
				for rel := range before {
					if _, kept := after[rel]; !kept {
						if err := os.Remove(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
							t.Fatal(err)
						}
					}
				}
				changed := map[string]string{}
				for rel, content := range after {
					if before[rel] != content {
						changed[rel] = content
					}
				}
				additionWrite(t, dir, changed, time.Now().Add(3*time.Second))
				returned, err := mgr.IncrementalScan(dir, reg)
				if err != nil {
					t.Fatalf("IncrementalScan: %v", err)
				}
				got := breakage(t, mgr, returned)
				if os.Getenv("BATCH_DEBUG") != "" {
					stored, _ := mgr.ListWarnings("", "", "")
					for _, w := range returned {
						t.Logf("RETURNED [%s] src=%s tgt=%s :: %s", w.Kind, w.SourceID, w.TargetID, w.Message)
					}
					for _, w := range stored {
						t.Logf("STORED   [%s] src=%s tgt=%s :: %s", w.Kind, w.SourceID, w.TargetID, w.Message)
					}
				}

				if c.wantCaller == "" {
					if len(got) != 0 {
						t.Errorf("nothing is broken after this scan, but it warned:\n  %s", strings.Join(describe(got), "\n  "))
					}
					return
				}
				parts := strings.SplitN(c.wantCaller, ".", 2)
				callerFile := filepath.Join(dir, filepath.FromSlash(lang.file(parts[0])))
				found := false
				for _, w := range got {
					if lang.callerNamed(w.SourceID, parts[0], parts[1]) {
						found = true
					} else if sameFile(w.SourceID, callerFile) {
						// The caller's FILE importing a module that no longer exists is the
						// same breakage, stated at the import. Allowed, not required.
					} else {
						t.Errorf("warned something other than the broken caller: [%s] %s", w.Kind, w.Message)
					}
				}
				if !found {
					t.Errorf("%s is left calling something that does not exist, and was not warned (got %v)",
						c.wantCaller, describe(got))
				}
			})
		}
	}
}

func sameFile(id, path string) bool {
	return helper.CanonicalPath(id) == helper.CanonicalPath(path)
}

// breakage is every breakage warning the scan produced or left stored, de-duplicated by id.
func breakage(t *testing.T, mgr *topology.TopologyManager, returned []domain.TopologyWarning) []domain.TopologyWarning {
	t.Helper()
	stored, err := mgr.ListWarnings("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	var out []domain.TopologyWarning
	for _, w := range append(returned, stored...) {
		if !breakageKinds[w.Kind] || seen[w.ID] {
			continue
		}
		seen[w.ID] = true
		out = append(out, w)
	}
	return out
}

func describe(ws []domain.TopologyWarning) []string {
	var out []string
	for _, w := range ws {
		out = append(out, fmt.Sprintf("[%s] %s", w.Kind, w.Message))
	}
	return out
}

// A warning lives exactly as long as the breakage it describes, across scans:
//
//	scan 1: file1.fun1 calls file2.fun2, both exist          -> no warning
//	scan 2: fun2 removed, fun1 still calls it                -> warning on fun1
//	scan 3: fun1 deleted / fun1 stops calling / fun2 back    -> warning gone
//
// "Gone" means gone everywhere the agent could meet it: the warnings table and the report of that
// scan. Every language, and each way the breakage can end.
func TestWarningLifecycleAcrossScans(t *testing.T) {
	start := state{"a": leaf("fun2", "keepCallee"), "b": caller("fun1", "fun2", "a")}
	broken := state{"a": leaf("keepCallee"), "b": caller("fun1", "fun2", "a")}
	endings := []struct {
		name string
		end  state
	}{
		{"caller file deleted", state{"a": leaf("keepCallee"), "c": leaf("other")}},
		{"caller removed by an edit", state{"a": leaf("keepCallee"), "b": leaf("keepCaller")}},
		{"caller stops calling", state{"a": leaf("keepCallee"), "b": leaf("fun1", "keepCaller")}},
		{"callee restored", start},
	}
	for _, lang := range batchLangs() {
		for _, ending := range endings {
			t.Run(lang.name+"/"+ending.name, func(t *testing.T) {
				dir := t.TempDir()
				reg := contractRegistry()
				mgr := topology.New()
				mgr.Load(filepath.Join(dir, "topology.db"))

				current := lang.render(start)
				additionWrite(t, dir, current, time.Time{})
				if err := mgr.FullScan(dir, reg); err != nil {
					t.Fatalf("scan 1: %v", err)
				}
				if got := breakage(t, mgr, nil); len(got) != 0 {
					t.Fatalf("scan 1: nothing is broken, but: %v", describe(got))
				}

				step := func(n int, next state) []domain.TopologyWarning {
					t.Helper()
					target := lang.render(next)
					for rel := range current {
						if _, kept := target[rel]; !kept {
							if err := os.Remove(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
								t.Fatal(err)
							}
						}
					}
					changed := map[string]string{}
					for rel, content := range target {
						if current[rel] != content {
							changed[rel] = content
						}
					}
					additionWrite(t, dir, changed, time.Now().Add(time.Duration(n*3)*time.Second))
					current = target
					returned, err := mgr.IncrementalScan(dir, reg)
					if err != nil {
						t.Fatalf("scan %d: %v", n, err)
					}
					return breakage(t, mgr, returned)
				}

				got := step(2, broken)
				warned := false
				for _, w := range got {
					if lang.callerNamed(w.SourceID, "b", "fun1") {
						warned = true
					}
				}
				if !warned {
					t.Fatalf("scan 2: b.fun1 calls a removed function and was not warned (got %v)", describe(got))
				}

				if got := step(3, ending.end); len(got) != 0 {
					t.Errorf("scan 3 (%s): the breakage is over, but warnings remain:\n  %s",
						ending.name, strings.Join(describe(got), "\n  "))
				}
			})
		}
	}
}

// The missing-reference warning has the same lifecycle as a removal warning: it stands while the
// reference is broken and goes the moment it is not.
//
//	scan 2: fun1 calls fun2, which exists nowhere            -> warning on fun1
//	scan 3: fun2 added / fun1 deleted / fun1 stops calling   -> warning gone
func TestMissingReferenceLifecycleAcrossScans(t *testing.T) {
	clean := state{"a": leaf("keepCallee"), "b": leaf("fun1", "keepCaller")}
	broken := state{"a": leaf("keepCallee"), "b": caller("fun1", "fun2", "a")}
	endings := []struct {
		name string
		end  state
	}{
		{"callee added", state{"a": leaf("keepCallee", "fun2"), "b": caller("fun1", "fun2", "a")}},
		{"caller file deleted", state{"a": leaf("keepCallee"), "c": leaf("other")}},
		{"caller removed by an edit", state{"a": leaf("keepCallee"), "b": leaf("keepCaller")}},
		{"caller stops calling", clean},
	}
	for _, lang := range batchLangs() {
		for _, ending := range endings {
			t.Run(lang.name+"/"+ending.name, func(t *testing.T) {
				p := newBatchProject(t, lang, clean)
				got := p.step(2, broken)
				warned := false
				for _, w := range got {
					if w.Kind == domain.WarnUseMissingNode && lang.callerNamed(w.SourceID, "b", "fun1") {
						warned = true
					}
				}
				if !warned {
					t.Fatalf("scan 2: b.fun1 calls fun2, which exists nowhere, and was not warned (got %v)", describe(got))
				}
				if got := p.step(3, ending.end); len(got) != 0 {
					t.Errorf("scan 3 (%s): nothing is broken any more, but:\n  %s", ending.name, strings.Join(describe(got), "\n  "))
				}
			})
		}
	}
}

// batchProject is a scanned project that steps through states, one IncrementalScan per step.
type batchProject struct {
	t       *testing.T
	lang    batchLang
	dir     string
	mgr     *topology.TopologyManager
	current map[string]string
}

func newBatchProject(t *testing.T, lang batchLang, s state) *batchProject {
	t.Helper()
	dir := t.TempDir()
	p := &batchProject{t: t, lang: lang, dir: dir, mgr: topology.New(), current: lang.render(s)}
	additionWrite(t, dir, p.current, time.Time{})
	p.mgr.Load(filepath.Join(dir, "topology.db"))
	if err := p.mgr.FullScan(dir, contractRegistry()); err != nil {
		t.Fatalf("scan 1: %v", err)
	}
	if got := breakage(t, p.mgr, nil); len(got) != 0 {
		t.Fatalf("scan 1: nothing is broken, but: %v", describe(got))
	}
	return p
}

func (p *batchProject) step(n int, next state) []domain.TopologyWarning {
	p.t.Helper()
	target := p.lang.render(next)
	for rel := range p.current {
		if _, kept := target[rel]; !kept {
			if err := os.Remove(filepath.Join(p.dir, filepath.FromSlash(rel))); err != nil {
				p.t.Fatal(err)
			}
		}
	}
	changed := map[string]string{}
	for rel, content := range target {
		if p.current[rel] != content {
			changed[rel] = content
		}
	}
	additionWrite(p.t, p.dir, changed, time.Now().Add(time.Duration(n*3)*time.Second))
	p.current = target
	returned, err := p.mgr.IncrementalScan(p.dir, contractRegistry())
	if err != nil {
		p.t.Fatalf("scan %d: %v", n, err)
	}
	return breakage(p.t, p.mgr, returned)
}

// A reference this project cannot judge is never a warning: a builtin, a third-party package, a
// CommonJS export assigned at run time, a Java method inherited from the JDK. Each of these is the
// shape a careless implementation of the missing-reference rule gets wrong, and each would put a
// false "does not exist" in front of the agent after every edit that touches it.
func TestMissingReferenceIgnoresWhatTheProjectCannotSee(t *testing.T) {
	pom := `<project xmlns="http://maven.apache.org/POM/4.0.0"><modelVersion>4.0.0</modelVersion>` +
		`<groupId>com.ext</groupId><artifactId>ext</artifactId><version>1.0.0</version></project>` + "\n"
	cases := []struct {
		name  string
		files map[string]string
	}{
		{"python: stdlib, third-party and builtins", map[string]string{
			"a.py": "def keep():\n    return 1\n",
			"b.py": "import os\nfrom collections import OrderedDict\nimport requests\nfrom a import keep\n\n\n" +
				"def use():\n    print(len([keep()]))\n    requests.get('x')\n    return os.getcwd(), OrderedDict()\n",
		}},
		{"typescript: npm named, default and namespace imports", map[string]string{
			"package.json": `{ "name": "extts", "version": "1.0.0" }` + "\n",
			"a.ts":         "export function keep(): number { return 1; }\n",
			"b.ts": "import { debounce } from 'lodash';\nimport express from 'express';\nimport * as path from 'path';\n" +
				"import { keep } from './a';\nexport function use() { debounce(keep, 1); express(); return path.join('a'); }\n",
		}},
		{"typescript: exports the scanner does not model (destructuring, as in every Redux slice)", map[string]string{
			"package.json": `{ "name": "extts2", "version": "1.0.0" }` + "\n",
			"a.ts": "const slice = { actions: { loaded: (n: number) => n, cleared: () => 0 } };\n" +
				"export const { loaded, cleared } = slice.actions;\n",
			"b.ts": "import { loaded, cleared } from './a';\nimport * as a from './a';\n" +
				"export function use() { a.cleared(); return loaded(cleared()); }\n",
		}},
		{"javascript: CommonJS require with run-time exports", map[string]string{
			"package.json": `{ "name": "extjs", "version": "1.0.0" }` + "\n",
			"a.js":         "const handlers = {};\nhandlers.dynamic = () => 1;\nmodule.exports = handlers;\n",
			"b.js":         "const { dynamic } = require('./a');\nconst lib = require('./a');\nfunction use() { lib.other(); return dynamic(); }\nmodule.exports = { use };\n",
		}},
		{"javascript: default import of a module", map[string]string{
			"package.json": `{ "name": "extjs2", "version": "1.0.0", "type": "module" }` + "\n",
			"a.js":         "export default function () { return 1; }\n",
			"b.js":         "import build from './a.js';\nexport function use() { return build(); }\n",
		}},
		{"rust: std and external crates", map[string]string{
			"Cargo.toml": "[package]\nname = \"extrs\"\nversion = \"0.1.0\"\nedition = \"2021\"\n\n[dependencies]\nserde_json = \"1\"\n",
			"src/lib.rs": "pub mod a;\npub mod b;\n",
			"src/a.rs":   "pub fn keep() -> i32 { 1 }\n",
			"src/b.rs":   "use std::cmp::max;\nuse crate::a::keep;\npub fn use_it() -> i32 { let _ = serde_json::to_string(&1); max(keep(), std::cmp::min(1, 2)) }\n",
		}},
		{"java: JDK receivers, Object methods and inherited interface defaults", map[string]string{
			"pom.xml": pom,
			"src/main/java/com/ext/Bag.java": "package com.ext;\n\nimport java.util.Iterator;\nimport java.util.List;\n\n" +
				"public class Bag implements Iterable<String> {\n    private List<String> items;\n" +
				"    public Iterator<String> iterator() { return items.iterator(); }\n}\n",
			"src/main/java/com/ext/Base.java": "package com.ext;\n\nimport java.util.ArrayList;\n\npublic class Base extends ArrayList<String> {\n}\n",
			"src/main/java/com/ext/Use.java": "package com.ext;\n\nimport java.util.ArrayList;\n\npublic class Use {\n" +
				"    public String run() {\n        Bag b = new Bag();\n        b.forEach(s -> {});\n" +
				"        Base base = new Base();\n        base.add(\"x\");\n" +
				"        ArrayList<String> l = new ArrayList<>();\n        l.add(\"y\");\n        return b.toString() + l.size();\n    }\n}\n",
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			additionWrite(t, dir, c.files, time.Time{})
			mgr := topology.New()
			mgr.Load(filepath.Join(dir, "topology.db"))
			if err := mgr.FullScan(dir, contractRegistry()); err != nil {
				t.Fatalf("FullScan: %v", err)
			}
			if got := breakage(t, mgr, nil); len(got) != 0 {
				t.Errorf("cold scan warned about code outside the project:\n  %s", strings.Join(describe(got), "\n  "))
			}
			// And on the edit path: touch the caller so it is re-resolved incrementally.
			var callerRel string
			for rel := range c.files {
				if strings.Contains(rel, "b.") || strings.HasSuffix(rel, "Use.java") {
					callerRel = rel
				}
			}
			additionWrite(t, dir, map[string]string{callerRel: c.files[callerRel] + "\n"}, time.Now().Add(3*time.Second))
			returned, err := mgr.IncrementalScan(dir, contractRegistry())
			if err != nil {
				t.Fatalf("IncrementalScan: %v", err)
			}
			if got := breakage(t, mgr, returned); len(got) != 0 {
				t.Errorf("incremental scan warned about code outside the project:\n  %s", strings.Join(describe(got), "\n  "))
			}
		})
	}
}
