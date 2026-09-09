package universaltools_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/llm/languages/universaltools"
	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner/goscanner"
)

func scanFixture(t *testing.T) (*topology.TopologyManager, string) {
	t.Helper()
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module probe\n\ngo 1.25\n")
	write("dep.go", "package probe\n\n// Helper does a helpful thing.\nfunc Helper() int { return 7 }\n")
	write("main.go", "package probe\n\nfunc Entry() int { return Helper() }\n")
	if err := os.MkdirAll(filepath.Join(dir, ".aracne"), 0o755); err != nil {
		t.Fatal(err)
	}
	mgr := topology.New()
	if err := mgr.Load(filepath.Join(dir, ".aracne", "topology.db")); err != nil {
		t.Fatal(err)
	}
	reg := scanner.NewRegistry()
	reg.Register(goscanner.NewGoScanner())
	if err := mgr.FullScan(dir, reg); err != nil {
		t.Fatal(err)
	}
	return mgr, dir
}

// TestContextFilterOffAppliesToAWholeFileRead pins AR-05.
//
// ReadIDs resolved read.context_filter and threaded it into the per-language managers, which
// honour it -- but fileUnit and genericUnit built their context through neighborContext, which
// took no filter at all. So the project-wide dial had no effect on the two shapes a
// terminal-surface project sees most: an intercepted `cat file.go`, and any variable or
// unsupported-language resource.
func TestContextFilterOffAppliesToAWholeFileRead(t *testing.T) {
	mgr, dir := scanFixture(t)
	cfg := helper.DefaultConfig()
	cfg.Read.ContextFilter = helper.ContextFilterOff
	cfg.Read.FileMode = helper.FileModeFull

	out, err := universaltools.NewRead(mgr, cfg, false, nil).
		ReadIDs([]string{filepath.Join(dir, "main.go")}, universaltools.ReadIDsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "# CONTEXT:") {
		t.Fatalf("context_filter \"off\" must suppress a whole-file read's context block:\n%s", out)
	}
	if !strings.Contains(out, "func Entry()") {
		t.Fatalf("the file's own source must still be returned:\n%s", out)
	}
}

// TestContextFilterNormalStillRendersAWholeFileContext is the other half: the filter must
// narrow the block, not delete the feature.
func TestContextFilterNormalStillRendersAWholeFileContext(t *testing.T) {
	mgr, dir := scanFixture(t)
	cfg := helper.DefaultConfig()
	cfg.Read.FileMode = helper.FileModeFull

	out, err := universaltools.NewRead(mgr, cfg, false, nil).
		ReadIDs([]string{filepath.Join(dir, "main.go")}, universaltools.ReadIDsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "# CONTEXT:") || !strings.Contains(out, "probe.Helper") {
		t.Fatalf("the default filter must still list a whole-file read's neighbours:\n%s", out)
	}
}

// TestGoReadHasNoSelfImport pins the rendered half of AR-06: the fabricated
// `import ("<own package>")` that opened a read of almost any function in a multi-file
// package. It is a compile error in Go and it sits inside the fence a read promises is
// verbatim source, which is exactly the text a model would paste into an `edit` old_string.
func TestGoReadHasNoSelfImport(t *testing.T) {
	mgr, _ := scanFixture(t)
	out, err := universaltools.NewRead(mgr, helper.DefaultConfig(), false, nil).
		ReadIDs([]string{"probe.Entry"}, universaltools.ReadIDsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, `"probe"`) {
		t.Fatalf("a read must not print a self-import:\n%s", out)
	}
}
