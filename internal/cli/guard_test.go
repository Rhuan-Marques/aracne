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
		want    []string
	}{
		{"", nil},
		{"   ", nil},
		{"cat f", []string{"read"}},
		{"head -n5 f", []string{"read"}},
		{"tail -f log", []string{"read"}},
		{"less f", []string{"read"}},
		{"grep x f", []string{"grep"}},
		{"rg x", []string{"grep"}},
		{"sed -i s/a/b/ f", []string{"edit"}},
		{"awk '{print}' f", []string{"edit"}},
		{"cat f | grep x", []string{"read", "grep"}},
		{"ls; grep x f", []string{"grep"}},
		{"true && grep x", []string{"grep"}},
		{"FOO=1 grep x", []string{"grep"}},
		{"sudo grep x", []string{"grep"}},
		{"/usr/bin/grep x", []string{"grep"}},
		{`echo "use grep here"`, nil},
		{"git grep foo", nil},
		{"npm test", nil},
		{"Get-Content f", []string{"read"}},
		{"Select-String x f", []string{"grep"}},
		{"echo $(grep x)", []string{"grep"}},
	}
	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			got := commandKeys(tt.command)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("commandKeys(%q) = %v, want %v", tt.command, got, tt.want)
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
		wantDeny  bool
		wantInMsg string
	}{
		{"native grep allowed", "Grep", nil, set(), false, ""},
		{"native grep blocked", "Grep", nil, set("grep"), true, "mcp__aracne__grep"},
		{"native read blocked", "Read", nil, set("read"), true, "blocked_tools: read"},
		{"native write blocked", "Write", nil, set("write"), true, "mcp__aracne__write"},
		{"native edit not in blocked", "Edit", nil, set("grep"), false, ""},
		{"bash grep blocked", "Bash", bash("grep x | head"), set("grep"), true, "grep"},
		{"bash cat allowed", "Bash", bash("cat f"), set(), false, ""},
		{"bash cat blocked via read", "Bash", bash("cat f"), set("read"), true, "blocked_tools: read"},
		{"whole bash blocked", "Bash", bash("npm test"), set("bash"), true, "blocked_tools: bash"},
		{"bash unblocked", "Bash", bash("npm test"), set(), false, ""},
		{"multiedit ignored", "MultiEdit", nil, set("edit", "read", "grep", "write", "bash"), false, ""},
		{"glob ignored", "Glob", nil, set("read"), false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := decideGuard(tt.tool, tt.input, tt.blocked)
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
