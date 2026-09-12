package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// WRN-02. The guard-reported-warnings ledger exists so a warning reaches the model ONCE,
// whichever surface found it: `arac update-file --claude-hook` reports through it, and so does
// the guard's PostToolUse drift check. `arac edit` and `arac write` printed the tool's own
// warning list and never touched the ledger, so the same warning arrived twice for one edit --
// or, for the spellings the drift check skips as pure-arac commands, arrived later attached to
// an unrelated `go build`, which had changed nothing.

// editProject writes a two-file Go project whose main.go calls lib.Add, scans nothing (the CLI
// entry points build the topology themselves), and returns its directory.
func editProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/m\n\ngo 1.21\n")
	write("lib/lib.go", "package lib\n\nfunc Add(a, b int) int { return a + b }\n")
	write("main.go", "package main\n\nimport \"example.com/m/lib\"\n\nfunc main() { println(lib.Add(1, 2)) }\n")
	return dir
}

// runCLIWithStdin runs one of the stdin-driven CLI entry points inside dir and returns what it
// printed.
func runCLIWithStdin(t *testing.T, dir, payload string, run func()) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(wd) })

	in := filepath.Join(t.TempDir(), "stdin.json")
	if err := os.WriteFile(in, []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(in)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	outPath := filepath.Join(t.TempDir(), "stdout.txt")
	captured, err := os.Create(outPath)
	if err != nil {
		t.Fatal(err)
	}
	oldStdin, oldStdout := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = f, captured
	run()
	os.Stdin, os.Stdout = oldStdin, oldStdout
	captured.Close()

	printed, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	return string(printed)
}

func TestWRN02_CliEditDoesNotLeaveItsWarningsForTheGuardToRepeat(t *testing.T) {
	dir := editProject(t)
	dbPath := filepath.Join(dir, ".aracne", "topology.db")

	printed := runCLIWithStdin(t, dir, `{"file_path":"lib/lib.go","old_string":"a, b int","new_string":"a, b, c int"}`, RunEdit)

	// Reported ONCE is not the same as reported never: the edit itself must still say what it
	// broke.
	if !strings.Contains(printed, "signature_changed") || !strings.Contains(printed, "example.com/m.main") {
		t.Fatalf("`arac edit` did not report the caller it broke:\n%s", printed)
	}

	// The edit broke main.go's call, so the warning exists...
	if _, list, ok := currentWarningIDs(dbPath); !ok || len(list) == 0 {
		t.Fatal("the breaking edit raised no warning at all")
	}
	// ...and `arac edit` has already shown it, so the guard's drift check must have nothing
	// new to say about this edit -- on its own call or on any later command.
	if msg := driftCheck(dbPath, "Bash"); msg != "" {
		t.Fatalf("the guard repeated a warning `arac edit` had already printed:\n%s", msg)
	}
}

func TestWRN02_CliWriteDoesNotLeaveItsWarningsForTheGuardToRepeat(t *testing.T) {
	dir := editProject(t)
	dbPath := filepath.Join(dir, ".aracne", "topology.db")

	payload := `{"file_path":"lib/lib.go","content":"package lib\n\nfunc Add(a, b, c int) int { return a + b + c }\n"}`
	printed := runCLIWithStdin(t, dir, payload, RunWrite)
	if !strings.Contains(printed, "signature_changed") {
		t.Fatalf("`arac write` did not report the caller it broke:\n%s", printed)
	}

	if _, list, ok := currentWarningIDs(dbPath); !ok || len(list) == 0 {
		t.Fatal("the breaking write raised no warning at all")
	}
	if msg := driftCheck(dbPath, "Bash"); msg != "" {
		t.Fatalf("the guard repeated a warning `arac write` had already printed:\n%s", msg)
	}
}

// The ledger must only cover what was actually shown: a break made LATER, by something else,
// still reaches the model.
func TestWRN02_ALaterWarningIsStillReported(t *testing.T) {
	dir := editProject(t)
	dbPath := filepath.Join(dir, ".aracne", "topology.db")

	runCLIWithStdin(t, dir, `{"file_path":"lib/lib.go","old_string":"a, b int","new_string":"a, b, c int"}`, RunEdit)
	if msg := driftCheck(dbPath, "Bash"); msg != "" {
		t.Fatalf("precondition: the edit's own warning was repeated:\n%s", msg)
	}

	// A second function with its own caller, indexed clean.
	if err := os.WriteFile(filepath.Join(dir, "lib", "other.go"),
		[]byte("package lib\n\nfunc Mul(a, b int) int { return a * b }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"),
		[]byte("package main\n\nimport \"example.com/m/lib\"\n\nfunc main() { println(lib.Add(1, 2, 3) + lib.Mul(2, 3)) }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	driftCheck(dbPath, "Bash")

	// Now break Mul outside any aracne command, the way a native edit or a git checkout does.
	if err := os.WriteFile(filepath.Join(dir, "lib", "other.go"),
		[]byte("package lib\n\nfunc Mul(a, b, c int) int { return a * b * c }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	msg := driftCheck(dbPath, "Bash")
	if !strings.Contains(msg, "Mul") {
		t.Fatalf("a break made after the edit was not reported:\n%s", msg)
	}
}
