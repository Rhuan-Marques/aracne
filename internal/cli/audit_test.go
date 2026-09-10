//go:build audit

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topogrep"
	"github.com/Rhuan-Marques/aracne/internal/topology"
)

// firePostToolUse runs the guard's PostToolUse path and returns the additionalContext it
// emitted, or "" when it stayed silent.
func firePostToolUse(t *testing.T, root, toolName string, toolInput map[string]interface{}) string {
	t.Helper()
	raw, err := json.Marshal(map[string]interface{}{
		"hook_event_name": "PostToolUse",
		"tool_name":       toolName,
		"tool_input":      toolInput,
		"cwd":             root,
	})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	runClaudeGuardHook(bytes.NewReader(raw), &out)
	return out.String()
}

// A-07: preToolScan seeds the already-reported set BEFORE it runs the scan, so warnings the
// scan itself discovers -- a git checkout, a rebase, a teammate's edit -- are absent from the
// seed and get reported by the next drift check under "Topology re-synced after a shell
// command wrote to the project". The seed exists to prevent exactly this misattribution.
func TestAudit_GuardDoesNotBlameTheModelForExternalDrift(t *testing.T) {
	root, _ := scannedProject(t)

	// A change nothing in the session made: Serve grows a parameter, so main's call no
	// longer fits it.
	body := "package main\n\nfunc Serve(n int) string { return \"ok\" }\n\nfunc main() { _ = Serve() }\n"
	if err := os.WriteFile(filepath.Join(root, "app.go"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	// A read-only command. The pre-tool scan runs and discovers the drift.
	firePreToolUse(t, root, "Bash", map[string]interface{}{"command": "go build ./..."})
	out := firePostToolUse(t, root, "Bash", map[string]interface{}{"command": "go build ./..."})

	if strings.Contains(out, "Topology re-synced after a shell command wrote to the project") {
		t.Errorf("`go build ./...` was blamed for a change it did not make:\n%s", out)
	}
}

// A-08: an answer over the over-serve ceiling makes serveCommand return ok=false, and RunCmd
// answers ok=false by running the command the caller typed. For a RESOURCE ID operand that
// means the shell runs `cat <id>` and the model gets "No such file or directory" -- the exact
// outcome rawWindowBytes' doc comment names as the bug it was written to fix. The 32 KB
// OverserveMaxBytes cap keeps it reachable however accurate the estimate is.
func TestAudit_OverBudgetResourceIDIsRefusedNotPassedThrough(t *testing.T) {
	root, dbPath := scannedProject(t)

	// One function, under the 160-line symbol abridgement threshold, whose rendered source
	// still exceeds the 32 KB ceiling.
	var b strings.Builder
	b.WriteString("package main\n\nfunc Wide() {\n")
	for i := 0; i < 120; i++ {
		b.WriteString("\t// " + strings.Repeat("payload ", 60) + "\n")
	}
	b.WriteString("}\n")
	if err := os.WriteFile(filepath.Join(root, "wide.go"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := `{"mode":"intercept_id","read":{"max_file_size":1048576}}`
	if err := os.WriteFile(filepath.Join(root, ".aracne", "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	mgr := topology.New()
	if err := mgr.Load(dbPath); err != nil {
		t.Fatal(err)
	}
	if err := mgr.FullScan(root, NewScannerRegistry()); err != nil {
		t.Fatal(err)
	}

	t.Chdir(root)
	_, _, refusal, ok := serveCommand([]string{"cat", "example.com/proj.Wide"}, false)
	if !ok && refusal == nil {
		t.Errorf("an over-budget read of a resource ID fell through to passthrough; " +
			"the shell then runs `cat example.com/proj.Wide`, which reports " +
			"\"No such file or directory\" about an id that resolves perfectly well")
	}
}

// A-09: displayRoot decides a path escapes the topology root with HasPrefix(rel, ".."), which
// also matches a directory legitimately named "..data" -- what Kubernetes ConfigMap and Secret
// volume mounts are actually called. guard_scope.isUnder gets the same test right.
func TestAudit_DisplayRootHandlesADotDotPrefixedDirectory(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "..data")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "app.go")
	t.Chdir(root)

	if got := displayRoot(target); filepath.IsAbs(got) {
		t.Errorf("displayRoot(%q) = %q; the file is inside the root, so the relative form "+
			"%q was expected -- the absolute path is repeated in every grep row",
			target, got, filepath.Join("..data", "app.go"))
	}
}

// A-10: restrictResultToRange narrows a resource-scoped search to the resource's own lines and
// recomputes Files/Counts/Total -- but leaves Result.Context untouched. FormatResult reads
// context by absolute line number, so a -B/-A window still prints lines from outside the
// declaration the caller scoped the search to.
func TestAudit_RangeRestrictionAlsoDropsOutOfRangeContext(t *testing.T) {
	res := &topogrep.Result{
		Matches: []topogrep.Match{
			{Path: "app.go", Line: 10, Text: "match inside the resource"},
			{Path: "app.go", Line: 2, Text: "match above the resource"},
		},
		Counts: map[string]int{"app.go": 2},
		Files:  []string{"app.go"},
		Total:  2,
		Context: map[string]map[int]string{
			"app.go": {8: "line 8 - above the resource", 9: "line 9 - above the resource"},
		},
	}

	// The resource spans lines 10..20.
	restrictResultToRange(res, 10, 20)

	out := topogrep.FormatResult(res, topogrep.Options{Mode: topogrep.OutputContent, Before: 2})
	if strings.Contains(out, "above the resource") {
		t.Errorf("a search scoped to lines 10-20 rendered context from outside that range:\n%s", out)
	}
}
