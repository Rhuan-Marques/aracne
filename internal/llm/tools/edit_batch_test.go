package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// WHY BATCHING EXISTS, measured. Over netguard-20260831b the aracne arm spent 4.8 tool calls
// per task on edits against the baseline's 2.2 — and the baseline was not editing less, it was
// editing in bulk via a `python3` heredoc doing ten replacements in one Bash call. Twenty-one
// bursts of consecutive edits held 100 calls; batching collapses them to 21. Extra turns were
// the arm's one real regression, and edits were the largest contributor.

func batchDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// jsonPath quotes a path AS a JSON string, quotes included.
//
// A Windows path concatenated raw into JSON text is not JSON: `C:\Users\...` carries `\U`,
// which is an invalid escape, and every batch below was refused before it reached the tool.
func jsonPath(t *testing.T, p string) string {
	t.Helper()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func runEdits(t *testing.T, edits string) (string, error) {
	t.Helper()
	return NewEdit(nil, nil).Run(json.RawMessage(`{"edits":` + edits + `}`))
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestBatchAppliesEveryEditAcrossFiles(t *testing.T) {
	dir := batchDir(t, map[string]string{
		"a.go": "package a\n\nconst One = 1\nconst Two = 2\n",
		"b.go": "package b\n\nconst Three = 3\n",
	})
	a, b := filepath.Join(dir, "a.go"), filepath.Join(dir, "b.go")
	out, err := runEdits(t, `[
		{"file_path":`+jsonPath(t, a)+`,"old_string":"const One = 1","new_string":"const One = 10"},
		{"file_path":`+jsonPath(t, a)+`,"old_string":"const Two = 2","new_string":"const Two = 20"},
		{"file_path":`+jsonPath(t, b)+`,"old_string":"const Three = 3","new_string":"const Three = 30"}
	]`)
	if err != nil {
		t.Fatalf("batch failed: %v", err)
	}
	if !strings.Contains(out, "3 edits applied across 2 file(s)") {
		t.Errorf("summary should report the batch, got %q", out)
	}
	if got := read(t, a); !strings.Contains(got, "One = 10") || !strings.Contains(got, "Two = 20") {
		t.Errorf("both edits to a.go should land:\n%s", got)
	}
	if got := read(t, b); !strings.Contains(got, "Three = 30") {
		t.Errorf("edit to b.go should land:\n%s", got)
	}
}

// Edits apply in sequence against the accumulating content, so one may target text an earlier
// edit produced. That is the semantics of the shell heredoc this replaces, and without it a
// batch would be strictly weaker than the thing models already do by hand.
func TestBatchEditsSeeEachOthersChanges(t *testing.T) {
	dir := batchDir(t, map[string]string{"m.go": "package m\n\nfunc Old() {}\n"})
	p := filepath.Join(dir, "m.go")
	if _, err := runEdits(t, `[
		{"file_path":`+jsonPath(t, p)+`,"old_string":"func Old()","new_string":"func Middle()"},
		{"file_path":`+jsonPath(t, p)+`,"old_string":"func Middle()","new_string":"func New()"}
	]`); err != nil {
		t.Fatalf("chained edits failed: %v", err)
	}
	if got := read(t, p); !strings.Contains(got, "func New()") {
		t.Errorf("second edit should have seen the first's output:\n%s", got)
	}
}

// The property that makes a batch SAFER than a sequence of single edits, not just cheaper. A
// partly-applied batch is the worst outcome available: the model believes it made one coherent
// change, the repo holds half of it, and the half that landed has already invalidated the
// old_strings of the half that did not.
func TestBatchWritesNothingWhenAnyEditFails(t *testing.T) {
	dir := batchDir(t, map[string]string{
		"a.go": "package a\n\nconst One = 1\n",
		"b.go": "package b\n\nconst Two = 2\n",
	})
	a, b := filepath.Join(dir, "a.go"), filepath.Join(dir, "b.go")
	beforeA, beforeB := read(t, a), read(t, b)

	_, err := runEdits(t, `[
		{"file_path":`+jsonPath(t, a)+`,"old_string":"const One = 1","new_string":"const One = 10"},
		{"file_path":`+jsonPath(t, b)+`,"old_string":"NOT PRESENT ANYWHERE","new_string":"x"}
	]`)
	if err == nil {
		t.Fatal("a batch with an unmatched edit must fail")
	}
	if read(t, a) != beforeA || read(t, b) != beforeB {
		t.Fatal("no file may change when any edit in the batch fails")
	}
	// The report has to be actionable: which edit, and that the repo is untouched.
	for _, want := range []string{"edit 2 of 2", "nothing was written"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should contain %q, got: %v", want, err)
		}
	}
}

// A failure caused by an edit EARLIER in the same file must also roll the file back, including
// the changes that had already matched in memory.
func TestBatchRollsBackWithinASingleFile(t *testing.T) {
	dir := batchDir(t, map[string]string{"a.go": "package a\n\nconst One = 1\nconst Two = 2\n"})
	p := filepath.Join(dir, "a.go")
	before := read(t, p)
	if _, err := runEdits(t, `[
		{"file_path":`+jsonPath(t, p)+`,"old_string":"const One = 1","new_string":"const One = 10"},
		{"file_path":`+jsonPath(t, p)+`,"old_string":"const Missing = 9","new_string":"x"}
	]`); err == nil {
		t.Fatal("expected failure")
	}
	if read(t, p) != before {
		t.Fatalf("the first edit must not survive a later failure:\n%s", read(t, p))
	}
}

// Ambiguity is still refused, and inside a batch it must say WHICH edit was ambiguous.
func TestBatchReportsAmbiguityByIndex(t *testing.T) {
	dir := batchDir(t, map[string]string{"a.go": "x\nx\n"})
	p := filepath.Join(dir, "a.go")
	_, err := runEdits(t, `[
		{"file_path":`+jsonPath(t, p)+`,"old_string":"x","new_string":"y"}
	]`)
	if err == nil || !strings.Contains(err.Error(), "matched 2 times") {
		t.Fatalf("ambiguous single edit should be refused, got: %v", err)
	}
	// replace_all is per-edit, so the same batch succeeds when the caller opts in.
	if _, err := runEdits(t, `[
		{"file_path":`+jsonPath(t, p)+`,"old_string":"x","new_string":"y","replace_all":true}
	]`); err != nil {
		t.Fatalf("replace_all should apply per edit: %v", err)
	}
	if got := read(t, p); got != "y\ny\n" {
		t.Errorf("file = %q, want %q", got, "y\ny\n")
	}
}

// One lock per distinct file, taken in sorted order. Two agents batching the same pair in
// opposite orders would otherwise deadlock — a batch is the first thing in this tool that can
// hold more than one lock at once.
func TestDistinctPathsAreSortedAndDeduplicated(t *testing.T) {
	// An absolute root valid on every platform: distinctPaths reports each path through
	// filepath.Abs, and "/z/a.go" is rooted but carries no VOLUME, so on Windows it came back
	// drive-qualified and matched neither literal.
	root, err := filepath.Abs(filepath.FromSlash("/z"))
	if err != nil {
		t.Fatal(err)
	}
	a, b := filepath.Join(root, "a.go"), filepath.Join(root, "b.go")
	ops := []editOp{
		{FilePath: b}, {FilePath: a}, {FilePath: b},
	}
	got := distinctPaths(ops)
	if len(got) != 2 || got[0] != a || got[1] != b {
		t.Fatalf("distinctPaths = %v, want sorted unique [%s %s]", got, a, b)
	}
}
