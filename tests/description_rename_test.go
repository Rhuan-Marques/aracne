package tests_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
)

// DESCRIPTIONS ARE KEYED BY ID, NOT BY FILE — in every language.
//
// Every scanner preserves descriptions across a re-parse, and every one of them keys the
// carry-over on the FILE: goscanner builds its old-symbol map from `oldFile.Functions()`, and
// pyscanner, jsscanner, rustscanner and javascanner all have the same shape. That is right for
// an edit and blind to a rename — the file being registered is brand new, so the map is empty
// and the descriptions sitting on the identical ids under the old path are never consulted.
// Renaming `pkg/a.go` to `pkg/b.go` returned every symbol in it with an empty description.
//
// It is the expensive half of a codebase that loses out. A declaration with a doc comment is
// re-described from source on every parse and never notices; a declaration WITHOUT one is
// exactly what an `arac descriptions generate` sweep paid a model to write, and it came back
// blank with nothing to say anything had been dropped. So every fixture below describes a
// symbol that carries no doc comment.
//
// WHAT THE FIX CAN AND CANNOT REACH. TopologyManager.restoreDescriptions refills a blank
// description from the pre-update snapshot by ID, so it holds wherever the id survives the
// rename — and the id schemes differ:
//
//	Go      module/package.Symbol        path-independent inside a package  → survives
//	Java    FQN + signature              from the `package` declaration     → survives
//	Python  module.symbol                minted from the filename           → new id
//	JS/TS   module.symbol                minted from the filename           → new id
//	Rust    crate::module::symbol        module IS the filename             → new id
//
// The four modules-first languages need the identity remap (helper.MatchDescriptions), which
// today only FullReScan reaches. That is deliberately NOT asserted as "the description is
// lost": the invariant below is stated as "a description follows a surviving id", which is
// vacuously true for them today and starts protecting them the moment the remap lands.
// idSurvivesRename is what pins the id schemes themselves, so a change to one fails loudly here
// rather than silently widening or narrowing what the fix covers.

// langDescCase is one language's fixture plus the two mutations that matter.
type langDescCase struct {
	name  string
	files map[string]string
	// describeID and describeKind name an UNDOCUMENTED declaration to hang a description on.
	describeID   string
	describeKind string
	// idSurvivesRename records whether this language's ids are path-independent, which is what
	// decides whether an id-keyed carry-over can reach the rename at all.
	idSurvivesRename bool
	// edit changes the file without moving it: the case every scanner already handles.
	edit func(t *testing.T, dir string)
	// rename moves the file within its own directory.
	rename func(t *testing.T, dir string)
}

func langDescCases() []langDescCase {
	return []langDescCase{
		{
			name: "go",
			files: map[string]string{
				"go.mod": "module example.com/g\n\ngo 1.21\n",
				"pkg/a.go": `package pkg

func Alpha() int { return 1 }

func Beta() int { return Alpha() }
`,
			},
			describeID:       "example.com/g/pkg.Alpha",
			describeKind:     "function",
			idSurvivesRename: true,
			edit: func(t *testing.T, dir string) {
				writeFileMk(t, dir, "pkg/a.go", `package pkg

func Alpha() int { return 1 }

func Beta() int { return Alpha() }

func Gamma() int { return 3 }
`)
			},
			rename: func(t *testing.T, dir string) {
				mustRename(t, filepath.Join(dir, "pkg", "a.go"), filepath.Join(dir, "pkg", "b.go"))
			},
		},
		{
			name: "java",
			files: map[string]string{
				"pom.xml": "<project></project>\n",
				"src/com/example/A.java": `package com.example;

public class A {
    public int alpha() { return 1; }
    public int beta() { return alpha(); }
}
`,
			},
			describeID:   "com.example.A.alpha()",
			describeKind: "method",
			// A Java id is the FQN from the `package` DECLARATION plus the signature, so it
			// does not move with the file. That also covers the realistic restructuring case
			// -- src/ to src/main/java/ -- which keeps the declaration and changes the path.
			idSurvivesRename: true,
			edit: func(t *testing.T, dir string) {
				writeFileMk(t, dir, "src/com/example/A.java", `package com.example;

public class A {
    public int alpha() { return 1; }
    public int beta() { return alpha(); }
    public int gamma() { return 3; }
}
`)
			},
			rename: func(t *testing.T, dir string) {
				mustRename(t,
					filepath.Join(dir, "src", "com", "example", "A.java"),
					filepath.Join(dir, "src", "com", "example", "B.java"))
			},
		},
		{
			name: "python",
			files: map[string]string{
				"mod_a.py": "def alpha():\n    return 1\n\ndef beta():\n    return alpha()\n",
			},
			describeID:       "mod_a.alpha",
			describeKind:     "function",
			idSurvivesRename: false, // modules-first: mod_a.alpha becomes mod_b.alpha
			edit: func(t *testing.T, dir string) {
				writeFileMk(t, dir, "mod_a.py",
					"def alpha():\n    return 1\n\ndef beta():\n    return alpha()\n\ndef gamma():\n    return 3\n")
			},
			rename: func(t *testing.T, dir string) {
				mustRename(t, filepath.Join(dir, "mod_a.py"), filepath.Join(dir, "mod_b.py"))
			},
		},
		{
			name: "javascript",
			files: map[string]string{
				"package.json": "{\"name\":\"j\",\"version\":\"1.0.0\"}\n",
				"a.js":         "export function alpha() { return 1 }\n\nexport function beta() { return alpha() }\n",
			},
			describeID:       "a.alpha",
			describeKind:     "function",
			idSurvivesRename: false,
			edit: func(t *testing.T, dir string) {
				writeFileMk(t, dir, "a.js",
					"export function alpha() { return 1 }\n\nexport function beta() { return alpha() }\n\nexport function gamma() { return 3 }\n")
			},
			rename: func(t *testing.T, dir string) {
				mustRename(t, filepath.Join(dir, "a.js"), filepath.Join(dir, "b.js"))
			},
		},
		{
			name: "typescript",
			files: map[string]string{
				"tsconfig.json": "{\"compilerOptions\":{}}\n",
				"a.ts":          "export function alpha(): number { return 1 }\n\nexport function beta(): number { return alpha() }\n",
			},
			describeID:       "a.alpha",
			describeKind:     "function",
			idSurvivesRename: false,
			edit: func(t *testing.T, dir string) {
				writeFileMk(t, dir, "a.ts",
					"export function alpha(): number { return 1 }\n\nexport function beta(): number { return alpha() }\n\nexport function gamma(): number { return 3 }\n")
			},
			rename: func(t *testing.T, dir string) {
				mustRename(t, filepath.Join(dir, "a.ts"), filepath.Join(dir, "b.ts"))
			},
		},
		{
			name: "rust",
			files: map[string]string{
				"Cargo.toml": "[package]\nname = \"r\"\nversion = \"0.1.0\"\nedition = \"2021\"\n",
				"src/lib.rs": "pub mod a;\n",
				"src/a.rs":   "pub fn alpha() -> i32 { 1 }\n\npub fn beta() -> i32 { alpha() }\n",
			},
			describeID:       "r::a::alpha",
			describeKind:     "function",
			idSurvivesRename: false, // the module path IS the filename
			edit: func(t *testing.T, dir string) {
				writeFileMk(t, dir, "src/a.rs",
					"pub fn alpha() -> i32 { 1 }\n\npub fn beta() -> i32 { alpha() }\n\npub fn gamma() -> i32 { 3 }\n")
			},
			rename: func(t *testing.T, dir string) {
				mustRename(t, filepath.Join(dir, "src", "a.rs"), filepath.Join(dir, "src", "b.rs"))
				// A Rust module rename is never just a rename: the parent has to declare it.
				writeFileMk(t, dir, "src/lib.rs", "pub mod b;\n")
			},
		},
	}
}

// TestDescriptionsFollowTheirIDAcrossARename is the invariant, stated once for every language:
// a description belongs to the ID it was written for, and a file rename does not take it away.
func TestDescriptionsFollowTheirIDAcrossARename(t *testing.T) {
	for _, tc := range langDescCases() {
		t.Run(tc.name, func(t *testing.T) {
			dir := describedFixture(t, tc)
			before := descriptionsOf(t, dir)

			tc.rename(t, dir)
			mustRun(t, dir, "scan")
			after := descriptionsOf(t, dir)

			// THE INVARIANT. Every id that came through the rename keeps what it was told.
			// Non-vacuous for the languages whose ids are path-independent, and the assertion
			// that will start covering the others the moment the identity remap lands.
			for id, want := range before {
				if want == "" {
					continue
				}
				got, survived := after[id]
				if !survived {
					continue // the id itself is gone; that is the remap's problem, not this one
				}
				if got != want {
					t.Errorf("%s: description of %q did not survive the rename: got %q, want %q",
						tc.name, id, got, want)
				}
			}

			// And pin the id scheme, so a change to one is a loud failure here rather than a
			// silent change in what the carry-over can reach.
			_, idSurvived := after[tc.describeID]
			if idSurvived != tc.idSurvivesRename {
				t.Fatalf("%s: id %q survived the rename = %v, want %v — the id scheme changed, so "+
					"which languages the id-keyed carry-over covers has changed too.\nafter: %v",
					tc.name, tc.describeID, idSurvived, tc.idSurvivesRename, keysOf(after))
			}
			if tc.idSurvivesRename && after[tc.describeID] != describedText {
				t.Errorf("%s: %q kept its id but lost its description (%q) — the file-keyed "+
					"carry-over in the scanner cannot see across a rename, and "+
					"TopologyManager.restoreDescriptions is what covers it",
					tc.name, tc.describeID, after[tc.describeID])
			}
		})
	}
}

// TestDescriptionsSurviveAnOrdinaryEdit pins the case each scanner already handles, in every
// language. It is the other half of the invariant: restoreDescriptions must not be the only
// thing holding it up, and it must not have broken it.
func TestDescriptionsSurviveAnOrdinaryEdit(t *testing.T) {
	for _, tc := range langDescCases() {
		t.Run(tc.name, func(t *testing.T) {
			dir := describedFixture(t, tc)

			tc.edit(t, dir)
			mustRun(t, dir, "scan")

			after := descriptionsOf(t, dir)
			if got := after[tc.describeID]; got != describedText {
				t.Errorf("%s: an ordinary edit dropped the description of %q: got %q, want %q\nafter: %v",
					tc.name, tc.describeID, got, describedText, keysOf(after))
			}
		})
	}
}

// TestDescriptionsSurviveARenameThroughUpdateFile covers the other entrance to the same rule.
//
// A rename reaches `arac update-file` as TWO calls -- one registering the new path, one
// removing the old -- which is how the Claude Code update-file hook and OpenCode's edit-sync
// plugin see it. That path has its own snapshot and its own removal, so the carry-over has to
// hold there as well or the graph depends on which surface noticed the edit.
func TestDescriptionsSurviveARenameThroughUpdateFile(t *testing.T) {
	tc := langDescCases()[0] // go: the id-stable case, so the assertion has something to check
	dir := describedFixture(t, tc)

	mustRename(t, filepath.Join(dir, "pkg", "a.go"), filepath.Join(dir, "pkg", "b.go"))
	mustRun(t, dir, "update-file", "pkg/b.go") // the addition
	mustRun(t, dir, "update-file", "pkg/a.go") // the removal

	after := descriptionsOf(t, dir)
	if got := after[tc.describeID]; got != describedText {
		t.Errorf("update-file lost the description of %q: got %q, want %q\nafter: %v",
			tc.describeID, got, describedText, keysOf(after))
	}
	// And the declarations themselves are still there — the ownership guard in
	// helper.RemoveFileResources, exercised through the hook path rather than through a scan.
	if _, ok := after[tc.describeID]; !ok {
		t.Errorf("update-file removed %q along with its old file", tc.describeID)
	}
}

// describedText is what every fixture's description is set to. Distinctive enough that a stray
// doc comment could not produce it by accident.
const describedText = "Set by the test, not harvested from a doc comment."

// describedFixture lays a language's files down, scans, and hangs a description on the named
// undocumented declaration. It skips the whole case when the language produced no topology at
// all -- pyscanner needs a python3 on PATH, and the tree-sitter scanners need the CGO build --
// so a partial toolchain reports "skipped" rather than a misleading failure.
func describedFixture(t *testing.T, tc langDescCase) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range tc.files {
		writeFileMk(t, dir, rel, content)
	}
	mustRun(t, dir, "scan", "--all")

	if _, ok := descriptionsOf(t, dir)[tc.describeID]; !ok {
		t.Skipf("%s: %q was not indexed — no scanner for this language in this build",
			tc.name, tc.describeID)
	}
	mustRun(t, dir, "update-description", tc.describeID, tc.describeKind, describedText)

	if got := descriptionsOf(t, dir)[tc.describeID]; got != describedText {
		t.Fatalf("%s: fixture setup failed, %q reads %q", tc.name, tc.describeID, got)
	}
	return dir
}

// descriptionsOf reads every declaration's description out of the project's own database.
// Files and packages are excluded: a file description is harvested from the source header and
// says nothing about whether an authored one survived.
func descriptionsOf(t *testing.T, dir string) map[string]string {
	t.Helper()
	topo, err := helper.ReadDb(filepath.Join(dir, ".aracne", "topology.db"))
	if err != nil {
		t.Fatalf("read topology: %v", err)
	}
	out := map[string]string{}
	for id, res := range topo.Resources {
		switch res.Kind {
		case "file", "package", "dependency":
			continue
		}
		out[id] = res.Description
	}
	return out
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func mustRename(t *testing.T, from, to string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(to), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(from, to); err != nil {
		t.Fatal(err)
	}
}
