package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// features.warning_reads turns a warning report from "here are two ids" into "here is the code
// behind them". The point is the turn it saves: acting on the summary alone means reading the
// caller first, and every turn re-sends the whole transcript.

// warnReadProject writes a Go project whose callers all call lib.Add, one per function, so a
// single signature change raises one warning per caller. config is the literal
// `.aracne/config.json` to place before anything runs; "" leaves the project unconfigured.
func warnReadProject(t *testing.T, config string, callers ...string) string {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/m\n\ngo 1.21\n")
	write("lib/lib.go", "package lib\n\n// Add sums two numbers.\nfunc Add(a, b int) int { return a + b }\n")

	body := "package main\n\nimport \"example.com/m/lib\"\n\n"
	for _, name := range callers {
		body += fmt.Sprintf("// %s adds through lib.\nfunc %s() int { return lib.Add(1, 2) }\n\n", name, name)
	}
	body += "func main() {}\n"
	write("main.go", body)

	if config != "" {
		write(".aracne/config.json", config)
	}
	return dir
}

// breakAdd widens lib.Add's signature through `arac edit`, and returns everything the command
// printed -- summary and, where the feature is on, the expansion under it.
func breakAdd(t *testing.T, dir string) string {
	t.Helper()
	return runCLIWithStdin(t, dir,
		`{"file_path":"lib/lib.go","old_string":"a, b int","new_string":"a, b, c int"}`, RunEdit)
}

// The default is off, and an existing project's config has no features key at all: adding this
// must not start spending bytes on every edit for a project that never asked.
func TestWarningReadsAreOffByDefault(t *testing.T) {
	printed := breakAdd(t, warnReadProject(t, "", "CallA"))

	if !strings.Contains(printed, "signature_changed") {
		t.Fatalf("precondition: the edit reported no warning at all:\n%s", printed)
	}
	if strings.Contains(printed, "Warned code") {
		t.Fatalf("features.warning_reads is off; nothing should have been expanded:\n%s", printed)
	}
}

// The feature's whole claim: the caller's source arrives with the warning, so the model can fix
// it without a read turn. The callee comes too -- it is the signature the fix has to match.
func TestWarningReadsAttachTheCallerAndTheCallee(t *testing.T) {
	dir := warnReadProject(t, `{"features":{"warning_reads":true}}`, "CallA")
	printed := breakAdd(t, dir)

	if !strings.Contains(printed, "Warned code, read in full") {
		t.Fatalf("no expansion under the summary:\n%s", printed)
	}
	if !strings.Contains(printed, "func CallA() int { return lib.Add(1, 2) }") {
		t.Fatalf("the caller's source is what the model has to edit, and it is missing:\n%s", printed)
	}
	if !strings.Contains(printed, "func Add(a, b, c int) int") {
		t.Fatalf("the new signature the caller must match is missing:\n%s", printed)
	}
}

// One read call for the whole batch, so the shared render ledger can do its job: six warnings
// name one callee, and the callee's declaration must appear once rather than six times.
func TestWarningReadsRenderTheSharedCalleeOnce(t *testing.T) {
	dir := warnReadProject(t, `{"features":{"warning_reads":true}}`,
		"CallA", "CallB", "CallC", "CallD", "CallE", "CallF")
	printed := breakAdd(t, dir)

	if got := strings.Count(printed, "func Add(a, b, c int) int"); got != 1 {
		t.Fatalf("the shared callee was rendered %d times, want 1:\n%s", got, printed)
	}
}

// The cap is what keeps a wide breakage from burying the report it is attached to. It bounds
// WARNINGS, and the summary above still names every one of them.
func TestWarningReadsStopAtTheConfiguredLimit(t *testing.T) {
	dir := warnReadProject(t, `{"features":{"warning_reads":true,"warning_read_limit":2}}`,
		"CallA", "CallB", "CallC", "CallD")
	printed := breakAdd(t, dir)

	// Ordered by kind, then source, then target -- so "the first two" is the same two on
	// every run, which a map-ordered list could not promise.
	for _, want := range []string{"CallA", "CallB"} {
		if !strings.Contains(printed, "func "+want+"() int") {
			t.Fatalf("%s is within the limit and was not expanded:\n%s", want, printed)
		}
	}
	for _, unwanted := range []string{"CallC", "CallD"} {
		if strings.Contains(printed, "func "+unwanted+"() int") {
			t.Fatalf("%s is past the limit and was expanded anyway:\n%s", unwanted, printed)
		}
		if !strings.Contains(printed, unwanted) {
			t.Fatalf("%s is past the limit but must still appear in the summary:\n%s", unwanted, printed)
		}
	}
	// A truncation the reader cannot see is one it will assume did not happen. The count and
	// the remedy live in the note under the read -- see warningsLeftNote.
	if !strings.Contains(printed, "... 2 warnings left. Use `arac warnings list --read` to continue fixing.") {
		t.Fatalf("the expansion did not say it was truncated, or how to continue:\n%s", printed)
	}
}

// Zero is "no limit", not "expand nothing" -- the state a project opts into deliberately.
func TestWarningReadLimitZeroExpandsEverything(t *testing.T) {
	dir := warnReadProject(t, `{"features":{"warning_reads":true,"warning_read_limit":0}}`,
		"CallA", "CallB", "CallC", "CallD", "CallE", "CallF")
	printed := breakAdd(t, dir)

	for _, want := range []string{"CallA", "CallB", "CallC", "CallD", "CallE", "CallF"} {
		if !strings.Contains(printed, "func "+want+"() int") {
			t.Fatalf("limit 0 means unlimited and %s was left out:\n%s", want, printed)
		}
	}
	if strings.Contains(printed, "warnings left") {
		t.Fatalf("nothing was truncated, so there must be no continuation note:\n%s", printed)
	}
}

// node_removed and use_missing_node name a target that is GONE by construction. Handing it to
// the read anyway would put an "# UNRESOLVED:" block under every such report, telling the model
// its own warning was a typo.
func TestWarningReadsSkipATargetThatNoLongerExists(t *testing.T) {
	dir := warnReadProject(t, `{"features":{"warning_reads":true}}`, "CallA")
	if err := os.WriteFile(filepath.Join(dir, "lib", "lib.go"),
		[]byte("package lib\n\n// Add sums two numbers.\nfunc Add(a, b int) int { return a + b }\n\n// Mul multiplies.\nfunc Mul(a, b int) int { return a * b }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"),
		[]byte("package main\n\nimport \"example.com/m/lib\"\n\n// CallA adds and multiplies.\nfunc CallA() int { return lib.Add(1, 2) + lib.Mul(3, 4) }\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Delete Mul out from under its caller.
	printed := runCLIWithStdin(t, dir,
		`{"file_path":"lib/lib.go","old_string":"\n\n// Mul multiplies.\nfunc Mul(a, b int) int { return a * b }","new_string":""}`,
		RunEdit)

	if !strings.Contains(printed, "node_removed") {
		t.Fatalf("precondition: deleting Mul raised no node_removed warning:\n%s", printed)
	}
	if strings.Contains(printed, "# UNRESOLVED") {
		t.Fatalf("the removed target was handed to the read and came back unresolved:\n%s", printed)
	}
	if !strings.Contains(printed, "func CallA() int") {
		t.Fatalf("the referrer -- the half that still exists -- was not expanded:\n%s", printed)
	}
}

// Which half of a warning holds the code to change depends on the kind, and the batch renders
// whatever is named first in full. The fix site has to be first.
func TestWarningReadTargetsPutTheFixSiteFirst(t *testing.T) {
	for _, tc := range []struct {
		kind  domain.WarningKind
		first string
	}{
		{domain.WarnSignatureChanged, "caller"},  // the call site is the edit
		{domain.WarnInterfaceConflict, "caller"}, // TargetID is the implementer
		{domain.WarnNodeRemoved, "callee"},       // SourceID still points at something gone
		{domain.WarnUseMissingNode, "callee"},    //  "
	} {
		got := warningReadTargets(domain.TopologyWarning{
			Kind: tc.kind, SourceID: "callee", TargetID: "caller",
		})
		if len(got) != 2 || got[0] != tc.first {
			t.Errorf("%s: got %v, want %q first", tc.kind, got, tc.first)
		}
	}
}
