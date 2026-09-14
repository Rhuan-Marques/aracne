package cli

import (
	"os"
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
	if !strings.Contains(printed, "Warned code, read in full") {
		t.Fatalf("--read printed no expansion:\n%s", printed)
	}
	if !strings.Contains(printed, "func CallA() int { return lib.Add(1, 2) }") {
		t.Fatalf("--read did not print the caller's source:\n%s", printed)
	}
}

// Without the flag the command is exactly what it always was. A listing that started dumping
// source unasked would break every script that parses it.
func TestWarningsListWithoutReadIsUnchanged(t *testing.T) {
	dir := warnReadProject(t, `{"features":{"warning_reads":true}}`, "CallA")
	breakAdd(t, dir)

	printed := runWarningsList(t, dir)
	if strings.Contains(printed, "Warned code") {
		t.Fatalf("the plain listing expanded anything at all:\n%s", printed)
	}
	if !strings.Contains(printed, "signature_changed") {
		t.Fatalf("the plain listing lost its warnings:\n%s", printed)
	}
}

// A flag that silently does nothing is worse than no flag: the caller cannot tell "nothing to
// read" from "this project has the feature off".
func TestWarningsListReadSaysSoWhenTheFeatureIsOff(t *testing.T) {
	dir := warnReadProject(t, "", "CallA")
	breakAdd(t, dir)

	printed := runWarningsList(t, dir, "--read")
	if strings.Contains(printed, "Warned code") {
		t.Fatalf("--read expanded with features.warning_reads off:\n%s", printed)
	}
	if !strings.Contains(printed, "features.warning_reads is off") {
		t.Fatalf("--read did nothing and did not say why:\n%s", printed)
	}
}

// THE LOOP, end to end, and the claim the note makes. Seven warnings, a cap of five: the
// listing expands five and says two are left; fixing those five and asking again expands the
// other two and says nothing is left.
//
// No cursor is stored anywhere, deliberately -- the page advances because a FIXED warning has
// retired itself from the table. A remembered page would have shown warnings 6-7 while 1-5 were
// still broken.
func TestWarningsListReadAdvancesAsWarningsAreFixed(t *testing.T) {
	callers := []string{"CallA", "CallB", "CallC", "CallD", "CallE", "CallF", "CallG"}
	dir := warnReadProject(t, `{"features":{"warning_reads":true,"warning_read_limit":5}}`, callers...)
	breakAdd(t, dir)

	first := runWarningsList(t, dir, "--read")
	for _, want := range callers[:5] {
		if !strings.Contains(first, "func "+want+"() int") {
			t.Fatalf("%s is in the first page and was not expanded:\n%s", want, first)
		}
	}
	for _, unwanted := range callers[5:] {
		if strings.Contains(first, "func "+unwanted+"() int") {
			t.Fatalf("%s is past the cap and was expanded anyway:\n%s", unwanted, first)
		}
	}
	if !strings.Contains(first, "... 2 warnings left. Use `arac warnings list --read` to continue fixing.") {
		t.Fatalf("the first page did not say what was left, or how to get it:\n%s", first)
	}

	// Fix the first five the way the model would: from the code the page just handed over,
	// through a surface that re-syncs the topology. Writing the file behind aracne's back
	// would leave the warnings standing, which is a statement about the index rather than
	// about this feature.
	var edits []string
	for _, name := range callers[:5] {
		edits = append(edits, `{"file_path":"main.go",`+
			`"old_string":"func `+name+`() int { return lib.Add(1, 2) }",`+
			`"new_string":"func `+name+`() int { return lib.Add(1, 2, 3) }"}`)
	}
	runCLIWithStdin(t, dir, `{"edits":[`+strings.Join(edits, ",")+`]}`, RunEdit)

	second := runWarningsList(t, dir, "--read")
	for _, want := range callers[5:] {
		if !strings.Contains(second, "func "+want+"() int") {
			t.Fatalf("%s is the next page and was not expanded:\n%s", want, second)
		}
	}
	for _, fixedName := range callers[:5] {
		if strings.Contains(second, "func "+fixedName+"() int") {
			t.Fatalf("%s was fixed and came back on the next page:\n%s", fixedName, second)
		}
	}
	if strings.Contains(second, "warnings left") {
		t.Fatalf("nothing is left, so the note must be gone:\n%s", second)
	}
}

// The note on the post-edit report is the same note, and it is what points at the command
// above. Under the cap there is nothing left, so there is no note.
func TestWarningsLeftNoteOnlyAppearsWhenTheCapBit(t *testing.T) {
	over := warnReadProject(t, `{"features":{"warning_reads":true,"warning_read_limit":2}}`,
		"CallA", "CallB", "CallC")
	if printed := breakAdd(t, over); !strings.Contains(printed,
		"... 1 warning left. Use `arac warnings list --read` to continue fixing.") {
		t.Fatalf("the report did not point at the continuation, or miscounted:\n%s", printed)
	}

	under := warnReadProject(t, `{"features":{"warning_reads":true,"warning_read_limit":5}}`, "CallA")
	if printed := breakAdd(t, under); strings.Contains(printed, "warning left") {
		t.Fatalf("everything was expanded, so nothing should be left:\n%s", printed)
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
	dir := warnReadProject(t, `{"features":{"warning_reads":true,"warning_read_limit":2}}`,
		"CallA", "CallB", "CallC")
	breakAdd(t, dir)

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
	direct := warnread.Section(dbPath, NewScannerRegistry(), warnings, warnread.NoBudget)
	if direct == "" {
		t.Fatal("precondition: the shared expansion produced nothing")
	}
	if !strings.Contains(withRead, direct) {
		t.Fatalf("the tool's read is not the shared expansion.\ntool:\n%s\nshared:\n%s", withRead, direct)
	}
	// Including the note -- the tool is in a cli-mode project, so it points at the CLI verb.
	if !strings.Contains(withRead, "... 1 warning left. Use `arac warnings list --read` to continue fixing.") {
		t.Fatalf("the tool's read dropped the continuation note:\n%s", withRead)
	}

	without, err := tool.Run([]byte(`{}`))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(without, "Warned code") {
		t.Fatalf("the tool expanded without read=true:\n%s", without)
	}
}
