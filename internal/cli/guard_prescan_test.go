package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology"
)

// addFunctionOutsideAracne writes a change the way everything the guard cannot see does it --
// a git checkout, an editor save, a command aracne never classified. Nothing tells the
// topology about it, so only a scan can notice.
func addFunctionOutsideAracne(t *testing.T, root string) {
	t.Helper()
	body := "package main\n\nfunc Serve() string { return \"ok\" }\n\nfunc AddedOutside() int { return 7 }\n\nfunc main() { _ = Serve() }\n"
	if err := os.WriteFile(filepath.Join(root, "app.go"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// topologyKnows reports whether the stored topology has a resource whose ID names fn.
func topologyKnows(t *testing.T, dbPath, fn string) bool {
	t.Helper()
	mgr := topology.New()
	if err := mgr.Load(dbPath); err != nil {
		t.Fatalf("load topology: %v", err)
	}
	topo, err := mgr.ReadAll()
	if err != nil {
		t.Fatalf("read topology: %v", err)
	}
	for id := range topo.Resources {
		if strings.Contains(id, fn) {
			return true
		}
	}
	return false
}

// firePreToolUse runs the guard's PreToolUse path for one tool call and returns whatever
// decision it emitted.
func firePreToolUse(t *testing.T, root, toolName string, toolInput map[string]interface{}) string {
	t.Helper()
	event := map[string]interface{}{
		"hook_event_name": "PreToolUse",
		"tool_name":       toolName,
		"tool_input":      toolInput,
		"cwd":             root,
	}
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	runClaudeGuardHook(bytes.NewReader(raw), &out)
	return out.String()
}

// The default: every tool call the guard sees re-syncs the graph first, so a change made
// outside the session is already indexed by the time the call reads it.
func TestPreToolScanRunsBeforeEveryGuardedCall(t *testing.T) {
	for _, call := range []struct {
		tool  string
		input map[string]interface{}
	}{
		{"Read", map[string]interface{}{"file_path": "app.go"}},
		{"Grep", map[string]interface{}{"pattern": "Serve"}},
		{"Bash", map[string]interface{}{"command": "go build ./..."}},
	} {
		t.Run(call.tool, func(t *testing.T) {
			root, dbPath := scannedProject(t)
			if topologyKnows(t, dbPath, "AddedOutside") {
				t.Fatal("fixture already knows AddedOutside")
			}
			addFunctionOutsideAracne(t, root)

			firePreToolUse(t, root, call.tool, call.input)

			if !topologyKnows(t, dbPath, "AddedOutside") {
				t.Fatalf("%s did not re-sync the topology before the call", call.tool)
			}
		})
	}
}

// scan.pre_tool: "none" is the one spelling that switches the freshness guarantee off, and it
// must switch it off completely -- that is what a project pairing aracne with its own live
// scanner is buying.
func TestPreToolScanNoneSkipsTheScan(t *testing.T) {
	root, dbPath := scannedProject(t)
	cfg := `{"scan":{"pre_tool":"none"},"read":{"max_file_size":524288}}`
	if err := os.WriteFile(filepath.Join(root, ".aracne", "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	addFunctionOutsideAracne(t, root)

	firePreToolUse(t, root, "Read", map[string]interface{}{"file_path": "app.go"})

	if topologyKnows(t, dbPath, "AddedOutside") {
		t.Fatal("pre_tool \"none\" still scanned")
	}
}

// The scan must not change what the guard decides. A read of an indexed file is still
// rewritten to `arac cmd`, and the rewrite must carry the original command text.
func TestPreToolScanLeavesTheDecisionAlone(t *testing.T) {
	root, _ := scannedProject(t)
	addFunctionOutsideAracne(t, root)
	command := "cat " + filepath.Join(root, "app.go")

	out := firePreToolUse(t, root, "Bash", map[string]interface{}{"command": command})

	// Decoded, not matched as text: `out` is JSON, so every separator in a Windows path is
	// escaped in it and a search for the raw command finds nothing there.
	var payload struct {
		HookSpecificOutput struct {
			UpdatedInput struct {
				Command string `json:"command"`
			} `json:"updatedInput"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("hook output is not JSON (%v): %s", err, out)
	}
	if got := payload.HookSpecificOutput.UpdatedInput.Command; !strings.Contains(got, "cmd -- "+command) {
		t.Fatalf("interception did not survive the pre-tool scan, got: %s", out)
	}
}

// `arac guard --pre-scan` is the OpenCode half of the same behavior: no event, no decision,
// just the scan the plugin runs in front of a tool call.
func TestPreScanCommandSyncsFromTheWorkingDirectory(t *testing.T) {
	root, dbPath := scannedProject(t)
	addFunctionOutsideAracne(t, root)

	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(prev)

	runPreToolScanCommand()

	if !topologyKnows(t, dbPath, "AddedOutside") {
		t.Fatal("`arac guard --pre-scan` did not re-sync the topology")
	}
}
