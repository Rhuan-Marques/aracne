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

// The case that motivated the range translation: the agent inserts lines ABOVE a neighbour, so
// the neighbour's present-day range no longer names its committed lines. Describing it with
// the untranslated range would cut whatever now sits there.
func TestCutFromCommitTranslatesAShiftedNeighbour(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git unavailable: %v: %s", err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "t")

	path := filepath.Join(dir, "app.go")
	// Neighbour sits at lines 3-5 when committed.
	committed := "package main\n\nfunc Neighbour() string {\n\treturn \"clean neighbour\"\n}\n"
	if err := os.WriteFile(path, []byte(committed), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-qm", "base")

	// The agent inserts a whole function above it. Neighbour is now at lines 7-9.
	dirty := "package main\n\nfunc AgentAdded() int {\n\treturn 42\n}\n\n" +
		"func Neighbour() string {\n\treturn \"clean neighbour\"\n}\n"
	if err := os.WriteFile(path, []byte(dirty), 0o644); err != nil {
		t.Fatal(err)
	}

	cut, ok := cutFromCommit("HEAD", path, "Neighbour", 7, 9)
	if !ok {
		t.Fatal("expected the shifted neighbour to map back to its committed lines")
	}
	if !strings.Contains(cut, "clean neighbour") || strings.Contains(cut, "AgentAdded") {
		t.Errorf("mapped to the wrong lines: %q", cut)
	}

	// The inserted function itself has no committed image and must be refused.
	if _, ok := cutFromCommit("HEAD", path, "AgentAdded", 3, 5); ok {
		t.Error("described a declaration that does not exist at the commit")
	}
}

func TestMapRangeToPast(t *testing.T) {
	// 3 lines inserted at new-line 3; everything after shifts by +3.
	ins := []hunk{{oldStart: 2, oldCount: 0, newStart: 3, newCount: 3}}
	if s, e, ok := mapRangeToPast(ins, 7, 9); !ok || s != 4 || e != 6 {
		t.Errorf("after an insertion: got (%d,%d,%v) want (4,6,true)", s, e, ok)
	}
	if _, _, ok := mapRangeToPast(ins, 3, 5); ok {
		t.Error("a range inside the inserted hunk must be refused")
	}
	if s, e, ok := mapRangeToPast(ins, 1, 2); !ok || s != 1 || e != 2 {
		t.Errorf("before the hunk: got (%d,%d,%v) want (1,2,true)", s, e, ok)
	}
	// 2 lines deleted: what follows shifts the other way.
	del := []hunk{{oldStart: 3, oldCount: 2, newStart: 2, newCount: 0}}
	if s, e, ok := mapRangeToPast(del, 5, 6); !ok || s != 7 || e != 8 {
		t.Errorf("after a deletion: got (%d,%d,%v) want (7,8,true)", s, e, ok)
	}
	if s, e, ok := mapRangeToPast(nil, 4, 8); !ok || s != 4 || e != 8 {
		t.Errorf("no hunks is identity: got (%d,%d,%v)", s, e, ok)
	}
}

// The two mappings that decide whether this feature is useful or merely safe. Both concern the
// declaration the agent actually EDITED -- which is the one a task is about, and the one an
// over-cautious refusal would throw away.
func TestEditedDeclarationStillMaps(t *testing.T) {
	// A same-size line rewrite inside the body: the range is unchanged at the commit.
	inPlace := []hunk{{oldStart: 4, oldCount: 1, newStart: 4, newCount: 1}}
	if s, e, ok := mapRangeToPast(inPlace, 3, 5); !ok || s != 3 || e != 5 {
		t.Errorf("in-place body edit: got (%d,%d,%v) want (3,5,true)", s, e, ok)
	}
	// Two lines added inside the body: the committed declaration is that much shorter.
	inserted := []hunk{{oldStart: 4, oldCount: 0, newStart: 5, newCount: 2}}
	if s, e, ok := mapRangeToPast(inserted, 3, 8); !ok || s != 3 || e != 6 {
		t.Errorf("insertion inside body: got (%d,%d,%v) want (3,6,true)", s, e, ok)
	}
}
