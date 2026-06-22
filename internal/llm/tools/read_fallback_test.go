package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aracne/internal/topology"
)

// TestReadRunRawFileFallback checks the pure read tool falls back to returning
// the raw text of a file that is not in the topology, given its path.
func TestReadRunRawFileFallback(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(path, []byte("# Title\nbody\n"), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewRead(topology.New())
	args, _ := json.Marshal(map[string]string{"resource_id": path})
	out, err := tool.Run(args)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.HasPrefix(out, "doc.md\n") || !strings.Contains(out, "body") {
		t.Fatalf("unexpected output: %q", out)
	}
}

// TestReadRunMissingResource checks a path that matches neither a topology
// resource nor a readable file still errors.
func TestReadRunMissingResource(t *testing.T) {
	tool := NewRead(topology.New())
	args, _ := json.Marshal(map[string]string{"resource_id": filepath.Join(t.TempDir(), "absent.go")})
	if _, err := tool.Run(args); err == nil {
		t.Fatal("expected not-found error for missing resource/file")
	}
}
