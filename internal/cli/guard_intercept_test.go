package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
)

// interceptingConfig is a project in a mode that rewrites shell READS. The shipped default is
// ModeCLI, which intercepts searches only, so a read-interception test has to say which
// mode it is testing rather than lean on the default.
func interceptingConfig() *helper.Config {
	cfg := helper.DefaultConfig()
	cfg.Mode = helper.ModeInterceptLineRanges
	return cfg
}

// rewriteOf runs interceptCommand against a real scanned project and returns the rewritten
// command, or "" when the call was left alone.
func rewriteOf(t *testing.T, dbPath, command string) string {
	t.Helper()
	out, ok := interceptCommand(command, dbPath, interceptingConfig())
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
		// A substitution's output is a VALUE the enclosing command consumes, not something
		// the model reads. Rewriting one put aracne's rendering where the file's bytes were.
		{"X=$(cat " + app + ")", "a command substitution: the caller captures the bytes"},
		{"echo `cat " + app + "`", "backticks are a substitution too"},
		{"for f in x; do cat " + app + "; done", "a loop body is not a top-level command"},
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
		// A producer feeding a pipe carries `--piped`, so the answer is the rows the real
		// command would have printed rather than the annotated rendering. See serveGrep.
		{"grep -rn Serve " + root + " | head -30", "grep "},
		{"sed -n '1,5p' " + app + "; sed -n '10,20p' " + app, "sed "},
	} {
		got := rewriteOf(t, dbPath, tc.cmd)
		if got == "" {
			t.Errorf("%q was not intercepted", tc.cmd)
			continue
		}
		if !strings.Contains(got, "cmd -- "+tc.wantPrefixedBefore) &&
			!strings.Contains(got, "cmd --piped -- "+tc.wantPrefixedBefore) {
			t.Errorf("%q: prefix not spliced before %q:\n%s", tc.cmd, tc.wantPrefixedBefore, got)
		}
		// Everything the model wrote must still be there, in order.
		stripped := strings.ReplaceAll(strings.ReplaceAll(got, "cmd --piped -- ", ""), "cmd -- ", "")
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
		// A stage split in two by a mis-read `&` used to hide the real consumer from the
		// adjacency walk, so these reached the model as answers to a different question.
		{"cat " + app + " 2>&1 | grep Serve", "`2>&1` is a redirect, not the end of the command"},
		{"grep -rn Serve " + root + " 2>&1 | wc -l", "the count is of aracne's annotated rows"},
		{"grep -rn Serve " + root + " | grep -v _test 2>&1 | wc -l", "the pipeline still ends at wc"},
		{"{ cat " + app + "; } | grep Serve", "a group around the producer is still the producer"},
		{"( cat " + app + " ) | grep Serve", "and so is a subshell"},
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

// The body of a substitution is a value, and it must survive untouched however the enclosing
// command is treated.
//
// The enclosing command is a different question and is deliberately not asserted here:
// `grep -rn X $(cat list)` is an ordinary search whose operands happen to be computed, and the
// shell hands `arac cmd` the expanded argv, which is what settles it. What must never happen is
// the substitution ITSELF being answered from the topology -- the caller asked for the file's
// bytes and would silently receive fences, elision markers and a context block instead.
func TestASubstitutionBodyIsNeverRewritten(t *testing.T) {
	root, dbPath := scannedProject(t)
	app := filepath.Join(root, "app.go")

	for _, cmd := range []string{
		"X=$(cat " + app + "); echo ${#X}",
		"grep -rn Serve $(cat " + app + ")",
		"echo `cat " + app + "`",
		"wc -l $(grep -rl Serve " + root + ")",
	} {
		got := rewriteOf(t, dbPath, cmd)
		if got == "" {
			continue // left alone entirely, which is also fine
		}
		for _, forbidden := range []string{"cmd -- cat ", "cmd -- grep -rl "} {
			if strings.Contains(got, forbidden) {
				t.Errorf("%q: the substitution body was rewritten (%q):\n%s", cmd, forbidden, got)
			}
		}
	}
}

// The scanner is what the two rules above stand on, so it is asserted directly: a redirection
// `&` must not end a command, and everything inside a substitution, a subshell or a group must
// carry a depth the interception rules can refuse.
func TestSegmentsKeepPipelinesWholeAndRecordNesting(t *testing.T) {
	seg := func(command string) []commandSegment { return splitCommandSegments(command) }

	// `2>&1` stays inside its own command, so the consumer really is the next segment.
	got := seg("cat f 2>&1 | grep x")
	if len(got) != 2 {
		t.Fatalf("`cat f 2>&1 | grep x` split into %d segments, want 2: %#v", len(got), got)
	}
	if strings.TrimSpace(got[0].text) != "cat f 2>&1" {
		t.Errorf("producer text = %q, want the whole command including the redirect", got[0].text)
	}
	if !got[1].pipedInto {
		t.Error("the consumer is no longer recorded as reading the pipe")
	}
	// A bare `&` is still a separator, and `&&` still is too.
	if n := len(seg("cat f & cat g")); n < 2 {
		t.Errorf("a background `&` stopped separating: %d segment(s)", n)
	}
	if n := len(seg("cat f && cat g")); n < 2 {
		t.Errorf("`&&` stopped separating: %d segment(s)", n)
	}

	for _, tc := range []struct {
		command string
		inner   string
		want    int
	}{
		{"X=$(cat f)", "cat f", 1},
		{"echo `cat f`", "cat f", 1},
		{"( cat f )", "cat f", 1},
		{"{ cat f; }", "cat f", 1},
		{"cat f", "cat f", 0},
		{"cd d && cat f", "cat f", 0},
	} {
		found := false
		for _, s := range seg(tc.command) {
			if strings.TrimSpace(s.text) != tc.inner {
				continue
			}
			found = true
			if s.depth != tc.want {
				t.Errorf("%q: %q has depth %d, want %d", tc.command, tc.inner, s.depth, tc.want)
			}
		}
		if !found {
			t.Errorf("%q: never produced a %q segment", tc.command, tc.inner)
		}
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

// READ interception is off in the two toolful modes -- there the read capability already has a
// surface, and rewriting the model's `cat` on top of it would answer one question twice.
//
// SEARCH interception stays on in all four, which is the half that is easy to get wrong: the
// annotated grep reaches node names and stored descriptions, and no read tool and no `arac read`
// answers that, so there is no mode in which handing a search back to the real binary is right.
func TestReadInterceptionFollowsTheModeButSearchNeverStops(t *testing.T) {
	root, dbPath := scannedProject(t)
	app := filepath.Join(root, "app.go")

	for _, tc := range []struct {
		mode      string
		wantReads bool
	}{
		{helper.ModeMCP, false},
		{helper.ModeCLI, false},
		{helper.ModeInterceptID, true},
		{helper.ModeInterceptLineRanges, true},
	} {
		cfg := helper.DefaultConfig()
		cfg.Mode = tc.mode

		if _, ok := interceptCommand("head -20 "+app, dbPath, cfg); ok != tc.wantReads {
			t.Errorf("mode %q: read intercepted = %v, want %v", tc.mode, ok, tc.wantReads)
		}
		if _, ok := interceptCommand("grep -n Serve "+app, dbPath, cfg); !ok {
			t.Errorf("mode %q: a search was not intercepted", tc.mode)
		}
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

// blockedIn returns a config in the given mode with `keys` denied.
func blockedIn(mode string, keys ...string) *helper.Config {
	cfg := helper.DefaultConfig()
	cfg.Mode = mode
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

// blocked_tools does nothing in the INTERCEPTING modes, and this is where that has to be true
// end to end.
//
// It used to deny on every surface, with the refusal text rewritten to name shell forms instead
// of MCP tools. That was the wrong half to fix: in an intercepting mode the command was about to
// be ANSWERED, so the denial spent a turn refusing a question asked correctly. That reasoning
// is specific to those modes and still holds.
func TestBlockedToolsDenyNothingInTheInterceptingModes(t *testing.T) {
	root, _ := scannedProject(t)
	app := filepath.Join(root, "app.go")

	for _, mode := range []string{helper.ModeInterceptID, helper.ModeInterceptLineRanges} {
		cfg := blockedIn(mode, "read", "grep", "edit", "write")

		// `od -c` is a read aracne models no spelling of, so nothing else can be suppressing
		// the denial -- if blocked_tools were live here, this would be refused.
		if reason := denyReason(t, cfg, root, "od -c "+app); reason != "" {
			t.Errorf("mode %q denied a read that blocked_tools should no longer gate:\n%s", mode, reason)
		}
		if reason := denyReason(t, cfg, root, "sed -i s/a/b/ "+app); reason != "" {
			t.Errorf("mode %q denied an edit that blocked_tools should no longer gate:\n%s", mode, reason)
		}
	}
}

// ModeCLI is the third mode where a refusal has somewhere to send the model: `arac read`
// is a real command there, and reads are NOT intercepted, so a block is not refusing something
// aracne was about to hand over.
//
// This reverses an earlier decision that made the knob inert here, whose stated reason was that
// the contract had already taught `arac read` so the refusal added nothing. Knowing is not
// using: the smoke cell that prompted this change read files with `sed -n 1,200p` while the
// contract sat in its context. The knob stays OFF by default -- blocked_tools is empty in a
// generated config -- so this makes an opt-in setting work rather than changing what a project
// gets without asking.
func TestBlockedToolsBiteInAracneReadWhenAsked(t *testing.T) {
	root, _ := scannedProject(t)
	app := filepath.Join(root, "app.go")
	cfg := blockedIn(helper.ModeCLI, "read", "grep", "edit", "write")

	if reason := denyReason(t, cfg, root, "od -c "+app); reason == "" {
		t.Error("a configured read block must deny in cli, where `arac read` is the surface")
	}
	if reason := denyReason(t, cfg, root, "sed -i s/a/b/ "+app); reason == "" {
		t.Error("a configured edit block must deny in cli")
	}
	// grep is the entry that must NOT bite: aracne answers a search in every mode, so refusing
	// one denies a command the guard was one step from answering itself.
	if reason := denyReason(t, cfg, root, "grep -rn Handle "+root); reason != "" {
		t.Errorf("search is intercepted in cli and must never be refused:\n%s", reason)
	}
}

// And with nothing configured -- the shape every generated project has -- cli denies
// nothing at all.
func TestAracneReadDeniesNothingByDefault(t *testing.T) {
	root, _ := scannedProject(t)
	app := filepath.Join(root, "app.go")
	cfg := blockedIn(helper.ModeCLI)

	for _, cmd := range []string{"od -c " + app, "sed -i s/a/b/ " + app, "cat " + app} {
		if reason := denyReason(t, cfg, root, cmd); reason != "" {
			t.Errorf("default cli must deny nothing, refused %q:\n%s", cmd, reason)
		}
	}
}

// Interception runs first and wins. A rewrite hands the model the answer inside the call it
// already made; a denial costs it another turn for the same information.
func TestInterceptionBeatsADenialForTheSameCommand(t *testing.T) {
	root, _ := scannedProject(t)
	cfg := blockedIn(helper.ModeInterceptLineRanges, "read", "grep")

	if reason := denyReason(t, cfg, root, "head -20 "+filepath.Join(root, "app.go")); reason != "" {
		t.Fatalf("a command aracne can serve was denied instead of rewritten:\n%s", reason)
	}
}

// ModeMCP still denies, and its refusal still names the tool that exists -- which since the
// rework is the read tool and only the read tool. A denial for grep must point at `arac grep`,
// because no mode registers an MCP grep any more.
func TestMCPDenialsNameOnlyToolsThatExist(t *testing.T) {
	root, _ := scannedProject(t)
	app := filepath.Join(root, "app.go")

	// `od -c` over TWO files: a read aracne models no spelling of and cannot proxy either, so
	// it reaches the denial rather than being answered with the file's content.
	readReason := denyReason(t, blockedIn(helper.ModeMCP, "read"), root,
		"od -c "+app+" "+filepath.Join(root, "CHANGELOG.md"))
	if !strings.Contains(readReason, "mcp__aracne__") {
		t.Errorf("mcp read denial should name the MCP read tool:\n%s", readReason)
	}

	grepReason := denyReason(t, blockedIn(helper.ModeMCP, "grep"), root, "grep -o Serve "+app)
	if grepReason == "" {
		t.Fatal("a blocked grep was not denied in mcp mode")
	}
	if strings.Contains(grepReason, "mcp__aracne__grep") {
		t.Errorf("mcp grep denial names a tool no mode registers:\n%s", grepReason)
	}
	if !strings.Contains(grepReason, "arac grep") {
		t.Errorf("mcp grep denial should point at `arac grep`:\n%s", grepReason)
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

	app := filepath.Join(root, "app.go")

	// ModeCLI: nothing intercepts a read, so the nudge names the subcommand that does.
	aracneRead := helper.DefaultConfig()
	aracneRead.Mode = helper.ModeCLI
	got := nudge(aracneRead, "Read", map[string]interface{}{"file_path": app})
	if !strings.Contains(got, "arac read") || strings.Contains(got, "mcp__aracne__") {
		t.Errorf("cli nudge should point at the subcommand:\n%s", got)
	}

	// An intercepting mode names the shell spellings aracne answers instead -- there is no
	// subcommand to reach for when the command the model just typed is the surface.
	lineRange := helper.DefaultConfig()
	lineRange.Mode = helper.ModeInterceptLineRanges
	got = nudge(lineRange, "Read", map[string]interface{}{"file_path": app})
	if !strings.Contains(got, "answered from the topology") || strings.Contains(got, "mcp__aracne__") {
		t.Errorf("intercept_line_ranges nudge should name the shell forms:\n%s", got)
	}

	// ModeInterceptID is the one mode whose nudge may teach ids, because it is the one whose
	// contract does.
	interceptID := helper.DefaultConfig()
	interceptID.Mode = helper.ModeInterceptID
	if got := nudge(interceptID, "Read", map[string]interface{}{"file_path": app}); !strings.Contains(got, "resource ID") {
		t.Errorf("intercept_id nudge should mention resource IDs:\n%s", got)
	}
	if got := nudge(lineRange, "Read", map[string]interface{}{"file_path": app}); strings.Contains(got, "resource ID") {
		t.Errorf("intercept_line_ranges nudge must not re-teach resource IDs:\n%s", got)
	}

	// Where reads ARE intercepted, a Bash read was either already answered by the rewrite or
	// is one aracne cannot answer at all, so nudging it is bytes spent on nothing.
	if got := nudge(lineRange, "Bash", map[string]interface{}{"command": "head -5 " + app}); strings.Contains(got, "answered from the topology") {
		t.Errorf("an intercepted Bash read should not also be nudged:\n%s", got)
	}
}

// The shape the model actually writes. Over 664 real shell commands from hard9-modes and
// navcheck-20260902a, 133 were a piped grep; the head/tail-only rule served 88 of them, and
// nearly all of the 45 it refused were `| grep -v <noise> | head -N`. That refusal is why
// n_intercepted was 0 across the pilot's aracne cells while the arm still paid for its
// contract.
func TestGrepIsInterceptedThroughLinePreservingFilters(t *testing.T) {
	root, dbPath := scannedProject(t)
	for _, cmd := range []string{
		"grep -rn Serve " + root + " | grep -v _test",
		"grep -rn Serve " + root + " | grep -v _test | head -30",
		"grep -rn Serve " + root + " | grep -v _test | grep -v testdata | head -40",
	} {
		got := rewriteOf(t, dbPath, cmd)
		if got == "" {
			t.Errorf("expected a rewrite for %q", cmd)
			continue
		}
		// `--piped`, because a pipeline reads this answer: annotation, node rows, the head
		// limit and the tiered order are all switched off so the stages downstream see the
		// lines the real grep would have given them. See topogrep.Options.Plain.
		if !strings.Contains(got, "cmd --piped -- grep") {
			t.Errorf("rewrote the wrong segment of %q: %s", cmd, got)
		}
		// Everything downstream of the producer must survive byte-for-byte: the whole point is
		// that the rest of the pipeline still runs, over aracne's answer.
		if tailPart := cmd[strings.Index(cmd, "|"):]; !strings.Contains(got, tailPart) {
			t.Errorf("downstream of the pipe was not preserved for %q: %s", cmd, got)
		}
	}
}

// A consumer whose meaning depends on bytes aracne never promised to reproduce must leave the
// producer alone. `wc -l` over aracne's answer counts its `# path:a-b` annotation lines too,
// and returns a bare number with nothing in it to reveal the substitution.
func TestGrepIsNotInterceptedIntoOpaqueConsumers(t *testing.T) {
	root, dbPath := scannedProject(t)
	for _, cmd := range []string{
		"grep -rn Serve " + root + " | wc -l",
		"grep -rln Serve " + root + " | xargs sed -i s/a/b/",
		"grep -rn Serve " + root + " | tee /tmp/out.txt",
		"grep -rn Serve " + root + " | head -5 | wc -l",
	} {
		if got := rewriteOf(t, dbPath, cmd); got != "" {
			t.Errorf("must NOT rewrite %q (consumer changes what the pipeline means), got %s", cmd, got)
		}
	}
}

// A READ producer stays untouched whatever follows it: `cat f | grep x` would search aracne's
// rendering, whose elided bodies are not in the file's text, and return FEWER matches than the
// real command with nothing marking it as a different answer.
func TestReadIsNeverInterceptedIntoAPipe(t *testing.T) {
	root, dbPath := scannedProject(t)
	app := filepath.Join(root, "app.go")
	for _, cmd := range []string{
		"cat " + app + " | grep Serve",
		"cat " + app + " | head -30",
		"sed -n 1,40p " + app + " | grep -v test",
	} {
		if got := rewriteOf(t, dbPath, cmd); got != "" {
			t.Errorf("must NOT rewrite a read into a pipe: %q -> %s", cmd, got)
		}
	}
}

// The guard's fallback pointer is keyed on the MODE, not on !InterceptReads().
//
// Those two are not the same set: !InterceptReads() is true in ModeCLI as well as ModeMCP, so
// keying the fallback on it would print the MCP tool pointer after a shell read ModeCLI
// deliberately leaves alone and was never going to refuse. That regression shipped once, when
// the shell-read nudge still occupied the case above it and hid the fall-through. The nudge is
// gone; this pins the difference the nudge used to mask.
func TestTheMCPFallbackIsKeyedOnTheModeNotOnInterception(t *testing.T) {
	cfg := helper.DefaultConfig()
	cfg.Mode = helper.ModeCLI
	if cfg.InterceptReads() {
		t.Fatal("ModeCLI must not intercept reads -- the trap this guards depends on it")
	}
	if cfg.EffectiveMode() == helper.ModeMCP {
		t.Error("ModeCLI must not reach the MCP fallback")
	}
}

// A SUBSTITUTION BETWEEN A PRODUCER AND ITS CONSUMER IS NOT A CONSUMER.
//
// The scanner cuts a new segment at every `(`, `)`, backtick and `{`, so a command carrying a
// substitution is spread over several segments with the substitution's BODY between them. The
// check that asks what a producer feeds used to read `segments[i+1]` -- adjacency -- and found
// the body, whose pipedInto is false. It concluded "feeds no pipe" and rewrote
// `grep -rn X $(echo .) | wc -l`, the exact shape it exists to refuse: measured on a two-match
// fixture the real pipeline answered 2 and the intercepted one 4.
//
// This is the same break `2>&1` had before isRedirectAmpersand, on a path that had no rule.
func TestASubstitutionDoesNotHideAnOpaqueConsumer(t *testing.T) {
	root, dbPath := scannedProject(t)

	for _, cmd := range []string{
		"grep -rn Serve $(echo " + root + ") | wc -l",
		"grep -rn Serve `echo " + root + "` | wc -l",
		"grep -rn Serve ${PWD} | wc -l",
		"grep -rn Serve " + root + " | wc -l",
		// The consumer is inside a group, so its command word is a level down and cannot be
		// read from here. An unreadable stage is an opaque one.
		"grep -rn Serve " + root + " | ( wc -l )",
	} {
		if got := rewriteOf(t, dbPath, cmd); got != "" {
			t.Errorf("%q feeds a consumer aracne cannot serve, but was rewritten:\n%s", cmd, got)
		}
	}

	// The narrower shape of the same fault: the producer's own OPERANDS continue after the
	// substitution, so the segment following it has a command word of its own. Deciding "is this
	// a new command?" by looking for one read `extra.go` as the start of a new command and lost
	// the pipe again -- which is why the scanner records why it cut each segment rather than
	// leaving the walk to guess. See commandSegment.endedBy.
	for _, cmd := range []string{
		"grep -rn Serve $(echo " + root + ") " + filepath.Join(root, "app.go") + " | wc -l",
		"grep -rn Serve ${PWD} " + filepath.Join(root, "app.go") + " | wc -l",
		"grep -rn Serve `echo " + root + "` " + filepath.Join(root, "app.go") + " | grep -c x",
	} {
		if got := rewriteOf(t, dbPath, cmd); got != "" {
			t.Errorf("%q feeds an opaque consumer past its own operands, but was rewritten:\n%s", cmd, got)
		}
	}

	// A list separator is not a pipe, and must not be read as one.
	for _, cmd := range []string{
		"grep -rn Serve " + root + " ; wc -l",
		"grep -rn Serve " + root + " && echo done",
	} {
		if got := rewriteOf(t, dbPath, cmd); got == "" {
			t.Errorf("%q feeds nothing; it must still be served", cmd)
		} else if strings.Contains(got, "--piped") {
			t.Errorf("%q was read as feeding a pipe:\n%s", cmd, got)
		}
	}

	// And the capability is not lost with it: the same substitution with NO pipe, or with a
	// line-preserving one, is still served.
	for _, cmd := range []string{
		"grep -rn Serve $(echo " + root + ")",
		"grep -rn Serve ${PWD}",
		"grep -rn Serve $(echo " + root + ") " + filepath.Join(root, "app.go"),
		"grep -rn Serve " + root + " | head -30",
		"grep -rn Serve $(echo " + root + ") " + filepath.Join(root, "app.go") + " | head -30",
	} {
		if got := rewriteOf(t, dbPath, cmd); got == "" {
			t.Errorf("%q is servable and must still be rewritten", cmd)
		}
	}
}

// A PIPELINE READS DIFFERENTLY FROM A MODEL, and the rewrite says which one is reading.
//
// Everything aracne adds to a search -- the `#` resource headers, the rows a node earned on its
// name or description, the trailer, the tiered order, the 200-row cap -- is addressed to a
// reader. Handed to a pipeline they are lines it counts, filters and caps alongside the real
// matches, and the cap in particular loses matches the consumer cannot know are missing. So a
// producer that feeds a pipe is marked `--piped` and answered plain.
func TestAPipedProducerIsAnsweredPlain(t *testing.T) {
	root, dbPath := scannedProject(t)
	app := filepath.Join(root, "app.go")

	piped := rewriteOf(t, dbPath, "grep -rn Serve "+root+" | head -30")
	if !strings.Contains(piped, "cmd --piped -- grep") {
		t.Errorf("a piped search must be answered plain:\n%s", piped)
	}
	// The rest of the pipeline is untouched, which is the whole point of splicing rather than
	// replacing.
	if !strings.HasSuffix(piped, "| head -30") {
		t.Errorf("downstream of the pipe was not preserved:\n%s", piped)
	}

	// A search nothing consumes keeps the annotated answer -- that IS the product.
	unpiped := rewriteOf(t, dbPath, "grep -rn Serve "+root)
	if strings.Contains(unpiped, "--piped") {
		t.Errorf("an unpiped search must keep its annotation:\n%s", unpiped)
	}
	// So does a read: a read producer is never rewritten across a pipe at all, so the flag can
	// only ever reach a search.
	read := rewriteOf(t, dbPath, "cat "+app)
	if strings.Contains(read, "--piped") {
		t.Errorf("a read is never piped-plain:\n%s", read)
	}
}

// `--piped` has to survive the round trip into the verb, or the flag is decoration.
func TestParseCmdFlagsReadsPiped(t *testing.T) {
	for _, tc := range []struct {
		args     []string
		wantArgv []string
		wantPipe bool
	}{
		{[]string{"--", "grep", "x"}, []string{"grep", "x"}, false},
		{[]string{"--piped", "--", "grep", "x"}, []string{"grep", "x"}, true},
		// A caller who forgot the `--` still gets their command run rather than a usage error
		// about its first argument.
		{[]string{"grep", "x"}, []string{"grep", "x"}, false},
		{[]string{"--piped"}, nil, true},
		// `--` ends the flags: a command's own `--piped` argument is its own business.
		{[]string{"--", "grep", "--piped"}, []string{"grep", "--piped"}, false},
	} {
		argv, piped := parseCmdFlags(tc.args)
		if piped != tc.wantPipe || !reflect.DeepEqual(argv, tc.wantArgv) {
			t.Errorf("parseCmdFlags(%v) = %v, %v; want %v, %v",
				tc.args, argv, piped, tc.wantArgv, tc.wantPipe)
		}
	}
}

// A search reading REDIRECTED stdin is the pipe case spelled differently, and is left alone. The
// shell strips `< f` before the command sees its argv, so `rg retry < README.md` reached shellcmd
// as a path-less `rg retry` -- the working directory, for rg -- and came back as a search of the
// whole tree: matches from five files where the real command printed one README line.
func TestInputRedirectedSearchesAreNotRewritten(t *testing.T) {
	root, dbPath := scannedProject(t)
	app := filepath.Join(root, "app.go")

	for _, cmd := range []string{
		"rg Serve < " + app,
		"rg Serve <" + app,
		"rg -n Serve < " + app,
		"ag Serve < " + app,
		"grep -rn Serve < " + app,
		"grep -rn Serve " + root + " < " + app,
		"rg Serve <(cat " + app + ")",
		// The redirect sits in the segment AFTER the substitution's body; see
		// commandRedirectsInput.
		"grep -rn Serve $(echo " + root + ") < " + app,
		"cd " + root + " && rg Serve < " + app,
	} {
		if got := rewriteOf(t, dbPath, cmd); got != "" {
			t.Errorf("%q was rewritten to %q -- it reads a file on stdin, so it must run as typed", cmd, got)
		}
	}

	// Nothing else changes. A path-less search with no redirect walks the tree from the Bash
	// tool's /dev/null stdin, and `arac cmd` re-checks the stdin it actually gets; a stderr
	// redirect is not an input; a `<` inside a quoted pattern is not an operator at all.
	for _, cmd := range []string{
		"rg Serve",
		"grep -rn Serve",
		"grep -rn Serve " + app + " 2>/dev/null",
		"grep -rn '<Serve' " + app,
	} {
		if got := rewriteOf(t, dbPath, cmd); got == "" {
			t.Errorf("%q must still be rewritten", cmd)
		}
	}
}
