package tests_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// OVER-SERVE.
//
// terminal.max_overserve is one ceiling over every surface that answers in something else's
// place: the intercepted shell command, the guard's proxied read, and the read and search
// tools, which stand in for the harness's own. The rule is the same everywhere -- an answer
// disproportionate to the plain one it replaces is not a cheaper answer -- and only the
// fallback differs, because only some of these surfaces have a real command to run instead.

// overserveProject is a project whose topology says far more than its source does: one line
// that literally holds the pattern, and a crowd of nodes whose stored descriptions hold it
// while their code never says it. Searching for that pattern is the shape the ceiling exists
// for -- one row the caller grepped for, buried under rows only the database could produce.
func overserveProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "go.mod"), "module demo\n\ngo 1.21\n")

	var b strings.Builder
	b.WriteString("package pkg\n\n")
	// The one line a plain grep would find.
	b.WriteString("const Marker = \"quicksilver\"\n\n")
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&b, "func Node%02d(n int) int {\n\treturn n + %d\n}\n\n", i, i)
	}
	writeFile(t, filepath.Join(dir, "pkg", "nodes.go"), b.String())
	mustRun(t, dir, "scan", "--hard", "--root", ".", "--output", ".aracne/topology.db")

	// The descriptions go into the database, not the file: a description that is also a doc
	// comment is a line grep finds by itself, and then there is nothing to measure.
	dbPath := filepath.Join(dir, ".aracne", "topology.db")
	mgr := topology.New()
	if err := mgr.Load(dbPath); err != nil {
		t.Fatal(err)
	}
	topo, err := mgr.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	// One line, inside the per-kind description budget the topology enforces.
	prose := strings.Repeat("quicksilver value ", 6)
	for id, res := range topo.Resources {
		if res.Kind != domain.ResourceFunction || !strings.Contains(res.Name, "Node") {
			continue
		}
		if err := mgr.UpdateDescription(id, res.Kind, prose); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// setMaxOverserve rewrites terminal.max_overserve in place, so everything else `arac scan`
// decided about the project survives.
func setMaxOverserve(t *testing.T, dir string, n int) {
	t.Helper()
	path := filepath.Join(dir, ".aracne", "config.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	terminal, _ := cfg["terminal"].(map[string]any)
	if terminal == nil {
		terminal = map[string]any{}
	}
	terminal["max_overserve"] = n
	cfg["terminal"] = terminal
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, string(out))
}

// `arac grep` has no real command to hand back to, so an answer over the ceiling becomes the
// same result rendered the way grep would have rendered it: the lines the file holds, and none
// of the rows only the database could produce.
func TestASearchToolFallsBackToThePlainAnswer(t *testing.T) {
	dir := overserveProject(t)

	// Off first, to show there is something to strip: this is the answer the ceiling refuses.
	setMaxOverserve(t, dir, 0)
	full := mustRun(t, dir, "grep", "quicksilver", ".")
	if !strings.Contains(full, "func Node00") {
		t.Fatalf("the topology answer carries no node rows to begin with:\n%s", full)
	}

	setMaxOverserve(t, dir, 4)
	got := mustRun(t, dir, "grep", "quicksilver", ".")
	if strings.Contains(got, "func Node00") {
		t.Errorf("a search 30x its plain answer still served its node rows:\n%s", got)
	}
	if !strings.Contains(got, "quicksilver") {
		t.Errorf("the fallback lost the line a plain grep would have found:\n%s", got)
	}
	if len(got) >= len(full) {
		t.Errorf("the fallback is not smaller: %d bytes against %d", len(got), len(full))
	}
}

// The shell surface has the fallback the tools do not -- the command the caller typed -- so it
// runs it, and the caller gets exactly what grep would have said.
func TestAnInterceptedSearchPassesThroughWhenItOutgrowsItsQuestion(t *testing.T) {
	dir := overserveProject(t)
	setMaxOverserve(t, dir, 4)

	got, _ := runAracGrep(t, dir, "-rn", "quicksilver", ".")
	if strings.Contains(got, "func Node00") || strings.Contains(got, "# ") {
		t.Errorf("an intercepted search over the ceiling still answered from the topology:\n%s", got)
	}
	want, _ := runGrep(t, dir, "-rn", "quicksilver", ".")
	if strings.TrimSpace(got) != strings.TrimSpace(want) {
		t.Errorf("passthrough did not produce the real grep's answer:\ngot:\n%s\nwant:\n%s",
			got, want)
	}
}

// And the ceiling is the project's to switch off: with it off, the same search comes back
// enriched, on the surface that would otherwise have passed it through.
func TestOverserveOffRestoresTheTopologyAnswer(t *testing.T) {
	dir := overserveProject(t)
	setMaxOverserve(t, dir, 0)

	got, _ := runAracGrep(t, dir, "-rn", "quicksilver", ".")
	if !strings.Contains(got, "func Node00") {
		t.Errorf("max_overserve 0 still withheld the topology answer:\n%s", got)
	}
}
