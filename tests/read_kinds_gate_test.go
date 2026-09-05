package tests_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// read.kinds is the project's answer to "what may be read here", and it now gates EVERY read
// entrance rather than only the MCP tool: `arac read`, an intercepted shell read in either
// intercepting mode, and the denial proxy all narrow through the same set.
//
// It used to gate the MCP tool alone. Every other path passed AllReadKinds explicitly, on the
// reasoning that the setting existed to narrow what a MODEL is offered through a tool schema
// and should not lock a person out of their own topology. The effect was two policies wearing
// one name: a project that had said "do not read named types here" was still served them by
// `arac read` and by every `cat` the guard rewrote, which is to say by the surfaces its agents
// actually used in three of the four modes.

// kindsProject is a Go project carrying one of each kind the gate distinguishes: a named_type
// (off by default), plus the function/struct/interface/file set that is on.
func kindsProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "go.mod"), "module demo\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "pkg", "units.go"), `package pkg

// Meters is a distance in metres.
type Meters float64

// Box is a rectangle.
type Box struct {
	W Meters
	H Meters
}

// Area returns the box's area in square metres.
func (b Box) Area() Meters {
	return b.W * b.H
}

// Describe renders a box as text.
func Describe(b Box) string {
	if b.Area() > 0 {
		return "a box"
	}
	return "nothing"
}
`)
	mustRun(t, dir, "scan", "--hard", "--root", ".", "--output", ".aracne/topology.db")
	return dir
}

// `arac read` on a kind read.kinds excludes fails, and says which kinds it would have served.
// It used to print the resource: the CLI passed AllReadKinds and never consulted the setting.
func TestArcReadRefusesAKindReadKindsExcludes(t *testing.T) {
	dir := kindsProject(t)

	out, err := runLtp(t, dir, "read", "demo/pkg.Meters")
	if err == nil {
		t.Fatalf("reading an excluded kind should fail:\n%s", out)
	}
	if !strings.Contains(out, "read.kinds does not allow") {
		t.Fatalf("the refusal should name the setting that refused:\n%s", out)
	}
	// Naming the allowed set is the difference between a refusal and a dead end: the caller
	// has to be able to see what it CAN ask for without opening the config.
	if !strings.Contains(out, "named_type") || !strings.Contains(out, "file, function, interface, struct") {
		t.Fatalf("the refusal should name the kind and the allowed set:\n%s", out)
	}
	// And it must not have printed the resource anyway.
	if strings.Contains(out, "type Meters") {
		t.Fatalf("a refused read must not print the source:\n%s", out)
	}
}

// The gate is the CONFIGURED set, not a hard-coded one: a project that asks for named types
// gets them from the same command that just refused.
func TestArcReadServesAKindTheProjectAdded(t *testing.T) {
	dir := kindsProject(t)
	allowReadKinds(t, dir, "file", "function", "struct", "interface", "named_type")

	out := mustRun(t, dir, "read", "demo/pkg.Meters")
	if !strings.Contains(out, "type Meters") {
		t.Fatalf("an allowed kind should read normally:\n%s", out)
	}
}

// Kinds the default set includes keep working, which is the whole reason the default is what
// it is. A gate that broke the ordinary read would not be a gate, it would be an outage.
func TestArcReadStillServesTheDefaultKinds(t *testing.T) {
	dir := kindsProject(t)

	out := mustRun(t, dir, "read", "demo/pkg.Describe", "demo/pkg.Box")
	if !strings.Contains(out, "func Describe") || !strings.Contains(out, "type Box") {
		t.Fatalf("the default kinds should read normally:\n%s", out)
	}
}

// THE INTERCEPT CASE. `cat <id>` is how a model reads in intercept_id mode, so the gate has to
// reach it there too -- and it has to REFUSE rather than pass through. Passing through runs a
// real `cat` on a resource id, which reports "No such file or directory": a true sentence
// about the wrong thing, and one that reads as "you typed the id wrong" when the truth is that
// the project does not serve that kind.
func TestInterceptedCatRefusesAnExcludedKind(t *testing.T) {
	dir := kindsProject(t)
	setMode(t, dir, "intercept_id")

	out, err := runLtp(t, dir, "cmd", "--", "cat", "demo/pkg.Meters")
	if err == nil {
		t.Fatalf("an intercepted read of an excluded kind should fail:\n%s", out)
	}
	if !strings.Contains(out, "read.kinds does not allow") {
		t.Fatalf("the refusal should reach the shell surface:\n%s", out)
	}
	if strings.Contains(out, "No such file or directory") {
		t.Fatalf("the command must not fall through to the real cat:\n%s", out)
	}
}

// The WINDOWED branch is the one that never had a gate at all. `head -20 demo/pkg.Meters`
// resolves the id, then renders a slice of the FILE through ReadSlice -- a path that is
// addressed by path and line and knows nothing about the id the caller typed. It is gated
// where the operand is still an operand, so both branches agree.
func TestInterceptedWindowRefusesAnExcludedKind(t *testing.T) {
	dir := kindsProject(t)
	setMode(t, dir, "intercept_line_ranges")

	out, err := runLtp(t, dir, "cmd", "--", "head", "-2", "demo/pkg.Meters")
	if err == nil {
		t.Fatalf("a windowed read of an excluded kind should fail:\n%s", out)
	}
	if !strings.Contains(out, "read.kinds does not allow") {
		t.Fatalf("the windowed branch should refuse the same way:\n%s", out)
	}
}

// An allowed kind is still served as an id operand, so the gate has not cost the mode its
// point: addressing code by name instead of by path.
func TestInterceptedCatStillServesAnAllowedKind(t *testing.T) {
	dir := kindsProject(t)
	setMode(t, dir, "intercept_id")

	out := mustRun(t, dir, "cmd", "--", "cat", "demo/pkg.Describe")
	if !strings.Contains(out, "func Describe") {
		t.Fatalf("an allowed kind should still be served:\n%s", out)
	}
}

// An operand that resolves to NOTHING is the other case, and it must keep passing through.
// There is no policy in a typo: aracne has nothing to say about it, so the shell answers, and
// the answer is the real command's own error. Refusing here would put aracne's voice on a
// question it never understood.
func TestInterceptedCatStillPassesThroughAnUnresolvableOperand(t *testing.T) {
	dir := kindsProject(t)
	setMode(t, dir, "intercept_id")

	out, err := runLtp(t, dir, "cmd", "--", "cat", "demo/pkg.NoSuchThing")
	if err == nil {
		t.Fatalf("cat on a nonexistent operand should still fail:\n%s", out)
	}
	if strings.Contains(out, "read.kinds") {
		t.Fatalf("a typo is not a refusal:\n%s", out)
	}
	if !strings.Contains(out, "No such file") {
		t.Fatalf("the real command should have answered:\n%s", out)
	}
}
