package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// OpenCode had the before-half of the guard and not the after-half, so on a default install no
// topology warning ever reached the model. `arac guard --post-tool` is the after-half, through
// the same drift check and ledger the Claude hook uses.
func TestPostToolReportsWarningsOnce(t *testing.T) {
	root, _ := scannedProject(t)
	t.Chdir(root)
	t.Setenv("CLAUDE_PROJECT_DIR", root)

	post := func(tool, command string) string {
		t.Helper()
		var out bytes.Buffer
		runPostToolCommand(tool, command, &out)
		var answer struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(out.Bytes(), &answer); err != nil {
			t.Fatalf("--post-tool must always answer with JSON, got %q: %v", out.String(), err)
		}
		return answer.Message
	}

	// The call starts from the state the pre-call scan saw, which seeds the ledger.
	runPreToolScanCommand()
	// A native edit that breaks main's call to Serve.
	body := "package main\n\nfunc Serve(n int) string { return \"ok\" }\n\nfunc main() { _ = Serve() }\n"
	if err := os.WriteFile(filepath.Join(root, "app.go"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	if msg := post("edit", ""); !strings.Contains(msg, "[signature_changed]") {
		t.Fatalf("a native edit that broke a caller reported nothing: %q", msg)
	}
	if msg := post("bash", "ls"); msg != "" {
		t.Errorf("a warning already reported was reported again: %q", msg)
	}
	// Reads change nothing and earn nothing.
	if msg := post("read", ""); msg != "" {
		t.Errorf("a read earned a report: %q", msg)
	}
}

// The generated plugin carries the after-half, classifies the command the MODEL wrote (not the
// rewrite), and the edit-sync plugin no longer reports warnings of its own beside it.
func TestOpenCodePluginsReportWarningsThroughOneChannel(t *testing.T) {
	plugin := openCodePreToolScanPlugin()
	for _, want := range []string{
		`"tool.execute.after": async (input, output) =>`,
		`"guard", "--post-tool"`,
		"written.set(input.callID, output?.args?.command)",
		"output.output = (output.output ??",
	} {
		if !strings.Contains(plugin, want) {
			t.Errorf("the pre-tool plugin is missing %q:\n%s", want, plugin)
		}
	}
	if strings.Index(plugin, "written.set(") > strings.Index(plugin, "output.args.command = rewritten") {
		t.Error("the written command must be captured BEFORE the rewrite replaces it")
	}
	if edit := openCodeNativeEditPlugin(); strings.Contains(edit, "Aracne warnings for") {
		t.Errorf("the edit-sync plugin still reports warnings itself, so they would print twice:\n%s", edit)
	}
}

// A shell write reports too, and aracne's own commands are exempt -- the Claude hook's rules,
// reached through the same driftCheckApplies.
func TestPostToolReportsAShellWriteAndExemptsArac(t *testing.T) {
	root, _ := scannedProject(t)
	t.Chdir(root)
	t.Setenv("CLAUDE_PROJECT_DIR", root)
	post := func(tool, command string) string {
		var out bytes.Buffer
		runPostToolCommand(tool, command, &out)
		var answer struct {
			Message string `json:"message"`
		}
		json.Unmarshal(out.Bytes(), &answer)
		return answer.Message
	}

	runPreToolScanCommand()
	body := "package main\n\nfunc Serve(n int) string { return \"ok\" }\n\nfunc main() { _ = Serve() }\n"
	if err := os.WriteFile(filepath.Join(root, "app.go"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if msg := post("bash", "arac warnings list"); msg != "" {
		t.Errorf("an `arac` command earns no drift check, got %q", msg)
	}
	if msg := post("bash", "sed -i 's/Serve()/Serve(n int)/' app.go"); !strings.Contains(msg, "signature_changed") {
		t.Errorf("a shell write that broke a caller reported nothing: %q", msg)
	}
}
