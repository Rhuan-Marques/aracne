package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/llm/warnread"
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

// An untouched project gets the expanded report.
func TestWarningReadsAreOnByDefault(t *testing.T) {
	printed := breakAdd(t, warnReadProject(t, "", "CallA"))
	if !strings.Contains(printed, warnread.HeadlinePush) {
		t.Fatalf("an unconfigured project did not get the expanded report:\n%s", printed)
	}
}

// Turned off, the plain summary is the report.
func TestWarningReadsOffReportsThePlainSummary(t *testing.T) {
	printed := breakAdd(t, warnReadProject(t, `{"features":{"warning_reads":false}}`, "CallA"))

	if !strings.Contains(printed, "signature_changed") {
		t.Fatalf("precondition: the edit reported no warning at all:\n%s", printed)
	}
	if strings.Contains(printed, warnread.HeadlinePush) {
		t.Fatalf("features.warning_reads is off; nothing should have been expanded:\n%s", printed)
	}
	if !strings.Contains(printed, "Warnings found. Check these functions:") {
		t.Fatalf("the plain summary is missing with the reads off:\n%s", printed)
	}
}

// The feature's whole claim: the caller's source arrives with the warning, so the model can fix
// it without a read turn. The callee comes too -- it is the signature the fix has to match.
func TestWarningReadsAttachTheCallerAndTheCallee(t *testing.T) {
	dir := warnReadProject(t, `{"features":{"warning_reads":true}}`, "CallA")
	printed := breakAdd(t, dir)

	if !strings.Contains(printed, warnread.HeadlinePush) {
		t.Fatalf("no expansion in the report:\n%s", printed)
	}
	// The expansion REPLACES the summary rather than sitting under it: everything the summary
	// said is now written on the offending line, and printing both says it all twice.
	if strings.Contains(printed, "Topology warnings (functions that may need manual review):") {
		t.Fatalf("the summary was printed alongside the expansion:\n%s", printed)
	}
	// The warning is ON the line it broke, which is the whole point of the shape.
	if !strings.Contains(printed, "func CallA() int { return lib.Add(1, 2) } <- [signature_changed]") {
		t.Fatalf("the warning was not written on the call it broke:\n%s", printed)
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

// The callee is shown for its SIGNATURE, not its body. The caller has to be checked against the
// signature, and the callee is the code the agent just edited -- its body is already in context,
// and printing it spent the report's byte budget on nothing.
func TestWarningReadsShowTheCalleeAsItsSignature(t *testing.T) {
	dir := warnReadProject(t, `{"features":{"warning_reads":true}}`, "CallA")
	lib := "package lib\n\n// Add sums two numbers.\nfunc Add(a, b int) int {\n\tsum := a + b\n\treturn sum\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "lib", "lib.go"), []byte(lib), 0o644); err != nil {
		t.Fatal(err)
	}
	printed := breakAdd(t, dir)

	if !strings.Contains(printed, "func CallA() int { return lib.Add(1, 2) } <- [signature_changed]") {
		t.Fatalf("the caller is not shown in full with its warning:\n%s", printed)
	}
	if !strings.Contains(printed, "func Add(a, b, c int) int {") {
		t.Fatalf("the new signature the caller must match is missing:\n%s", printed)
	}
	if strings.Contains(printed, "sum := a + b") {
		t.Fatalf("the callee's body was printed:\n%s", printed)
	}
	if !strings.Contains(printed, "body not shown") || !strings.Contains(printed, "read example.com/m/lib.Add for its source") {
		t.Fatalf("the left-out body is not marked with the way to read it:\n%s", printed)
	}
}

// setWarningBudget rewrites the project's config to the feature with a byte budget. The
// topology is already built, so nothing is re-scanned: the next report reads the new budget.
func setWarningBudget(t *testing.T, dir string, maxBytes int) {
	t.Helper()
	body := fmt.Sprintf(`{"features":{"warning_reads":true,"warning_read_max_bytes":%d}}`, maxBytes)
	if err := os.WriteFile(filepath.Join(dir, ".aracne", "config.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// expandedCallers is which of names had their source expanded in printed, in order.
func expandedCallers(printed string, names []string) []string {
	var got []string
	for _, n := range names {
		if strings.Contains(printed, "func "+n+"() int") {
			got = append(got, n)
		}
	}
	return got
}

// The budget bounds what the MODEL RECEIVES: the whole printed output of `arac edit` -- the
// edit's own result line, the reads, the note -- not the expansion alone. And what it shows is
// a PAGE: the first warnings in the shared order, with the note counting the rest.
func TestWarningReadsFitTheByteBudget(t *testing.T) {
	callers := []string{"CallA", "CallB", "CallC", "CallD"}
	unlimited := breakAdd(t, warnReadProject(t,
		`{"features":{"warning_reads":true,"warning_read_max_bytes":0}}`, callers...))
	if got := expandedCallers(unlimited, callers); len(got) != len(callers) {
		t.Fatalf("precondition: an unlimited report expanded %v:\n%s", got, unlimited)
	}

	// Short of the whole report by a caller's worth of margin, so all four cannot fit.
	budget := len(unlimited) - 40
	printed := breakAdd(t, warnReadProject(t,
		fmt.Sprintf(`{"features":{"warning_reads":true,"warning_read_max_bytes":%d}}`, budget), callers...))

	if len(printed) > budget {
		t.Fatalf("the printed report is %d bytes, over the %d-byte budget:\n%s", len(printed), budget, printed)
	}
	got := expandedCallers(printed, callers)
	if len(got) == 0 || len(got) == len(callers) {
		t.Fatalf("expanded %v; the budget should admit some callers but not all:\n%s", got, printed)
	}
	// Ordered by kind, then source, then target -- so the page is always the same prefix.
	for i, name := range got {
		if name != callers[i] {
			t.Fatalf("expanded %v, which is not a prefix of %v:\n%s", got, callers, printed)
		}
	}
	left := len(callers) - len(got)
	want := fmt.Sprintf("... %d warning", left)
	if !strings.Contains(printed, want) || !strings.Contains(printed, "Use `arac warnings list --read` to continue fixing.") {
		t.Fatalf("the report did not say %d were left, or how to continue:\n%s", left, printed)
	}
}

// THE LONGEST page that fits, not merely one that fits. The budget is set to exactly the size of
// a smaller report's page plus what it would print for the next one short of a byte: that page
// must not fit, and the one below it must be what comes back.
func TestWarningReadsShowAsManyAsFit(t *testing.T) {
	callers := []string{"CallA", "CallB", "CallC", "CallD", "CallE"}
	dir := warnReadProject(t, `{"features":{"warning_reads":true,"warning_read_max_bytes":0}}`, callers...)
	breakAdd(t, dir)

	// Page sizes as the pull surface prints them, found by walking the budget down from the
	// full listing: each drop past a page's size loses exactly that warning.
	full := runWarningsList(t, dir, "--read")
	if got := expandedCallers(full, callers); len(got) != len(callers) {
		t.Fatalf("precondition: unlimited --read expanded %v", got)
	}
	setWarningBudget(t, dir, len(full))
	if again := runWarningsList(t, dir, "--read"); again != full {
		t.Fatalf("a budget of exactly the report's size must fit it whole:\n%s", again)
	}
	setWarningBudget(t, dir, len(full)-1)
	short := runWarningsList(t, dir, "--read")
	n := len(expandedCallers(short, callers))
	if n == 0 || n == len(callers) {
		t.Fatalf("one byte under the full report expanded %d of %d:\n%s", n, len(callers), short)
	}
	// short is the largest page that fit len(full)-1. So a budget of exactly len(short) must
	// return that same page, and len(short)-1 must lose at least one warning from it.
	setWarningBudget(t, dir, len(short))
	if same := runWarningsList(t, dir, "--read"); same != short {
		t.Fatalf("a budget of exactly this page's size returned a different page:\n%s\nwant:\n%s", same, short)
	}
	setWarningBudget(t, dir, len(short)-1)
	if smaller := runWarningsList(t, dir, "--read"); len(expandedCallers(smaller, callers)) >= n {
		t.Fatalf("a byte under the page still expanded %d warnings:\n%s", n, smaller)
	}
}

// Zero is "no limit", not "expand nothing" -- the state a project opts into deliberately.
func TestWarningReadMaxBytesZeroExpandsEverything(t *testing.T) {
	dir := warnReadProject(t, `{"features":{"warning_reads":true,"warning_read_max_bytes":0}}`,
		"CallA", "CallB", "CallC", "CallD", "CallE", "CallF")
	printed := breakAdd(t, dir)

	for _, want := range []string{"CallA", "CallB", "CallC", "CallD", "CallE", "CallF"} {
		if !strings.Contains(printed, "func "+want+"() int") {
			t.Fatalf("a budget of 0 means unlimited and %s was left out:\n%s", want, printed)
		}
	}
	if strings.Contains(printed, "warnings left") {
		t.Fatalf("nothing was truncated, so there must be no continuation note:\n%s", printed)
	}
}

// The fallback listing -- what a report prints when nothing could be expanded -- is cut to the
// same budget: a plain list of eighty warnings is as long as it looks, and it is cut to a preview
// by the same harness.
func TestFallbackSummaryFitsTheBudget(t *testing.T) {
	var warnings []domain.TopologyWarning
	for i := 0; i < 80; i++ {
		warnings = append(warnings, domain.TopologyWarning{
			ID: fmt.Sprintf("w%02d", i), Kind: domain.WarnNodeRemoved,
			SourceID: fmt.Sprintf("pkg.Referrer%02d", i), TargetID: "pkg.Gone",
			Message: fmt.Sprintf("pkg.Gone was removed, verify pkg.Referrer%02d which references it", i),
		})
	}
	cfg := &helper.Config{Mode: helper.ModeCLI}
	full, all := fitSummary(cfg, warnings, 0)
	if len(all) != len(warnings) {
		t.Fatalf("an unlimited summary shows %d of %d warnings", len(all), len(warnings))
	}
	if !strings.Contains(full, "Referrer79") || strings.Contains(full, "more warning") {
		t.Fatalf("an unlimited summary must list everything, with no note:\n%s", full)
	}

	for _, limit := range []int{2000, 700, 40} {
		got, shownWarnings := fitSummary(cfg, warnings, limit)
		shown := strings.Count(got, "\n - [")
		if len(shownWarnings) != shown {
			t.Errorf("limit %d: listed %d lines but reported %d warnings shown", limit, shown, len(shownWarnings))
		}
		if limit >= 700 && len(got) > limit {
			t.Errorf("limit %d: summary is %d bytes", limit, len(got))
		}
		want := fmt.Sprintf("... %d more warnings. Use `arac warnings list` to see them all.", 80-shown)
		if !strings.HasSuffix(got, want) {
			t.Errorf("limit %d: the note does not count the %d left out:\n%s", limit, 80-shown, got)
		}
		if !strings.HasPrefix(got, "Warnings found.") {
			t.Errorf("limit %d: the header is gone:\n%s", limit, got)
		}
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

// The hook joins a nudge and the report into one additionalContext. The report's reserve is
// everything in front of it, separator included, so the joined message is what fits the budget.
func TestJoinedLenReservesTheWholePrefix(t *testing.T) {
	parts := []string{"nudge text", "another"}
	report := "REPORT"
	joined := strings.Join(append(parts, report), "\n\n")
	if got, want := joinedLen(parts)+len(report), len(joined); got != want {
		t.Fatalf("reserve %d + report %d = %d, but the joined message is %d bytes",
			joinedLen(parts), len(report), got, want)
	}
	if joinedLen(nil) != 0 {
		t.Fatal("nothing in front of the report reserves nothing")
	}
}

// A TRANSIENT THE BUDGET PAGED OUT IS NOT LOST. Transients live only in the pending queue, and
// the report drains it to show them; one left off the page used to be drained and dropped while
// the note counted it as "left" and pointed at `--read`, which could not produce it. It now goes
// back to the queue, `--read` pages through it, and drains exactly what it shows.
func TestPagedOutTransientsAreRequeuedAndReadable(t *testing.T) {
	callers := []string{"CallA", "CallB", "CallC", "CallD", "CallE"}
	dir := warnReadProject(t, `{"features":{"warning_reads":true,"warning_read_max_bytes":0}}`, callers...)
	breakAdd(t, dir)
	dbPath := filepath.Join(dir, ".aracne", "topology.db")

	// Turn the stored warnings into transients: same sites, never in the table.
	mgr, _ := InitRegistry(dbPath)
	stored, err := mgr.ListWarnings("", "", "")
	if err != nil || len(stored) != len(callers) {
		t.Fatalf("precondition: %d stored warnings (err %v), want %d", len(stored), err, len(callers))
	}
	var transients []domain.TopologyWarning
	for _, w := range stored {
		w.ID += "-transient"
		w.Transient = true
		transients = append(transients, w)
	}
	full := driftWarningReport(dbPath, transients, 0)
	setWarningBudget(t, dir, len(full)/2)
	helper.QueueTransients(dbPath, transients)

	report := driftWarningReport(dbPath, helper.DrainTransients(dbPath), 0)
	shown := expandedCallers(report, callers)
	if len(shown) == 0 || len(shown) == len(callers) {
		t.Fatalf("precondition: the budget should page, expanded %v:\n%s", shown, report)
	}
	if got, want := len(helper.PeekTransients(dbPath)), len(callers)-len(shown); got != want {
		t.Fatalf("%d transients back in the queue, want the %d the page left out", got, want)
	}

	// The note's continuation produces them. Unlimited, so one page takes the rest.
	setWarningBudget(t, dir, 0)
	page := runWarningsList(t, dir, "--read")
	for _, name := range callers[len(shown):] {
		if !strings.Contains(page, "func "+name+"() int") {
			t.Fatalf("paged-out transient on %s is missing from --read:\n%s", name, page)
		}
	}
	if left := helper.PeekTransients(dbPath); len(left) != 0 {
		t.Fatalf("--read showed the transients and left %d queued; they are reported once", len(left))
	}
}
