package tests_test

import (
	"encoding/json"
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
	writeFile(t, configPath, `{"scan": {"mode": "all"}}`)

	out := mustRun(t, dir, "scan", "-root", dir, "-output", dbPath)

	if !strings.Contains(out, "Full re-scan") {
		t.Fatalf("expected 'Full re-scan' from config scan.mode=all, got:\n%s", out)
	}
}

// TestScanUnderHiddenRootIndexesFiles is the CLI-level regression for the
// dot-directory blackout.
//
// Ignore rules used to be applied to the ABSOLUTE path, so a checkout under a
// hidden ancestor matched on its own ancestry and produced "-0 files, -0
// functions, -0 errors" and exit 0 — indistinguishable from success. Beyond the
// empty graph it also wrote an empty file manifest, froze incremental scanning
// at 0/0/0, and made every subsequent edit delete that file's resources.
func TestScanUnderHiddenRootIndexesFiles(t *testing.T) {
	seed := func(dir string) {
		writeFile(t, filepath.Join(dir, "package.json"), `{ "name": "mini", "version": "1.0.0", "type": "module" }`+"\n")
		writeFile(t, filepath.Join(dir, "index.js"),
			"export function alpha(a) { return a + 1; }\nexport function beta(b) { return alpha(b); }\n")
	}

	hidden := filepath.Join(t.TempDir(), ".claude", "scratch", "mini")
	visible := filepath.Join(t.TempDir(), "repos", "mini")
	for _, d := range []string{hidden, visible} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		seed(d)
	}

	hiddenOut := mustRun(t, hidden, "scan", "-all", "-root", hidden, "-output", filepath.Join(hidden, "t.db"), "-progress", "never")
	visibleOut := mustRun(t, visible, "scan", "-all", "-root", visible, "-output", filepath.Join(visible, "t.db"), "-progress", "never")

	if strings.Contains(hiddenOut, "-0 files") {
		t.Fatalf("a repo under a hidden directory scanned to zero files:\n%s", hiddenOut)
	}
	for _, want := range []string{"-1 files", "-2 functions"} {
		if !strings.Contains(hiddenOut, want) {
			t.Errorf("expected %q under a hidden root, got:\n%s", want, hiddenOut)
		}
		if !strings.Contains(visibleOut, want) {
			t.Errorf("expected %q under a visible root, got:\n%s", want, visibleOut)
		}
	}
}

// A dot-NAMED root (the repo directory itself is hidden, e.g. ~/.dotfiles) must
// also scan: WalkDir's first callback is the root, and pruning by basename
// there killed the whole walk.
func TestScanWithDotNamedRootIndexesFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".dotfiles")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "go.mod"), "module dotfiles\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "main.go"), "package main\n\nfunc Alpha() int { return 1 }\n")

	out := mustRun(t, dir, "scan", "-all", "-root", dir, "-output", filepath.Join(dir, "t.db"), "-progress", "never")
	if !strings.Contains(out, "-1 files") {
		t.Fatalf("a repo in a dot-named directory scanned to zero:\n%s", out)
	}
}

// TestScanWarnsWhenNothingIndexed: "0 files, 0 errors, exit 0" reads as
// success. A detected project that indexes nothing has to say so — that silence
// is what turned this bug into an hour of false leads.
func TestScanWarnsWhenNothingIndexed(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "package.json"), `{ "name": "empty", "version": "1.0.0" }`+"\n")

	out := mustRun(t, dir, "scan", "-all", "-root", dir, "-output", filepath.Join(dir, "t.db"), "-progress", "never")
	if !strings.Contains(out, "-0 files") {
		t.Fatalf("expected an empty scan, got:\n%s", out)
	}
	if !strings.Contains(out, "indexed 0 files") {
		t.Errorf("an empty scan of a detected project must warn, got:\n%s", out)
	}
	if strings.Contains(out, "-0 errors") {
		t.Errorf("a detected-but-empty scanner should be recorded as an error, got:\n%s", out)
	}
}

// TestManifestNotEmptiedUnderHiddenRoot pins the half of the bug that was
// invisible in the scan output: SyncManifest filtered the topology through the
// same predicate, so it wrote an empty manifest and incremental scanning went
// permanently quiet.
func TestManifestNotEmptiedUnderHiddenRoot(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".cache", "proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "go.mod"), "module proj\n\ngo 1.21\n")
	writeFile(t, filepath.Join(dir, "main.go"), "package main\n\nfunc Alpha() int { return 1 }\n")

	dbPath := filepath.Join(dir, ".aracne", "topology.db")
	mustRun(t, dir, "scan", "-all", "-root", dir, "-output", dbPath, "-progress", "never")

	data, err := os.ReadFile(filepath.Join(dir, ".aracne", "file_manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest map[string]string
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	if len(manifest) == 0 {
		t.Fatal("the manifest was emptied under a hidden root; incremental scans would go permanently quiet")
	}
}
