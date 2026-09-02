package lazydesc

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitRepo builds a repo whose committed file differs from its working copy, which is exactly
// the state a benchmark cell is in once the agent has started editing.
func gitRepo(t *testing.T) (dir, path string) {
	t.Helper()
	dir = t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git unavailable: %v: %s", err, out)
		}
	}
	path = filepath.Join(dir, "app.go")
	committed := "package main\n\nfunc Serve() string {\n\treturn \"clean\"\n}\n"
	if err := os.WriteFile(path, []byte(committed), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", "base"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return dir, path
}

func TestCleanSourceRefSpellings(t *testing.T) {
	for _, tc := range []struct{ env, want string }{
		{"", ""}, {"0", ""}, {"false", ""}, {"off", ""},
		{"1", "HEAD"}, {"true", "HEAD"}, {"on", "HEAD"},
		{"deadbeef", "deadbeef"}, {"  HEAD~2 ", "HEAD~2"},
	} {
		t.Setenv(CleanSourceEnv, tc.env)
		if got := cleanSourceRef(); got != tc.want {
			t.Errorf("env %q: got %q want %q", tc.env, got, tc.want)
		}
	}
}

// The headline behaviour: the agent has rewritten the file, and we still describe the
// committed body.
func TestCutFromCommitIgnoresWorkingTreeEdits(t *testing.T) {
	_, path := gitRepo(t)
	dirty := "package main\n\nfunc Serve() string {\n\treturn \"THE FIX\"\n}\n"
	if err := os.WriteFile(path, []byte(dirty), 0o644); err != nil {
		t.Fatal(err)
	}
	cut, ok := cutFromCommit("HEAD", path, "Serve", 3, 5)
	if !ok {
		t.Fatal("expected a clean cut")
	}
	if !strings.Contains(cut, `"clean"`) || strings.Contains(cut, "THE FIX") {
		t.Errorf("cut describes the working tree, not the commit: %q", cut)
	}
}

// A file the agent CREATED has no committed version; describing it would describe the fix.
func TestCutFromCommitRefusesAnUncommittedFile(t *testing.T) {
	dir, _ := gitRepo(t)
	created := filepath.Join(dir, "brand_new.go")
	if err := os.WriteFile(created, []byte("package main\n\nfunc New() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := cutFromCommit("HEAD", created, "New", 3, 3); ok {
		t.Error("described a file that does not exist at the commit")
	}
}

// The guard that makes "clean" mean clean: an edit shifted the declaration, so the range is
// in-bounds at the commit but points at the wrong code.
func TestCutFromCommitRefusesAShiftedRange(t *testing.T) {
	_, path := gitRepo(t)
	if _, ok := cutFromCommit("HEAD", path, "Serve", 1, 2); ok {
		t.Error("returned a cut that does not contain the declaration name")
	}
}

func TestCutFromCommitRefusesOutOfBoundsAndNonRepo(t *testing.T) {
	_, path := gitRepo(t)
	if _, ok := cutFromCommit("HEAD", path, "Serve", 3, 9999); ok {
		t.Error("accepted a range past the end of the committed file")
	}
	plain := filepath.Join(t.TempDir(), "loose.go")
	if err := os.WriteFile(plain, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := cutFromCommit("HEAD", plain, "", 1, 1); ok {
		t.Error("accepted a file outside any git repository")
	}
}
