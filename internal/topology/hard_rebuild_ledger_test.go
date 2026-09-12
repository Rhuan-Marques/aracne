package topology_test

import (
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner/goscanner"
)

// DE-3: `arac scan --hard` rebuilds from scratch and drops every generated description, but
// the lazy fill's attempt ledger lives outside the graph under fingerprints the rebuild does not
// change. It survived, and every resource the fill had ever described stayed undescribed on the
// lazy path for good. A hard rebuild must take the ledger with it.
//
// `scan --all` (FullReScan) keeps descriptions, so it keeps the ledger too -- pinned alongside,
// so the fix does not overreach into the rescans that are not from scratch.
func TestHardRebuildClearsTheDescriptionAttemptLedger(t *testing.T) {
	mgr, dir := scanProject(t, map[string]string{
		"go.mod":  "module probe\n\ngo 1.25\n",
		"main.go": "package probe\n\nfunc Entry() int { return 7 }\n",
	})
	reg := scanner.NewRegistry()
	reg.Register(goscanner.NewGoScanner())
	topo, err := mgr.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	res, ok := topo.Resources["probe.Entry"]
	if !ok {
		t.Fatal("probe.Entry missing from the scan")
	}
	record := func() {
		t.Helper()
		if err := helper.RecordDescriptionAttempts(mgr.DbPath(), map[string]string{
			"probe.Entry": helper.DescriptionAttemptFingerprint(res),
		}); err != nil {
			t.Fatal(err)
		}
	}
	recorded := func() int {
		t.Helper()
		got, err := helper.ReadDescriptionAttempts(mgr.DbPath(), []string{"probe.Entry"})
		if err != nil {
			t.Fatal(err)
		}
		return len(got)
	}

	record()
	if _, err := mgr.FullReScan(dir, reg); err != nil {
		t.Fatal(err)
	}
	if recorded() != 1 {
		t.Error("scan --all dropped the attempt ledger; only a rebuild from scratch should")
	}

	if err := mgr.FullScan(dir, reg); err != nil {
		t.Fatal(err)
	}
	if n := recorded(); n != 0 {
		t.Errorf("the attempt ledger survived a hard rebuild (%d row(s)): the lazy fill will never "+
			"re-describe what the rebuild wiped", n)
	}
}
