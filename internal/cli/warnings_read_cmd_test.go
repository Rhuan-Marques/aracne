package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/llm/tools"
	"github.com/Rhuan-Marques/aracne/internal/llm/warnread"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// `arac warnings list --read` and the warnings_list tool's `read` are the continuation of the
// loop the post-edit report starts: the report expands the first N warnings and says how many
// it left, and these are what show the next ones.

// runWarningsList runs the command inside dir and returns what it printed.
func runWarningsList(t *testing.T, dir string, args ...string) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(wd) })

	outPath := filepath.Join(t.TempDir(), "stdout.txt")
	captured, err := os.Create(outPath)
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = captured
	RunWarningsList(args)
	os.Stdout = old
	captured.Close()

	printed, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	return string(printed)
}

func TestWarningsListReadPrintsTheWarnedCode(t *testing.T) {
	dir := warnReadProject(t, `{"features":{"warning_reads":true}}`, "CallA")
	breakAdd(t, dir)

	printed := runWarningsList(t, dir, "--read")
	if !strings.Contains(printed, warnread.HeadlinePull) {
		t.Fatalf("--read printed no expansion:\n%s", printed)
	}
	// The PULL headline, not the push one: this caller did not necessarily break anything.
	if strings.Contains(printed, warnread.HeadlinePush) {
		t.Fatalf("a listing claimed the caller's own changes caused the warnings:\n%s", printed)
	}
	if !strings.Contains(printed, "func CallA() int { return lib.Add(1, 2) } <- [signature_changed]") {
		t.Fatalf("--read did not annotate the caller's call line:\n%s", printed)
	}
	// --read REPLACES the listing; the two halves would otherwise say the same thing twice.
	if strings.Contains(printed, "Found 1 warning(s)") {
		t.Fatalf("--read printed the plain listing as well as the expansion:\n%s", printed)
	}
}

// Without the flag the command is exactly what it always was. A listing that started dumping
// source unasked would break every script that parses it.
func TestWarningsListWithoutReadIsUnchanged(t *testing.T) {
	dir := warnReadProject(t, `{"features":{"warning_reads":true}}`, "CallA")
	breakAdd(t, dir)

	printed := runWarningsList(t, dir)
	if strings.Contains(printed, warnread.HeadlinePull) {
		t.Fatalf("the plain listing expanded anything at all:\n%s", printed)
	}
	if !strings.Contains(printed, "signature_changed") {
		t.Fatalf("the plain listing lost its warnings:\n%s", printed)
	}
}

// A flag that silently does nothing is worse than no flag: the caller cannot tell "nothing to
// read" from "this project has the feature off".
func TestWarningsListReadSaysSoWhenTheFeatureIsOff(t *testing.T) {
	dir := warnReadProject(t, `{"features":{"warning_reads":false}}`, "CallA")
	breakAdd(t, dir)

	printed := runWarningsList(t, dir, "--read")
	if strings.Contains(printed, "Warned code") {
		t.Fatalf("--read expanded with features.warning_reads off:\n%s", printed)
	}
	if !strings.Contains(printed, "features.warning_reads is off") {
		t.Fatalf("--read did nothing and did not say why:\n%s", printed)
	}
}

// THE LOOP, end to end, and the claim the note makes. Seven warnings under a budget that cannot
// hold them all: each page expands a prefix of what is still broken and says how many are left;
// fixing that page and asking again expands the next, until nothing is left and the note is gone.
//
// No cursor is stored anywhere, deliberately -- the page advances because a FIXED warning has
// retired itself from the table. A remembered page would have shown later warnings while the
// first ones were still broken.
func TestWarningsListReadAdvancesAsWarningsAreFixed(t *testing.T) {
	callers := []string{"CallA", "CallB", "CallC", "CallD", "CallE", "CallF", "CallG"}
	dir := warnReadProject(t, `{"features":{"warning_reads":true,"warning_read_max_bytes":0}}`, callers...)
	breakAdd(t, dir)

	// Room for roughly half the report, so the loop has to take more than one page.
	budget := len(runWarningsList(t, dir, "--read")) / 2
	setWarningBudget(t, dir, budget)

	remaining := append([]string(nil), callers...)
	pages := 0
	for len(remaining) > 0 {
		if pages++; pages > len(callers) {
			t.Fatalf("still %v left after %d pages; the loop is not advancing", remaining, pages)
		}
		page := runWarningsList(t, dir, "--read")
		if len(page) > budget {
			t.Fatalf("page %d is %d bytes, over the %d budget:\n%s", pages, len(page), budget, page)
		}
		shown := expandedCallers(page, callers)
		if len(shown) == 0 {
			t.Fatalf("page %d expanded nothing with %v still broken:\n%s", pages, remaining, page)
		}
		for i, name := range shown {
			if i >= len(remaining) || name != remaining[i] {
				t.Fatalf("page %d expanded %v, not a prefix of what is left %v:\n%s", pages, shown, remaining, page)
			}
		}
		left := len(remaining) - len(shown)
		if left > 0 && !strings.Contains(page, fmt.Sprintf("... %d warning", left)+"") {
			t.Fatalf("page %d did not say %d were left:\n%s", pages, left, page)
		}
		if left == 0 && strings.Contains(page, "left. Use") {
			t.Fatalf("nothing is left after page %d, so the note must be gone:\n%s", pages, page)
		}

		// Fix the page the way the model would: from the code it just handed over, through a
		// surface that re-syncs the topology. Writing the file behind aracne's back would leave
		// the warnings standing, which is a statement about the index, not about this feature.
		var edits []string
		for _, name := range shown {
			edits = append(edits, `{"file_path":"main.go",`+
				`"old_string":"func `+name+`() int { return lib.Add(1, 2) }",`+
				`"new_string":"func `+name+`() int { return lib.Add(1, 2, 3) }"}`)
		}
		runCLIWithStdin(t, dir, `{"edits":[`+strings.Join(edits, ",")+`]}`, RunEdit)
		remaining = remaining[len(shown):]
	}
	if pages < 2 {
		t.Fatalf("the budget held everything in one page; the test did not exercise paging")
	}
}

// The note on the post-edit report is the same note, and it is what points at the command
// above. When everything fits there is nothing left, so there is no note.
func TestWarningsLeftNoteOnlyAppearsWhenTheBudgetBit(t *testing.T) {
	callers := []string{"CallA", "CallB", "CallC"}
	full := breakAdd(t, warnReadProject(t,
		`{"features":{"warning_reads":true,"warning_read_max_bytes":0}}`, callers...))
	over := warnReadProject(t,
		fmt.Sprintf(`{"features":{"warning_reads":true,"warning_read_max_bytes":%d}}`, len(full)-40), callers...)
	if printed := breakAdd(t, over); !strings.Contains(printed,
		"left. Use `arac warnings list --read` to continue fixing.") {
		t.Fatalf("the report did not point at the continuation:\n%s", printed)
	}

	under := warnReadProject(t, `{"features":{"warning_reads":true}}`, "CallA")
	if printed := breakAdd(t, under); strings.Contains(printed, "warning left") {
		t.Fatalf("everything fits the default budget, so nothing should be left:\n%s", printed)
	}
}

// The note names the surface THIS project has. In ModeMCP the model is served warnings_list as
// a tool and pointing it at a shell command would be pointing away from its own toolbox.
func TestWarningsLeftNoteNamesTheProjectsSurface(t *testing.T) {
	cli := &helper.Config{Mode: helper.ModeCLI}
	if got := warnread.LeftNote(cli, 3); !strings.Contains(got, "`arac warnings list --read`") {
		t.Errorf("cli mode note = %q, want the CLI verb", got)
	}
	mcp := &helper.Config{Mode: helper.ModeMCP}
	if got := warnread.LeftNote(mcp, 3); !strings.Contains(got, "`warnings_list` with `read: true`") {
		t.Errorf("mcp mode note = %q, want the tool", got)
	}
	if got := warnread.LeftNote(cli, 1); !strings.Contains(got, "1 warning left") {
		t.Errorf("note = %q, want a singular noun", got)
	}
	if got := warnread.LeftNote(cli, 0); got != "" {
		t.Errorf("nothing is left, so the note must be empty, got %q", got)
	}
}

// The tool reads its own config, so the `read` property is out of the schema wherever the
// project did not turn the feature on -- including a nil config.
func TestWarningsListToolOffersReadOnlyWhenEnabled(t *testing.T) {
	dir := warnReadProject(t, `{"features":{"warning_reads":true}}`, "CallA")
	breakAdd(t, dir) // builds the topology

	dbPath := filepath.Join(dir, ".aracne", "topology.db")
	hasRead := func(cfg *helper.Config) bool {
		mgr, _ := InitRegistry(dbPath)
		for _, p := range tools.NewWarningsList(mgr, cfg, NewScannerRegistry()).Parameters() {
			if p.Name == "read" {
				return true
			}
		}
		return false
	}

	on := helper.LoadConfig(helper.ConfigPath(dbPath))
	if !on.WarningReadsEnabled() {
		t.Fatal("precondition: the fixture did not enable the feature")
	}
	if !hasRead(on) {
		t.Error("features.warning_reads is on and the tool does not offer read")
	}

	off := helper.DefaultConfig()
	disabled := false
	off.Features.WarningReads = &disabled
	if hasRead(off) {
		t.Error("features.warning_reads is off and the tool offers read anyway")
	}
	if hasRead(nil) {
		t.Error("a nil config must not be read as an enabled feature")
	}
}

// The tool's `read` runs the SAME expansion the post-edit report and `--read` run -- one
// behaviour, three surfaces. If they diverge, a warning reads differently depending on how it
// was asked for, and the note pointing between them starts lying.
func TestWarningsListToolReadMatchesTheOtherSurfaces(t *testing.T) {
	dir := warnReadProject(t, `{"features":{"warning_reads":true,"warning_read_max_bytes":0}}`,
		"CallA", "CallB", "CallC")
	breakAdd(t, dir)
	// Short of the whole report, so the shared expansion has to page and print its note.
	setWarningBudget(t, dir, len(runWarningsList(t, dir, "--read"))-40)

	dbPath := filepath.Join(dir, ".aracne", "topology.db")
	cfg := helper.LoadConfig(helper.ConfigPath(dbPath))
	mgr, _ := InitRegistry(dbPath)
	tool := tools.NewWarningsList(mgr, cfg, NewScannerRegistry())

	withRead, err := tool.Run([]byte(`{"read":true}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	warnings, err := mgr.ListWarnings("", "", domain.WarningKind(""))
	if err != nil {
		t.Fatal(err)
	}
	direct := warnread.Section(dbPath, NewScannerRegistry(), warnings, warnread.Options{
		Budget:   warnread.NoBudget,
		Headline: warnread.HeadlinePull,
		Reserve:  1, // the tool's trailing newline, as the tool itself reserves it
	})
	if direct == "" {
		t.Fatal("precondition: the shared expansion produced nothing")
	}
	if !strings.Contains(withRead, direct) {
		t.Fatalf("the tool's read is not the shared expansion.\ntool:\n%s\nshared:\n%s", withRead, direct)
	}
	// Including the note -- the tool is in a cli-mode project, so it points at the CLI verb.
	if !strings.Contains(withRead, "left. Use `arac warnings list --read` to continue fixing.") {
		t.Fatalf("the tool's read dropped the continuation note:\n%s", withRead)
	}

	without, err := tool.Run([]byte(`{}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(without, warnread.HeadlinePull) {
		t.Fatalf("the tool expanded without read=true:\n%s", without)
	}
}

// An unrecognized argument must FAIL, for the reason --kind is validated: the failure mode is
// silence. `arac warnings list --raed` printed the listing, ignored the flag and exited 0 --
// which reads as "the flag ran and found nothing", and cost a real debugging session when
// `--read` was typed at a binary that predated the flag.
//
// Driven as a subprocess because the failure is an os.Exit(1), which a normal call would take
// the test binary down with.
func TestWarningsListRejectsAnUnknownFlag(t *testing.T) {
	if os.Getenv("ARAC_WARNINGS_FLAG_CHILD") == "1" {
		RunWarningsList([]string{"--raed"})
		return
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=TestWarningsListRejectsAnUnknownFlag")
	cmd.Env = append(os.Environ(), "ARAC_WARNINGS_FLAG_CHILD=1")
	cmd.Dir = warnReadProject(t, "", "CallA")
	out, err := cmd.CombinedOutput()

	if err == nil {
		t.Fatalf("an unknown flag exited 0:\n%s", out)
	}
	if !strings.Contains(string(out), `unknown argument "--raed"`) {
		t.Errorf("the error does not name the argument:\n%s", out)
	}
	// And the usage line has to carry the real flags, or the message is a dead end.
	if !strings.Contains(string(out), "--read") {
		t.Errorf("the usage line does not list the flags:\n%s", out)
	}
}
