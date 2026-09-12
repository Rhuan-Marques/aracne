package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// SET-01. OpenCode hands a plugin `worktree` as the GIT worktree: "/" for a project that is
// not a repository, and the repo root for a project nested inside one. Neither is where
// .aracne lives, and running `arac` from there scans the whole filesystem (or writes a second
// topology at the repo root). These tests pin that both generated plugins run arac in the
// instance directory OpenCode actually opened.

// recordedAracPattern matches the generated `const RECORDED_ARAC = "..."` line, so a test can
// point the plugin at a fake binary that records where it was run.
var recordedAracPattern = regexp.MustCompile(`const RECORDED_ARAC = .*`)

// fakeAracScript writes a shell script that appends its working directory to logPath.
func fakeAracScript(t *testing.T, dir, logPath string) string {
	t.Helper()
	script := filepath.Join(dir, "fake-arac.sh")
	body := "#!/bin/sh\npwd >> " + strconv.Quote(logPath) + "\nexit 0\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake arac: %v", err)
	}
	return script
}

// runPluginHook writes the plugin (pointed at a fake arac) plus an ESM harness that calls one
// of its hooks, runs both under node, and returns the working directories the fake recorded.
func runPluginHook(t *testing.T, pluginSrc, harnessBody string) []string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH")
	}
	work := t.TempDir()
	project := filepath.Join(work, "project")
	if err := os.MkdirAll(filepath.Join(project, ".aracne"), 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(project, "src"), 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, "src", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	logPath := filepath.Join(work, "cwd.log")
	fake := fakeAracScript(t, work, logPath)

	src := recordedAracPattern.ReplaceAllString(pluginSrc, "const RECORDED_ARAC = "+strconv.Quote(fake))
	if err := os.WriteFile(filepath.Join(work, "plugin.mjs"), []byte(src), 0o644); err != nil {
		t.Fatalf("write plugin: %v", err)
	}
	if err := os.WriteFile(filepath.Join(work, "harness.mjs"), []byte(harnessBody), 0o644); err != nil {
		t.Fatalf("write harness: %v", err)
	}

	// A neutral cwd, so a plugin that fell back to process.cwd() would not accidentally pass.
	neutral := filepath.Join(work, "neutral")
	if err := os.MkdirAll(neutral, 0o755); err != nil {
		t.Fatalf("mkdir neutral: %v", err)
	}
	cmd := exec.Command(node, filepath.Join(work, "harness.mjs"))
	cmd.Dir = neutral
	cmd.Env = append(os.Environ(), "PROJECT="+project)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("node harness failed: %v\n%s", err, out)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("fake arac was never run (no log): %v", err)
	}
	var dirs []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line != "" {
			dirs = append(dirs, line)
		}
	}
	if len(dirs) == 0 {
		t.Fatal("fake arac recorded no working directory")
	}
	return dirs
}

func TestSET01_EditSyncRunsAracInTheProjectNotTheGitWorktree(t *testing.T) {
	harness := strings.Join([]string{
		`import { AracNativeEditSync } from "./plugin.mjs"`,
		`const project = process.env.PROJECT`,
		`const hooks = await AracNativeEditSync({ directory: project, worktree: "/" })`,
		`await hooks["tool.execute.after"](`,
		`  { tool: "edit", args: { filePath: project + "/src/main.go" } },`,
		`  { output: "" },`,
		`)`,
		``,
	}, "\n")
	dirs := runPluginHook(t, openCodeNativeEditPlugin(), harness)
	for _, dir := range dirs {
		if dir == "/" {
			t.Fatalf("edit-sync ran arac from the git worktree %q; on a non-git project that scans the whole filesystem", dir)
		}
		if !strings.HasSuffix(dir, "/project") {
			t.Fatalf("edit-sync ran arac in %q, want the project directory", dir)
		}
	}
}

func TestSET01_PreToolScanRunsAracInTheProjectNotTheGitWorktree(t *testing.T) {
	harness := strings.Join([]string{
		`import { AracPreToolScan } from "./plugin.mjs"`,
		`const project = process.env.PROJECT`,
		`const hooks = await AracPreToolScan({ directory: project, worktree: "/" })`,
		`await hooks["tool.execute.before"]({ tool: "read", callID: "c1" }, { args: {} })`,
		``,
	}, "\n")
	dirs := runPluginHook(t, openCodePreToolScanPlugin(), harness)
	for _, dir := range dirs {
		if dir == "/" {
			t.Fatalf("pre-tool scan ran arac from the git worktree %q, where there is no topology", dir)
		}
		if !strings.HasSuffix(dir, "/project") {
			t.Fatalf("pre-tool scan ran arac in %q, want the project directory", dir)
		}
	}
}

// The edit sync is synchronous, so an arac that never returns would block OpenCode's event
// loop for every later call. It must be bounded like the pre-tool scan's execFile is.
func TestSET01_EditSyncBoundsTheSynchronousCall(t *testing.T) {
	plugin := openCodeNativeEditPlugin()
	if !strings.Contains(plugin, "timeout:") {
		t.Fatalf("edit-sync plugin runs execFileSync with no timeout:\n%s", plugin)
	}
}
