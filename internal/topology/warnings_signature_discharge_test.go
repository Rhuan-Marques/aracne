package topology_test

import (
	"path/filepath"
	"testing"

	"aracne/internal/topology"
	"aracne/internal/topology/domain"
	"aracne/internal/topology/scanner"
	"aracne/internal/topology/scanner/goscanner"
	"aracne/internal/topology/scanner/jsscanner"
	"aracne/internal/topology/scanner/pyscanner"
)

// Regression cover for a signature_changed warning that could never be
// withdrawn from the definition's side.
//
// The warning had exactly two exits, and both ran through the CALLER:
// re-parsing the caller's file (ClearReferrerWarningsForFile, keyed on
// TargetID) or the caller disappearing (CleanupOrphanedWarnings). Undoing the
// change itself was not an exit, because nothing on disk remembered what the
// warning had been raised against -- the database holds the current signature
// and each scan overwrites the previous one. So putting a definition back the
// way it was left one row per caller in the table forever, and only `arac scan
// --all`, which rebuilds the table from nothing, cleared them.
//
// Measured on grafana/k6 before the fix: adding a parameter to js/common.Throw
// raised 102 warnings, one per caller, correctly. Restoring the file
// byte-for-byte and re-running `arac update-file` left all 102 in place. That
// is the shape an agent mid-refactor produces constantly -- try an approach,
// back it out -- and the pile it leaves is indistinguishable from real damage.

const sigTargetOneArg = `package testproject
func FuncA(x int) int { return x }
`

const sigTargetTwoArgs = `package testproject
func FuncA(x int, y int) int { return x + y }
`

const sigTargetThreeArgs = `package testproject
func FuncA(x int, y int, z int) int { return x + y + z }
`

const sigCaller = `package testproject
func Caller() int { return FuncA(1) }
`

func sigWarns(t *testing.T, mgr *topology.TopologyManager) int {
	t.Helper()
	return countWarns(t, mgr, domain.WarnSignatureChanged)
}

// newSigProj scans a two-file project whose caller lives in a different file
// from the definition, which is what makes goscanner attribute the warning to a
// caller at all.
func newSigProj(t *testing.T) (*warnProj, *topology.TopologyManager) {
	t.Helper()
	p := newWarnProj(t)
	p.write(t, "target.go", sigTargetOneArg)
	p.write(t, "caller.go", sigCaller)
	mgr := p.scan(t)
	if n := countWarns(t, mgr, ""); n != 0 {
		t.Fatalf("expected a clean graph after the first scan, got %d warnings", n)
	}
	return p, mgr
}

// TestSignatureWarningClearsWhenTheChangeIsReverted is the k6 case in miniature.
func TestSignatureWarningClearsWhenTheChangeIsReverted(t *testing.T) {
	p, mgr := newSigProj(t)

	p.write(t, "target.go", sigTargetTwoArgs)
	got := p.updateFileWarnings(t, mgr, "target.go")
	if n := sigWarns(t, mgr); n != 1 {
		t.Fatalf("changing the signature should warn the caller, got %d warnings", n)
	}
	var raised *domain.TopologyWarning
	for i := range got {
		if got[i].Kind == domain.WarnSignatureChanged {
			raised = &got[i]
		}
	}
	if raised == nil {
		t.Fatalf("UpdateFile did not report the signature change: %+v", got)
	}
	if raised.Baseline == "" {
		t.Fatal("the warning carries no baseline, so nothing can ever discharge it")
	}

	// Byte-for-byte the file it started as.
	p.write(t, "target.go", sigTargetOneArg)
	after := p.updateFileWarnings(t, mgr, "target.go")

	if n := sigWarns(t, mgr); n != 0 {
		w, _ := mgr.ListWarnings("", "", domain.WarnSignatureChanged)
		t.Errorf("reverting the signature should clear the warning, %d left: %+v", n, w)
	}
	// The revert re-raises before it discharges; reporting that would print the
	// pile the command had just cleared.
	for _, w := range after {
		if w.Kind == domain.WarnSignatureChanged {
			t.Errorf("the reverting update reported a signature warning it had just cleared: %+v", w)
		}
	}
}

// TestSignatureWarningSurvivesFurtherEditsAndClearsOnlyAtTheOriginal pins the
// baseline to what the CALLERS were written against, not to whatever the
// previous edit left behind. A->B->C must still warn; C->A must clear.
func TestSignatureWarningSurvivesFurtherEditsAndClearsOnlyAtTheOriginal(t *testing.T) {
	p, mgr := newSigProj(t)

	p.write(t, "target.go", sigTargetTwoArgs)
	p.updateFileWarnings(t, mgr, "target.go")
	if n := sigWarns(t, mgr); n != 1 {
		t.Fatalf("A->B should warn, got %d", n)
	}

	p.write(t, "target.go", sigTargetThreeArgs)
	p.updateFileWarnings(t, mgr, "target.go")
	if n := sigWarns(t, mgr); n != 1 {
		t.Fatalf("B->C leaves the caller written against A, so the warning must stand; got %d", n)
	}

	p.write(t, "target.go", sigTargetOneArg)
	p.updateFileWarnings(t, mgr, "target.go")
	if n := sigWarns(t, mgr); n != 0 {
		w, _ := mgr.ListWarnings("", "", domain.WarnSignatureChanged)
		t.Errorf("C->A returns to what the caller expects and must clear, %d left: %+v", n, w)
	}
}

// TestSignatureWarningStandsWhileTheSignatureIsStillChanged is the other half:
// the discharge must not become a way for any later edit to silence a warning
// that is still true.
func TestSignatureWarningStandsWhileTheSignatureIsStillChanged(t *testing.T) {
	p, mgr := newSigProj(t)

	p.write(t, "target.go", sigTargetTwoArgs)
	p.updateFileWarnings(t, mgr, "target.go")

	// An unrelated edit to the same file: a new function, signature untouched.
	p.write(t, "target.go", sigTargetTwoArgs+`
func Unrelated() int { return 7 }
`)
	p.updateFileWarnings(t, mgr, "target.go")
	if n := sigWarns(t, mgr); n != 1 {
		t.Errorf("an unrelated edit must not discharge a live warning, got %d", n)
	}

	// A different signature is not the original one.
	p.write(t, "target.go", sigTargetThreeArgs)
	p.updateFileWarnings(t, mgr, "target.go")
	if n := sigWarns(t, mgr); n != 1 {
		t.Errorf("a different signature is not the caller's signature, got %d", n)
	}
}

// TestFixingTheCallerStillClearsTheSignatureWarning guards the exit that always
// worked. The definition-side discharge is an addition, not a replacement.
func TestFixingTheCallerStillClearsTheSignatureWarning(t *testing.T) {
	p, mgr := newSigProj(t)

	p.write(t, "target.go", sigTargetTwoArgs)
	p.updateFileWarnings(t, mgr, "target.go")
	if n := sigWarns(t, mgr); n != 1 {
		t.Fatalf("expected the caller to be warned, got %d", n)
	}

	p.write(t, "caller.go", `package testproject
func Caller() int { return FuncA(1, 2) }
`)
	p.updateFileWarnings(t, mgr, "caller.go")
	if n := sigWarns(t, mgr); n != 0 {
		w, _ := mgr.ListWarnings("", "", domain.WarnSignatureChanged)
		t.Errorf("re-parsing the caller should clear its warning, %d left: %+v", n, w)
	}
}

// TestSignatureWarningRevertClearsOnTheBatchPath covers IncrementalScan, which
// is a separate merge site from UpdateFile and stamps its baselines separately.
func TestSignatureWarningRevertClearsOnTheBatchPath(t *testing.T) {
	p, mgr := newSigProj(t)

	p.writeForIncrementalScan(t, "target.go", sigTargetTwoArgs)
	p.incrementalScan(t, mgr)
	if n := sigWarns(t, mgr); n != 1 {
		t.Fatalf("the batch path should warn on a signature change, got %d", n)
	}

	p.writeForIncrementalScan(t, "target.go", sigTargetOneArg)
	after := p.incrementalScan(t, mgr)
	if n := sigWarns(t, mgr); n != 0 {
		w, _ := mgr.ListWarnings("", "", domain.WarnSignatureChanged)
		t.Errorf("the batch path must discharge a reverted signature too, %d left: %+v", n, w)
	}
	for _, w := range after {
		if w.Kind == domain.WarnSignatureChanged {
			t.Errorf("the reverting scan reported a warning it had just cleared: %+v", w)
		}
	}
}

// --- the discharge is language-agnostic -------------------------------------
//
// The baseline is read from Properties["input"]/["output"], which every scanner
// fills, so one rule covers every language rather than five near-copies in five
// scanners. Go is covered above through goscanner's caller-attributed shape;
// these two go through the other shape -- a self-attributed warning that
// ExpandSignatureWarnings fans out to callers -- which is the path the four
// non-Go scanners take.

type sigLangCase struct {
	lang       string
	files      map[string]string
	target     string
	changed    string // the target file, with the signature changed
	wantSource string
}

func sigLangCases() []sigLangCase {
	return []sigLangCase{
		{
			lang: "javascript",
			files: map[string]string{
				"package.json": `{ "name": "sigjs", "version": "1.0.0", "type": "module" }` + "\n",
				"a.js":         "export function add(x) { return x; }\n",
				"b.js":         "import { add } from './a.js';\nexport function useIt(n) { return add(n); }\n",
			},
			target:     "a.js",
			changed:    "export function add(x, y) { return x + y; }\n",
			wantSource: "a.add",
		},
		{
			lang: "python",
			files: map[string]string{
				"a.py": "def add(x: int) -> int:\n    return x\n",
				"b.py": "from a import add\n\n\ndef use(x: int) -> int:\n    return add(x)\n",
			},
			target:     "a.py",
			changed:    "def add(x: int, y: int) -> int:\n    return x + y\n",
			wantSource: "a.add",
		},
	}
}

func TestSignatureWarningRevertClearsInEveryLanguage(t *testing.T) {
	for _, c := range sigLangCases() {
		t.Run(c.lang, func(t *testing.T) {
			reg := scanner.NewRegistry()
			reg.Register(goscanner.NewGoScanner())
			reg.Register(pyscanner.NewPythonScanner())
			reg.Register(jsscanner.NewJavaScriptScanner())
			reg.Register(jsscanner.NewTypeScriptScanner())

			dir := t.TempDir()
			crossLangCase{files: c.files}.writeAll(t, dir)

			mgr := topology.New()
			mgr.Load(filepath.Join(dir, "topology.db"))
			if err := mgr.FullScan(dir, reg); err != nil {
				t.Fatalf("FullScan: %v", err)
			}
			if n := countWarns(t, mgr, ""); n != 0 {
				t.Fatalf("expected a clean graph, got %d warnings", n)
			}

			targetPath := filepath.Join(dir, filepath.FromSlash(c.target))
			original := c.files[c.target]

			writeAndUpdate := func(content string) {
				t.Helper()
				crossLangCase{target: c.target}.writeFile(t, dir, content)
				if _, err := mgr.UpdateFile(targetPath, reg); err != nil {
					t.Fatalf("UpdateFile: %v", err)
				}
			}

			writeAndUpdate(c.changed)
			if n := sigWarns(t, mgr); n == 0 {
				t.Fatalf("%s: changing the signature raised no warning", c.lang)
			}

			writeAndUpdate(original)
			if n := sigWarns(t, mgr); n != 0 {
				w, _ := mgr.ListWarnings("", "", domain.WarnSignatureChanged)
				t.Errorf("%s: reverting should clear the warning, %d left: %+v", c.lang, n, w)
			}
		})
	}
}
