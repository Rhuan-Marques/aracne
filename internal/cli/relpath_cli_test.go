package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// A directory named `..data` is INSIDE the project. displayRoot and relativize both took a
// leading ".." for "outside the root" and printed such a file's absolute path in every row.
func TestDotDotNamedDirectoriesStayRelative(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "..data")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "app.go")
	t.Chdir(root)

	if got, want := displayRoot(target), filepath.Join("..data", "app.go"); got != want {
		t.Errorf("displayRoot(%q) = %q, want %q", target, got, want)
	}
	if got, want := relativize(root, []string{target}), []string{"..data/app.go"}; !reflect.DeepEqual(got, want) {
		t.Errorf("relativize = %v, want %v", got, want)
	}
	// A path that really is outside keeps its absolute form.
	outside := filepath.Join(filepath.Dir(root), "elsewhere.go")
	if got := relativize(root, []string{outside}); got[0] != outside {
		t.Errorf("an outside path must stay absolute, got %q", got[0])
	}
}
