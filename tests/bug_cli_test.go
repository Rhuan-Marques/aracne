package tests_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// bugProject scans a tiny Go project and returns its dir and the id of a real resource.
func bugProject(t *testing.T) (dir string, nodeID string) {
	t.Helper()
	dir = t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module bugproj\n\ngo 1.22\n")
	writeFile(t, filepath.Join(dir, "main.go"), "package main\n\nfunc Divide(a, b int) int { return a / b }\n\nfunc main() { _ = Divide(1, 1) }\n")
	mustRun(t, dir, "scan", ".")
	return dir, "bugproj.Divide"
}

// TestBugListJSONIsParseable covers the orchestration channel the generated slash commands
// drive. The main agent deliberately has no bug_list MCP tool -- a tool schema is
// unconditional context cost on every request, while a shell call costs nothing until the
// command that needs it runs -- so `arac bug list --json` is how the fan-out gets its work.
// That output has to decode.
func TestBugListJSONIsParseable(t *testing.T) {
	dir, nodeID := bugProject(t)

	// An empty result must still be an array, never the "No bugs found." prose: the consumer
	// is a parser, and an empty round is the common case in a later fan-out pass.
	var empty []map[string]any
	out := mustRun(t, dir, "bug", "list", "--json")
	if err := json.Unmarshal([]byte(lastJSON(out)), &empty); err != nil {
		t.Fatalf("empty --json output must decode as an array: %v\n%s", err, out)
	}
	if len(empty) != 0 {
		t.Fatalf("expected no bugs, got %d", len(empty))
	}

	mustRun(t, dir, "bug", "report", "--node", nodeID, "--description", "division by zero when b==0")

	var bugs []struct {
		ID          string `json:"id"`
		NodeID      string `json:"node_id"`
		Description string `json:"description"`
		State       string `json:"state"`
	}
	out = mustRun(t, dir, "bug", "list", "--state", "pending", "--json")
	if err := json.Unmarshal([]byte(lastJSON(out)), &bugs); err != nil {
		t.Fatalf("--json output must decode: %v\n%s", err, out)
	}
	if len(bugs) != 1 {
		t.Fatalf("expected 1 pending bug, got %d:\n%s", len(bugs), out)
	}
	if bugs[0].NodeID != nodeID || bugs[0].State != "pending" {
		t.Fatalf("unexpected bug: %+v", bugs[0])
	}
}

// TestBugReportRejectsUnknownNode is the end-to-end form of the lossy-intake defect: an id the
// topology does not hold used to be accepted and then silently deleted by the next scan.
func TestBugReportRejectsUnknownNode(t *testing.T) {
	dir, _ := bugProject(t)

	out, err := runLtp(t, dir, "bug", "report", "--node", "does.not.Exist", "--description", "x")
	if err == nil {
		t.Fatalf("reporting a bug on an unknown node must fail:\n%s", out)
	}
	if !strings.Contains(out, "does.not.Exist") {
		t.Errorf("the error should name the rejected id:\n%s", out)
	}
}

// TestBugDeleteRejectsMissingID: a delete that matched nothing must not report success.
func TestBugDeleteRejectsMissingID(t *testing.T) {
	dir, _ := bugProject(t)
	if out, err := runLtp(t, dir, "bug", "delete", "bug_does_not_exist"); err == nil {
		t.Fatalf("deleting an unknown bug id must fail:\n%s", out)
	}
}

// TestBugUsageFollowsTheFeatureFlag pins the "undocumented" half of gating: with the feature
// off the `arac bug` lines are absent from the usage banner, and with it on they are back.
// The subcommand itself keeps working either way -- it is the orchestration channel.
func TestBugUsageFollowsTheFeatureFlag(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module u\n\ngo 1.22\n")
	writeFile(t, filepath.Join(dir, "main.go"), "package main\n\nfunc main() {}\n")
	mustRun(t, dir, "scan", ".")

	out, _ := runLtp(t, dir, "help")
	if strings.Contains(out, "arac bug report") {
		t.Errorf("bug commands must be undocumented while the feature is off:\n%s", out)
	}

	enableBugManagement(t, dir)
	out, _ = runLtp(t, dir, "help")
	if !strings.Contains(out, "arac bug report") {
		t.Errorf("bug commands should be documented once the feature is on:\n%s", out)
	}
}

// lastJSON returns the trailing JSON document in a command's output, skipping any
// informational lines the CLI printed first (e.g. a scan notice on stderr-merged output).
func lastJSON(out string) string {
	if i := strings.IndexAny(out, "[{"); i >= 0 {
		return out[i:]
	}
	return out
}
