//go:build audit

package topogrep

import (
	"os"
	"path/filepath"
	"testing"
)

// A-09b: the same ".."-prefix test as cli.displayRoot, in the search renderer. A repository
// containing a directory legitimately named "..data" (a Kubernetes ConfigMap or Secret volume
// mount) gets its absolute path echoed into every matching row.
func TestAudit_DisplayPathHandlesADotDotPrefixedDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "..data"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)

	target := filepath.Join(root, "..data", "app.go")
	if got := displayPath(target); filepath.IsAbs(got) {
		t.Errorf("displayPath(%q) = %q, want the relative form %q",
			target, got, "..data/app.go")
	}
}
