package tests_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanGoOnlyProject(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module myapp\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "main.go"), `package main

type User struct {
    Name string
}

func Greet(u User) string {
    return "Hello " + u.Name
}
`)

	mustRun(t, dir, "scan", "-root", dir)
	out := mustRun(t, dir, "search", "User")

	if !strings.Contains(out, "myapp.User") && !strings.Contains(out, "User") {
		t.Fatalf("expected User in search results, got:\n%s", out)
	}
}

func TestScanPythonOnlyProject(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "setup.py"), `from setuptools import setup
setup(name="mypkg")
`)
	if err := os.MkdirAll(filepath.Join(dir, "mypkg"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "mypkg", "__init__.py"), "")
	writeFile(t, filepath.Join(dir, "mypkg", "core.py"), `
def greet(name):
    return "Hello " + name

class User:
    def __init__(self, name):
        self.name = name
`)

	mustRun(t, dir, "scan", "-root", dir)
	out := mustRun(t, dir, "search", "greet")

	if !strings.Contains(out, "greet") {
		t.Fatalf("expected greet in search results, got:\n%s", out)
	}
}

func TestScanMixedProject(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module mixedapp\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "main.go"), `package main

func GoHello() string { return "hi" }
`)
	writeFile(t, filepath.Join(dir, "tool.py"), `
def py_hello():
    return "hi"
`)

	mustRun(t, dir, "scan", "-root", dir)

	goOut := mustRun(t, dir, "search", "GoHello")
	if !strings.Contains(goOut, "GoHello") {
		t.Fatalf("expected GoHello in search, got:\n%s", goOut)
	}

	pyOut := mustRun(t, dir, "search", "py_hello")
	if !strings.Contains(pyOut, "py_hello") {
		t.Fatalf("expected py_hello in search, got:\n%s", pyOut)
	}
}

func TestBugCreateListAndDelete(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module bugtest\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "main.go"), `package main

type Service struct{}
`)

	dbPath := filepath.Join(dir, "test.db")
	mustRun(t, dir, "scan", "-root", dir, "-output", dbPath)

	reportOut := mustRun(t, dir, "bug", "report", "--db", dbPath, "--node", "bugtest.Service", "--description", "needs refactor")
	if !strings.Contains(reportOut, "Bug reported:") {
		t.Fatalf("expected bug reported, got:\n%s", reportOut)
	}

	// Extract bug ID from output
	bugID := ""
	for _, line := range strings.Split(reportOut, "\n") {
		if strings.HasPrefix(line, "Bug reported:") {
			parts := strings.Split(line, " ")
			if len(parts) >= 3 {
				bugID = parts[2]
				bugID = strings.TrimSuffix(bugID, " (state: pending)")
				break
			}
		}
	}
	if bugID == "" {
		t.Fatal("could not extract bug ID from output")
	}

	listOut := mustRun(t, dir, "bug", "list", "--db", dbPath)
	if !strings.Contains(listOut, bugID) {
		t.Fatalf("expected bug %s in list, got:\n%s", bugID, listOut)
	}
	if !strings.Contains(listOut, "needs refactor") {
		t.Fatalf("expected description in list, got:\n%s", listOut)
	}

	delOut := mustRun(t, dir, "bug", "delete", "--db", dbPath, bugID)
	if !strings.Contains(delOut, "deleted") {
		t.Fatalf("expected bug deleted, got:\n%s", delOut)
	}

	listOut2 := mustRun(t, dir, "bug", "list", "--db", dbPath)
	if !strings.Contains(listOut2, "No bugs found") {
		t.Fatalf("expected no bugs after delete, got:\n%s", listOut2)
	}
}

func TestBugStateTransition(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module statetest\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "main.go"), `package main

type Service struct{}
`)

	dbPath := filepath.Join(dir, "test.db")
	mustRun(t, dir, "scan", "-root", dir, "-output", dbPath)

	reportOut := mustRun(t, dir, "bug", "report", "--db", dbPath, "--node", "statetest.Service", "--description", "needs fix")

	bugID := ""
	for _, line := range strings.Split(reportOut, "\n") {
		if strings.HasPrefix(line, "Bug reported:") {
			parts := strings.Split(line, " ")
			if len(parts) >= 3 {
				bugID = strings.TrimSuffix(parts[2], " (state: pending)")
				break
			}
		}
	}
	if bugID == "" {
		t.Fatal("could not extract bug ID")
	}

	ackOut := mustRun(t, dir, "bug", "acknowledge", "--db", dbPath, bugID)
	if !strings.Contains(ackOut, "acknowledged") {
		t.Fatalf("expected acknowledged, got:\n%s", ackOut)
	}

	listAck := mustRun(t, dir, "bug", "list", "--db", dbPath, "--state", "acknowledged")
	if !strings.Contains(listAck, bugID) {
		t.Fatalf("expected bug %s in acknowledged list, got:\n%s", bugID, listAck)
	}

	dismissOut := mustRun(t, dir, "bug", "dismiss", "--db", dbPath, bugID)
	if !strings.Contains(dismissOut, "dismissed") {
		t.Fatalf("expected dismissed, got:\n%s", dismissOut)
	}

	listDismiss := mustRun(t, dir, "bug", "list", "--db", dbPath, "--state", "dismissed")
	if !strings.Contains(listDismiss, bugID) {
		t.Fatalf("expected bug %s in dismissed list, got:\n%s", bugID, listDismiss)
	}
}

func TestReadFunctionByName(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module readtest\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "main.go"), `package main

func Greet(name string) string {
    return "Hello " + name
}
`)

	mustRun(t, dir, "scan", "-root", dir)
	out := mustRun(t, dir, "search", "Greet")

	if !strings.Contains(out, "Greet") {
		t.Fatalf("expected Greet in search, got:\n%s", out)
	}
}

func TestWarningsListFiltersByKind(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module filtertest\n\ngo 1.21\n")
	mainGo := filepath.Join(dir, "main.go")
	writeFile(t, mainGo, `package main

func main() {
    missingFunc()
}
`)

	dbPath := filepath.Join(dir, "test.db")
	mustRun(t, dir, "scan", "-root", dir, "-output", dbPath)

	// Should have use_missing_node warnings
	warnOut := mustRun(t, dir, "warnings", "list", "--db", dbPath, "--kind", "use_missing_node")
	if strings.Contains(warnOut, "No warnings found") {
		out := mustRun(t, dir, "warnings", "list", "--db", dbPath)
		t.Fatalf("expected use_missing_node warnings, all warnings:\n%s", out)
	}
	if !strings.Contains(warnOut, "use_missing_node:") {
		t.Fatalf("expected use_missing_node kind in filtered output, got:\n%s", warnOut)
	}
}

func TestWarningsListFiltersBySource(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module sourcetest\n\ngo 1.21\n")
	mainGo := filepath.Join(dir, "main.go")
	writeFile(t, mainGo, `package main

func main() {
    missingFunc()
}
`)

	dbPath := filepath.Join(dir, "test.db")
	mustRun(t, dir, "scan", "-root", dir, "-output", dbPath)

	// Get the source ID from the warning
	warnOut := mustRun(t, dir, "warnings", "list", "--db", dbPath)
	sourceID := ""
	for _, line := range strings.Split(warnOut, "\n") {
		if strings.HasPrefix(line, "    source:") {
			sourceID = strings.TrimSpace(strings.TrimPrefix(line, "    source:"))
			break
		}
	}
	if sourceID == "" {
		t.Fatalf("could not extract source ID from warnings:\n%s", warnOut)
	}

	filtered := mustRun(t, dir, "warnings", "list", "--db", dbPath, "--source", sourceID)
	if strings.Contains(filtered, "No warnings found") {
		t.Fatalf("expected warnings filtered by source %s, got:\n%s", sourceID, filtered)
	}
}
