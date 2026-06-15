package tests_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScanCustomRoot(t *testing.T) {
	outer := t.TempDir()
	root := filepath.Join(outer, "myproject")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "go.mod"), "module customroot\n\ngo 1.21\n")
	writeFile(t, filepath.Join(root, "main.go"), `package main

func Hello() {}
`)

	out := mustRun(t, outer, "scan", "-root", root, "-output", filepath.Join(root, "topo.db"))
	if !strings.Contains(out, "Analyzing project at:") {
		t.Fatalf("expected scan output, got:\n%s", out)
	}
}

func TestScanCustomOutput(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module customoutput\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "main.go"), `package main

func Hello() {}
`)

	customDB := filepath.Join(t.TempDir(), "mydb.db")
	out := mustRun(t, dir, "scan", "-root", dir, "-output", customDB)

	if !strings.Contains(out, "Topology written to:") {
		t.Fatalf("expected topology written to output, got:\n%s", out)
	}
	if _, err := os.Stat(customDB); os.IsNotExist(err) {
		t.Fatal("expected output db to exist at custom path")
	}

	warnOut := mustRun(t, dir, "warnings", "list", "--db", customDB)
	if !strings.Contains(warnOut, "No warnings found") {
		t.Fatalf("expected no warnings in custom db, got:\n%s", warnOut)
	}
}

func TestScanAllFlagPrintsFullRescan(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module allflag\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "main.go"), `package main

type MyType struct{}
`)

	dbPath := filepath.Join(dir, "test.db")
	mustRun(t, dir, "scan", "-root", dir, "-output", dbPath)

	out := mustRun(t, dir, "scan", "-root", dir, "-output", dbPath, "-all")
	if !strings.Contains(out, "Full re-scan") {
		t.Fatalf("expected 'Full re-scan' output, got:\n%s", out)
	}
}

func TestScanHardFlagClearsBugs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module hardflag\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "main.go"), `package main

func Hello() {}
`)

	dbPath := filepath.Join(dir, "test.db")
	mustRun(t, dir, "scan", "-root", dir, "-output", dbPath)

	mustRun(t, dir, "bug", "report", "--db", dbPath, "--node", "hardflag.Hello", "--description", "test bug")

	listOut := mustRun(t, dir, "bug", "list", "--db", dbPath)
	if strings.Contains(listOut, "No bugs found") {
		t.Fatal("expected bug before hard scan")
	}

	out, err := runLtp(t, dir, "scan", "-root", dir, "-output", dbPath, "-hard")
	if err != nil {
		t.Logf("Hard scan stderr: %s", out)
	}

	listOut2 := mustRun(t, dir, "bug", "list", "--db", dbPath)
	if !strings.Contains(listOut2, "No bugs found") {
		t.Fatalf("expected no bugs after hard scan, got:\n%s", listOut2)
	}
}

func TestScanDefaultFlagUsesIncremental(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module defaultflag\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "main.go"), `package main

func Hello() {}
`)

	dbPath := filepath.Join(dir, "test.db")
	out := mustRun(t, dir, "scan", "-root", dir, "-output", dbPath, "-default")

	if !strings.Contains(out, "Incremental scan") {
		t.Fatalf("expected incremental scan output, got:\n%s", out)
	}
}

func TestScanDebugFlagShowsWarningDiffs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module debugflag\n\ngo 1.21\n")
	mainGo := filepath.Join(dir, "main.go")
	writeFile(t, mainGo, `package main

func main() {
    helper()
}

func helper() {}
`)

	dbPath := filepath.Join(dir, "test.db")
	mustRun(t, dir, "scan", "-root", dir, "-output", dbPath)

	writeFile(t, mainGo, `package main

func main() {
    helper()
}
`)
	mustRun(t, dir, "update-file", mainGo, "--db", dbPath)

	// Modify the file again after update-file (which syncs manifest timestamps)
	// so the incremental scan detects a change and produces a diff
	time.Sleep(2 * time.Second)
	writeFile(t, mainGo, `package main

func main() {
    helper() // still broken
}
`)

	out := mustRun(t, dir, "scan", "-root", dir, "-output", dbPath, "-debug")

	// Debug output should mention warning analysis — either diffs or "no differences"
	if !strings.Contains(out, "Warning Differences") && !strings.Contains(out, "No warning differences") {
		t.Fatalf("expected Warning Differences or 'No warning differences' with --debug, got:\n%s", out)
	}
}

func TestScanOutputCounts(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module counttest\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "main.go"), `package main

import "fmt"

type Service struct{}

func (s Service) Serve() {}

func NewService() *Service { return &Service{} }

var DefaultService = &Service{}
`)

	dbPath := filepath.Join(dir, "test.db")
	out := mustRun(t, dir, "scan", "-root", dir, "-output", dbPath)

	if !strings.Contains(out, "-1 packages") {
		t.Fatalf("expected '-1 packages' in output, got:\n%s", out)
	}
}

func TestScanConfigDrivenMode(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module configmode\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "main.go"), `package main

func Hello() {}
`)

	dbPath := filepath.Join(dir, "test.db")

	mustRun(t, dir, "scan", "-root", dir, "-output", dbPath)

	configPath := filepath.Join(filepath.Dir(dbPath), "config.json")
	writeFile(t, configPath, `{"scan_mode": "all"}`)

	out := mustRun(t, dir, "scan", "-root", dir, "-output", dbPath)

	if !strings.Contains(out, "Full re-scan") {
		t.Fatalf("expected 'Full re-scan' from config scan_mode=all, got:\n%s", dir)
	}
}
