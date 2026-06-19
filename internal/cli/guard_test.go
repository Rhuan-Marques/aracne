package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
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
		{"awk '{print}' f", true, []string{"edit"}},
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
		// file reads are still classified, and edit (sed/awk) is never exempt.
		{"git log | head -50", true, nil},
		{"cmd | tail", true, nil},
		{"cmd | grep err", true, nil},
		{"kubectl logs x | grep e | tail", true, nil},
		{"cat f | grep x", true, []string{"read"}},
		{"a || cat f", true, []string{"read"}},
		{"cmd | sed s/a/b/", true, []string{"edit"}},

		// Exemption off restores strict classification of piped reads.
		{"git log | head -50", false, []string{"read"}},
		{"cmd | grep err", false, []string{"grep"}},
		{"cat f | grep x", false, []string{"read", "grep"}},
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
		{"native grep blocked", "Grep", nil, set("grep"), true, true, "mcp__aracne__grep"},
		{"native read blocked", "Read", nil, set("read"), true, true, "blocked_tools: read"},
		{"native write blocked", "Write", nil, set("write"), true, true, "mcp__aracne__write"},
		{"native edit not in blocked", "Edit", nil, set("grep"), true, false, ""},
		{"bash grep blocked", "Bash", bash("grep x | head"), set("grep"), true, true, "grep"},
		{"bash cat allowed", "Bash", bash("cat f"), set(), true, false, ""},
		{"bash cat blocked via read", "Bash", bash("cat f"), set("read"), true, true, "blocked_tools: read"},
		{"whole bash blocked", "Bash", bash("npm test"), set("bash"), true, true, "blocked_tools: bash"},
		{"bash unblocked", "Bash", bash("npm test"), set(), true, false, ""},
		{"multiedit ignored", "MultiEdit", nil, set("edit", "read", "grep", "write", "bash"), true, false, ""},
		{"glob ignored", "Glob", nil, set("read"), true, false, ""},
		{"piped tail exempt", "Bash", bash("cmd | tail"), set("read"), true, false, ""},
		{"piped tail strict", "Bash", bash("cmd | tail"), set("read"), false, true, "blocked_tools: read"},
		{"piped grep exempt", "Bash", bash("cmd | grep x"), set("grep"), true, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := decideGuard(tt.tool, tt.input, tt.blocked, tt.exempt)
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

const blocksGrepConfig = `{"scan":{"mode":"default"},"llm":{"claude_code":{"main_agent":{"blocked_tools":["grep"]}}}}`

func TestRunClaudeGuardHook_PreToolDeny(t *testing.T) {
	withConfig(t, blocksGrepConfig, func() {
		var out bytes.Buffer
		in := strings.NewReader(`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"grep foo src"}}`)
		runClaudeGuardHook(in, &out)
		got := out.String()
		if !strings.Contains(got, `"permissionDecision":"deny"`) {
			t.Fatalf("expected deny decision, got: %q", got)
		}
		if !strings.Contains(got, "mcp__aracne__grep") {
			t.Fatalf("expected guidance in deny reason, got: %q", got)
		}
	})
}

const blocksReadConfig = `{"scan":{"mode":"default"},"llm":{"claude_code":{"main_agent":{"blocked_tools":["read"]}}}}`
const blocksReadStrictConfig = `{"scan":{"mode":"default"},"read":{"pipe_passthrough":false},"llm":{"claude_code":{"main_agent":{"blocked_tools":["read"]}}}}`

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

func TestRunClaudeGuardHook_PostToolWarning(t *testing.T) {
	withConfig(t, "", func() {
		var out bytes.Buffer
		in := strings.NewReader(`{"hook_event_name":"PostToolUse","tool_name":"Grep","tool_input":{"pattern":"x"}}`)
		runClaudeGuardHook(in, &out)
		got := out.String()
		if !strings.Contains(got, `"additionalContext"`) || !strings.Contains(got, "mcp__aracne__grep") {
			t.Fatalf("expected PostToolUse warning, got: %q", got)
		}
	})
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
