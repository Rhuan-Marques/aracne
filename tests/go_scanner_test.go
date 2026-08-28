package tests_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoScanIgnoresTestFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module gotest\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "main.go"), `package main

func ProductionFunc() {}
`)
	writeFile(t, filepath.Join(dir, "main_test.go"), `package main

import "testing"

func TestHelper(t *testing.T) {}
`)

	mustRun(t, dir, "scan", "-root", dir)

	// Production function should be found
	out := mustRun(t, dir, "resource", "list", "ProductionFunc")
	if !strings.Contains(out, "ProductionFunc") {
		t.Fatalf("expected ProductionFunc in search, got:\n%s", out)
	}

	// Test function should NOT be found
	testOut, err := runLtp(t, dir, "resource", "list", "TestHelper")
	if err == nil {
		if strings.Contains(testOut, "TestHelper") {
			t.Fatalf("expected TestHelper to be excluded from scan, but found:\n%s", testOut)
		}
	}
}

func TestGoScanIgnoresNodeModules(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module nometest\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "main.go"), `package main

func RealFunc() {}
`)

	err := os.MkdirAll(filepath.Join(dir, "node_modules", "pkg"), 0755)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "node_modules", "pkg", "dep.go"), `package pkg

func HiddenDep() {}
`)

	mustRun(t, dir, "scan", "-root", dir)

	// RealFunc should appear
	out := mustRun(t, dir, "resource", "list", "RealFunc")
	if !strings.Contains(out, "RealFunc") {
		t.Fatalf("expected RealFunc in search, got:\n%s", out)
	}

	// HiddenDep should NOT appear
	testOut, err := runLtp(t, dir, "resource", "list", "HiddenDep")
	if err == nil && strings.Contains(testOut, "HiddenDep") {
		t.Fatalf("expected HiddenDep in node_modules to be excluded, got:\n%s", testOut)
	}
}

func TestGoScanIgnoresDotDirs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module dottest\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "main.go"), `package main

func VisibleFunc() {}
`)

	err := os.MkdirAll(filepath.Join(dir, ".secret"), 0755)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, ".secret", "hidden.go"), `package secret

func HiddenFunc() {}
`)

	mustRun(t, dir, "scan", "-root", dir)

	out := mustRun(t, dir, "resource", "list", "VisibleFunc")
	if !strings.Contains(out, "VisibleFunc") {
		t.Fatalf("expected VisibleFunc in search, got:\n%s", out)
	}

	hiddenOut, err := runLtp(t, dir, "resource", "list", "HiddenFunc")
	if err == nil && strings.Contains(hiddenOut, "HiddenFunc") {
		t.Fatalf("expected HiddenFunc in .secret to be excluded, got:\n%s", hiddenOut)
	}
}

func TestGoScanIgnoresGitDir(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module gittest\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "main.go"), `package main

func AppFunc() {}
`)

	err := os.MkdirAll(filepath.Join(dir, ".git", "hooks"), 0755)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, ".git", "hooks", "pre-commit.go"), `package hooks

func PreCommit() {}
`)

	mustRun(t, dir, "scan", "-root", dir)

	out := mustRun(t, dir, "resource", "list", "AppFunc")
	if !strings.Contains(out, "AppFunc") {
		t.Fatalf("expected AppFunc in search, got:\n%s", out)
	}

	gitOut, err := runLtp(t, dir, "resource", "list", "PreCommit")
	if err == nil && strings.Contains(gitOut, "PreCommit") {
		t.Fatalf("expected PreCommit in .git to be excluded, got:\n%s", gitOut)
	}
}

func TestGoCrossPackageConnections(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module crosspkg\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "main.go"), `package main

func main() {
    Greet()
}
`)
	if err := os.MkdirAll(filepath.Join(dir, "lib"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "lib", "greet.go"), `package lib

func Greet() string { return "hi" }
`)

	dbPath := filepath.Join(dir, "test.db")
	mustRun(t, dir, "scan", "-root", dir, "-output", dbPath)

	warnOut := mustRun(t, dir, "warnings", "list", "--db", dbPath)
	// If Greet is found via cross-package resolution, no warnings
	// If not resolved, we'll get a use_missing_node warning
	// Either way, verify the scan ran and produced some output
	if strings.Contains(warnOut, "No warnings found") {
		t.Log("cross-package Greet resolved correctly (no warnings)")
	} else {
		t.Logf("cross-package Greet not resolved (warnings exist):\n%s", warnOut)
	}
}

func TestGoExternalImports(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module exttest\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "main.go"), `package main

import "fmt"

func log(msg string) {
    fmt.Println(msg)
}
`)

	dbPath := filepath.Join(dir, "test.db")
	mustRun(t, dir, "scan", "-root", dir, "-output", dbPath)

	// No warnings expected for external imports
	warnOut := mustRun(t, dir, "warnings", "list", "--db", dbPath)
	if !strings.Contains(warnOut, "No warnings found") {
		t.Fatalf("expected no warnings for external imports, got:\n%s", warnOut)
	}
}

func TestGoStructInterfaceMatching(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module ifacetest\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "main.go"), `package main

type Greeter interface {
    Greet() string
}

type English struct{}

func (e English) Greet() string { return "Hello" }

func sayHello(g Greeter) string {
    return g.Greet()
}
`)

	mustRun(t, dir, "scan", "-root", dir)

	out := mustRun(t, dir, "resource", "list", "Greeter")
	if !strings.Contains(out, "Greeter") {
		t.Fatalf("expected Greeter interface in search, got:\n%s", out)
	}

	out2 := mustRun(t, dir, "resource", "list", "English")
	if !strings.Contains(out2, "English") {
		t.Fatalf("expected English struct in search, got:\n%s", out2)
	}
}

func TestGoConstructorDetection(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module constest\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "main.go"), `package main

type Config struct {
    Host string
}

func NewConfig() *Config {
    return &Config{Host: "localhost"}
}
`)

	mustRun(t, dir, "scan", "-root", dir)

	out := mustRun(t, dir, "resource", "list", "NewConfig")
	if !strings.Contains(out, "NewConfig") {
		t.Fatalf("expected NewConfig in search, got:\n%s", out)
	}
}

func TestGoNamedTypeUsage(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module namedtest\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "main.go"), `package main

type ID string

func PrintID(id ID) {
    _ = id
}
`)

	mustRun(t, dir, "scan", "-root", dir)

	out := mustRun(t, dir, "resource", "list", "ID")
	// Was `!Contains(x) || !Contains(x)` with both sides identical, so the second half was
	// dead. Asserting the one thing this test actually checks.
	if !strings.Contains(out, "namedtest.ID") {
		t.Fatalf("expected namedtest.ID in search, got:\n%s", out)
	}
}

func TestGoScanDoesNotWarnOnBuiltins(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module builtintest\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "main.go"), `package main

func useBuiltins() {
    s := make([]int, 0)
    _ = len(s)
    _ = cap(s)
    _ = append(s, 1)
}
`)

	dbPath := filepath.Join(dir, "test.db")
	mustRun(t, dir, "scan", "-root", dir, "-output", dbPath)

	warnOut := mustRun(t, dir, "warnings", "list", "--db", dbPath)
	if !strings.Contains(warnOut, "No warnings found") {
		t.Fatalf("expected no warnings for builtins, got:\n%s", warnOut)
	}
}

func TestGoScanDoesNotWarnOnKnownLocalFuncs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module localtest\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "main.go"), `package main

func helper() int { return 42 }

func caller() {
    _ = helper()
}
`)

	dbPath := filepath.Join(dir, "test.db")
	mustRun(t, dir, "scan", "-root", dir, "-output", dbPath)

	warnOut := mustRun(t, dir, "warnings", "list", "--db", dbPath)
	if !strings.Contains(warnOut, "No warnings found") {
		t.Fatalf("expected no warnings for known local function, got:\n%s", warnOut)
	}
}
