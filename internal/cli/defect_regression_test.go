package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// TestUpdateFileHookSpeaksTheHookProtocol pins AR-09.
//
// Two PostToolUse hooks ship and they used different output protocols. The guard emits
// hookSpecificOutput.additionalContext as JSON; `arac update-file --claude-hook` wrote plain
// text to stdout, which a PostToolUse hook that exits 0 has treated as transcript material
// rather than context. So the topology warnings raised by a NATIVE Edit/Write -- the one path
// this hook exists to cover, and the one where the model has no other signal -- were
// formatted, written, and dropped.
func TestUpdateFileHookSpeaksTheHookProtocol(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte("package p\n\nfunc F() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module p\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	event := `{"tool_name":"Edit","tool_input":{"file_path":"` + src + `"}}`
	var out bytes.Buffer
	runClaudeUpdateFileHook(strings.NewReader(event), &out)

	if out.Len() == 0 {
		return // nothing to report is a legitimate answer; the protocol only matters when there is
	}
	var decoded map[string]any
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("hook output must be JSON the harness can read, got:\n%s", out.String())
	}
	hook, ok := decoded["hookSpecificOutput"].(map[string]any)
	if !ok {
		t.Fatalf("hook output must carry hookSpecificOutput, got:\n%s", out.String())
	}
	if hook["hookEventName"] != "PostToolUse" {
		t.Fatalf("hookEventName = %v, want PostToolUse", hook["hookEventName"])
	}
	if _, ok := hook["additionalContext"]; !ok {
		t.Fatalf("warnings must reach the model as additionalContext, got:\n%s", out.String())
	}
}

// TestGuardHooksRunAnAbsoluteAracPath pins AR-22.
//
// The generated scripts hard-coded the bare name `arac` and hoped it was on PATH, though
// `arac setup` is the one moment where the binary's own location is known for certain. A build
// kept at ./bin/arac, a Homebrew shell a GUI-launched editor does not inherit, or a login PATH
// the harness does not share turned every tool call into a failing hook.
func TestGuardHooksRunAnAbsoluteAracPath(t *testing.T) {
	for name, script := range map[string]string{
		"guard":       claudeGuardHookShellScript(),
		"update-file": claudeUpdateFileHookShellScript(),
	} {
		if strings.Contains(script, "exec arac ") {
			t.Errorf("%s hook still runs a bare `arac`:\n%s", name, script)
		}
		if !strings.Contains(script, aracBinary()) {
			t.Errorf("%s hook should run the resolved binary %q:\n%s", name, aracBinary(), script)
		}
	}
}

// TestWarnStateSurvivesAnUnparseableRecord pins AR-16.
//
// readReportedWarnings treated a present-but-unparseable record as "never seeded", and
// seedReportedWarnings reads that as licence to mark every warning standing at that moment as
// already delivered -- permanently suppressing the one channel the design says has no
// substitute. A torn write is exactly how that file becomes unparseable, which is why it is
// also written atomically now.
func TestWarnStateSurvivesAnUnparseableRecord(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, ".aracne", "topology.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(warnStateFile(dbPath), []byte("{tru"), 0o644); err != nil {
		t.Fatal(err)
	}

	seen, seeded := readReportedWarnings(dbPath)
	if !seeded {
		t.Fatal("a record that exists but cannot be parsed must count as seeded, not as absent")
	}
	if len(seen) != 0 {
		t.Fatalf("an unparseable record carries no ids, got %v", seen)
	}
}

// TestWarnStateIsWrittenAtomically: the record has more than one writer (hooks for concurrent
// tool calls are separate processes), so it must never be observed half-written.
func TestWarnStateIsWrittenAtomically(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, ".aracne", "topology.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	writeReportedWarnings(dbPath, map[string]bool{"b": true, "a": true})

	raw, err := os.ReadFile(warnStateFile(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	if err := json.Unmarshal(raw, &ids); err != nil {
		t.Fatalf("the record must be valid JSON: %v (%s)", err, raw)
	}
	if len(ids) != 2 || ids[0] != "a" || ids[1] != "b" {
		t.Fatalf("the record should be sorted so it is diffable, got %v", ids)
	}

	// No temp files left behind next to it.
	entries, _ := os.ReadDir(filepath.Dir(dbPath))
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("atomic write left a temp file behind: %s", e.Name())
		}
	}
}

// TestVizServeResolvesTheProjectDatabase pins AR-13: viz was the one verb that passed its raw
// flag default straight through, so started from a subdirectory it served the SPA happily and
// answered every API call with "unable to open database file".
func TestVizServeResolvesTheProjectDatabase(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, DefaultDBRelative)
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dbPath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "internal", "cli")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	cwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(sub); err != nil {
		t.Fatal(err)
	}

	got := ProjectDBPath(DefaultDBRelative)
	if got == DefaultDBRelative {
		t.Fatal("the default database path must walk up to the project root from a subdirectory")
	}
	if _, err := os.Stat(got); err != nil {
		t.Fatalf("resolved path %q does not exist: %v", got, err)
	}
}

// TestCmdRefusesACommandTheShellCouldNotRun pins the "manufactured capability" defect.
//
// `arac cmd` answers IN PLACE OF the real command, and the safety argument for interception is
// that whatever aracne does not model runs exactly as it would have. Serving a command whose
// binary is not installed inverts that: on a box without ripgrep, `rg foo` came back as a full
// annotated search with exit 0, so the model learned ripgrep was available -- and the next `rg`
// carrying a flag shellcmd does not model (`-o`, `--files`, `--hidden`) fell through to
// passthrough and returned "rg: command not found", exit 127.
func TestCmdRefusesACommandTheShellCouldNotRun(t *testing.T) {
	// An empty PATH is the hermetic way to say "this command is not installed" for a name
	// shellcmd genuinely models; a made-up name would be KindPassthrough anyway and would
	// never reach the gate.
	t.Setenv("PATH", "")

	for _, argv := range [][]string{
		{"rg", "needle"},
		{"grep", "-rn", "needle", "."},
		{"bat", "main.go"},
		{"cat", "main.go"},
	} {
		if _, _, _, ok := serveCommand(argv, false); ok {
			t.Errorf("serveCommand(%v) served a command with no binary on PATH", argv)
		}
	}
}

// TestCommandAvailabilityExemptsShellResolvedCommands pins the other half of the same gate.
//
// Get-Content is the PowerShell reader shellcmd models, and it is a cmdlet: there is no
// executable of that name on any machine. Judging it by LookPath would refuse the one read
// shape the PowerShell surface has, and hand it to a passthrough that cannot run it either.
func TestCommandAvailabilityExemptsShellResolvedCommands(t *testing.T) {
	t.Setenv("PATH", "")

	if !commandIsAvailable("Get-Content") {
		t.Error("Get-Content is a cmdlet, not a PATH lookup -- it must stay servable")
	}
	if commandIsAvailable("definitely-not-installed-9f3a") {
		t.Error("a binary that is not on PATH must not be reported as available")
	}
	exe, err := os.Executable()
	if err == nil && !commandIsAvailable(exe) {
		t.Errorf("an absolute path to a real executable must be available: %s", exe)
	}
}

// TestDriftCheckCoversNativeEdits pins the missing-warning defect.
//
// The drift check used to fire for Bash alone, on the reasoning that the `arac update-file`
// hook covers a native Edit/Write. That hook is installed only when the project lists the
// edit-update-db-plugin, and no shipped path lists it -- the default config sets no plugins and
// `arac init` never asks. So a native edit produced no warning at the moment it broke a caller,
// and the warning the next PreToolUse scan discovered was delivered by whichever later Bash
// command happened to be unclassified, and attributed to that.
func TestDriftCheckCoversNativeEdits(t *testing.T) {
	for _, tool := range []string{"Edit", "Write", "MultiEdit", "NotebookEdit"} {
		if !driftCheckApplies(tool, []string{"edit"}, nil) {
			t.Errorf("%s must earn a drift check: nothing else reports its warnings", tool)
		}
	}
	// Reads and searches change nothing, so re-scanning after them is pure cost.
	for _, tool := range []string{"Read", "Grep"} {
		if driftCheckApplies(tool, []string{"read"}, nil) {
			t.Errorf("%s changes no source and must not trigger a scan", tool)
		}
	}
	// Bash keeps its own rule: a recognized write, or nothing recognized at all.
	if !driftCheckApplies("Bash", []string{"bash"}, map[string]interface{}{"command": "go build ./..."}) {
		t.Error("an unclassified Bash command is exactly what the backstop is for")
	}
	if driftCheckApplies("Bash", []string{"read", "bash"}, map[string]interface{}{"command": "cat f.go"}) {
		t.Error("a classified Bash read must not trigger a scan")
	}
	if driftCheckApplies("Bash", []string{"bash"}, map[string]interface{}{"command": "arac bug list"}) {
		t.Error("arac's own CLI already syncs the topology and must stay exempt")
	}
}

// TestDriftWarningsDoNotBlameAShellCommand pins the false-attribution defect.
//
// The header asserted "Topology re-synced after a shell command wrote to the project", which the
// renderer is in no position to claim: the warnings come from unreportedWarnings, which reports
// what is NEW IN THE TABLE however it got there. Measured on a real fixture, the line arrived
// attached to an `ls` that had written nothing.
func TestDriftWarningsDoNotBlameAShellCommand(t *testing.T) {
	msg := formatDriftWarnings([]domain.TopologyWarning{{
		Kind: domain.WarnSignatureChanged, Message: "m", SourceID: "s", TargetID: "t",
	}})
	if msg == "" {
		t.Fatal("a warning must still render")
	}
	if strings.Contains(msg, "shell command") {
		t.Errorf("the drift header must not claim a cause it cannot know:\n%s", msg)
	}
}

// TestUpdateFileHookNeverBuildsATopology pins the stray-database defect.
//
// The hook resolved its database with ProjectDBPath -- an upward walk from the process working
// directory, which is the one thing a hook cannot depend on. Run from anywhere outside the tree,
// InitRegistry did not bail: it CREATED a `.aracne/` there, printed "No topology found.
// Scanning project..." and exited 1, leaving the real topology unsynced and junk behind.
func TestUpdateFileHookNeverBuildsATopology(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", "")

	var out bytes.Buffer
	runClaudeUpdateFileHook(strings.NewReader(
		`{"tool_name":"Edit","cwd":"`+dir+`","tool_input":{"file_path":"`+filepath.Join(dir, "x.go")+`"}}`), &out)

	if _, err := os.Stat(filepath.Join(dir, ".aracne")); err == nil {
		t.Fatal("a hook with no topology has nothing to do, and must not create one")
	}
	if out.Len() != 0 {
		t.Fatalf("nothing to sync should say nothing, got:\n%s", out.String())
	}
}

// TestNudgeRequiresSomethingToOffer pins the unindexed-nudge defect.
//
// namesAnIndexedFile existed, carried a doc comment describing the exact regression it was
// written to prevent, and had NO CALLERS -- so the PostToolUse pointer fired on every native
// Read, including files the topology holds nothing for. It told the model that "`cat` on an
// indexed file is answered from the topology" about a CHANGELOG, a lockfile, a template: cost
// on every call, teaching a rule that fails the next time it is tried.
func TestNudgeRequiresSomethingToOffer(t *testing.T) {
	dir := t.TempDir()
	tracked := filepath.Join(dir, "main.go")
	if err := os.WriteFile(tracked, []byte("package p\n\nfunc F() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	untracked := filepath.Join(dir, "CHANGELOG.md")
	if err := os.WriteFile(untracked, []byte("# Changelog\n\n- a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module p\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", dir)
	RunScan(nil)

	dbPath := guardDBPath(dir)
	if !namesAnIndexedFile([]string{tracked}, dbPath) {
		t.Fatal("fixture is wrong: main.go should be indexed")
	}

	// A read of a file with no nodes buys nothing, so it earns no pointer.
	if worthNudging(map[string]interface{}{"file_path": untracked}, dbPath) {
		t.Error("a read of an unindexed file must not be nudged: aracne cannot answer it better")
	}
	if !worthNudging(map[string]interface{}{"file_path": tracked}, dbPath) {
		t.Error("a read of an indexed file is exactly what the pointer is for")
	}
	// A search names a pattern, not a path: with nothing to resolve there is no
	// counter-evidence and the guidance holds for the tree.
	if !worthNudging(map[string]interface{}{"pattern": "needle"}, dbPath) {
		t.Error("a search with no path operand must keep its pointer")
	}
	// A MUTATION earns no pointer at all any more, whatever the index says: an edit reports
	// its own topology warnings, so `arac edit` guidance beside them is a second spelling of
	// what the model just got. See nudgeKeys.
	if len(nudgeKeys([]string{"edit"}, false)) != 0 {
		t.Error("an edit must not be nudged: it reports its own warnings")
	}
	if len(nudgeKeys([]string{"write"}, false)) != 0 {
		t.Error("a write must not be nudged: it reports its own warnings")
	}
}

// TestDisableRemovesABlockMissingItsClosingLine pins the "already clean" defect.
//
// The generated block has no delimiter -- it is found by its opening heading and by whichever
// real closing sentence the contract ends on. `arac setup` had a fallback for a reader who
// edited that sentence away; `arac disable` did not, and returned the content unchanged while
// printing "already clean". Both now ask aracIntegrationBounds.
//
// The block is a REAL contract with its closing line removed: since aracne stopped taking any
// `# Aracne` heading as its own (a team's own section by that name was being deleted), a block
// is recognised by the contract's opening prose, and placeholder prose is correctly left alone.
func TestDisableRemovesABlockMissingItsClosingLine(t *testing.T) {
	contract := strings.Replace(contractFor(helper.ModeMCP), AracIntegrationEnd+"\n", "", 1)
	doc := "# Mine\n\nBefore.\n\n" + contract
	if got := stripAracneIntegrationSegment(doc); strings.Contains(got, "pre-analyzed graph") {
		t.Fatalf("disable left the contract behind:\n%s", got)
	}
	// What the reader wrote survives.
	got := stripAracneIntegrationSegment(doc)
	if !strings.Contains(got, "# Mine") || !strings.Contains(got, "Before.") {
		t.Fatalf("disable took the reader's own content with it:\n%s", got)
	}
	// And the writer replaces the same span rather than stacking a second copy above it.
	updated := updateMarkdownIntegrationSegment(doc, "# Aracne\n\nfresh\n\nGood Luck in your task.\n")
	if strings.Count(updated, "# Aracne") != 1 {
		t.Fatalf("setup stacked a second contract:\n%s", updated)
	}
}

// TestGeneratedMCPEntriesRunTheResolvedBinary pins the bare-`arac` defect.
//
// aracBinary exists because `arac setup` is the one moment the binary's location is known for
// certain, and its comment lists what a bare name costs: a build kept at ./bin/arac, a Homebrew
// install whose shell a GUI-launched editor does not inherit, a login PATH the harness does not
// share. The hooks were fixed; the three MCP configuration sites were not, and an MCP server
// that fails to start is quiet in both harnesses -- the tool list is simply short.
func TestGeneratedMCPEntriesRunTheResolvedBinary(t *testing.T) {
	frontmatter := claudeMCPServersFrontmatter("descriptions-generation-executor")
	if strings.Contains(frontmatter, "command: arac\n") {
		t.Errorf("sub-agent frontmatter still runs a bare `arac`:\n%s", frontmatter)
	}
	// strconv.Quote is what writes it (claudeMCPServersFrontmatter), so the quoted form is
	// what is in there: on Windows every separator in the path is escaped, and searching for
	// the raw spelling finds nothing.
	if want := strconv.Quote(aracBinary()); !strings.Contains(frontmatter, want) {
		t.Errorf("sub-agent frontmatter should run %s:\n%s", want, frontmatter)
	}
}

// TestProxyReadDoesNotWaitOnDescriptions pins the timeout inversion.
//
// NewRead attaches a descriptions filler, and the filler used to be awaited INLINE for up to
// descriptions.lazy.timeout_seconds -- 45s by default, far longer than proxyReadTimeout's 5s. On
// a repository whose descriptions are not yet written (a fresh install, exactly when the filler
// works hardest) the proxy reliably gave up and the model got a bare pointer instead of the
// file: the two-turns-for-one-question failure the proxy exists to end. The fix at the time was
// to attach no filler here at all.
//
// Generation is detached now, so a fill costs a claim and a spawn and then only WAITS -- and the
// proxy waits on its own budget rather than the read's. The invariant is therefore no longer
// "attach nothing"; it is that whatever this path waits for cannot outlast the path itself. That
// is what is pinned here, because getting it wrong reintroduces the original failure exactly.
func TestProxyReadDoesNotWaitOnDescriptions(t *testing.T) {
	if proxyFillWait >= proxyReadTimeout {
		t.Fatalf("a fill may wait %s inside a proxy budget of %s: the proxy would give up and "+
			"the model would get a bare pointer instead of the file",
			proxyFillWait, proxyReadTimeout)
	}
	src, err := os.ReadFile("guard_proxy.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	if strings.Contains(body, "WithFiller(nil)") {
		return // attaching nothing is still a correct answer
	}
	// Anything else must be the BOUNDED constructor. A plain lazydesc.New here would take the
	// read's 45s wait, which is the inversion this test exists to catch.
	if !strings.Contains(body, "lazydesc.NewBounded(") || !strings.Contains(body, "proxyFillWait") {
		t.Fatal("proxyRead must attach either no filler or one bounded by proxyFillWait; " +
			"an unbounded filler here waits far longer than the proxy is allowed to")
	}
}
