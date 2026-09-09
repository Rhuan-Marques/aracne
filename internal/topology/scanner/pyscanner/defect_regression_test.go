package pyscanner

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSyntaxErrorNamesTheFileNotThePath pins AR-04.
//
// The interpreter loop treated every failure as a reason to try the next name. A non-zero exit
// from an interpreter that RAN means the target file failed to parse, and continuing threw that
// diagnosis away: on a machine with no `python` (which is most of them now) the reported error
// became `python parse failed: : exec: "python": executable file not found in $PATH` -- a
// sentence about PATH for a file with a SyntaxError, pointing at the wrong fix and discarding
// the stderr from python3 that named the file, the line and the error. This repository carried
// an unparseable Python fixture for exactly that long.
func TestSyntaxErrorNamesTheFileNotThePath(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not on PATH")
	}
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.py")
	if err := os.WriteFile(bad, []byte("def f(:\n    pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := ParseFile(bad, "pkg", dir)
	if err == nil {
		t.Fatal("expected a parse error")
	}
	msg := err.Error()
	if strings.Contains(msg, "executable file not found") {
		t.Fatalf("a syntax error must not be reported as a missing interpreter:\n%s", msg)
	}
	if !strings.Contains(msg, "SyntaxError") {
		t.Fatalf("the interpreter's own diagnosis must survive:\n%s", msg)
	}
	if !strings.Contains(msg, bad) {
		t.Fatalf("the error must name the file that failed:\n%s", msg)
	}
}

// TestValidFileStillParses guards the other direction: stopping at the first interpreter that
// ran must not stop a file that parses cleanly from parsing.
func TestValidFileStillParses(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not on PATH")
	}
	dir := t.TempDir()
	good := filepath.Join(dir, "good.py")
	if err := os.WriteFile(good, []byte("def f(x):\n    \"\"\"Doc.\"\"\"\n    return x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pr, err := ParseFile(good, "pkg", dir)
	if err != nil {
		t.Fatalf("a valid file must parse: %v", err)
	}
	if len(pr.Functions) != 1 || pr.Functions[0].Function.Name != "f" {
		t.Fatalf("expected one function named f, got %+v", pr.Functions)
	}
}
