package tests_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A COLD INDEX is a topology database sitting next to source it was not built from: a restored
// benchmark fixture, a `git checkout` made while nothing was watching, an edit by a tool that
// does not tell aracne. Every symbol in a drifted file then has a recorded span that does not
// fit the file, and the read that hits it used to die with
//
//	Error: EndsAt 347 out of range (max 338)
//
// which to a model is indistinguishable from "that symbol does not exist". A benchmarked flask
// instance lost all three of its seeds to exactly that: `Config.from_file` resolved fine, the
// class around it was indexed nine lines past EOF, and the model spent its turns grepping.
//
// These tests build that state directly -- scan, then shrink a file behind the index's back --
// and assert the three behaviours that make it recoverable: the read heals itself, a miss says
// "not indexed" rather than "not found", and the health surface reports the drift.

// staleProject scans a two-file Go project and then TRUNCATES one of the files without
// re-scanning, leaving the index describing lines that no longer exist.
func staleProject(t *testing.T) (dir string, shrunk string) {
	t.Helper()
	dir = t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "go.mod"), "module demo\n\ngo 1.21\n")

	// Area's body runs to the end of the file, exactly like the flask `Config` class whose
	// recorded span ended at line 347 of a 338-line file.
	writeFile(t, filepath.Join(dir, "pkg", "shapes.go"), `package pkg

// Circle is a round shape.
type Circle struct {
	R float64
}

// Area returns the circle's area.
func (c Circle) Area() float64 {
	radius := c.R
	squared := radius * radius
	scaled := 3.14 * squared
	_ = radius
	_ = squared
	return scaled
}
`)
	writeFile(t, filepath.Join(dir, "pkg", "report.go"), `package pkg

// Report names a shape.
func Report() string { return "report" }
`)
	mustRun(t, dir, "scan", "--hard", "--root", ".", "--output", ".aracne/topology.db")

	// Same symbols, fewer lines: the index now points past the end of the file.
	shrunk = filepath.Join(dir, "pkg", "shapes.go")
	writeFile(t, shrunk, `package pkg

// Circle is a round shape.
type Circle struct {
	R float64
}

// Area returns the circle's area.
func (c Circle) Area() float64 { return 3.14 * c.R * c.R }
`)
	// The manifest is mtime-based and the write above may land inside the same clock tick as
	// the scan, which would hide the drift from a diff that is not the bug under test.
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(shrunk, future, future); err != nil {
		t.Fatal(err)
	}
	return dir, shrunk
}

func TestReadHealsAStaleIndexInsteadOfFailing(t *testing.T) {
	dir, _ := staleProject(t)

	// Area still exists, but shapes.go is nine lines shorter than the index believes. Before
	// the fix this returned "EndsAt N out of range (max M)".
	out := mustRun(t, dir, "read", "demo/pkg.(Circle).Area")

	if !strings.Contains(out, "func (c Circle) Area") {
		t.Fatalf("a read over a stale index should re-index the file and answer:\n%s", out)
	}
	if strings.Contains(out, "out of range") {
		t.Fatalf("the range error survived into the response:\n%s", out)
	}
	// The heal is a real re-index, not a one-off rescue: the removed symbol is gone from the
	// graph afterwards.
	if out := mustRun(t, dir, "check-updates"); !strings.Contains(out, "up to date") {
		t.Fatalf("the healed file should no longer be reported as drifted:\n%s", out)
	}
}

func TestStaleIndexIsReportedAsNotIndexedNotNotFound(t *testing.T) {
	dir, _ := staleProject(t)

	// A miss while the index is stale must not read as "your ID is wrong": that is the
	// distinction the model (and the benchmark) needs to pick a recovery.
	out, _ := runLtp(t, dir, "read", "demo/pkg.NoSuchThing")
	if !strings.Contains(out, "index is STALE") {
		t.Fatalf("a miss over a stale index should say so:\n%s", out)
	}
	if !strings.Contains(out, "not indexed") {
		t.Fatalf("the note should name the \"not indexed\" possibility:\n%s", out)
	}
	if !strings.Contains(out, "pkg/shapes.go") {
		t.Fatalf("the note should name the drifted file:\n%s", out)
	}
}

func TestCheckUpdatesReportsIndexHealth(t *testing.T) {
	dir, _ := staleProject(t)

	out, err := runLtp(t, dir, "check-updates")
	if err == nil {
		t.Fatalf("check-updates should exit non-zero on a stale index so a script can gate on it:\n%s", out)
	}
	if !strings.Contains(out, "pkg/shapes.go") || !strings.Contains(out, "STALE") {
		t.Fatalf("expected the drifted file to be named:\n%s", out)
	}

	mustRun(t, dir, "scan", "--root", ".", "--output", ".aracne/topology.db")
	if out := mustRun(t, dir, "check-updates"); !strings.Contains(out, "up to date") {
		t.Fatalf("after a re-scan the index should be reported healthy:\n%s", out)
	}
}

func TestOneFileUpdateDoesNotMarkTheWholeTreeFresh(t *testing.T) {
	dir, _ := staleProject(t)

	// Drift a SECOND file that nothing is about to touch.
	other := filepath.Join(dir, "pkg", "report.go")
	writeFile(t, other, "package pkg\n\n// Report names a shape.\nfunc Report() string { return \"changed\" }\n")
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(other, future, future); err != nil {
		t.Fatal(err)
	}

	// Re-index only shapes.go. SyncManifest used to stamp EVERY file in the topology with its
	// current mtime, so this single-file update declared report.go freshly indexed too -- and
	// no later incremental scan would ever look at it again. That is how a restored database
	// stayed incomplete for a whole benchmark run: the agent's first edit froze the staleness
	// in.
	mustRun(t, dir, "update-file", filepath.Join("pkg", "shapes.go"))

	out, err := runLtp(t, dir, "check-updates")
	if err == nil || !strings.Contains(out, "pkg/report.go") {
		t.Fatalf("updating one file must not mark the rest of the tree indexed:\n%s", out)
	}
	if strings.Contains(out, "pkg/shapes.go") {
		t.Fatalf("the file that WAS re-indexed should no longer be listed:\n%s", out)
	}
}
