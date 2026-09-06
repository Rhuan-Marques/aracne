package tests_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// `arac init` is the interactive half of the pair, and these are the two things that have to be
// true about it from the outside: it refuses rather than hanging when nobody is there, and it
// says which command does the same job unattended.
//
// The wizard itself is not exercised here. The test binary runs with pipes on both ends, so
// there is no terminal to draw on -- which is exactly the path below. The questions are tested
// against a scripted keyboard in internal/cli/init_questions_test.go.

func TestInitWithoutATerminalRefusesAndNamesSetup(t *testing.T) {
	dir := t.TempDir()
	out, err := runLtp(t, dir, "init")
	if err == nil {
		t.Fatalf("init with no terminal must refuse, got:\n%s", out)
	}
	for _, want := range []string{"arac setup", "interactive", "mode", "contract_verbosity", "descriptions"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal should mention %q, got:\n%s", want, out)
		}
	}
}

// It refuses BEFORE it writes anything. A command that cannot ask its questions has no answers
// to save, and a half-set-up project is worse than an untouched one.
func TestInitWithoutATerminalWritesNothing(t *testing.T) {
	dir := t.TempDir()
	if _, err := runLtp(t, dir, "init"); err == nil {
		t.Fatal("init with no terminal must refuse")
	}
	for _, rel := range []string{".aracne/config.json", "CLAUDE.md", ".claude", "AGENTS.md"} {
		assertNotExists(t, dir, rel)
	}
}

// The sweep no longer asks who writes the descriptions -- `arac init` does. What it does
// instead is name that command, and then the keys, for everything that will never run a wizard.
func TestDescriptionsGenerateRefusesUnconfiguredAndNamesInit(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module scratch\n\ngo 1.25\n")
	writeFile(t, filepath.Join(dir, "main.go"), "package main\n\nfunc main() {}\n")
	mustRun(t, dir, "scan", "-root", dir)

	out, err := runLtp(t, dir, "descriptions", "generate")
	if err == nil {
		t.Fatalf("an unconfigured sweep must refuse, got:\n%s", out)
	}
	for _, want := range []string{"arac init", "provider", "cli_provider_command"} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal should mention %q, got:\n%s", want, out)
		}
	}
}
