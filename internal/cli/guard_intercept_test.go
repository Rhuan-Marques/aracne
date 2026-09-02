package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"aracne/internal/helper"
)

// terminalConfig is the shipped default: the terminal surface, interception on.
func terminalConfig() *helper.Config { return helper.DefaultConfig() }

// rewriteOf runs interceptCommand against a real scanned project and returns the rewritten
// command, or "" when the call was left alone.
func rewriteOf(t *testing.T, dbPath, command string) string {
	t.Helper()
	out, ok := interceptCommand(command, dbPath, terminalConfig())
	if !ok {
		return ""
	}
	return out
}

// The commands interception exists for. Each must come back prefixed with `arac cmd --` and
// the ORIGINAL text intact -- the original, because it is the only spelling guaranteed to
// re-split the way the shell already split it.
func TestModelledReadsAndSearchesAreRewritten(t *testing.T) {
	root, dbPath := scannedProject(t)
	app := filepath.Join(root, "app.go")

	for _, cmd := range []string{
		"head -20 " + app,
		"tail -5 " + app,
		"cat " + app,
		"sed -n '1,10p' " + app,
		"awk 'NR>=2 && NR<=4' " + app,
		"grep -rn Serve " + app,
		// A resource ID is not a path at all, so no plain command could ever answer it. It is
		// also the form the contract pushes the model toward, so it must intercept.
		"head -10 example.com/proj.Serve",
	} {
		got := rewriteOf(t, dbPath, cmd)
		if got == "" {
			t.Errorf("%q was not intercepted", cmd)
			continue
		}
		if !strings.Contains(got, " cmd -- "+cmd) {
			t.Errorf("%q rewrote to %q, want the original text after `cmd --`", cmd, got)
		}
	}
}

// Everything the hook must leave completely alone. Each row is a way a rewrite would change
// what the command means, or would fire for no gain.
func TestCommandsThatMustNotBeRewritten(t *testing.T) {
	root, dbPath := scannedProject(t)
	app := filepath.Join(root, "app.go")

	for _, tc := range []struct{ cmd, why string }{
		{"cat " + app + " | head -5", "a pipeline: the prefix would capture only the producer"},
		{"head -5 " + app + " > /tmp/out", "a redirect belongs to the original command"},
		{"sed -i 's/a/b/' " + app, "a mutation, never a read"},
		{"xxd " + app, "an encoding view, not a line window"},
		{"nl " + app, "a rendering, not a line window"},
		{"tail -f " + app, "follows a growing file"},
		{"head -c 20 " + app, "counts bytes, not lines"},
		{"ls -la", "not a read at all"},
		{"go build ./...", "not a read at all"},
		{"head -5 " + filepath.Join(root, "CHANGELOG.md"), "indexed by nothing: aracne would return the same bytes"},
		{"head -5 /etc/hosts", "outside the project"},
		{"FOO=1 head -5 " + app, "an env prefix: the rewrite would front the assignment"},
		{"sudo head -5 " + app, "a wrapper: the rewrite would front sudo"},
		{"head -5 " + app + "\nhead -5 " + app, "two lines are two commands"},
	} {
		if got := rewriteOf(t, dbPath, tc.cmd); got != "" {
			t.Errorf("%q was rewritten to %q -- must be left alone (%s)", tc.cmd, got, tc.why)
		}
	}
}

// The shapes a real transcript is actually made of. Measured over the control arm of
// fair-20260901a, 256 of 462 commands were compound, and the two shapes aracne is good at --
// a whole-file read and a search -- were mostly inside them. Each of these must have the
// prefix spliced into the READ segment and every other byte left alone.
func TestCompoundCommandsAreRewrittenSegmentWise(t *testing.T) {
	root, dbPath := scannedProject(t)
	app := filepath.Join(root, "app.go")

	for _, tc := range []struct{ cmd, wantPrefixedBefore string }{
		{"cd " + root + " && cat " + app, "cat "},
		{"ls " + root + " && sed -n '1,20p' " + app, "sed "},
		{"grep -rn Serve " + root + " | head -30", "grep "},
		{"sed -n '1,5p' " + app + "; sed -n '10,20p' " + app, "sed "},
	} {
		got := rewriteOf(t, dbPath, tc.cmd)
		if got == "" {
			t.Errorf("%q was not intercepted", tc.cmd)
			continue
		}
		if !strings.Contains(got, "cmd -- "+tc.wantPrefixedBefore) {
			t.Errorf("%q: prefix not spliced before %q:\n%s", tc.cmd, tc.wantPrefixedBefore, got)
		}
		// Everything the model wrote must still be there, in order.
		stripped := strings.ReplaceAll(got, "cmd -- ", "")
		for _, piece := range strings.Fields(tc.cmd) {
			if !strings.Contains(stripped, piece) {
				t.Errorf("%q: rewrite lost %q:\n%s", tc.cmd, piece, got)
			}
		}
	}

	// Both halves of a chain of two reads get their own prefix.
	two := rewriteOf(t, dbPath, "cat "+app+" && cat "+filepath.Join(root, "app.go"))
	if strings.Count(two, "cmd -- ") != 2 {
		t.Errorf("a chain of two reads should be rewritten twice:\n%s", two)
	}
}

// A pipe consumer decides whether the producer may be rewritten, and the rule is about what
// the consumer does to the bytes -- not about pipes as such.
func TestOnlySearchesAreRewrittenUpstreamOfAPipe(t *testing.T) {
	root, dbPath := scannedProject(t)
	app := filepath.Join(root, "app.go")

	if got := rewriteOf(t, dbPath, "grep -rn Serve "+root+" | head -30"); got == "" {
		t.Error("`grep … | head` is the most common search shape and must intercept")
	}
	for _, tc := range []struct{ cmd, why string }{
		{"cat " + app + " | grep Serve", "the consumer would search aracne's rendering, not the file"},
		{"grep -rn Serve " + root + " | wc -l", "aracne's annotated rows would give a different count"},
		{"grep -rn Serve " + root + " | sort -u", "a transform, not a cap"},
		{"cat " + app + " | head -30", "a read piped to a pager is a window; leave it to the plain command"},
	} {
		if got := rewriteOf(t, dbPath, tc.cmd); got != "" {
			t.Errorf("%q was rewritten (%s):\n%s", tc.cmd, tc.why, got)
		}
	}
}

// The rewrite is itself a Bash command containing a read command word. Without an explicit
// stop the hook rewrites its own rewrite on the next call, forever.
func TestARewriteIsNeverRewrittenAgain(t *testing.T) {
	root, dbPath := scannedProject(t)
	first := rewriteOf(t, dbPath, "head -20 "+filepath.Join(root, "app.go"))
	if first == "" {
		t.Fatal("fixture did not intercept the first command")
	}
	if second := rewriteOf(t, dbPath, first); second != "" {
		t.Fatalf("rewrote a rewrite: %q -> %q", first, second)
	}
}

// A quoted operand must survive: the rewrite prefixes the original text, which the shell then
// re-splits with its own quoting rules.
func TestQuotingSurvivesTheRewrite(t *testing.T) {
	root, dbPath := scannedProject(t)
	// A quoted sed script is the case that broke naive re-tokenizing: the classifier has to
	// see `1,10p` as one word to recognize the window at all.
	cmd := "sed -n '1,10p' " + filepath.Join(root, "app.go")
	got := rewriteOf(t, dbPath, cmd)
	if !strings.HasSuffix(got, cmd) {
		t.Fatalf("rewrote to %q, want it to end with the original %q", got, cmd)
	}
}

// Interception is off on the MCP surface, so a project that opted out keeps the denial
// behaviour it configured, unchanged.
func TestMCPModeNeverIntercepts(t *testing.T) {
	root, dbPath := scannedProject(t)
	cfg := helper.DefaultConfig()
	cfg.Integration.Mode = helper.IntegrationMCP
	if _, ok := interceptCommand("head -20 "+filepath.Join(root, "app.go"), dbPath, cfg); ok {
		t.Fatal("intercepted on the mcp surface")
	}
}

// The emitted event must carry the whole original tool_input with only `command` replaced:
// Claude Code validates updatedInput against the Bash tool's schema, and dropping a field the
// caller set silently changes how the command runs.
func TestRewriteEventPreservesTheRestOfTheInput(t *testing.T) {
	var buf bytes.Buffer
	emitPreToolRewrite(&buf, map[string]interface{}{
		"command":           "head -5 app.go",
		"description":       "peek",
		"run_in_background": false,
	}, "/usr/local/bin/arac cmd -- head -5 app.go")

	var got struct {
		HookSpecificOutput struct {
			HookEventName string                 `json:"hookEventName"`
			UpdatedInput  map[string]interface{} `json:"updatedInput"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("emitted invalid JSON: %v", err)
	}
	if got.HookSpecificOutput.HookEventName != "PreToolUse" {
		t.Errorf("hookEventName = %q", got.HookSpecificOutput.HookEventName)
	}
	in := got.HookSpecificOutput.UpdatedInput
	if in["command"] != "/usr/local/bin/arac cmd -- head -5 app.go" {
		t.Errorf("command = %v", in["command"])
	}
	if in["description"] != "peek" {
		t.Errorf("description was dropped: %v", in)
	}
	if _, ok := in["run_in_background"]; !ok {
		t.Errorf("run_in_background was dropped: %v", in)
	}
}

// A path with a space in it must not become two arguments.
func TestQuoteForShell(t *testing.T) {
	if got := quoteForShell("/usr/local/bin/arac"); got != "/usr/local/bin/arac" {
		t.Errorf("plain path was quoted: %q", got)
	}
	got := quoteForShell(`/c/Program Files/arac.exe`)
	if !strings.HasPrefix(got, "'") || !strings.HasSuffix(got, "'") {
		t.Errorf("path with a space was not quoted: %q", got)
	}
}

// blockedIn returns a config on the given surface with `keys` denied.
func blockedIn(mode string, keys ...string) *helper.Config {
	cfg := helper.DefaultConfig()
	cfg.Integration.Mode = mode
	agent := cfg.LLM.ClaudeCode.MainAgent
	agent.BlockedTools = keys
	cfg.LLM.ClaudeCode.MainAgent = agent
	return cfg
}

// denyReason runs the PreToolUse hook end to end and returns the denial text, or "" when the
// call was not denied. Going through the hook rather than decideGuard is the point: the ORDER
// of interception and denial is part of what is being asserted.
func denyReason(t *testing.T, cfg *helper.Config, dir, command string) string {
	t.Helper()
	cfgPath := helper.ConfigPath(filepath.Join(dir, ".aracne", "topology.db"))
	if err := helper.SaveConfig(cfg, cfgPath); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]interface{}{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Bash",
		"cwd":             dir,
		"tool_input":      map[string]interface{}{"command": command},
	})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	runClaudeGuardHook(bytes.NewReader(raw), &out)
	if out.Len() == 0 {
		return ""
	}
	var got struct {
		HookSpecificOutput struct {
			PermissionDecision string `json:"permissionDecision"`
			Reason             string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("hook emitted invalid JSON: %v\n%s", err, out.String())
	}
	if got.HookSpecificOutput.PermissionDecision != "deny" {
		return ""
	}
	return got.HookSpecificOutput.Reason
}

// blocked_tools has to keep working on the terminal surface. What changes is where the refusal
// SENDS the model: an `mcp__aracne__*` name is worse than useless to an agent with no MCP
// server, so the reason names the shell forms aracne answers and the `arac` subcommands.
func TestTerminalDenialsRedirectToTheShellSurface(t *testing.T) {
	root, _ := scannedProject(t)
	cfg := blockedIn(helper.IntegrationTerminal, "read", "grep")
	app := filepath.Join(root, "app.go")

	// `od -c` over two files: a read aracne models no spelling of and cannot proxy either,
	// so it is refused rather than served -- and the refusal is the only place the model
	// learns which spelling WOULD work.
	reason := denyReason(t, cfg, root, "od -c "+app+" "+filepath.Join(root, "CHANGELOG.md"))
	if reason == "" {
		t.Fatal("a blocked read was not denied on the terminal surface")
	}
	if strings.Contains(reason, "mcp__aracne__") {
		t.Errorf("terminal denial names an MCP tool that is not served:\n%s", reason)
	}
	for _, want := range []string{"arac read", "resource ID"} {
		if !strings.Contains(reason, want) {
			t.Errorf("terminal read denial does not mention %q:\n%s", want, reason)
		}
	}

	greason := denyReason(t, cfg, root, "grep -o Serve "+app)
	if greason == "" {
		t.Fatal("a blocked grep was not denied on the terminal surface")
	}
	if strings.Contains(greason, "mcp__aracne__") || !strings.Contains(greason, "arac grep") {
		t.Errorf("terminal grep denial should point at `arac grep`:\n%s", greason)
	}
}

// Interception runs first and wins. A rewrite hands the model the answer inside the call it
// already made; a denial costs it another turn for the same information.
func TestInterceptionBeatsADenialForTheSameCommand(t *testing.T) {
	root, _ := scannedProject(t)
	cfg := blockedIn(helper.IntegrationTerminal, "read", "grep")

	if reason := denyReason(t, cfg, root, "head -20 "+filepath.Join(root, "app.go")); reason != "" {
		t.Fatalf("a command aracne can serve was denied instead of rewritten:\n%s", reason)
	}
}

// The MCP surface keeps naming MCP tools -- that is where they exist.
func TestMCPDenialsStillNameTheMCPTool(t *testing.T) {
	root, _ := scannedProject(t)
	cfg := blockedIn(helper.IntegrationMCP, "grep")

	reason := denyReason(t, cfg, root, "grep -o Serve "+filepath.Join(root, "app.go"))
	if !strings.Contains(reason, "mcp__aracne__grep") {
		t.Errorf("mcp denial should name the MCP tool:\n%s", reason)
	}
}

// A native Read/Grep is never intercepted -- the hook only rewrites Bash -- so the PostToolUse
// nudge is the one place the model learns the shell forms are the cheaper question. It must
// name the surface the project is actually on.
func TestNativeToolNudgeFollowsTheSurface(t *testing.T) {
	root, _ := scannedProject(t)

	nudge := func(cfg *helper.Config, tool string, input map[string]interface{}) string {
		t.Helper()
		if err := helper.SaveConfig(cfg, helper.ConfigPath(filepath.Join(root, ".aracne", "topology.db"))); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(map[string]interface{}{
			"hook_event_name": "PostToolUse",
			"tool_name":       tool,
			"cwd":             root,
			"tool_input":      input,
		})
		var out bytes.Buffer
		runClaudeGuardHook(bytes.NewReader(raw), &out)
		return out.String()
	}

	terminal := helper.DefaultConfig()
	got := nudge(terminal, "Read", map[string]interface{}{"file_path": filepath.Join(root, "app.go")})
	if !strings.Contains(got, "arac read") || strings.Contains(got, "mcp__aracne__") {
		t.Errorf("native Read nudge should point at the shell surface:\n%s", got)
	}

	// A Bash read on this surface was either already answered by the rewrite or is one aracne
	// cannot answer at all, so nudging it is bytes spent on nothing.
	if got := nudge(terminal, "Bash", map[string]interface{}{"command": "head -5 " + filepath.Join(root, "app.go")}); strings.Contains(got, "arac read") {
		t.Errorf("an intercepted Bash read should not also be nudged:\n%s", got)
	}
}
