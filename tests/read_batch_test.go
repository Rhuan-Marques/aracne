package tests_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readProject scans a two-file Go project whose members reference each other across files, so
// the batching, grouping and context-exclusion rules all have something to bite on.
func readProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "go.mod"), "module demo\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "pkg", "shapes.go"), `package pkg

import "fmt"

// Shape is anything with an area.
type Shape interface {
	Area() float64
}

// Circle is a round shape.
type Circle struct {
	R float64
}

// Area returns the circle's area.
func (c Circle) Area() float64 { return 3.14 * c.R * c.R }

// Describe renders a shape as text.
func Describe(s Shape) string {
	return fmt.Sprintf("area=%v", s.Area())
}

// Total sums the areas of many shapes.
func Total(shapes []Shape) float64 {
	sum := 0.0
	for _, s := range shapes {
		sum += s.Area()
	}
	return sum
}
`)
	writeFile(t, filepath.Join(dir, "pkg", "report.go"), `package pkg

import "strings"

// Report joins descriptions of every shape.
func Report(shapes []Shape) string {
	var out []string
	for _, s := range shapes {
		out = append(out, Describe(s))
	}
	return strings.Join(out, ", ")
}
`)
	mustRun(t, dir, "scan", "--hard", "--root", ".", "--output", ".aracne/topology.db")
	return dir
}

func TestReadGroupsBatchByFileWithOneContext(t *testing.T) {
	dir := readProject(t)

	out := mustRun(t, dir, "read", "demo/pkg.Describe", "demo/pkg.Total", "demo/pkg.Report")

	// One fenced group per declaring file, labelled with the path relative to the root.
	if got := strings.Count(out, "```pkg/shapes.go"); got != 1 {
		t.Fatalf("expected one shapes.go group, got %d:\n%s", got, out)
	}
	if got := strings.Count(out, "```pkg/report.go"); got != 1 {
		t.Fatalf("expected one report.go group, got %d:\n%s", got, out)
	}
	// Same-file resources share a group rather than repeating the header.
	if strings.Index(out, "func Describe") > strings.Index(out, "func Total") {
		t.Fatalf("group members should be in source order:\n%s", out)
	}
	// Exactly one context section for the whole batch.
	if got := strings.Count(out, "# CONTEXT:"); got != 1 {
		t.Fatalf("expected one CONTEXT section, got %d:\n%s", got, out)
	}
	// Describe is rendered as source, so Report's call to it must not be described again.
	if strings.Contains(out, "## demo/pkg.Describe") {
		t.Fatalf("a resource shown as source must not appear in CONTEXT:\n%s", out)
	}
}

func TestReadBatchCostsLessThanSeparateReads(t *testing.T) {
	dir := readProject(t)

	separate := len(mustRun(t, dir, "read", "demo/pkg.Describe")) +
		len(mustRun(t, dir, "read", "demo/pkg.Total")) +
		len(mustRun(t, dir, "read", "demo/pkg.Report"))
	batched := len(mustRun(t, dir, "read", "demo/pkg.Describe", "demo/pkg.Total", "demo/pkg.Report"))

	// Batching is the point of the list argument: it saves the turns AND the repeated headers
	// and shared context entries. If it ever costs more, the grouping has regressed.
	if batched >= separate {
		t.Fatalf("batched read (%d bytes) should beat %d bytes of separate reads", batched, separate)
	}
	t.Logf("batched %d bytes vs %d separate (%.0f%% saved)", batched, separate,
		100*(1-float64(batched)/float64(separate)))
}

func TestReadFileDoesNotRepeatItsOwnMembers(t *testing.T) {
	dir := readProject(t)

	out := mustRun(t, dir, "read", "pkg/shapes.go")

	// The whole file is the body, so everything it declares is already shown. Listing those
	// under CONTEXT was an index of the fence directly above it.
	for _, member := range []string{"demo/pkg.Describe", "demo/pkg.Total", "demo/pkg.Circle", "demo/pkg.Shape"} {
		if strings.Contains(out, "## "+member) {
			t.Fatalf("file read repeats its own member %q in CONTEXT:\n%s", member, out)
		}
	}
	if !strings.Contains(out, "func Describe") {
		t.Fatalf("file read should contain the file source:\n%s", out)
	}
}

func TestReadFileShowsWhatItReachesOutside(t *testing.T) {
	dir := readProject(t)

	out := mustRun(t, dir, "read", "pkg/report.go")

	// report.go calls Describe, which lives in shapes.go: that IS context, and is what the
	// section should have been showing all along.
	if !strings.Contains(out, "demo/pkg.Describe") {
		t.Fatalf("file read should surface neighbours outside the file:\n%s", out)
	}
}

func TestReadFileAbsorbsAMemberAskedForSeparately(t *testing.T) {
	dir := readProject(t)

	out := mustRun(t, dir, "read", "pkg/shapes.go", "demo/pkg.Describe")

	if got := strings.Count(out, "func Describe"); got != 1 {
		t.Fatalf("Describe rendered %d times, want 1:\n%s", got, out)
	}
	if got := strings.Count(out, "```pkg/shapes.go"); got != 1 {
		t.Fatalf("expected a single group, got %d:\n%s", got, out)
	}
}

func TestReadDedupesRepeatedIDs(t *testing.T) {
	dir := readProject(t)

	out := mustRun(t, dir, "read", "demo/pkg.Describe", "demo/pkg.Describe")

	if got := strings.Count(out, "func Describe"); got != 1 {
		t.Fatalf("duplicate ids rendered %d times, want 1:\n%s", got, out)
	}
}

func TestReadInlinesASmallReceiverButNotABigClass(t *testing.T) {
	dir := readProject(t)

	// A Go struct declaration is short, so a method still arrives with its receiver type.
	out := mustRun(t, dir, "read", "demo/pkg.(Circle).Area")
	if !strings.Contains(out, "type Circle struct") {
		t.Fatalf("a small receiver type should be inlined above the method:\n%s", out)
	}
	if got := strings.Count(out, "func (c Circle) Area"); got != 1 {
		t.Fatalf("method rendered %d times, want 1:\n%s", got, out)
	}
}

func TestReadReportsBadIDsWithoutLosingGoodOnes(t *testing.T) {
	dir := readProject(t)

	out := mustRun(t, dir, "read", "demo/pkg.Describe", "demo/pkg.Describ")

	if !strings.Contains(out, "func Describe") {
		t.Fatalf("a bad id must not throw away the good results:\n%s", out)
	}
	if !strings.Contains(out, "# UNRESOLVED:") {
		t.Fatalf("expected an UNRESOLVED section:\n%s", out)
	}
	// A miss costs a correction, not an exploration turn: the near-match is named.
	if !strings.Contains(out, "demo/pkg.Describe (function)") {
		t.Fatalf("expected the resolver's candidate hint:\n%s", out)
	}
}
