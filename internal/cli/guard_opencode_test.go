package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
)

// INTERCEPTION IS NOT A CLAUDE CODE FEATURE, it is the product on the two intercepting modes.
//
// It was written against the one mechanism Claude Code offers -- a PreToolUse hook returning
// `hookSpecificOutput.updatedInput` -- and OpenCode, whose `tool.execute.before` receives a
// mutable `output.args`, got nothing. So an OpenCode project on either intercepting mode had no
// read surface at all while the AGENTS.md `arac setup` wrote for it said `cat`, `head -40` and
// `sed -n` came back enriched, and every mode's contract promised an annotated `grep` that
// nothing delivered. `arac guard --rewrite` is that decision on its own, so both harnesses go
// through interceptCommand and cannot disagree.
func TestRewriteCommandMatchesTheHookDecision(t *testing.T) {
	root, _ := scannedProject(t)
	app := filepath.Join(root, "app.go")
	t.Chdir(root)

	rewrite := func(command string) string {
		t.Helper()
		var out bytes.Buffer
		runRewriteCommand(command, &out)
		var answer struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal(out.Bytes(), &answer); err != nil {
			t.Fatalf("--rewrite must always answer with JSON, got %q: %v", out.String(), err)
		}
		return answer.Command
	}

	// The shapes the intercepting modes exist to serve.
	for _, command := range []string{
		"cat " + app,
		"head -20 " + app,
		"sed -n '1,10p' " + app,
		"grep -rn Serve " + root,
	} {
		got := rewrite(command)
		if got == "" {
			t.Errorf("%q must be rewritten on this surface too", command)
			continue
		}
		if !strings.Contains(got, "cmd -") {
			t.Errorf("%q rewrote to something that is not an `arac cmd`: %s", command, got)
		}
	}

	// And every guard rail the hook applies applies here, because it is the same function.
	for _, command := range []string{
		"grep -rn Serve " + root + " | wc -l",        // an opaque consumer
		"cat " + filepath.Join(root, "CHANGELOG.md"), // nothing indexed to serve
		"cat " + app + " > /tmp/copy.go",             // a redirect
		"echo hi",                                    // not a read or a search
		"arac cmd -- cat " + app,                     // its own output
	} {
		if got := rewrite(command); got != "" {
			t.Errorf("%q must not be rewritten, got: %s", command, got)
		}
	}
}

// The two surfaces must reach the SAME answer for the same command, or a project gets one
// product on Claude Code and another on OpenCode.
func TestRewriteCommandAgreesWithTheClaudeHook(t *testing.T) {
	root, _ := scannedProject(t)
	app := filepath.Join(root, "app.go")
	t.Chdir(root)

	viaHook := func(command string) string {
		t.Helper()
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
			return ""
		}
		var decoded struct {
			HookSpecificOutput struct {
				UpdatedInput map[string]interface{} `json:"updatedInput"`
			} `json:"hookSpecificOutput"`
		}
		if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
		cmd, _ := decoded.HookSpecificOutput.UpdatedInput["command"].(string)
		return cmd
	}
	viaPlugin := func(command string) string {
		t.Helper()
		var out bytes.Buffer
		runRewriteCommand(command, &out)
		var answer struct {
			Command string `json:"command"`
		}
		json.Unmarshal(out.Bytes(), &answer)
		return answer.Command
	}

	for _, command := range []string{
		"cat " + app,
		"grep -rn Serve " + root,
		"grep -rn Serve " + root + " | head -30",
		"grep -rn Serve " + root + " | wc -l",
		"cd " + root + " && cat " + app,
		"echo hi",
	} {
		if hook, plugin := viaHook(command), viaPlugin(command); hook != plugin {
			t.Errorf("the two harnesses disagree about %q:\n  claude code: %q\n  opencode:    %q",
				command, hook, plugin)
		}
	}
}

// The generated plugin has to actually CALL it, and on the argument OpenCode gives it.
func TestOpenCodePluginRewritesTheBashCommand(t *testing.T) {
	plugin := openCodePreToolScanPlugin()
	for _, want := range []string{
		`"guard", "--pre-scan"`,  // the freshness half, unchanged
		`"guard", "--rewrite"`,   // the interception half
		"output.args.command",    // OpenCode's spelling of updatedInput
		`input?.tool !== "bash"`, // only a shell call carries a command
	} {
		if !strings.Contains(plugin, want) {
			t.Errorf("the generated plugin is missing %q:\n%s", want, plugin)
		}
	}
	// The hook signature has to take the output object, or there is nothing to mutate.
	if !strings.Contains(plugin, `"tool.execute.before": async (input, output) =>`) {
		t.Error("tool.execute.before must receive the mutable args object")
	}
}

// The mode is read at CALL time on both surfaces, so flipping it takes effect without
// re-running setup -- and a mode that does not intercept reads must not start.
func TestRewriteHonoursTheModeAtCallTime(t *testing.T) {
	root, dbPath := scannedProject(t)
	app := filepath.Join(root, "app.go")
	t.Chdir(root)

	rewrite := func() string {
		var out bytes.Buffer
		runRewriteCommand("cat "+app, &out)
		var answer struct {
			Command string `json:"command"`
		}
		json.Unmarshal(out.Bytes(), &answer)
		return answer.Command
	}

	if rewrite() == "" {
		t.Fatal("the fixture is on an intercepting mode; a read must be rewritten")
	}

	cfg := helper.DefaultConfig()
	cfg.Mode = helper.ModeCLI
	if err := helper.SaveConfig(cfg, helper.ConfigPath(dbPath)); err != nil {
		t.Fatal(err)
	}
	if got := rewrite(); got != "" {
		t.Errorf("ModeCLI answers reads through `arac read`; rewriting one is a second answer: %s", got)
	}
}
