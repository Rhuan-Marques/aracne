package helper

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable here: %v", err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// assertStillLink fails when path is no longer a symbolic link to want.
func assertStillLink(t *testing.T, path, want string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s was replaced by a regular file; the link must survive the write", path)
	}
	// Slash-normalized: Windows stores a link target with its own separators, so a link
	// created as "../../shared/shared.go" reads back as "..\..\shared\shared.go". The
	// target is unchanged -- only its spelling is the platform's.
	if got, _ := os.Readlink(path); filepath.ToSlash(got) != filepath.ToSlash(want) {
		t.Fatalf("%s now points at %q, want %q", path, got, want)
	}
}

// assertPerm checks a file's Unix permission bits, where there are any.
//
// Windows has none: os.Chmod there toggles the read-only attribute and nothing else, so every
// writable file reports 0666 however it was created. The bits are what this file is about on
// every other platform, so the check is skipped rather than weakened.
func assertPerm(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s has permissions %v, want %v", path, got, want)
	}
}

// TestAtomicWriteFile_WritesThroughSymlink pins ST-6: the rename replaced the symlink itself,
// so `arac edit` / `arac write` on a symlinked source reported success, cut the link, and left
// the real file unchanged.
func TestAtomicWriteFile_WritesThroughSymlink(t *testing.T) {
	dir := t.TempDir()
	shared := filepath.Join(dir, "shared")
	pkg := filepath.Join(dir, "proj", "pkg")
	for _, d := range []string{shared, pkg} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	real := filepath.Join(shared, "shared.go")
	if err := os.WriteFile(real, []byte("return 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("relative", func(t *testing.T) {
		link := filepath.Join(pkg, "rel.go")
		mustSymlink(t, "../../shared/shared.go", link)
		if err := AtomicWriteFile(link, []byte("return 42\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		assertStillLink(t, link, "../../shared/shared.go")
		if got := mustRead(t, real); got != "return 42\n" {
			t.Fatalf("the link's target must receive the write, got %q", got)
		}
		assertPerm(t, real, 0o600) // the target keeps its own permissions
	})

	t.Run("chain", func(t *testing.T) {
		abs := filepath.Join(pkg, "abs.go")
		mustSymlink(t, real, abs)
		chain := filepath.Join(pkg, "chain.go")
		mustSymlink(t, "abs.go", chain)
		if err := AtomicWriteFile(chain, []byte("return 7\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		assertStillLink(t, chain, "abs.go")
		assertStillLink(t, abs, real)
		if got := mustRead(t, real); got != "return 7\n" {
			t.Fatalf("a chain of links is followed to its end, got %q", got)
		}
	})

	t.Run("dangling", func(t *testing.T) {
		link := filepath.Join(pkg, "dangling.go")
		mustSymlink(t, "../../shared/new.go", link)
		if err := AtomicWriteFile(link, []byte("new\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		assertStillLink(t, link, "../../shared/new.go")
		if got := mustRead(t, filepath.Join(shared, "new.go")); got != "new\n" {
			t.Fatalf("a dangling link creates its target, as os.WriteFile would, got %q", got)
		}
	})

	t.Run("loop", func(t *testing.T) {
		a, b := filepath.Join(pkg, "loopa.go"), filepath.Join(pkg, "loopb.go")
		mustSymlink(t, "loopb.go", a)
		mustSymlink(t, "loopa.go", b)
		if err := AtomicWriteFile(a, []byte("x\n"), 0o644); err == nil {
			t.Fatal("a symlink loop is an error, not a write that replaces one of the links")
		}
		assertStillLink(t, a, "loopb.go")
	})
}

// TestAtomicWriteFile_RegularFileUnchanged pins the behaviour ST-6 must not disturb: a regular
// file is replaced whole, keeps its permissions, and no temp file is left behind.
func TestAtomicWriteFile_RegularFileUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "run.sh")
	if err := os.WriteFile(path, []byte("old\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := AtomicWriteFile(path, []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, path); got != "new\n" {
		t.Fatalf("got %q", got)
	}
	info, _ := os.Lstat(path)
	if !info.Mode().IsRegular() {
		t.Fatalf("a regular file stays regular, got %v", info.Mode())
	}
	assertPerm(t, path, 0o755) // and keeps its permissions
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("no temp file may be left behind, got %d entries", len(entries))
	}
	fresh := filepath.Join(dir, "fresh.txt")
	if err := AtomicWriteFile(fresh, []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	assertPerm(t, fresh, 0o640) // a new file gets perm
}
