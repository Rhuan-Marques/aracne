package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests drive Edit with a nil manager, which skips the topology update:
// they are about the file mechanics (deletion, unique-match enforcement,
// replace_all). The topology side of edit is covered by the cross-language
// warning tests in internal/topology.

func seedFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "f.go")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// An empty new_string deletes the matched text. Without this, removing a block
// meant rewriting the whole file with `write`, or escaping to a shell the guard
// exists to discourage.
func TestEditApplyEmptyNewStringDeletes(t *testing.T) {
	path := seedFile(t, "keep me\nDELETE THIS\nkeep me too\n")
	e := NewEdit(nil, nil)

	out, err := e.apply(path, "DELETE THIS\n", "", false, false)
	if err != nil {
		t.Fatalf("deletion should be allowed, got %v", err)
	}
	if !strings.Contains(out, "edit succeeded") {
		t.Errorf("unexpected result %q", out)
	}
	if got, want := readFile(t, path), "keep me\nkeep me too\n"; got != want {
		t.Errorf("file = %q, want %q", got, want)
	}
}

// The CLI entry point used to reject an empty new_string before it ever reached
// the tool, so `arac edit` could not delete at all.
func TestEditRunEmptyNewStringDeletes(t *testing.T) {
	path := seedFile(t, "alpha\nbravo\ncharlie\n")
	e := NewEdit(nil, nil)

	args, _ := json.Marshal(map[string]any{
		"file_path": path, "old_string": "bravo\n", "new_string": "",
	})
	if _, err := e.Run(args); err != nil {
		t.Fatalf("Run with an empty new_string: %v", err)
	}
	if got, want := readFile(t, path), "alpha\ncharlie\n"; got != want {
		t.Errorf("file = %q, want %q", got, want)
	}
}

// old_string is still required: an empty one would match everywhere.
func TestEditRunStillRequiresOldString(t *testing.T) {
	path := seedFile(t, "alpha\n")
	e := NewEdit(nil, nil)
	args, _ := json.Marshal(map[string]any{
		"file_path": path, "old_string": "", "new_string": "x",
	})
	if _, err := e.Run(args); err == nil {
		t.Fatal("expected an error for an empty old_string")
	}
	if got := readFile(t, path); got != "alpha\n" {
		t.Errorf("file must be untouched, got %q", got)
	}
}

// A non-unique old_string silently took the FIRST match, which is a wrong edit
// that reports success — and worse for a deletion than a replacement.
func TestEditApplyAmbiguousMatchIsRefused(t *testing.T) {
	path := seedFile(t, "x := 1\ny := 2\nx := 1\n")
	e := NewEdit(nil, nil)

	_, err := e.apply(path, "x := 1\n", "", false, false)
	if err == nil {
		t.Fatal("expected an ambiguous match to be refused")
	}
	if !strings.Contains(err.Error(), "matched 2 times") {
		t.Errorf("error should say how many times it matched, got %q", err)
	}
	if !strings.Contains(err.Error(), "replace_all") {
		t.Errorf("error should point at the escape hatch, got %q", err)
	}
	// nothing may be written when the edit is refused
	if got, want := readFile(t, path), "x := 1\ny := 2\nx := 1\n"; got != want {
		t.Errorf("file must be untouched after a refused edit, got %q", got)
	}
}

func TestEditApplyUniqueMatchStillSucceeds(t *testing.T) {
	path := seedFile(t, "alpha\nbravo\ncharlie\n")
	e := NewEdit(nil, nil)
	if _, err := e.apply(path, "bravo", "delta", false, false); err != nil {
		t.Fatalf("a unique match must still succeed: %v", err)
	}
	if got, want := readFile(t, path), "alpha\ndelta\ncharlie\n"; got != want {
		t.Errorf("file = %q, want %q", got, want)
	}
}

// replace_all is the escape hatch the ambiguity guard makes necessary: without
// it, changing N occurrences would cost N context-expanded edits.
func TestEditApplyReplaceAll(t *testing.T) {
	path := seedFile(t, "old\nkeep\nold\nold\n")
	e := NewEdit(nil, nil)
	if _, err := e.apply(path, "old", "new", true, false); err != nil {
		t.Fatalf("replace_all: %v", err)
	}
	if got, want := readFile(t, path), "new\nkeep\nnew\nnew\n"; got != want {
		t.Errorf("file = %q, want %q", got, want)
	}
}

func TestEditApplyReplaceAllDeletesEveryOccurrence(t *testing.T) {
	path := seedFile(t, "// TODO\nreal code\n// TODO\n")
	e := NewEdit(nil, nil)
	if _, err := e.apply(path, "// TODO\n", "", true, false); err != nil {
		t.Fatalf("replace_all deletion: %v", err)
	}
	if got, want := readFile(t, path), "real code\n"; got != want {
		t.Errorf("file = %q, want %q", got, want)
	}
}

func TestEditRunAcceptsReplaceAll(t *testing.T) {
	path := seedFile(t, "a\na\n")
	e := NewEdit(nil, nil)
	args, _ := json.Marshal(map[string]any{
		"file_path": path, "old_string": "a", "new_string": "b", "replace_all": true,
	})
	if _, err := e.Run(args); err != nil {
		t.Fatalf("Run with replace_all: %v", err)
	}
	if got, want := readFile(t, path), "b\nb\n"; got != want {
		t.Errorf("file = %q, want %q", got, want)
	}
}

// The schema must keep new_string required. JSON Schema `required` enforces
// presence, not content, so "" already validates; making it optional would let
// an omitted field silently delete code.
func TestEditParametersContract(t *testing.T) {
	required := map[string]bool{}
	types := map[string]string{}
	for _, p := range NewEdit(nil, nil).Parameters() {
		required[p.Name] = p.Required
		types[p.Name] = p.Type
	}
	for _, name := range []string{"file_path", "old_string", "new_string"} {
		if !required[name] {
			t.Errorf("%s must stay required", name)
		}
	}
	if required["replace_all"] {
		t.Error("replace_all must be optional")
	}
	if types["replace_all"] != "boolean" {
		t.Errorf("replace_all type = %q, want boolean", types["replace_all"])
	}
	if !strings.Contains(NewEdit(nil, nil).Description(), "empty new_string") {
		t.Error("the description must tell the model that deletion is supported")
	}
}
