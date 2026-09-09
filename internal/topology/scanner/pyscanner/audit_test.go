//go:build audit

package pyscanner

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A-14: parsePythonFile tries python3 then python, overwriting cmdErr each time, and treats a
// NON-ZERO EXIT as "try the next interpreter". A SyntaxError in the file being parsed is a
// non-zero exit, so python3's real diagnostic is discarded and replaced by whatever the second
// attempt said -- usually "executable file not found in $PATH". The operator is told the wrong
// thing about the wrong file and the actual cause is unrecoverable from the message.
func TestAudit_PythonSyntaxErrorSurvivesTheInterpreterFallback(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not on PATH")
	}
	dir := t.TempDir()
	bad := filepath.Join(dir, "broken.py")
	if err := os.WriteFile(bad, []byte("def f(:\n    pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := parsePythonFile(bad)
	if err == nil {
		t.Fatal("expected a parse failure")
	}
	msg := err.Error()
	if !strings.Contains(msg, "SyntaxError") {
		t.Errorf("python3's diagnostic was lost to the interpreter fallback.\n"+
			"  got: %s\n"+
			"  the file has a SyntaxError; the message names the second interpreter instead", msg)
	}
}
