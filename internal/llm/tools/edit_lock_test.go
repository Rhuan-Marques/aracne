package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology"
)

// TestEditApplyStaleEditError checks the concurrent-edit feedback: when an edit
// was queued behind another agent (waited=true) and its old_string is now gone,
// the error tells the agent to re-read and retry; an uncontended miss stays a
// plain not-found error.
func TestEditApplyStaleEditError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("hello world\n"), 0644); err != nil {
		t.Fatal(err)
	}
	e := NewEdit(topology.New(), nil)

	_, err := e.apply(path, "MISSING", "x", false, true)
	if err == nil || !strings.Contains(err.Error(), "another agent changed this file") {
		t.Fatalf("waited stale edit: got err %v, want re-read guidance", err)
	}

	_, err = e.apply(path, "MISSING", "x", false, false)
	if err == nil || strings.Contains(err.Error(), "another agent changed this file") {
		t.Fatalf("uncontended miss: got err %v, want plain not-found", err)
	}
}
