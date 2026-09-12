package tools

import (
	"os"
	"path/filepath"
	"testing"
)

// TestEditAndWriteFollowSymlinks pins ST-6 at the tools the model calls: `edit` and `write` on
// a symlinked source used to replace the link with a regular file and never touch the real one.
func TestEditAndWriteFollowSymlinks(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "symshared", "shared.go")
	link := filepath.Join(dir, "proj", "pkg", "shared.go")
	for _, d := range []string{filepath.Dir(real), filepath.Dir(link)} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(real, []byte("package pkg\n\nfunc Shared() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../symshared/shared.go", link); err != nil {
		t.Skipf("symlinks unavailable here: %v", err)
	}
	stillLink := func() {
		t.Helper()
		if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("the symlink must survive, got %v, %v", info, err)
		}
	}

	if _, err := NewEdit(nil, nil).apply(link, "return 1", "return 42", false, false); err != nil {
		t.Fatal(err)
	}
	stillLink()
	if got := readFile(t, real); got != "package pkg\n\nfunc Shared() int { return 42 }\n" {
		t.Fatalf("edit must change the link's target, got %q", got)
	}

	if _, err := NewWrite(nil, nil).apply(link, "package pkg\n"); err != nil {
		t.Fatal(err)
	}
	stillLink()
	if got := readFile(t, real); got != "package pkg\n" {
		t.Fatalf("write must change the link's target, got %q", got)
	}
}
