package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/shellcmd"
	"github.com/Rhuan-Marques/aracne/internal/topology"
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

// A search scoped to a resource keeps that resource's matches however many precede it.
//
// The scope used to trim the FINISHED result, after the 200-row head limit had kept the file's
// first 200 matches -- so a function below 250 earlier hits lost both of its own:
// `grep -n needle pkg.Target` printed nothing and exited 1 while `-c` on the same operand said
// 2, and the trim reset the truncation flag, so nothing mentioned a cap. Both surfaces that
// scope by resource are pinned: the intercepted shell grep and `arac grep`.
func TestResourceScopedSearchIsCappedAfterItsSpan(t *testing.T) {
	root := t.TempDir()
	var src strings.Builder
	src.WriteString("package main\n\nfunc Early() {\n")
	for i := 0; i < 250; i++ {
		fmt.Fprintf(&src, "\t_ = \"needle %d\"\n", i)
	}
	src.WriteString("}\n\n// Target is the function the search is scoped to.\n" +
		"func Target() {\n\t_ = \"needle A\"\n\t_ = \"needle B\"\n}\n\nfunc main() {}\n")
	for rel, body := range map[string]string{
		"go.mod": "module example.com/big\n\ngo 1.21\n",
		"big.go": src.String(),
	} {
		if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dbPath := filepath.Join(root, ".aracne", "topology.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	mgr := topology.New()
	if err := mgr.Load(dbPath); err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := mgr.FullScan(root, NewScannerRegistry()); err != nil {
		t.Fatalf("scan: %v", err)
	}
	t.Chdir(root)

	out, status, _, ok := serveCommand([]string{"grep", "-n", "needle", "example.com/big.Target"}, false)
	if !ok || status != 0 || !strings.Contains(out, "needle A") || !strings.Contains(out, "needle B") {
		t.Errorf("`grep -n needle example.com/big.Target` (served=%v, exit %d) lost the "+
			"resource's own matches:\n%s", ok, status, out)
	}

	// A head limit the resource's own matches exceed: the cap counts them, and says so.
	var stdout, stderr bytes.Buffer
	code := runGrep([]string{"--head-limit", "1", "needle", "example.com/big.Target"}, &stdout, &stderr)
	if got := stdout.String(); code != 0 || !strings.Contains(got, "needle A") ||
		!strings.Contains(got, "showing 1 of 2") {
		t.Errorf("`arac grep --head-limit 1` on the resource (exit %d):\n%s%s", code, got, stderr.String())
	}
}

// A path-less rg reads its stdin instead of the tree exactly when the real one would: when stdin
// is a file or a pipe. /dev/null -- what an agent's Bash tool hands a command -- is a character
// device, and rg walks the tree for it. See shellcmd.StdinRule.
func TestPathlessSearchReadsStdinOnlyWhenTheRealToolWould(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stdin kinds are POSIX file types")
	}
	file, err := os.Create(filepath.Join(t.TempDir(), "in.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	pipe, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pipe.Close()
	defer w.Close()
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer devNull.Close()

	for _, tc := range []struct {
		name  string
		rule  shellcmd.StdinRule
		stdin *os.File
		want  bool
	}{
		{"rg < file", shellcmd.StdinIfData, file, true},
		{"… | rg", shellcmd.StdinIfData, pipe, true},
		{"rg with /dev/null", shellcmd.StdinIfData, devNull, false},
		{"ug with /dev/null", shellcmd.StdinUnlessTerminal, devNull, true},
		{"… | ug", shellcmd.StdinUnlessTerminal, pipe, true},
		{"grep -r < file", shellcmd.StdinIgnored, file, false},
		{"grep -r in a pipe", shellcmd.StdinIgnored, pipe, false},
	} {
		if got := stdinWouldBeSearched(tc.rule, tc.stdin); got != tc.want {
			t.Errorf("%s: stdinWouldBeSearched = %v, want %v", tc.name, got, tc.want)
		}
	}
}
