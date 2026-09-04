package tests_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The terminal surface, end to end: `arac cmd` is what an intercepted shell command actually
// runs, so these assertions are the contract the guard's rewrite is promising.
//
// Two properties matter and they pull against each other. An answer must be ENRICHED -- the
// lines asked for, framed by their declaration, with the resources they touch -- and a
// command aracne does not model must be UNTOUCHED, byte for byte. The second is what makes
// the first safe to turn on by default, so it is tested at least as hard.

// terminalProject scans a small Go project shaped so that windows land inside declarations
// rather than covering them, and returns its root.
func terminalProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module demo\n\ngo 1.21\n")
	if err := os.MkdirAll(filepath.Join(root, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "pkg", "shapes.go"), `package pkg

import (
	"fmt"
	"strings"
)

// Shape is anything with an area.
type Shape interface {
	Area() float64
}

// Circle is a round shape.
type Circle struct {
	Radius float64
}

// Area returns the circle's area.
func (c Circle) Area() float64 {
	return 3.14159 * c.Radius * c.Radius
}

// Describe renders a shape as text. Deliberately long, so a window lands inside it.
func Describe(s Shape) string {
	var b strings.Builder
	b.WriteString("shape:")
	b.WriteString(fmt.Sprintf("%T", s))
	b.WriteString(" area=")
	b.WriteString(fmt.Sprintf("%.2f", s.Area()))
	b.WriteString(" ")
	b.WriteString("done")
	total := Total([]Shape{s})
	b.WriteString(fmt.Sprintf("total=%.2f", total))
	return b.String()
}

// Total sums the areas of every shape.
func Total(shapes []Shape) float64 {
	sum := 0.0
	for _, s := range shapes {
		sum += s.Area()
	}
	return sum
}
`)
	// An unindexed file, to exercise the out-of-scope branch against a real read.
	writeFile(t, filepath.Join(root, "NOTES.md"), "alpha\nbeta\ngamma\ndelta\nepsilon\n")
	mustRun(t, root, "scan", "--hard", "--root", ".", "--output", ".aracne/topology.db")
	// `arac scan` writes the shipped default, which is ModeCLI -- searches intercepted,
	// reads not. These tests are about the read half, so the project has to say which of the
	// two intercepting modes it is in rather than lean on a default that is neither.
	setMode(t, root, "intercept_line_ranges")
	return root
}

// setMode rewrites the project's mode in place. It edits the file rather than regenerating it
// so everything else `arac scan` decided about this project survives.
func setMode(t *testing.T, root, mode string) {
	t.Helper()
	path := filepath.Join(root, ".aracne", "config.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	cfg["mode"] = mode
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
}

// aracCmd runs `arac cmd -- ...` in dir and returns its combined output.
func aracCmd(t *testing.T, dir string, argv ...string) string {
	t.Helper()
	out, _ := runLtp(t, dir, append([]string{"cmd", "--"}, argv...)...)
	return out
}

// nativeOut runs the real command the same way a shell would.
func nativeOut(t *testing.T, dir string, argv ...string) (string, bool) {
	t.Helper()
	bin, err := exec.LookPath(argv[0])
	if err != nil {
		return "", false
	}
	cmd := exec.Command(bin, argv[1:]...)
	cmd.Dir = dir
	out, _ := cmd.Output() // a non-zero status is still a valid comparison
	return string(out), true
}

func TestWindowedReadShowsExactlyTheLinesAskedForPlusItsFrame(t *testing.T) {
	root := terminalProject(t)

	// `tail -4` on the file: the last four lines sit inside Total, so its signature must be
	// shown as a frame and the elided middle must be marked.
	got := aracCmd(t, root, "tail", "-4", "pkg/shapes.go")
	if !strings.Contains(got, "func Total(shapes []Shape) float64 {") {
		t.Errorf("tail lost the enclosing signature:\n%s", got)
	}
	if !strings.Contains(got, "⋯") {
		t.Errorf("tail did not mark the elided body:\n%s", got)
	}
	if !strings.Contains(got, "return sum") {
		t.Errorf("tail did not return the lines asked for:\n%s", got)
	}
	// The point of a tail is that it is a TAIL. Lines from the top of the file must not be here.
	if strings.Contains(got, "package pkg") || strings.Contains(got, "type Circle struct") {
		t.Errorf("tail returned the whole file rather than its last lines:\n%s", got)
	}
}

func TestAResourceIDStandsWhereAPathDoes(t *testing.T) {
	root := terminalProject(t)

	// The window applies to the resource's BODY; its signature is a frame, not one of the N.
	got := aracCmd(t, root, "head", "-3", "demo/pkg.Describe")
	if !strings.Contains(got, "func Describe(s Shape) string {") {
		t.Fatalf("head on a resource lost its signature:\n%s", got)
	}
	if !strings.Contains(got, "var b strings.Builder") {
		t.Errorf("head on a resource did not return its first body line:\n%s", got)
	}
	if strings.Contains(got, "return b.String()") {
		t.Errorf("head -3 returned the end of the function:\n%s", got)
	}

	tail := aracCmd(t, root, "tail", "-2", "demo/pkg.Describe")
	if !strings.Contains(tail, "return b.String()") {
		t.Errorf("tail on a resource did not return its last line:\n%s", tail)
	}
	if strings.Contains(tail, "var b strings.Builder") {
		t.Errorf("tail -2 returned the start of the function:\n%s", tail)
	}
}

// The context section must describe what is ON SCREEN, not what the enclosing declaration
// touches over its whole length. This is the difference between a windowed read and a
// resource read wearing a window's name.
func TestContextIsRestrictedToWhatTheWindowMentions(t *testing.T) {
	root := terminalProject(t)

	whole := mustRun(t, root, "read", "demo/pkg.Describe")
	if !strings.Contains(whole, "demo/pkg.Total") {
		t.Skipf("fixture's whole-resource read does not list Total, nothing to restrict:\n%s", whole)
	}

	// These last two lines mention `total` (a local) but never `Total`, so the callee must
	// drop out of the context even though the enclosing function calls it.
	got := aracCmd(t, root, "sed", "-n", "37,38p", "pkg/shapes.go")
	if idx := strings.Index(got, "# CONTEXT:"); idx >= 0 {
		if strings.Contains(got[idx:], "demo/pkg.Total") {
			t.Errorf("context was not restricted to the window:\n%s", got)
		}
	}
}

// Case 3, the property that makes interception safe to default on: anything aracne does not
// model must be indistinguishable from the command itself.
func TestUnmodelledAndUnindexedCommandsAreByteIdentical(t *testing.T) {
	root := terminalProject(t)

	for _, argv := range [][]string{
		{"head", "-3", "NOTES.md"},            // a real file with no topology nodes
		{"head", "-c", "20", "pkg/shapes.go"}, // a byte count, not a line window
		{"nl", "NOTES.md"},                    // a rendering, not a window
		{"tac", "NOTES.md"},                   // ditto
		{"cat", "-n", "NOTES.md"},             // a flag aracne does not model
	} {
		want, ok := nativeOut(t, root, argv...)
		if !ok {
			t.Logf("skipping %v: not installed", argv)
			continue
		}
		if got := aracCmd(t, root, argv...); got != want {
			t.Errorf("`arac cmd -- %s` diverged from the real command:\ngot:  %q\nwant: %q",
				strings.Join(argv, " "), got, want)
		}
	}
}

// A search must keep grep's contract, including the part callers branch on: exit 1 when
// nothing matched.
func TestSearchKeepsGrepsExitStatus(t *testing.T) {
	root := terminalProject(t)

	if out, err := runLtp(t, root, "cmd", "--", "grep", "WriteString", "pkg/shapes.go"); err != nil {
		t.Errorf("a matching search must exit 0: %v\n%s", err, out)
	}
	if _, err := runLtp(t, root, "cmd", "--", "grep", "zzz-no-such-token", "pkg/shapes.go"); err == nil {
		t.Error("a search with no matches must exit non-zero, as grep does")
	}
}

// A search scoped to a resource ID must not spill into the rest of the file.
func TestSearchScopedToAResourceStaysInside(t *testing.T) {
	root := terminalProject(t)

	got := aracCmd(t, root, "grep", "WriteString", "demo/pkg.Total")
	if strings.Contains(got, "b.WriteString") {
		t.Errorf("a search scoped to Total returned hits from Describe:\n%s", got)
	}
	inside := aracCmd(t, root, "grep", "WriteString", "demo/pkg.Describe")
	if !strings.Contains(inside, "b.WriteString") {
		t.Errorf("a search scoped to Describe found none of its own hits:\n%s", inside)
	}
}

// The whole point of the surface is that it costs less than what it replaces. A windowed read
// buys its enclosing signature and a context block; past a few multiples of the raw lines it
// is no longer a cheaper read, and `arac cmd` is required to pass through instead.
func TestAWindowedReadStaysProportionalToTheWindow(t *testing.T) {
	root := terminalProject(t)

	whole, err := os.ReadFile(filepath.Join(root, "pkg", "shapes.go"))
	if err != nil {
		t.Fatal(err)
	}
	got := aracCmd(t, root, "tail", "-2", "pkg/shapes.go")
	if len(got) > len(whole) {
		t.Errorf("a 2-line tail returned %d bytes, more than the whole %d-byte file:\n%s",
			len(got), len(whole), got)
	}
}
