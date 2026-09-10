package cli

import (
	"bytes"
	"strings"
	"testing"
)

// `arac grep` resolves its operands the way the intercepted shell grep does: every path, and a
// resource id scoped to that declaration. It used to search the first path only -- silently,
// with exit 0 -- and to fail on an id with a raw `stat path:` error.
func TestAracGrepTakesEveryOperandAndResourceIDs(t *testing.T) {
	root, _ := scannedProject(t)
	t.Chdir(root)

	grep := func(args ...string) (string, string, int) {
		t.Helper()
		var out, errOut bytes.Buffer
		code := runGrep(args, &out, &errOut)
		return out.String(), errOut.String(), code
	}

	// "first release" lives only in the SECOND operand.
	out, _, code := grep("first release", "app.go", "CHANGELOG.md")
	if code != 0 || !strings.Contains(out, "CHANGELOG.md") {
		t.Errorf("the second operand was not searched (exit %d):\n%s", code, out)
	}

	// A resource id scopes the search to that declaration's lines.
	out, errOut, code := grep("return", "example.com/proj.Serve")
	if code != 0 || !strings.Contains(out, `return "ok"`) {
		t.Errorf("a resource id operand was not searched (exit %d):\n%s%s", code, out, errOut)
	}

	// An operand that is nothing at all is a usage error, reported cleanly.
	_, errOut, code = grep("Serve", "missing.go")
	if code != 2 || !strings.Contains(errOut, "missing.go") || strings.Contains(errOut, "stat path") {
		t.Errorf("a missing operand: exit %d, stderr %q", code, errOut)
	}

	// -F reads the pattern literally; -w refuses what \b cannot express.
	if out, _, code := grep("-F", `"ok"`, "app.go"); code != 0 || !strings.Contains(out, `"ok"`) {
		t.Errorf("-F found nothing (exit %d):\n%s", code, out)
	}
	if _, errOut, code := grep("-w", "==", "app.go"); code != 2 || !strings.Contains(errOut, "-w") {
		t.Errorf("-w on a non-word pattern must be refused, got exit %d: %s", code, errOut)
	}
}
