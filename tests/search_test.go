package tests_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSearchFindsResourcesByName(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module searchtest\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "main.go"), `package main

type NeedleType struct{}

func NeedleFunc() {}

func OtherFunc() {}
`)

	mustRun(t, dir, "scan", "-root", dir)
	out := mustRun(t, dir, "resource", "list", "needle")

	// `resource list` prints a "Found N resources." header followed by one
	// matching resource ID per line, sorted by ID.
	if !strings.Contains(out, "Found 2 resources.") {
		t.Fatalf("expected 2 matching resources, got:\n%s", out)
	}
	if !strings.Contains(out, "NeedleFunc") || !strings.Contains(out, "NeedleType") {
		t.Fatalf("expected NeedleFunc and NeedleType matches, got:\n%s", out)
	}
	if strings.Contains(out, "OtherFunc") {
		t.Fatalf("did not expect OtherFunc in results, got:\n%s", out)
	}
}

func TestSearchFindsResourcesByIDSegment(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module segmenttest\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "main.go"), `package main

func PlainName() {}
`)

	mustRun(t, dir, "scan", "-root", dir)
	out := mustRun(t, dir, "resource", "list", "segmenttest")

	if !strings.Contains(out, "segmenttest.PlainName") {
		t.Fatalf("expected ID segment match, got:\n%s", out)
	}
	if strings.Contains(out, string(os.PathSeparator)+".aracne") {
		t.Fatalf("expected resource IDs only, got:\n%s", out)
	}
}
