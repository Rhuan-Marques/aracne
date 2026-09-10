package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/toolspec"
)

func TestCommandKeys(t *testing.T) {
	tests := []struct {
		command string
		exempt  bool
		want    []string
	}{
		{"", true, nil},
		{"   ", true, nil},
		{"cat f", true, []string{"read"}},
		{"head -n5 f", true, []string{"read"}},
		{"tail -f log", true, []string{"read"}},
		{"less f", true, []string{"read"}},
		{"grep x f", true, []string{"grep"}},
		{"rg x", true, []string{"grep"}},
		{"sed -i s/a/b/ f", true, []string{"edit"}},
		// Non-mutating sed/awk read and filter; they are classified as reads, so
		// a DIRECT file read is still gated (like `head -20 f`) but a piped one
		// is exempt (see the pipe-exemption block below).
		{"awk '{print}' f", true, []string{"read"}},
		{"sed -n 1,10p f.go", true, []string{"read"}},
		{"ls; grep x f", true, []string{"grep"}},
		{"true && grep x", true, []string{"grep"}},
		{"FOO=1 grep x", true, []string{"grep"}},
		{"sudo grep x", true, []string{"grep"}},
		{"/usr/bin/grep x", true, []string{"grep"}},
		{`echo "use grep here"`, true, nil},
		{"git grep foo", true, nil},
		{"npm test", true, nil},
		{"Get-Content f", true, []string{"read"}},
		{"Select-String x f", true, []string{"grep"}},
		{"echo $(grep x)", true, []string{"grep"}},

		// Pipe exemption (exempt=true): a read/grep command fed by `|` views or
		// filters command output and is skipped; the producer side and direct
		// file reads are still classified.
		{"git log | head -50", true, nil},
		{"cmd | tail", true, nil},
		{"cmd | grep err", true, nil},
		{"kubectl logs x | grep e | tail", true, nil},
		{"cat f | grep x", true, []string{"read"}},
		{"a || cat f", true, []string{"read"}},
		// `git log --oneline | sed -n '30,60p'` is a read of command output with
		// no file operand and no -i. Refusing it cost the agent a turn to be
		// told no to a legal request.
		{"cmd | sed s/a/b/", true, nil},
		{"git log --oneline | sed -n 30,60p", true, nil},
		{"cmd | awk '{print $2}'", true, nil},
		// stderr duplication is not an output redirect
		{"cmd 2>&1 | sed -n 1p", true, nil},

		// ...but a mutating stream editor is an edit however it is invoked.
		{"cmd | sed -i s/a/b/ f", true, []string{"edit"}},
		{"sed --in-place s/a/b/ f", true, []string{"edit"}},
		{"sed -i.bak s/a/b/ f", true, []string{"edit"}},
		{"sed -ni s/a/b/ f", true, []string{"edit"}},
		{"awk -i inplace '{print}' f", true, []string{"edit"}},
		// writing the output to a file is an edit too
		{"sed s/a/b/ in.go > out.go", true, []string{"edit"}},
		{"cmd | sed s/a/b/ >> out.go", true, []string{"edit"}},
		// a `>` inside quotes is part of the expression, not a redirect
		{"sed 's/a>b/c/' f", true, []string{"read"}},

		// Exemption off restores strict classification of piped reads.
		{"git log | head -50", false, []string{"read"}},
		{"cmd | grep err", false, []string{"grep"}},
		{"cat f | grep x", false, []string{"read", "grep"}},
		{"cmd | sed -n 1p", false, []string{"read"}},
	}
	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			got := commandKeys(tt.command, tt.exempt)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("commandKeys(%q, exempt=%v) = %v, want %v", tt.command, tt.exempt, got, tt.want)
			}
		})
	}
}

func TestDecideGuard(t *testing.T) {
	bash := func(cmd string) map[string]interface{} { return map[string]interface{}{"command": cmd} }
	set := func(keys ...string) map[string]bool {
		m := make(map[string]bool)
		for _, k := range keys {
			m[k] = true
		}
		return m
	}
	tests := []struct {
		name      string
		tool      string
		input     map[string]interface{}
		blocked   map[string]bool
		exempt    bool
		wantDeny  bool
		wantInMsg string
	}{
		{"native grep allowed", "Grep", nil, set(), true, false, ""},
		{"native grep blocked", "Grep", nil, set("grep"), true, true, "arac grep"},
		{"native read blocked", "Read", nil, set("read"), true, true, "blocked_tools: read"},
		{"native write blocked", "Write", nil, set("write"), true, true, "arac write"},
		{"native edit not in blocked", "Edit", nil, set("grep"), true, false, ""},
		{"bash grep blocked", "Bash", bash("grep x | head"), set("grep"), true, true, "grep"},
		{"bash cat allowed", "Bash", bash("cat f"), set(), true, false, ""},
		{"bash cat blocked via read", "Bash", bash("cat f"), set("read"), true, true, "blocked_tools: read"},
		{"whole bash blocked", "Bash", bash("npm test"), set("bash"), true, true, "blocked_tools: bash"},
		{"bash unblocked", "Bash", bash("npm test"), set(), true, false, ""},
		// MultiEdit is an edit, and blocking `edit` has to reach it. It used to be absent
		// from both the guard's matcher and toolspec's native map, so a project that blocked
		// edits still had its MultiEdit calls waved through.
		{"multiedit denied as edit", "MultiEdit", nil, set("edit"), true, true, "blocked_tools: edit"},
		{"multiedit allowed when edit is not blocked", "MultiEdit", nil, set("read"), true, false, ""},
		{"notebookedit denied as edit", "NotebookEdit", nil, set("edit"), true, true, "blocked_tools: edit"},
		{"glob ignored", "Glob", nil, set("read"), true, false, ""},
		{"piped tail exempt", "Bash", bash("cmd | tail"), set("read"), true, false, ""},
		{"piped tail strict", "Bash", bash("cmd | tail"), set("read"), false, true, "blocked_tools: read"},
		{"piped grep exempt", "Bash", bash("cmd | grep x"), set("grep"), true, false, ""},

		// The reported defect: a piped, non-mutating sed is a read of command
		// output. It must not be denied even when `edit` is blocked outright.
		{"piped sed not denied as edit", "Bash", bash("git log --oneline | sed -n 30,60p"), set("edit"), true, false, ""},
		{"piped sed exempt as read", "Bash", bash("git log | sed -n 30,60p"), set("read"), true, false, ""},
		{"piped awk not denied as edit", "Bash", bash("cmd | awk '{print $2}'"), set("edit"), true, false, ""},
		// ...but in-place sed is still an edit, and a direct file read is still
		// gated exactly like `head -20 f`.
		{"in-place sed still blocked", "Bash", bash("sed -i s/a/b/ f.go"), set("edit"), true, true, "blocked_tools: edit"},
		{"redirecting sed still blocked", "Bash", bash("sed s/a/b/ in.go > out.go"), set("edit"), true, true, "blocked_tools: edit"},
		{"direct sed read gated", "Bash", bash("sed -n 1,20p f.go"), set("read"), true, true, "blocked_tools: read"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := decideGuard(tt.tool, tt.input, tt.blocked, tt.exempt, "", toolspec.SurfaceMCP)
			if d.Deny != tt.wantDeny {
				t.Fatalf("decideGuard(%q) Deny = %v, want %v (msg: %q)", tt.tool, d.Deny, tt.wantDeny, d.Message)
			}
			if tt.wantInMsg != "" && !strings.Contains(d.Message, tt.wantInMsg) {
				t.Fatalf("decideGuard(%q) message %q missing %q", tt.tool, d.Message, tt.wantInMsg)
			}
			if !tt.wantDeny && d.Message != "" {
				t.Fatalf("decideGuard(%q) should have empty message when not denied, got %q", tt.tool, d.Message)
			}
		})
	}
}

// withConfig runs fn in a temp working dir, optionally writing an .aracne
// config.json first. It returns to the original dir afterward.
func withConfig(t *testing.T, configJSON string, fn func()) {
	t.Helper()
	dir := t.TempDir()
	prev, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(prev)
	if configJSON != "" {
		if err := os.MkdirAll(".aracne", 0755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(".aracne", "config.json"), []byte(configJSON), 0644); err != nil {
			t.Fatalf("write config: %v", err)
		}
	}
	fn()
}

// mcpSurfaceConfig is the minimum config that puts a project on the MCP surface, where
// blocked_tools denials and the MCP-tool nudges apply.
const mcpSurfaceConfig = `{"mode":"mcp"}`

const blocksGrepConfig = `{"mode":"mcp","llm":{"claude_code":{"main_agent":{"blocked_tools":["grep"]}}}}`

// The NATIVE Grep tool, not a Bash one: interception only rewrites Bash, and a Bash grep is
// intercepted in every mode now -- which beats the denial, and should, since a rewrite hands the
// model the answer inside the call it already made.
func TestRunClaudeGuardHook_PreToolDeny(t *testing.T) {
	withConfig(t, blocksGrepConfig, func() {
		var out bytes.Buffer
		in := strings.NewReader(`{"hook_event_name":"PreToolUse","tool_name":"Grep","tool_input":{"pattern":"foo"}}`)
		runClaudeGuardHook(in, &out)
		got := out.String()
		if !strings.Contains(got, `"permissionDecision":"deny"`) {
			t.Fatalf("expected deny decision, got: %q", got)
		}
		if !strings.Contains(got, "arac grep") {
			t.Fatalf("expected guidance in deny reason, got: %q", got)
		}
	})
}

// A Bash grep in the same project is REWRITTEN rather than denied. The denial costs a turn for
// information the rewrite delivers inside the call the model already made.
func TestRunClaudeGuardHook_ABashGrepIsRewrittenNotDenied(t *testing.T) {
	withConfig(t, blocksGrepConfig, func() {
		var out bytes.Buffer
		in := strings.NewReader(`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"grep foo src"}}`)
		runClaudeGuardHook(in, &out)
		got := out.String()
		if strings.Contains(got, `"permissionDecision":"deny"`) {
			t.Fatalf("a bash grep was denied instead of rewritten: %q", got)
		}
		if !strings.Contains(got, "updatedInput") {
			t.Fatalf("expected a rewrite, got: %q", got)
		}
	})
}

const blocksReadConfig = `{"mode":"mcp","llm":{"claude_code":{"main_agent":{"blocked_tools":["read"]}}}}`
const blocksReadStrictConfig = `{"mode":"mcp","read":{"pipe_passthrough":false},"llm":{"claude_code":{"main_agent":{"blocked_tools":["read"]}}}}`

func TestRunClaudeGuardHook_PipedReadExempt(t *testing.T) {
	// Default pipe_passthrough (true): a read command fed by a pipe is exempt.
	withConfig(t, blocksReadConfig, func() {
		var out bytes.Buffer
		in := strings.NewReader(`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"git log | tail -50"}}`)
		runClaudeGuardHook(in, &out)
		if out.Len() != 0 {
			t.Fatalf("piped read should not be denied with pipe_passthrough on, got: %q", out.String())
		}
	})
}

func TestRunClaudeGuardHook_PipedReadStrict(t *testing.T) {
	// pipe_passthrough=false gates piped reads alongside direct file reads.
	withConfig(t, blocksReadStrictConfig, func() {
		var out bytes.Buffer
		in := strings.NewReader(`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"git log | tail -50"}}`)
		runClaudeGuardHook(in, &out)
		if !strings.Contains(out.String(), `"permissionDecision":"deny"`) {
			t.Fatalf("piped read should be denied with pipe_passthrough off, got: %q", out.String())
		}
	})
}

func TestRunClaudeGuardHook_PreToolFailOpen(t *testing.T) {
	// No config on disk -> fail-open -> no deny even though grep would map.
	withConfig(t, "", func() {
		var out bytes.Buffer
		in := strings.NewReader(`{"hook_event_name":"PreToolUse","tool_name":"Grep","tool_input":{"pattern":"x"}}`)
		runClaudeGuardHook(in, &out)
		if out.Len() != 0 {
			t.Fatalf("expected no output (fail-open allow), got: %q", out.String())
		}
	})
}

// The PostToolUse nudge fires on a NATIVE tool call, which interception never sees, so it is
// the only place the model learns the cheaper spelling. It names `arac grep` rather than an MCP
// tool because no mode registers an MCP grep.
func TestRunClaudeGuardHook_PostToolWarning(t *testing.T) {
	withConfig(t, mcpSurfaceConfig, func() {
		var out bytes.Buffer
		in := strings.NewReader(`{"hook_event_name":"PostToolUse","tool_name":"Grep","tool_input":{"pattern":"x"}}`)
		runClaudeGuardHook(in, &out)
		got := out.String()
		if !strings.Contains(got, `"additionalContext"`) || !strings.Contains(got, "arac grep") {
			t.Fatalf("expected PostToolUse warning, got: %q", got)
		}
		if strings.Contains(got, "mcp__aracne__grep") {
			t.Fatalf("nudge names a tool no mode registers: %q", got)
		}
	})
}

// A MUTATION reports its own topology warnings, so it earns no pointer beside them -- and an
// edit that breaks nothing earns nothing at all. The `arac edit` nudge used to fire on every
// one, which on the common case (an edit with no callers to warn about) was a hook block with
// no finding in it.
func TestRunClaudeGuardHook_MutationsAreNotNudged(t *testing.T) {
	root, _ := scannedProject(t)
	app := filepath.Join(root, "app.go")

	// An INDEXED file rewritten to the same bytes: the strongest case for the unconditional
	// nudge this replaces, and the one that produced output with nothing to act on.
	body, err := os.ReadFile(app)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(app, body, 0o644); err != nil {
		t.Fatal(err)
	}
	postToolUse := func(tool string) string {
		t.Helper()
		raw, err := json.Marshal(map[string]interface{}{
			"hook_event_name": "PostToolUse",
			"tool_name":       tool,
			"cwd":             root,
			"tool_input":      map[string]interface{}{"file_path": app},
		})
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		runClaudeGuardHook(bytes.NewReader(raw), &out)
		return out.String()
	}

	if got := postToolUse("Edit"); got != "" {
		t.Errorf("an edit with no warnings must say nothing at all, got:\n%s", got)
	}
	if got := postToolUse("Write"); got != "" {
		t.Errorf("a write with no warnings must say nothing at all, got:\n%s", got)
	}
}

// A bash GREP is intercepted in every mode, so the model already holds the annotated answer
// and the pointer would describe it back. A bash READ is intercepted in neither toolful mode,
// so ModeMCP keeps the one place it can name the read tool.
//
// It keeps it FOR A FILE THE TOPOLOGY CAN ANSWER BETTER, which is why this runs against a real
// scanned project rather than a bare config. The arm used to emit unconditionally, so a `cat`
// of an unindexed file, a missing file or a file outside the tree all came back recommending
// `mcp__aracne__read_resource` for something the tool cannot serve -- the per-call cost
// worthNudging exists to stop on the native path, on the one arm that never got it.
func TestRunClaudeGuardHook_BashGrepNotNudgedBashReadIs(t *testing.T) {
	root, dbPath := scannedProject(t)
	cfg := helper.DefaultConfig()
	cfg.Mode = helper.ModeMCP
	if err := helper.SaveConfig(cfg, helper.ConfigPath(dbPath)); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)

	post := func(command string) string {
		var out bytes.Buffer
		raw, err := json.Marshal(map[string]interface{}{
			"hook_event_name": "PostToolUse",
			"tool_name":       "Bash",
			"cwd":             root,
			"tool_input":      map[string]interface{}{"command": command},
		})
		if err != nil {
			t.Fatal(err)
		}
		runClaudeGuardHook(bytes.NewReader(raw), &out)
		return out.String()
	}

	if got := post("grep -rn foo " + root); strings.Contains(got, "arac grep") {
		t.Errorf("an intercepted bash grep must not also be nudged: %q", got)
	}
	if got := post("cat " + filepath.Join(root, "app.go")); !strings.Contains(got, "mcp__aracne__read_resource") {
		t.Errorf("ModeMCP intercepts no bash read, so it keeps its pointer: %q", got)
	}

	// And the three shapes the pointer has nothing to offer for. Each one used to get it.
	for _, command := range []string{
		"cat " + filepath.Join(root, "CHANGELOG.md"), // indexed by nothing: no nodes to serve
		"cat " + filepath.Join(root, "absent.go"),    // not there at all
		"cat /etc/hostname",                          // outside the project entirely
	} {
		if got := post(command); strings.Contains(got, "mcp__aracne__read_resource") {
			t.Errorf("%q has nothing for the read tool to answer better, but was nudged: %q",
				command, got)
		}
	}
}

func TestRunClaudeGuardHook_NoWarningForUnmappedTool(t *testing.T) {
	withConfig(t, "", func() {
		var out bytes.Buffer
		in := strings.NewReader(`{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"npm test"}}`)
		runClaudeGuardHook(in, &out)
		if out.Len() != 0 {
			t.Fatalf("expected no warning for unmapped command, got: %q", out.String())
		}
	})
}

func TestRunClaudeGuardHook_FailsOpenOnBadInput(t *testing.T) {
	for _, in := range []string{"", "   ", "not json", `{"tool_name":""}`} {
		var out bytes.Buffer
		runClaudeGuardHook(strings.NewReader(in), &out)
		if out.Len() != 0 {
			t.Fatalf("input %q should produce no output, got: %q", in, out.String())
		}
	}
}

// TestGuardHookMatcherCoversNativeTools pins the guard's hook matcher to toolspec's native
// tool map. A name in one and not the other is silent: a call the hook never sees, or a call
// it sees and cannot classify. They were typed separately once, and MultiEdit fell through
// the gap.
func TestGuardHookMatcherCoversNativeTools(t *testing.T) {
	got := strings.Split(guardHookMatcher, "|")
	if len(got) != len(toolspec.NativeToolNames()) {
		t.Fatalf("guardHookMatcher = %q, want one alternative per toolspec.NativeToolNames()", guardHookMatcher)
	}
	for _, name := range toolspec.NativeToolNames() {
		if !strings.Contains(guardHookMatcher, name) {
			t.Fatalf("guardHookMatcher = %q, missing native tool %q", guardHookMatcher, name)
		}
		if _, ok := toolspec.NativeToolKey(name); !ok {
			t.Fatalf("toolspec.NativeToolNames() returned %q, which NativeToolKey does not know", name)
		}
	}
}

// A WHOLE-BASH BLOCK IS NOT A ROUTING DECISION, so none of the routing exemptions may reach it.
//
// It used to be decided last, inside decideGuard, behind two rules that are each right on their
// own. Interception runs first and takes precedence over a denial, so `grep foo .` was rewritten
// and RAN under a config that had switched the Bash tool off. operatesOutsideProject returns an
// empty decision for a command touching nothing indexed, so `rm -rf /tmp/x` was permitted while
// `ls -la` was refused. And the refusal everything else got named the shell forms and the `arac`
// subcommands -- all of which need the tool just denied, so a model following the advice looped.
func TestBlockedBashIsNotBypassedByInterceptionOrScope(t *testing.T) {
	root, dbPath := scannedProject(t)
	cfg := helper.DefaultConfig()
	cfg.Mode = helper.ModeCLI
	cfg.LLM.Any.MainAgent.BlockedTools = []string{"bash"}
	if err := helper.SaveConfig(cfg, helper.ConfigPath(dbPath)); err != nil {
		t.Fatal(err)
	}

	pre := func(command string) (string, string) {
		raw, err := json.Marshal(map[string]interface{}{
			"hook_event_name": "PreToolUse",
			"tool_name":       "Bash",
			"cwd":             root,
			"tool_input":      map[string]interface{}{"command": command},
		})
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		runClaudeGuardHook(bytes.NewReader(raw), &out)
		if out.Len() == 0 {
			return "allowed", ""
		}
		var decoded struct {
			HookSpecificOutput struct {
				PermissionDecision       string                 `json:"permissionDecision"`
				PermissionDecisionReason string                 `json:"permissionDecisionReason"`
				UpdatedInput             map[string]interface{} `json:"updatedInput"`
			} `json:"hookSpecificOutput"`
		}
		if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.HookSpecificOutput.UpdatedInput != nil {
			return "rewritten", ""
		}
		return decoded.HookSpecificOutput.PermissionDecision, decoded.HookSpecificOutput.PermissionDecisionReason
	}

	for _, command := range []string{
		"ls -la",
		"grep -rn Serve " + root, // interception must not outrank the block
		"cat " + filepath.Join(root, "app.go"),
		"rm -rf /tmp/scratch-file", // outside the project is still Bash
		"arac read app.go",
	} {
		decision, reason := pre(command)
		if decision != "deny" {
			t.Errorf("%q: decision %q, want deny", command, decision)
			continue
		}
		// The advice has to be something the agent can still do. Naming a shell form or an
		// `arac` subcommand is naming the tool that was just refused.
		for _, unrunnable := range []string{"shell forms", "`arac "} {
			if strings.Contains(reason, unrunnable) {
				t.Errorf("%q: refusal points at %q, which needs the Bash tool it just denied:\n%s",
					command, unrunnable, reason)
			}
		}
	}
}

// The search nudge is the only channel a NATIVE Grep has -- interception never sees one -- and
// a directory is how that tool is normally scoped.
//
// worthNudging asks namesAnIndexedFile for evidence, and existingReadFiles keeps regular files
// only: correct for a read, where a directory is not a target, and inverted as the sole test for
// a search. `Grep{pattern, path: "pkg"}` resolved to no file, reported "nothing indexed" and was
// silently exempted, while a Grep with NO path was nudged.
func TestNativeGrepScopedToADirectoryIsStillNudged(t *testing.T) {
	root, dbPath := scannedProject(t)
	cfg := helper.DefaultConfig()
	cfg.Mode = helper.ModeCLI
	if err := helper.SaveConfig(cfg, helper.ConfigPath(dbPath)); err != nil {
		t.Fatal(err)
	}

	nudged := func(input map[string]interface{}) bool {
		raw, err := json.Marshal(map[string]interface{}{
			"hook_event_name": "PostToolUse",
			"tool_name":       "Grep",
			"cwd":             root,
			"tool_input":      input,
		})
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		runClaudeGuardHook(bytes.NewReader(raw), &out)
		return strings.Contains(out.String(), "arac grep")
	}

	for _, input := range []map[string]interface{}{
		{"pattern": "Serve"},                   // nothing to resolve: no counter-evidence
		{"pattern": "Serve", "path": root},     // the project root
		{"pattern": "Serve", "path": "."},      // the same, as the model writes it
		{"pattern": "Serve", "path": "doc"},    // a subdirectory holding no indexed file
		{"pattern": "Serve", "path": "app.go"}, // a single indexed file
	} {
		if !nudged(input) {
			t.Errorf("Grep %v names a scope aracne can search better, but earned no nudge", input)
		}
	}
	// A directory outside the project is not aracne's to offer anything about.
	if nudged(map[string]interface{}{"pattern": "Serve", "path": "/etc"}) {
		t.Error("a Grep outside the project must not be nudged")
	}
}
