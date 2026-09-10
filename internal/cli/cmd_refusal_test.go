package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology"
)

// interceptIDProject is scannedProject on ModeInterceptID, with the working directory and the
// project env pointing at it so `arac cmd` resolves the same database the test built.
func interceptIDProject(t *testing.T, extra map[string]string) (root, dbPath string) {
	t.Helper()
	root, dbPath = scannedProject(t)
	for rel, body := range extra {
		if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := helper.DefaultConfig()
	cfg.Mode = helper.ModeInterceptID
	if err := helper.SaveConfig(cfg, helper.ConfigPath(dbPath)); err != nil {
		t.Fatal(err)
	}
	if len(extra) > 0 {
		mgr := topology.New()
		if err := mgr.Load(dbPath); err != nil {
			t.Fatal(err)
		}
		if err := mgr.FullScan(root, NewScannerRegistry()); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(root)
	t.Setenv("CLAUDE_PROJECT_DIR", root)
	return root, dbPath
}

// An over-budget read of a RESOURCE ID used to fall through to the real `cat`, which reported
// "No such file or directory" about an id that resolves. It is refused now, with a read that fits;
// a PATH over budget still runs as the real command, which is a correct answer for a path.
func TestOverBudgetResourceIDIsRefusedNotPassedThrough(t *testing.T) {
	var b strings.Builder
	b.WriteString("package main\n\nfunc Wide() {\n")
	for i := 0; i < 120; i++ {
		b.WriteString("\t// " + strings.Repeat("payload ", 60) + "\n")
	}
	b.WriteString("}\n")
	interceptIDProject(t, map[string]string{"wide.go": b.String()})

	_, _, refusal, ok := serveCommand([]string{"cat", "example.com/proj.Wide"}, false)
	if ok || refusal == nil {
		t.Fatalf("an over-budget id must be refused, got ok=%v refusal=%v", ok, refusal)
	}
	if msg := refusal.Error(); !strings.Contains(msg, "sed -n '") || !strings.Contains(msg, "wide.go") {
		t.Errorf("the refusal must name a read that fits: %s", msg)
	}

	if _, _, refusal, ok := serveCommand([]string{"sed", "-n", "1,130p", "wide.go"}, false); ok || refusal != nil {
		t.Errorf("an over-budget PATH read must pass through to the real command, got ok=%v refusal=%v", ok, refusal)
	}
}

// The intercept_id contract says a miss "returns the nearest candidates rather than an error".
// A near-miss id used to reach the shell and come back as "No such file or directory".
func TestNearMissIDIsAnsweredWithCandidates(t *testing.T) {
	interceptIDProject(t, nil)

	_, _, refusal, ok := serveCommand([]string{"cat", "example.com/proj.Servee"}, false)
	if ok || refusal == nil || !strings.Contains(refusal.Error(), "example.com/proj.Serve") {
		t.Fatalf("a near-miss id must be answered with its candidates, got ok=%v refusal=%v", ok, refusal)
	}

	// Nothing resembling it, or a mistyped FILE, is still the real command's to report.
	for _, operand := range []string{"zqxjkv", "missing.go", "docs/NOTES.md"} {
		if _, _, refusal, ok := serveCommand([]string{"cat", operand}, false); ok || refusal != nil {
			t.Errorf("%s: want a passthrough, got ok=%v refusal=%v", operand, ok, refusal)
		}
	}
}
