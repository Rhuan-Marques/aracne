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
	out := mustRun(t, dir, "search", "needle")
	lines := strings.Split(strings.TrimSpace(out), "\n")

	if len(lines) != 2 {
		t.Fatalf("expected 2 matching resources, got %d:\n%s", len(lines), out)
	}
	if !strings.Contains(lines[0], "NeedleFunc") || !strings.Contains(lines[1], "NeedleType") {
		t.Fatalf("expected NeedleFunc and NeedleType matches, got:\n%s", out)
	}
	if strings.Contains(out, "Found") {
		t.Fatalf("expected one resource per line without summary, got:\n%s", out)
	}
}

func TestSearchFindsResourcesByIDSegment(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module segmenttest\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "main.go"), `package main

func PlainName() {}
`)

	mustRun(t, dir, "scan", "-root", dir)
	out := mustRun(t, dir, "search", "segmenttest")

	if !strings.Contains(out, "segmenttest.PlainName") {
		t.Fatalf("expected ID segment match, got:\n%s", out)
	}
	if strings.Contains(out, string(os.PathSeparator)+".aracne") {
		t.Fatalf("expected resource IDs only, got:\n%s", out)
	}
}
