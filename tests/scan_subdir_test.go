package tests_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// A BARE `arac scan` SCANS THE PROJECT IT IS RUN INSIDE, not the directory it is run from.
//
// This was the last verb resolving the default database literally, and ProjectDBPath's comment
// describes what that cost: run from a subdirectory it built a SECOND, partial topology under
// `<subdir>/.aracne/` beside a fresh default config -- so the project's mode was silently
// replaced by `cli` for that subtree, guardDBPath's upward walk found the nested database
// first, and interception stopped. `arac init` refuses the same situation with a pointer; this
// created it without a word.
func TestBareScanFromASubdirectoryDoesNotForkTheProject(t *testing.T) {
	dir := t.TempDir()
	writeFileMk(t, dir, "go.mod", "module example.com/s\n\ngo 1.21\n")
	writeFileMk(t, dir, "pkg/sub/a.go", "package sub\n\nfunc A() int { return 1 }\n")
	mustRun(t, dir, "scan", "--all")

	// Put the project on a mode a nested default config would not have.
	configPath := filepath.Join(dir, ".aracne", "config.json")
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	cfg["mode"] = "intercept_line_ranges"
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, out, 0o644); err != nil {
		t.Fatal(err)
	}

	sub := filepath.Join(dir, "pkg", "sub")
	mustRun(t, sub, "scan")

	if _, err := os.Stat(filepath.Join(sub, ".aracne")); err == nil {
		t.Error("a bare scan from a subdirectory forked a second topology under it")
	}
	// The project's own answer survived.
	raw, err = os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	cfg = nil
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["mode"] != "intercept_line_ranges" {
		t.Errorf("the project's mode became %v", cfg["mode"])
	}
}

// The escape hatch `arac init`'s refusal points at has to keep working: naming a flag is the
// caller saying where, and `arac scan -root .` is how a subdirectory is deliberately set up as
// its own project.
func TestExplicitRootStillSetsUpASubdirectoryAsItsOwnProject(t *testing.T) {
	dir := t.TempDir()
	writeFileMk(t, dir, "go.mod", "module example.com/s\n\ngo 1.21\n")
	writeFileMk(t, dir, "pkg/sub/go.mod", "module example.com/s/sub\n\ngo 1.21\n")
	writeFileMk(t, dir, "pkg/sub/a.go", "package sub\n\nfunc A() int { return 1 }\n")
	mustRun(t, dir, "scan", "--all")

	sub := filepath.Join(dir, "pkg", "sub")
	mustRun(t, sub, "scan", "-root", ".")

	if _, err := os.Stat(filepath.Join(sub, ".aracne", "topology.db")); err != nil {
		t.Errorf("an explicit -root must create the nested project it asked for: %v", err)
	}
}
