package topology_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner/goscanner"
)

// scannedGoProject writes a Go module, full-scans it, and returns the root, db path, manager
// and registry.
func scannedGoProject(t *testing.T, files map[string]string) (string, string, *topology.TopologyManager, *scanner.Registry) {
	t.Helper()
	dir := t.TempDir()
	files["go.mod"] = "module example.com/p\n\ngo 1.21\n"
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dbPath := filepath.Join(dir, ".aracne", "topology.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		domain.SetActiveIgnore(nil)
		domain.SetActivePathVisibility(nil)
	})
	mgr := topology.New()
	if err := mgr.Load(dbPath); err != nil {
		t.Fatal(err)
	}
	reg := scanner.NewRegistry()
	reg.Register(goscanner.NewGoScanner())
	if err := mgr.FullScan(dir, reg); err != nil {
		t.Fatal(err)
	}
	return dir, dbPath, mgr, reg
}

func hasResource(t *testing.T, dbPath, id string) bool {
	t.Helper()
	topo, err := helper.ReadDb(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, ok := topo.Resources[id]
	return ok
}

// TestIncrementalScan_OlderReplacementIsReindexed pins ST-3 end to end: `mv` of an older copy
// over an indexed file put new declarations on disk under an older mtime, and neither the scan
// nor a read's freshness check noticed.
func TestIncrementalScan_OlderReplacementIsReindexed(t *testing.T) {
	dir, dbPath, mgr, reg := scannedGoProject(t, map[string]string{
		"pkg/a.go": "package pkg\n\nfunc One() int { return 1 }\n",
	})
	a := filepath.Join(dir, "pkg", "a.go")
	if got := mgr.StaleFiles([]string{a}); len(got) != 0 {
		t.Fatalf("a file just scanned is fresh, got stale %v", got)
	}

	if err := os.WriteFile(a, []byte("package pkg\n\nfunc One() int { return 1 }\n\nfunc Restored() int { return 2 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(a, old, old); err != nil {
		t.Fatal(err)
	}
	if got := mgr.StaleFiles([]string{a}); len(got) != 1 {
		t.Fatalf("a read must see a file replaced under an older mtime as stale, got %v", got)
	}

	if _, err := mgr.IncrementalScan(dir, reg); err != nil {
		t.Fatal(err)
	}
	if !hasResource(t, dbPath, "example.com/p/pkg.Restored") {
		t.Fatal("the incremental scan must re-index a file replaced under an older mtime")
	}
	if got := mgr.StaleFiles([]string{a}); len(got) != 0 {
		t.Fatalf("once re-indexed, the file is fresh again, got stale %v", got)
	}
}

// TestIncrementalScan_NowIgnoredLeavesTheGraph pins ST-4 end to end: a file a new scan.ignore
// or hidden `paths` rule covers must leave the graph on the next plain scan, as it does on
// `--hard`, and come back when the rule is lifted.
func TestIncrementalScan_NowIgnoredLeavesTheGraph(t *testing.T) {
	dir, dbPath, mgr, reg := scannedGoProject(t, map[string]string{
		"pkg/a.go": "package pkg\n\nfunc One() int { return 1 }\n",
		"gen/g.go": "package gen\n\nfunc Generated() int { return 1 }\n",
		"hid/h.go": "package hid\n\nfunc HiddenFn() int { return 1 }\n",
	})
	for _, id := range []string{"example.com/p/gen.Generated", "example.com/p/hid.HiddenFn"} {
		if !hasResource(t, dbPath, id) {
			t.Fatalf("setup: %s should be indexed", id)
		}
	}

	writeConfig := func(ignore []string, paths []domain.PathRule) {
		cfg := helper.LoadConfig(helper.ConfigPath(dbPath))
		cfg.Scan.Ignore = ignore
		cfg.Paths = paths
		if err := helper.SaveConfig(cfg, helper.ConfigPath(dbPath)); err != nil {
			t.Fatal(err)
		}
	}
	writeConfig([]string{"gen/"}, []domain.PathRule{{Path: "hid", Hidden: true}})
	if _, err := mgr.IncrementalScan(dir, reg); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"example.com/p/gen.Generated", "example.com/p/hid.HiddenFn"} {
		if hasResource(t, dbPath, id) {
			t.Errorf("%s is ignored or hidden now and must leave the graph", id)
		}
	}
	if !hasResource(t, dbPath, "example.com/p/pkg.One") {
		t.Error("a file no rule covers must stay")
	}
	health, err := mgr.IndexHealth(dir, reg)
	if err != nil {
		t.Fatal(err)
	}
	if health.Stale() {
		t.Errorf("after the scan the index matches the config, got %+v", health)
	}

	writeConfig([]string{}, []domain.PathRule{})
	if _, err := mgr.IncrementalScan(dir, reg); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"example.com/p/gen.Generated", "example.com/p/hid.HiddenFn"} {
		if !hasResource(t, dbPath, id) {
			t.Errorf("%s must come back once no rule covers it", id)
		}
	}
}

// TestLoadConfigAcceptsBOMEndToEnd is a guard for ST-8 at the level a scan sees it: a config
// saved with a UTF-8 BOM keeps its ignore rules instead of being reset to defaults.
func TestLoadConfigAcceptsBOMEndToEnd(t *testing.T) {
	dir, dbPath, mgr, reg := scannedGoProject(t, map[string]string{
		"pkg/a.go": "package pkg\n\nfunc One() int { return 1 }\n",
		"gen/g.go": "package gen\n\nfunc Generated() int { return 1 }\n",
	})
	raw, err := json.Marshal(map[string]any{"mode": "mcp", "scan": map[string]any{"ignore": []string{"gen/"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(helper.ConfigPath(dbPath), append([]byte("\xef\xbb\xbf"), raw...), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.IncrementalScan(dir, reg); err != nil {
		t.Fatal(err)
	}
	if hasResource(t, dbPath, "example.com/p/gen.Generated") {
		t.Error("the BOM-prefixed config's scan.ignore must apply")
	}
	if cfg := helper.EnsureConfig(helper.ConfigPath(dbPath)); cfg.EffectiveMode() != "mcp" {
		t.Errorf("the BOM-prefixed config must keep its mode, got %q", cfg.EffectiveMode())
	}
}
