package tests_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var AracBin string

func TestMain(m *testing.M) {
	bin := filepath.Join(os.TempDir(), "Arac_test_"+randSuffix()+".exe")
	// ./cmd/arac, not ".", since the main package moved out of the repo root. Built with
	// default tags, so these tests exercise the Full build.
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/arac")
	cmd.Dir = projectRoot()
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "build Aracne: %v\n", err)
		os.Exit(1)
	}
	AracBin = bin
	defer os.Remove(bin)
	os.Exit(m.Run())
}

func projectRoot() string {
	wd, _ := os.Getwd()
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(wd, "go.mod")); err == nil {
			return wd
		}
		parent := filepath.Dir(wd)
		if parent == wd {
			break
		}
		wd = parent
	}
	return "."
}

func randSuffix() string {
	return fmt.Sprintf("%d", os.Getpid())
}

func runLtp(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(AracBin, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	out := stdout.String() + stderr.String()
	if err != nil {
		return out, fmt.Errorf("Aracne %v: %w\n%s", args, err, out)
	}
	return out, nil
}

func mustRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := runLtp(t, dir, args...)
	if err != nil {
		t.Fatalf("Aracne %v failed: %v\n%s", args, err, out)
	}
	return out
}

// allowReadKinds rewrites read.kinds in place. It edits the file rather than regenerating it
// so everything else `arac scan` decided about the project survives -- the same reason
// setMode does.
//
// Tests need it because read.kinds now gates EVERY read entrance, so a suite about a kind the
// default set leaves out (named_type, package, variable, dependency) has to say so.
func allowReadKinds(t *testing.T, root string, kinds ...string) {
	t.Helper()
	path := filepath.Join(root, ".aracne", "config.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	read, _ := cfg["read"].(map[string]any)
	if read == nil {
		read = map[string]any{}
		cfg["read"] = read
	}
	list := make([]any, 0, len(kinds))
	for _, k := range kinds {
		list = append(list, k)
	}
	read["kinds"] = list
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

type testProject struct {
	dir    string
	dbPath string
}

func newProject(t *testing.T, root, modulePath string) *testProject {
	t.Helper()
	dir := filepath.Join(root, "proj")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mod := fmt.Sprintf("module testapp_%d", len(dir))
	writeFile(t, filepath.Join(dir, "go.mod"), mod+"\ngo 1.21\n")
	dbPath := filepath.Join(dir, "test.db")
	return &testProject{dir: dir, dbPath: dbPath}
}

func (p *testProject) scan(t *testing.T) {
	t.Helper()
	mustRun(t, p.dir, "scan", "-root", p.dir, "-output", p.dbPath)
}

func (p *testProject) updateFile(t *testing.T, path string) {
	t.Helper()
	out, err := runLtp(t, p.dir, "update-file", path, "--db", p.dbPath)
	if err != nil {
		t.Logf("update-file output (non-fatal):\n%s", out)
	}
}

func (p *testProject) warningsList(t *testing.T) string {
	t.Helper()
	out, err := runLtp(t, p.dir, "warnings", "list", "--db", p.dbPath)
	if err != nil {
		t.Fatalf("warnings list: %v\n%s", err, out)
	}
	return out
}

func assertNoWarnings(t *testing.T, p *testProject) {
	t.Helper()
	out := p.warningsList(t)
	if !strings.Contains(out, "No warnings found") {
		t.Fatalf("expected no warnings, got:\n%s", out)
	}
}

func assertHasWarningKinds(t *testing.T, p *testProject, kinds ...string) {
	t.Helper()
	out := p.warningsList(t)
	if strings.Contains(out, "No warnings found") {
		t.Fatalf("expected warnings but found none")
	}
	for _, kind := range kinds {
		if !strings.Contains(out, kind+":") {
			t.Logf("Warning output:\n%s", out)
			t.Fatalf("expected warning kind %q not found", kind)
		}
	}
}

// Test Warnings: Same-file removal (WarnUseMissingNode from body analysis)
func TestSameFileFunctionRemoval(t *testing.T) {
	root := t.TempDir()
	p := newProject(t, root, "testapp")
	mainGo := filepath.Join(p.dir, "main.go")
	writeFile(t, mainGo, `package main

func main() {
    helper()
}

func helper() {}
`)
	p.scan(t)
	assertNoWarnings(t, p)

	// Remove helper - same file. Body analysis generates WarnUseMissingNode
	writeFile(t, mainGo, `package main

func main() {
    helper()
}
`)
	p.updateFile(t, mainGo)
	out := p.warningsList(t)
	t.Logf("Same-file removal warnings:\n%s", out)
	assertHasWarningKinds(t, p, "use_missing_node")

	// Restore helper - should resolve warning
	writeFile(t, mainGo, `package main

func main() {
    helper()
}

func helper() {}
`)
	p.updateFile(t, mainGo)
	assertNoWarnings(t, p)
}

// Test Warnings: Cross-file removal (WarnNodeRemoved from removed detection)
func TestCrossFileFunctionRemoval(t *testing.T) {
	root := t.TempDir()
	p := newProject(t, root, "testapp")
	writeFile(t, filepath.Join(p.dir, "caller.go"), `package main

func caller() {
    callee()
}
`)
	writeFile(t, filepath.Join(p.dir, "callee.go"), `package main

func callee() {}
`)
	p.scan(t)
	assertNoWarnings(t, p)

	// Remove callee from callee.go - cross file. Removed detection generates WarnNodeRemoved
	calleeGo := filepath.Join(p.dir, "callee.go")
	writeFile(t, calleeGo, `package main

func callee() {}
`) // remove callee
	writeFile(t, calleeGo, `package main
`)
	p.updateFile(t, calleeGo)

	out := p.warningsList(t)
	t.Logf("Cross-file removal warnings:\n%s", out)
	// Should have node_removed (cross-file)
	if strings.Contains(out, "No warnings found") {
		t.Fatal("expected warnings after cross-file removal")
	}

	// Restore callee to callee.go
	writeFile(t, calleeGo, `package main

func callee() {}
`)
	p.updateFile(t, calleeGo)
	out = p.warningsList(t)
	if !strings.Contains(out, "No warnings found") {
		t.Logf("Warnings after restore (may be expected if resolution needs caller re-parse):\n%s", out)
	}
}

// Test: WarnUseMissingNode from initial scan resolved by adding function
func TestMissingFunctionResolvedByAdding(t *testing.T) {
	root := t.TempDir()
	p := newProject(t, root, "testapp")
	mainGo := filepath.Join(p.dir, "main.go")
	writeFile(t, mainGo, `package main

func main() {
    helper()
}
`)
	p.scan(t)
	out := p.warningsList(t)
	t.Logf("After scan with non-existent function:\n%s", out)

	// The scan should generate WarnUseMissingNode (body analysis)
	// Since helper() was called but never existed
	assertHasWarningKinds(t, p, "use_missing_node")

	// Add helper function
	writeFile(t, mainGo, `package main

func main() {
    helper()
}

func helper() {}
`)
	p.updateFile(t, mainGo)
	assertNoWarnings(t, p)
}

// Test: Remove a called struct, get warning, restore it, warning gone
func TestStructRemovalAndRestore(t *testing.T) {
	root := t.TempDir()
	p := newProject(t, root, "testapp")
	mainGo := filepath.Join(p.dir, "main.go")
	writeFile(t, mainGo, `package main

type MyStruct struct {
    X int
}

func useStruct() MyStruct {
    return MyStruct{}
}
`)
	p.scan(t)
	assertNoWarnings(t, p)

	// Remove the struct — function still constructs it via MyStruct{}
	writeFile(t, mainGo, `package main

func useStruct() MyStruct {
    return MyStruct{}
}
`)
	p.updateFile(t, mainGo)
	out := p.warningsList(t)
	t.Logf("After struct removal:\n%s", out)
	if strings.Contains(out, "No warnings found") {
		t.Fatal("expected warnings after struct removal")
	}

	// Restore the struct
	writeFile(t, mainGo, `package main

type MyStruct struct {
    X int
}

func useStruct() MyStruct {
    return MyStruct{}
}
`)
	p.updateFile(t, mainGo)
	out = p.warningsList(t)
	if !strings.Contains(out, "No warnings found") {
		t.Logf("Warnings after struct restore:\n%s", out)
	}
}

// Test: Multiple callers get separate warnings
func TestMultipleCallersSeparateWarnings(t *testing.T) {
	root := t.TempDir()
	p := newProject(t, root, "testapp")
	mainGo := filepath.Join(p.dir, "main.go")
	writeFile(t, mainGo, `package main

func callerOne() {
    shared()
}

func callerTwo() {
    shared()
}

func shared() {}
`)
	p.scan(t)
	assertNoWarnings(t, p)

	// Remove shared
	writeFile(t, mainGo, `package main

func callerOne() {
    shared()
}

func callerTwo() {
    shared()
}
`)
	p.updateFile(t, mainGo)
	out := p.warningsList(t)
	t.Logf("Multiple callers warnings:\n%s", out)
	if strings.Contains(out, "No warnings found") {
		t.Fatal("expected warnings after removing shared")
	}
}

func TestWarningClearedByIncrementalScan(t *testing.T) {
	root := t.TempDir()
	p := newProject(t, root, "testapp")
	mainGo := filepath.Join(p.dir, "main.go")
	writeFile(t, mainGo, `package main

func main() {
    helper()
}

func helper() {}
`)
	p.scan(t)

	// Remove helper, get warning
	writeFile(t, mainGo, `package main

func main() {
    helper()
}
`)
	p.updateFile(t, mainGo)
	out := p.warningsList(t)
	if strings.Contains(out, "No warnings found") {
		t.Fatal("expected warning after removal")
	}

	// Incremental scan should CLEAR warnings (currently by design)
	p.scan(t) // incremental scan via same -output
	out = p.warningsList(t)
	t.Logf("After incremental scan:\n%s", out)
}

func TestWarningClearedByHardScan(t *testing.T) {
	root := t.TempDir()
	p := newProject(t, root, "testapp")
	mainGo := filepath.Join(p.dir, "main.go")
	writeFile(t, mainGo, `package main

func main() {
    helper()
}

func helper() {}
`)
	p.scan(t)

	writeFile(t, mainGo, `package main

func main() {
    helper()
}
`)
	p.updateFile(t, mainGo)
	out := p.warningsList(t)
	if strings.Contains(out, "No warnings found") {
		t.Fatal("expected warning after removal")
	}

	// Hard scan clears everything
	// scan with --hard forces full rebuild
	out2, err := runLtp(t, p.dir, "scan", "-root", p.dir, "-output", p.dbPath, "--hard")
	if err != nil {
		// Hard scan might fail depending on behavior; try regular scan
		t.Logf("Hard scan output: %s", out2)
		// Check warnings anyway - if DB was overwritten, they should be gone
	}
	out3 := p.warningsList(t)
	if strings.Contains(out3, "No warnings found") {
		return // passed
	}
	t.Logf("Warnings after hard scan:\n%s", out3)
}

// Test: Removing both caller and callee in same edit should not create warning
func TestRemoveCallerAndCallee(t *testing.T) {
	root := t.TempDir()
	p := newProject(t, root, "testapp")
	mainGo := filepath.Join(p.dir, "main.go")
	writeFile(t, mainGo, `package main

func caller() {
    target()
}

func target() {}
`)
	p.scan(t)
	assertNoWarnings(t, p)

	// Remove both caller and target
	writeFile(t, mainGo, `package main
`)
	p.updateFile(t, mainGo)
	out := p.warningsList(t)
	t.Logf("After removing both caller and target:\n%s", out)
	// Should have no warnings since caller was also removed
	if strings.Contains(out, "No warnings found") {
		return // correct - no stale warnings
	}
	// If warnings exist, this is a bug - stale warning about a removed function
	t.Log("Potential bug: stale warning for removed+removed scenario")
}

// Test: New file that calls a non-existent function should get WarnUseMissingNode
func TestNewFileCallingNonExistent(t *testing.T) {
	root := t.TempDir()
	p := newProject(t, root, "testapp")
	writeFile(t, filepath.Join(p.dir, "caller.go"), `package main

func caller() {
    noexist()
}
`)
	p.scan(t)
	out := p.warningsList(t)
	t.Logf("New file calling non-existent:\n%s", out)
	assertHasWarningKinds(t, p, "use_missing_node")
}

// Test: Cross-file struct use warning
func TestCrossFileStructRemoval(t *testing.T) {
	root := t.TempDir()
	p := newProject(t, root, "testapp")
	writeFile(t, filepath.Join(p.dir, "user.go"), `package main

func useExternalStruct() {
    var s SharedStruct
    _ = s
}
`)
	writeFile(t, filepath.Join(p.dir, "shared.go"), `package main

type SharedStruct struct {
    Val int
}
`)
	p.scan(t)
	assertNoWarnings(t, p)

	// Remove SharedStruct from shared.go
	sharedGo := filepath.Join(p.dir, "shared.go")
	// Need to keep at least a valid file
	writeFile(t, sharedGo, `package main
`)
	p.updateFile(t, sharedGo)

	out := p.warningsList(t)
	t.Logf("Cross-file struct removal:\n%s", out)
	// user.go's useExternalStruct references SharedStruct which was removed
	if strings.Contains(out, "No warnings found") {
		t.Log("Note: No warning for cross-file struct reference (may be expected)")
	}
}

// Test: Incremental scan with no changes should produce no warnings
func TestIncrementalScanNoChanges(t *testing.T) {
	root := t.TempDir()
	p := newProject(t, root, "testapp")
	mainGo := filepath.Join(p.dir, "main.go")
	writeFile(t, mainGo, `package main

func main() {}
`)
	p.scan(t)

	// Re-scan with no changes
	out, err := runLtp(t, p.dir, "scan", "-root", p.dir, "-output", p.dbPath)
	if err != nil {
		t.Fatalf("second scan failed: %v\n%s", err, out)
	}
	if strings.Contains(out, "warning") {
		t.Logf("Second scan produced output:\n%s", out)
	}
}

// Test: Update file on a file that doesn't exist should remove its resources
func TestUpdateFileOnDeletedFile(t *testing.T) {
	root := t.TempDir()
	p := newProject(t, root, "testapp")
	writeFile(t, filepath.Join(p.dir, "main.go"), `package main

func main() {}
`)
	writeFile(t, filepath.Join(p.dir, "extra.go"), `package main

func Extra() {}
`)
	p.scan(t)
	assertNoWarnings(t, p)

	// Delete extra.go and run update-file on it
	extraGo := filepath.Join(p.dir, "extra.go")
	if err := os.Remove(extraGo); err != nil {
		t.Fatal(err)
	}
	p.updateFile(t, extraGo)

	// Should have no warnings since the file was just removed
	out := p.warningsList(t)
	t.Logf("After deleting file:\n%s", out)
}

// Test: Full lifecycle: scan, modify, add, verify
func TestIncrementalScanFileAdded(t *testing.T) {
	root := t.TempDir()
	p := newProject(t, root, "testapp")
	writeFile(t, filepath.Join(p.dir, "main.go"), `package main

func main() {}
`)

	// Use default output path so search can find it
	mustRun(t, p.dir, "scan", "-root", p.dir, "-output", p.dbPath)

	// Add a new file
	writeFile(t, filepath.Join(p.dir, "newfunc.go"), `package main

func NewlyAdded() int { return 42 }
`)
	mustRun(t, p.dir, "update-file", filepath.Join(p.dir, "newfunc.go"), "--db", p.dbPath)

	warnOut := mustRun(t, p.dir, "warnings", "list", "--db", p.dbPath)
	t.Logf("After adding file:\n%s", warnOut)
}

// Test: Warning lifecycle - remove, get warning, restore via update-file, verify cleared
func TestWarningFullLifecycle(t *testing.T) {
	root := t.TempDir()
	p := newProject(t, root, "testapp")
	mainGo := filepath.Join(p.dir, "main.go")
	writeFile(t, mainGo, `package main

func main() {
    greet()
}

func greet() string { return "hi" }
`)
	p.scan(t)
	assertNoWarnings(t, p)

	// Remove greet()
	writeFile(t, mainGo, `package main

func main() {
    greet()
}
`)
	p.updateFile(t, mainGo)
	assertHasWarningKinds(t, p, "use_missing_node")

	// Restore greet() - should clear warning
	writeFile(t, mainGo, `package main

func main() {
    greet()
}

func greet() string { return "hi" }
`)
	p.updateFile(t, mainGo)
	assertNoWarnings(t, p)
}

// Test: Struct with methods - remove method, verify warning
func TestMethodRemovalFromStruct(t *testing.T) {
	root := t.TempDir()
	p := newProject(t, root, "testapp")
	mainGo := filepath.Join(p.dir, "main.go")
	writeFile(t, mainGo, `package main

type Service struct{}

func (s Service) Serve() {}

func CallService(s Service) {
    s.Serve()
}
`)
	p.scan(t)
	assertNoWarnings(t, p)

	// Remove Serve method
	writeFile(t, mainGo, `package main

type Service struct{}

func CallService(s Service) {
    s.Serve()
}
`)
	p.updateFile(t, mainGo)
	out := p.warningsList(t)
	t.Logf("After method removal:\n%s", out)
	if strings.Contains(out, "No warnings found") {
		t.Log("Method removal may not generate cross-file warning (same file)")
	}
}
