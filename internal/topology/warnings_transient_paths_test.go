package topology_test

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner/goscanner"
)

// A signature change must be judged the same way whichever route re-indexes it.
//
// WHY THIS FILE EXISTS. The same edit reaches the topology through different routes, chosen by
// who notices it rather than by anything about the edit:
//
//   - UpdateFile: `arac edit`, `arac write`, the MCP edit/write tools, the edit-sync plugin.
//   - IncrementalScan: the guard's drift check, the pre-tool scan, `arac scanner run`,
//     `arac scan`. It takes the scoped partial path when every caller of a changed signature
//     is in the edited file, and the full two-phase path otherwise (see
//     partialNeedsCrossFileResolve) -- so the two fixtures below exist to reach both.
//
// They disagreed about the callers declared IN THE CALLEE'S OWN FILE. UpdateFile merged the
// warnings it had just raised and only then ran the edited-file clear over them, so a
// same-file caller was treated as a caller the agent had just fixed: arguments that fit
// deleted its warning even when only the RETURN type had changed, and an argument nobody
// could type deleted it too -- silently, with no transient, because the transient is born in
// the reconciler and the row never reached it. Retype a helper and the calls beside it went
// unreported through one route and reported through the other.
//
// Every fixture mirrors the argument forms that matter: a forwarded parameter and a literal,
// which the scanner can type, and a local and a field, which it cannot. A same-file caller
// must get exactly what its cross-file twin gets, on every route.

// pathsSupport is what the callers need and nothing that calls Target, so a fixture made of it
// alone has same-file callers only.
const pathsSupport = `package lib

type Cfg struct{ Name string }

func compute() string { return "x" }
`

const pathsCallers = pathsSupport + `
func CallerParam(s string) bool { return Target(s) }

func CallerLiteral() bool { return Target("literal") }

func CallerLocal() bool {
	t := compute()
	return Target(t)
}

func CallerField(c Cfg) bool { return Target(c.Name) }
`

// calleeFile renders the callee's file for a step: Target itself plus its four same-file callers.
func calleeFile(s step) string {
	sameParam := "func SameParam(s " + s.sameParam + ") bool { return Target(s) }\n\n"
	if s.sameParamLocal {
		sameParam = "func SameParam(s string) bool {\n\tt := compute()\n\treturn Target(t)\n}\n\n"
	}
	return "package lib\n\nfunc Target(" + s.sig + " {\n\treturn true\n}\n\n" +
		sameParam +
		"func SameLiteral() bool { return Target(\"literal\") }\n\n" +
		"func SameLocal() bool {\n\tt := compute()\n\treturn Target(t)\n}\n\n" +
		"func SameField(c Cfg) bool { return Target(c.Name) }\n"
}

// step is one edit to the callee's file. sig is Target's signature from the parameter list
// on, e.g. "s []byte) bool".
type step struct {
	sig string
	// sameParam is SameParam's own parameter type, so a scenario can fix that caller in the
	// same edit that breaks the others.
	sameParam string
	// sameParamLocal rewrites SameParam's call to pass a local instead of its parameter: an
	// EDIT to a caller, turning a readable argument into one nobody can type.
	sameParamLocal bool
	// keepsSignature says Target does not move in this step, which is what lets IncrementalScan
	// take the partial path even when callers live in other files.
	keepsSignature bool
}

func edit(sig string) step { return step{sig: sig, sameParam: "string"} }

// outcome is everything an agent can observe about one edit: what the edit REPORTED, split by
// channel; what the warnings table holds afterwards; and what reached the pending-transient
// queue, which is the channel the post-tool hook actually drains.
type outcome struct {
	persistent, transient, stored, queued []string
}

func (o outcome) String() string {
	j := func(s []string) string { return "[" + strings.Join(s, ",") + "]" }
	return "persistent=" + j(o.persistent) + " transient=" + j(o.transient) +
		" stored=" + j(o.stored) + " queued=" + j(o.queued)
}

type pathsProj struct {
	t    *testing.T
	dir  string
	db   string
	reg  *scanner.Registry
	mgr  *topology.TopologyManager
	edit int

	fixture fixture
}

// fixture is a starting project: which callers exist outside the callee's file.
type fixture struct {
	name string
	// callers is callers.go.
	callers string
	// partial is whether IncrementalScan takes the partial path for a signature change here:
	// only when no caller lives outside the callee's file.
	partial bool
}

var fixtures = []fixture{
	{name: "same-file and cross-file callers", callers: pathsCallers, partial: false},
	{name: "same-file callers only", callers: pathsSupport, partial: true},
}

// names narrows a caller list to the ones this fixture declares.
func (f fixture) names(all []string) []string {
	var out []string
	for _, n := range all {
		if f.partial && !strings.HasPrefix(n, "Same") {
			continue
		}
		out = append(out, n)
	}
	return out
}

func (f fixture) expect(o outcome) outcome {
	return outcome{
		persistent: f.names(o.persistent), transient: f.names(o.transient),
		stored: f.names(o.stored), queued: f.names(o.queued),
	}
}

func newPathsProj(t *testing.T, f fixture) *pathsProj {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":     "module example.com/app\n\ngo 1.21\n",
		"callee.go":  calleeFile(edit("s string) bool")),
		"other.go":   "package lib\n\nfunc Other() int { return 0 }\n",
		"callers.go": f.callers,
	}
	for rel, content := range files {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	reg := scanner.NewRegistry()
	reg.Register(goscanner.NewGoScanner())
	db := filepath.Join(dir, "topology.db")
	mgr := topology.New()
	mgr.Load(db)
	if err := mgr.FullScan(dir, reg); err != nil {
		t.Fatalf("FullScan: %v", err)
	}
	// Nothing from the baseline scan may count toward the first edit.
	helper.DrainTransients(db)
	return &pathsProj{t: t, dir: dir, db: db, reg: reg, mgr: mgr, fixture: f}
}

// write replaces a file and moves its mtime past anything the manifest recorded, so every
// route sees it as changed.
func (p *pathsProj) write(rel, content string) string {
	p.t.Helper()
	path := filepath.Join(p.dir, rel)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		p.t.Fatal(err)
	}
	mtime := time.Now().Add(time.Duration(2+2*p.edit) * time.Second)
	_ = os.Chtimes(path, mtime, mtime)
	return path
}

// route is one way the same edit reaches the topology.
type route struct {
	name string
	// partial reports whether this route must take the scoped partial path for a step.
	// Asserted, not assumed: a route that silently fell back to another would make an
	// equality test compare a path with itself.
	partial func(f fixture, s step) bool
	apply   func(p *pathsProj, s step) ([]domain.TopologyWarning, error)
}

var routes = []route{
	{name: "UpdateFile", partial: func(fixture, step) bool { return false },
		apply: func(p *pathsProj, s step) ([]domain.TopologyWarning, error) {
			return p.mgr.UpdateFile(p.write("callee.go", calleeFile(s)), p.reg)
		}},
	{name: "IncrementalScan", partial: func(f fixture, s step) bool { return f.partial || s.keepsSignature },
		apply: func(p *pathsProj, s step) ([]domain.TopologyWarning, error) {
			p.write("callee.go", calleeFile(s))
			return p.mgr.IncrementalScan(p.dir, p.reg)
		}},
	// A second changed file routes any batch to the full two-phase path, which is the only
	// route that reaches the edited-file clear in IncrementalScan. other.go declares nothing the
	// callee or its callers touch.
	{name: "IncrementalScan+batch", partial: func(fixture, step) bool { return false },
		apply: func(p *pathsProj, s step) ([]domain.TopologyWarning, error) {
			p.write("callee.go", calleeFile(s))
			p.write("other.go", "package lib\n\nfunc Other() int { return "+strconv.Itoa(p.edit+1)+" }\n")
			return p.mgr.IncrementalScan(p.dir, p.reg)
		}},
}

// run applies one edit through a route and records the outcome.
func (p *pathsProj) run(r route, s step) outcome {
	p.t.Helper()
	before := topology.PartialIncrementalCount()
	reported, err := r.apply(p, s)
	if err != nil {
		p.t.Fatalf("%s: %v", r.name, err)
	}
	p.edit++
	if took, want := topology.PartialIncrementalCount() > before, r.partial(p.fixture, s); took != want {
		p.t.Fatalf("%s: took the partial path = %v, want %v -- the route under test was not exercised", r.name, took, want)
	}

	var o outcome
	o.persistent, o.transient = splitUnique(reported)
	all, err := p.mgr.ListWarnings("", "", domain.WarnSignatureChanged)
	if err != nil {
		p.t.Fatal(err)
	}
	for _, w := range all {
		o.stored = append(o.stored, shortCaller(w.TargetID))
	}
	for _, w := range helper.DrainTransients(p.db) {
		o.queued = append(o.queued, shortCaller(w.TargetID))
	}
	o.stored, o.queued = uniqueSorted(o.stored), uniqueSorted(o.queued)
	return o
}

// splitUnique is split, deduplicated: the report is judged on WHICH callers each channel
// names, and a caller named twice in one channel is a presentation defect, not a verdict.
func splitUnique(ws []domain.TopologyWarning) (persistent, transient []string) {
	persistent, transient = split(ws)
	return uniqueSorted(persistent), uniqueSorted(transient)
}

func uniqueSorted(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, v := range s {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

var (
	unreadable = []string{"CallerField", "CallerLocal", "SameField", "SameLocal"}
	readable   = []string{"CallerLiteral", "CallerParam", "SameLiteral", "SameParam"}
	everyone   = []string{"CallerField", "CallerLiteral", "CallerLocal", "CallerParam",
		"SameField", "SameLiteral", "SameLocal", "SameParam"}
)

// scenario is a sequence of edits and what the LAST one must produce. The earlier steps set up
// the state the last one is judged in -- a widening before a narrowing, a break before a revert.
type scenario struct {
	name  string
	steps []step
	want  outcome
}

var scenarios = []scenario{
	{
		// The original bug. The readable callers are provable breaks and are written down; the
		// unreadable ones are reported once and stored nowhere. Same-file callers included.
		name:  "retype string to []byte",
		steps: []step{edit("s []byte) bool")},
		want:  outcome{persistent: readable, transient: unreadable, stored: readable, queued: unreadable},
	},
	{
		// Every caller still fits `any`, readable or not, so nothing is said anywhere.
		name:  "widen to any",
		steps: []step{edit("s any) bool")},
	},
	{
		// Back from `any`: the readable callers demonstrably pass a string, the unreadable ones
		// cannot be checked.
		name:  "narrow from any to string",
		steps: []step{edit("s any) bool"), edit("s string) bool")},
		want:  outcome{transient: unreadable, queued: unreadable},
	},
	{
		// Arity is judged on the argument count every call site records, so all eight are
		// provable breaks and nothing is unverified.
		name:  "add a parameter",
		steps: []step{edit("s string, extra int) bool")},
		want:  outcome{persistent: everyone, stored: everyone},
	},
	{
		// No call site records what a caller does with the result, so a return-type change is
		// held by the baseline alone, for every caller -- a same-file caller whose arguments
		// happen to fit has not been shown to cope with the new result any more than its
		// cross-file twin has.
		name:  "change only the return type",
		steps: []step{edit("s string) int")},
		want:  outcome{persistent: everyone, stored: everyone},
	},
	{
		// The provable breaks discharge. The unverified callers are asked about again: the
		// revert is itself a retype their arguments still cannot be checked against, and
		// nothing stored carries a baseline back for them.
		name:  "revert a retype",
		steps: []step{edit("s []byte) bool"), edit("s string) bool")},
		want:  outcome{transient: unreadable, queued: unreadable},
	},
	{
		// A same-file caller FIXED in the edit that breaks the others -- SameParam now takes
		// []byte and forwards it -- is judged on the call as it now stands, and says nothing.
		// Everything else is exactly the plain retype.
		name:  "retype and fix a same-file caller in one edit",
		steps: []step{{sig: "s []byte) bool", sameParam: "[]byte"}},
		want: outcome{
			persistent: []string{"CallerLiteral", "CallerParam", "SameLiteral"},
			transient:  unreadable,
			stored:     []string{"CallerLiteral", "CallerParam", "SameLiteral"},
			queued:     unreadable,
		},
	},
	{
		// The EDITED-CALLER half. Target stands still; the agent rewrites SameParam, whose
		// stored break is now a call passing a local nobody can type. That is not a fix and not
		// a break -- it is exactly the question a transient asks. The row goes and the report
		// takes its place; SameLiteral, re-parsed too but still provably broken, keeps its row;
		// the untouched cross-file callers keep theirs and are not re-announced.
		name: "edit a stored caller to pass an unreadable argument",
		steps: []step{
			edit("s []byte) bool"),
			{sig: "s []byte) bool", sameParam: "string", sameParamLocal: true, keepsSignature: true},
		},
		want: outcome{
			transient: []string{"SameParam"},
			stored:    []string{"CallerLiteral", "CallerParam", "SameLiteral"},
			queued:    []string{"SameParam"},
		},
	},
}

// runScenario plays a scenario through one route on a fresh project and returns the outcome of
// every step.
func runScenario(t *testing.T, f fixture, sc scenario, r route) []outcome {
	t.Helper()
	p := newPathsProj(t, f)
	out := make([]outcome, 0, len(sc.steps))
	for _, s := range sc.steps {
		out = append(out, p.run(r, s))
	}
	return out
}

// TestSignatureOutcomesAreIdenticalOnEveryRoute: the route an edit happens to take must not
// change what the agent is told about it. Every step is compared, not only the last, so a route
// that reaches the right end state by a different sequence of reports still fails.
func TestSignatureOutcomesAreIdenticalOnEveryRoute(t *testing.T) {
	for _, f := range fixtures {
		for _, sc := range scenarios {
			t.Run(f.name+"/"+sc.name, func(t *testing.T) {
				reference := runScenario(t, f, sc, routes[0])
				for _, r := range routes[1:] {
					got := runScenario(t, f, sc, r)
					for i := range reference {
						if got[i].String() != reference[i].String() {
							t.Errorf("step %d: %s disagrees with %s\n  %-17s %s\n  %-17s %s",
								i+1, r.name, routes[0].name,
								routes[0].name+":", reference[i], r.name+":", got[i])
						}
					}
				}
			})
		}
	}
}

// TestSignatureOutcomesAreCorrectOnEveryRoute: agreeing is not enough -- the routes could agree
// on the wrong answer. Each is held to the outcome the scenario states, including that every
// transient it reports is also what the post-tool hook will find queued.
func TestSignatureOutcomesAreCorrectOnEveryRoute(t *testing.T) {
	for _, f := range fixtures {
		for _, sc := range scenarios {
			for _, r := range routes {
				t.Run(f.name+"/"+sc.name+"/"+r.name, func(t *testing.T) {
					steps := runScenario(t, f, sc, r)
					got, want := steps[len(steps)-1], f.expect(sc.want)
					if got.String() != want.String() {
						t.Errorf("\n  got  %s\n  want %s", got, want)
					}
				})
			}
		}
	}
}
