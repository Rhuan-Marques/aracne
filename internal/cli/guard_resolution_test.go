package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
)

// The guard used to open the RELATIVE path ".aracne/topology.db", which the hook resolves
// against a working directory the MODEL controls -- the Bash tool's cwd persists between
// calls. One `cd packages/mui-joy/src/Select` and the config load failed, the guard fell
// through to its fail-open branch, and every later read ran unguarded. That is not a
// hypothesis: it is how one benchmark cell completed a whole task with 27 Bash calls, zero
// aracne calls and two denials that only landed after a command happened to `cd` back.
func TestGuardFindsItsDatabaseFromASubdirectory(t *testing.T) {
	root := t.TempDir()
	db := filepath.Join(root, guardDBRelative)
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(db, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(root, "packages", "mui-joy", "src", "Select")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	// The hook event's own cwd is the session directory and always wins.
	if got := guardDBPath(root); got != db {
		t.Errorf("guardDBPath(sessionCwd) = %q, want %q", got, db)
	}

	// With no event cwd, the upward walk from the process directory has to find it -- this is
	// the case the agent's `cd` created.
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
	if err := os.Chdir(deep); err != nil {
		t.Fatal(err)
	}
	got := guardDBPath("")
	// The temp dir may be reached through a symlink (/tmp -> /private/tmp), so compare the
	// resolved paths rather than the spellings.
	gotAbs, _ := filepath.EvalSymlinks(got)
	wantAbs, _ := filepath.EvalSymlinks(db)
	if gotAbs != wantAbs {
		t.Errorf("guardDBPath from a subdirectory = %q (%q), want %q", got, gotAbs, wantAbs)
	}
}

// CLAUDE_PROJECT_DIR is the documented hook environment variable, and is the fallback when a
// harness sends no cwd on the event.
func TestGuardFallsBackToProjectDirEnv(t *testing.T) {
	root := t.TempDir()
	db := filepath.Join(root, guardDBRelative)
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(db, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", root)
	if got := guardDBPath(""); got != db {
		t.Errorf("guardDBPath = %q, want %q", got, db)
	}
}

// A project with no database at all must still yield a path, so every caller keeps its
// existing fail-open behaviour instead of panicking on an empty string.
func TestGuardDBPathAlwaysReturnsSomething(t *testing.T) {
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	dir := t.TempDir()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if got := guardDBPath(""); got == "" {
		t.Error("guardDBPath returned an empty path")
	}
}

// The proxy answered every question with the whole file because soleReadTarget threw the line
// window away. Measured over one benchmark run that served 281,925 bytes for ~48,800 asked
// for, worst case a 40-line `awk` window returning an entire 59KB test file.
func TestProxyExtractsTheLineWindow(t *testing.T) {
	// A real file, because the window is now resolved against its length -- which is what
	// lets `tail` be expressed at all, and what clamps a request past the end.
	dir := t.TempDir()
	name := filepath.Join(dir, "args.rs")
	lines := make([]string, 1000)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i+1)
	}
	if err := os.WriteFile(name, []byte(strings.Join(lines, "\n")+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name           string
		cmd            string
		wantFrom, want int
		wantOK         bool
	}{
		{"sed range", `sed -n 660,760p ` + name, 660, 760, true},
		{"sed quoted range", `sed -n '110,300p' ` + name, 110, 300, true},
		{"sed single line", `sed -n 42p ` + name, 42, 42, true},
		{"awk range", `awk 'NR>=955 && NR<=995' ` + name, 955, 995, true},
		{"awk tight range", `awk 'NR>=282&&NR<=304' ` + name, 282, 304, true},
		{"head -n", `head -n 40 ` + name, 1, 40, true},
		{"head -N", `head -20 ` + name, 1, 20, true},
		// tail was missing from the proxy's own parser, so a denied `tail -n 20 f`
		// reported no window and the proxy answered it with the whole file.
		{"tail -n", `tail -n 20 ` + name, 981, 1000, true},
		{"tail -N", `tail -5 ` + name, 996, 1000, true},
		{"tail from line", `tail -n +990 ` + name, 990, 1000, true},
		{"cat has no window", `cat ` + name, 0, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := soleReadTarget(tc.cmd, dir)
			if got.hasWindow != tc.wantOK ||
				(got.hasWindow && (got.from != tc.wantFrom || got.to != tc.want)) {
				t.Errorf("soleReadTarget(%q) = %d-%d hasWindow=%v, want %d-%d hasWindow=%v",
					tc.cmd, got.from, got.to, got.hasWindow, tc.wantFrom, tc.want, tc.wantOK)
			}
			if got.path != name {
				t.Errorf("soleReadTarget(%q) path = %q, want %q", tc.cmd, got.path, name)
			}
		})
	}
}

// The budget is what stops the proxy turning a 22-line request into a 20KB answer. It is
// proportional so an honest whole-file read still passes, floored so a small window still gets
// its enclosing function plus context, and capped absolutely.
func TestProxyBudgetIsProportionalFlooredAndCapped(t *testing.T) {
	cfg := helper.DefaultConfig()

	// A 20-line window of a 1000-line, 40KB file asks for ~800 bytes; the floor governs.
	small := readTarget{hasWindow: true, from: 100, to: 119, totalLines: 1000}
	if got := proxyBudget(cfg, small, 40000); got != helper.OverserveReadFree {
		t.Errorf("small window budget = %d, want the floor %d", got, helper.OverserveReadFree)
	}
	// Half of a 60KB file asks for ~30KB; 4x that is over the absolute cap.
	half := readTarget{hasWindow: true, from: 1, to: 500, totalLines: 1000}
	if got := proxyBudget(cfg, half, 60000); got != helper.OverserveMaxBytes {
		t.Errorf("half-file budget = %d, want the cap %d", got, helper.OverserveMaxBytes)
	}
	// The 59KB whole-test-file answer that motivated all of this must not fit the budget its
	// own 40-line request earned.
	worst := readTarget{hasWindow: true, from: 955, to: 995, totalLines: 1500}
	if b := proxyBudget(cfg, worst, 59033); b >= 59033 {
		t.Errorf("a 40-line window still budgets %d bytes, enough for the whole 59KB file", b)
	}
	// terminal.max_overserve is the project's dial, and the proxy turns on it like every
	// other surface: zero is no ceiling at all.
	off := helper.DefaultConfig()
	off.Terminal.MaxOverserve = new(int)
	if b := proxyBudget(off, worst, 59033); b >= 0 {
		t.Errorf("max_overserve 0 still budgeted %d bytes", b)
	}
}
