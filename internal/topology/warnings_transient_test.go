package topology_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner/goscanner"
)

// The transient ("unverified") report, end to end.
//
// WHAT THESE PIN, and why the unit tables in internal/topology/contract are not enough: the
// verdict is only half the mechanism. The other half is that an Unverified verdict WITHDRAWS
// the stored warning and emits a report in its place -- so a test that only asserts a Verdict
// cannot tell the intended behaviour from the bug, where the warning was withdrawn and
// nothing was said. Every case below therefore checks both channels: what `arac warnings
// list` holds afterwards, and what the edit reported.
//
// The fixture is built around WHAT THE GO SCANNER CAN NAME, because that is the whole
// variable. It records a literal and a forwarded parameter, and nothing else -- so the four
// callers below are one of each readable form and two of the unreadable ones, which is the
// real distribution this feature exists for.

const transientCallee = `package lib

func Target(s string) bool { return s != "" }
`

const transientCallers = `package lib

type Cfg struct{ Name string }

func compute() string { return "x" }

// Readable: a forwarded parameter carries its declared type exactly.
func CallerParam(s string) bool { return Target(s) }

// Readable: a literal is recorded as its untyped class.
func CallerLiteral() bool { return Target("literal") }

// Unreadable: a local assigned from a call needs inference the scanner does not do.
func CallerLocal() bool {
	t := compute()
	return Target(t)
}

// Unreadable: a field selection, likewise.
func CallerField(c Cfg) bool { return Target(c.Name) }
`

type transientProj struct {
	t   *testing.T
	dir string
	reg *scanner.Registry
	mgr *topology.TopologyManager
}

func newTransientProj(t *testing.T) *transientProj {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":     "module example.com/app\n\ngo 1.21\n",
		"callee.go":  transientCallee,
		"callers.go": transientCallers,
	}
	for rel, content := range files {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	reg := scanner.NewRegistry()
	reg.Register(goscanner.NewGoScanner())
	mgr := topology.New()
	mgr.Load(filepath.Join(dir, "topology.db"))
	if err := mgr.FullScan(dir, reg); err != nil {
		t.Fatalf("FullScan: %v", err)
	}
	return &transientProj{t: t, dir: dir, reg: reg, mgr: mgr}
}

// retype rewrites the callee with a new parameter list and returns what the edit REPORTED.
func (p *transientProj) retype(params string) []domain.TopologyWarning {
	p.t.Helper()
	body := "package lib\n\nfunc Target(" + params + ") bool { return true }\n"
	path := filepath.Join(p.dir, "callee.go")
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		p.t.Fatal(err)
	}
	mtime := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(path, mtime, mtime)
	reported, err := p.mgr.UpdateFile(path, p.reg)
	if err != nil {
		p.t.Fatalf("UpdateFile: %v", err)
	}
	return reported
}

// stored is what survives in the warnings table -- the callers named by a signature_changed
// row. This is what `arac warnings list` prints.
func (p *transientProj) stored() []string {
	p.t.Helper()
	all, err := p.mgr.ListWarnings("", "", domain.WarnSignatureChanged)
	if err != nil {
		p.t.Fatal(err)
	}
	var out []string
	for _, w := range all {
		if w.Transient {
			p.t.Fatalf("a transient warning reached the database: %s", w.ID)
		}
		out = append(out, shortCaller(w.TargetID))
	}
	sort.Strings(out)
	return out
}

func shortCaller(id string) string {
	if i := strings.LastIndex(id, "."); i >= 0 {
		return id[i+1:]
	}
	return id
}

// split separates a reported batch into the callers told "this is broken" and the callers
// told "this might be broken, nobody could check".
func split(ws []domain.TopologyWarning) (persistent, transient []string) {
	for _, w := range ws {
		if w.Kind != domain.WarnSignatureChanged {
			continue
		}
		if w.Transient {
			transient = append(transient, shortCaller(w.TargetID))
			continue
		}
		persistent = append(persistent, shortCaller(w.TargetID))
	}
	sort.Strings(persistent)
	sort.Strings(transient)
	return persistent, transient
}

func eq(t *testing.T, label string, got, want []string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("%s: got [%s], want [%s]", label, strings.Join(got, ","), strings.Join(want, ","))
	}
}

// TestWideningToAnyWarnsNobody is the false-POSITIVE guard, and the one the user asked for by
// name. Every existing caller still fits `any`, so neither channel may say anything -- not
// even about the two callers whose arguments cannot be read, because what they pass cannot
// contradict a parameter that accepts everything.
func TestWideningToAnyWarnsNobody(t *testing.T) {
	p := newTransientProj(t)
	persistent, transient := split(p.retype("s any"))
	eq(t, "persistent", persistent, nil)
	eq(t, "transient", transient, nil)
	eq(t, "stored", p.stored(), nil)
}

// TestNarrowingFromAnyJudgesEachCallerOnWhatItPasses is the other half of the user's rule.
// Going back to `string` can break a caller, so each one is judged on its own argument: the
// two readable ones demonstrably still fit and stay silent, the two unreadable ones cannot be
// checked and are reported as unverified WITHOUT being written down.
func TestNarrowingFromAnyJudgesEachCallerOnWhatItPasses(t *testing.T) {
	p := newTransientProj(t)
	p.retype("s any") // widen first, so the callers are now written against `any`
	persistent, transient := split(p.retype("s string"))
	// Both readable callers demonstrably pass a string, so neither is told anything.
	eq(t, "persistent", persistent, nil)
	// Neither unreadable caller can be checked, so both are reported and neither is stored.
	eq(t, "transient", transient, []string{"CallerField", "CallerLocal"})
	eq(t, "stored", p.stored(), nil)
}

// TestRetypeReportsTheUnreadableCallersAndStoresNothingForThem is the original bug, stated as
// a test. `string -> []byte` breaks all four callers. The two readable ones are provable and
// are written down; the two unreadable ones are reported and are NOT.
//
// Before this change the last two produced nothing at all, in either channel -- which an agent
// reads as "your change is safe".
func TestRetypeReportsTheUnreadableCallersAndStoresNothingForThem(t *testing.T) {
	p := newTransientProj(t)
	persistent, transient := split(p.retype("s []byte"))
	eq(t, "persistent", persistent, []string{"CallerLiteral", "CallerParam"})
	eq(t, "transient", transient, []string{"CallerField", "CallerLocal"})
	// The stored table holds the provable ones and only those.
	eq(t, "stored", p.stored(), []string{"CallerLiteral", "CallerParam"})
}

// TestTransientsAreNotPersistedAcrossEdits is the defining property. A transient is news
// about the edit that produced it; it must not accumulate, and it must not be re-reported
// after an unrelated edit, because the thing it could not read is still unreadable.
func TestTransientsAreNotPersistedAcrossEdits(t *testing.T) {
	p := newTransientProj(t)
	if _, transient := split(p.retype("s []byte")); len(transient) == 0 {
		t.Fatal("expected the first retype to report unverified callers")
	}
	stored := p.stored()
	// An unrelated edit to the same file must not resurrect them.
	path := filepath.Join(p.dir, "callee.go")
	body := "package lib\n\n// a comment that changes nothing\nfunc Target(s []byte) bool { return true }\n"
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	mtime := time.Now().Add(4 * time.Second)
	_ = os.Chtimes(path, mtime, mtime)
	reported, err := p.mgr.UpdateFile(path, p.reg)
	if err != nil {
		t.Fatal(err)
	}
	if _, transient := split(reported); len(transient) != 0 {
		t.Errorf("a comment-only edit re-reported unverified callers: %v", transient)
	}
	eq(t, "stored unchanged", p.stored(), stored)
}

// TestUnchangedPositionsDoNotManufactureDoubt is the precision guard, and the reason the
// baseline is consulted at all. Only the SECOND parameter moves; CallerLocal's unreadable
// argument sits at the first. Without the baseline every caller of a two-parameter function
// would be reported unverified forever, on every edit, which is the noise that would get this
// channel ignored.
func TestUnchangedPositionsDoNotManufactureDoubt(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":    "module example.com/app\n\ngo 1.21\n",
		"callee.go": "package lib\n\nfunc Target(a string, b int) bool { return b > 0 }\n",
		"callers.go": `package lib

func compute() string { return "x" }

// The unreadable argument is at position 0, which does not move.
func CallerLocal() bool {
	t := compute()
	return Target(t, 1)
}
`,
	}
	for rel, content := range files {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	reg := scanner.NewRegistry()
	reg.Register(goscanner.NewGoScanner())
	mgr := topology.New()
	mgr.Load(filepath.Join(dir, "topology.db"))
	if err := mgr.FullScan(dir, reg); err != nil {
		t.Fatal(err)
	}
	// Move ONLY the second parameter, to a type the recorded #int still satisfies.
	path := filepath.Join(dir, "callee.go")
	if err := os.WriteFile(path, []byte("package lib\n\nfunc Target(a string, b int64) bool { return b > 0 }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	mtime := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(path, mtime, mtime)
	reported, err := mgr.UpdateFile(path, reg)
	if err != nil {
		t.Fatal(err)
	}
	persistent, transient := split(reported)
	eq(t, "persistent", persistent, nil)
	eq(t, "transient", transient, nil)
}

// TestArityChangeStillWarnsEveryCaller is the regression guard for the path that already
// worked. Adding a parameter is judged by the argument COUNT, which every call site records,
// so all four callers are provable breaks and all four are written down. No transients: there
// is nothing unverified about a call passing one argument to a two-argument function.
func TestArityChangeStillWarnsEveryCaller(t *testing.T) {
	p := newTransientProj(t)
	persistent, transient := split(p.retype("s string, extra int"))
	eq(t, "persistent", persistent, []string{"CallerField", "CallerLiteral", "CallerLocal", "CallerParam"})
	eq(t, "transient", transient, nil)
	eq(t, "stored", p.stored(), []string{"CallerField", "CallerLiteral", "CallerLocal", "CallerParam"})
}

// TestRevertClearsEveryStoredWarning pins the escape hatch, and one asymmetry that falls out
// of not persisting a transient.
//
// The stored warnings discharge exactly as they always did: the baseline records the shape the
// callers were written against, and putting that shape back is a string comparison.
//
// The two UNVERIFIED callers behave differently, and deliberately. Nothing was written down
// for them, so nothing carries a baseline back -- and the revert is itself a parameter-type
// change (`[]byte` -> `string`) whose arguments are still unreadable. They are reported again.
// That is the honest answer rather than a bug: the edit did change the signature, and the
// question of whether those two calls fit is exactly as unanswerable as it was before. The
// cost of never storing a maybe is that a maybe cannot be discharged; the benefit is that it
// can never get stuck, which is what a stored one with no discharge event would do.
func TestRevertClearsEveryStoredWarning(t *testing.T) {
	p := newTransientProj(t)
	p.retype("s []byte")
	if len(p.stored()) == 0 {
		t.Fatal("expected stored warnings after the break")
	}
	persistent, transient := split(p.retype("s string"))
	// The provable breaks are gone from both channels -- that is the discharge working.
	eq(t, "persistent after revert", persistent, nil)
	eq(t, "stored after revert", p.stored(), nil)
	// The unverifiable ones are asked about again, and still stored nowhere.
	eq(t, "transient after revert", transient, []string{"CallerField", "CallerLocal"})
}

// TestEveryTransientCarriesAnActionableMessage: a report the reader cannot act on is worse
// than silence, because it costs a turn to discover it says nothing. The two sentences have to
// stay distinguishable -- one instructs a fix, the other asks a question -- since that
// difference is the whole reason the transient channel exists.
func TestEveryTransientCarriesAnActionableMessage(t *testing.T) {
	p := newTransientProj(t)
	reported := p.retype("s []byte")

	seen := 0
	for _, w := range reported {
		if !w.Transient {
			// The stored sentence instructs, and must not hedge.
			if strings.Contains(w.Message, "check if caller") {
				t.Errorf("a stored warning reads as a maybe: %q", w.Message)
			}
			continue
		}
		seen++
		if w.SourceID == "" || w.TargetID == "" {
			t.Errorf("transient names no endpoints: %+v", w)
		}
		// Names the callee that moved and the caller to go look at.
		if !strings.Contains(w.Message, "Target") || !strings.Contains(w.Message, shortCaller(w.TargetID)) {
			t.Errorf("transient message %q does not name both ends", w.Message)
		}
		// Asks rather than instructs.
		if !strings.Contains(w.Message, "check if caller") || !strings.Contains(w.Message, "still supports it") {
			t.Errorf("transient message %q does not read as a question", w.Message)
		}
		if strings.Contains(w.Message, "fix caller") {
			t.Errorf("a transient must not instruct a fix it cannot justify: %q", w.Message)
		}
	}
	if seen == 0 {
		t.Fatal("no transients reported")
	}
}

// TestStashAndPopDoesNotRepeatTheReports replays `git stash && go test ...; git stash pop`: the
// agent's own edit is taken away and put back inside one command. Every scan in between re-judges
// the unreadable callers, and each re-judgement used to queue the same reports again -- measured
// on a real run, thirteen repeated transients paged across two tool calls. What the agent is told
// is drained from the queue, so that is what this checks.
func TestStashAndPopDoesNotRepeatTheReports(t *testing.T) {
	p := newTransientProj(t)
	db := p.mgr.DbPath()
	drain := func() []string {
		var out []string
		for _, w := range helper.DrainTransients(db) {
			out = append(out, shortCaller(w.TargetID))
		}
		sort.Strings(out)
		return out
	}

	p.retype("s []byte")
	eq(t, "reported by the edit", drain(), []string{"CallerField", "CallerLocal"})

	p.retype("s string") // git stash
	p.retype("s []byte") // git stash pop
	eq(t, "reported after stash/pop", drain(), nil)

	// A genuinely new shape of the callee is a new question.
	p.retype("s int")
	eq(t, "reported after a new retype", drain(), []string{"CallerField", "CallerLocal"})

	// The agent answers the report by editing the caller: its argument stays unreadable, its body
	// changes. A caller-only edit raises nothing, and the ledger now holds this caller.
	callers := filepath.Join(p.dir, "callers.go")
	edited := strings.Replace(transientCallers, "t := compute()\n", "t := compute()\n\tt = t + t\n", 1)
	if edited == transientCallers {
		t.Fatal("fixture edit did not apply")
	}
	bump := 10
	writeCallers := func(body string) {
		if err := os.WriteFile(callers, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
		bump += 2
		mtime := time.Now().Add(time.Duration(bump) * time.Second)
		_ = os.Chtimes(callers, mtime, mtime)
		if _, err := p.mgr.UpdateFile(callers, p.reg); err != nil {
			t.Fatal(err)
		}
	}
	writeCallers(edited)
	eq(t, "reported by the caller edit", drain(), nil)

	// THE MEASURED CASE: the stash takes BOTH files back, the pop restores both. The re-raise sees
	// the caller the agent itself wrote after the report, which is not news.
	p.retype("s string")
	writeCallers(transientCallers) // git stash
	p.retype("s int")
	writeCallers(edited) // git stash pop
	eq(t, "reported after stash/pop over an edited caller", drain(), nil)

	// The caller half still counts where the callee moves: []byte was reported against the old
	// CallerLocal, so it is a new question for that caller -- and a repeat for CallerField.
	p.retype("s []byte")
	eq(t, "reported after a retype back to a shape seen with the old caller", drain(), []string{"CallerLocal"})
}

// TestAnUpdateKeepsTheBodyHashesOfTheCallersItRebuilds: UpdateFile re-resolves the files that
// call into the edited one, and used to write those callers back with no fingerprints -- which
// made every caller of an edited function unmatchable after a later move, and gave a transient's
// state a different caller half depending on which scan path raised it.
func TestAnUpdateKeepsTheBodyHashesOfTheCallersItRebuilds(t *testing.T) {
	p := newTransientProj(t)
	const caller = "example.com/app.CallerLocal"
	read := func() string {
		got, err := helper.ReadResourcesByIDs(p.mgr.DbPath(), []string{caller})
		if err != nil {
			t.Fatal(err)
		}
		return got[caller].NormHash
	}
	before := read()
	if before == "" {
		t.Fatal("the full scan did not fingerprint the caller")
	}
	p.retype("s []byte")
	if after := read(); after != before {
		t.Errorf("editing the callee changed the untouched caller's fingerprint: %q -> %q", before, after)
	}
}
