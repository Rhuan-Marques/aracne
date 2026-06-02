package topology_test

import (
	"encoding/json"
	"os"
	"testing"

	"llm-topology/internal/helper"
	"llm-topology/internal/topology"
	"llm-topology/internal/topology/domain"
	"llm-topology/internal/topology/golang"
	"llm-topology/internal/topology/python"
	"llm-topology/internal/topology/scanner"
	"llm-topology/internal/topology/scanner/goscanner"
	"llm-topology/internal/topology/scanner/pyscanner"
)

func newTestRegistry() *scanner.Registry {
	reg := scanner.NewRegistry()
	reg.Register(goscanner.NewGoScanner())
	return reg
}

func TestCut(t *testing.T) {
	dbPath := "../test_cut.db"
	defer os.Remove(dbPath)

	reg := newTestRegistry()
	mgr := topology.New()
	mgr.Load(dbPath)
	if err := mgr.FullScan("../..", reg); err != nil {
		t.Fatal(err)
	}

	topo, err := helper.ReadDb(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	gt := golang.FromGeneric(topo)
	for id, fn := range gt.Functions {
		entry, err := mgr.Cut(fn.Loc)
		if err != nil {
			t.Fatalf("Cut(%s): %v", id, err)
		}
		if entry.Cut == "" {
			t.Fatalf("Cut(%s): empty cut", id)
		}
		if entry.Location.Path != fn.Loc.Path {
			t.Fatalf("Cut(%s): path mismatch", id)
		}
		break
	}
}

func TestCreateAndListBugs(t *testing.T) {
	dbPath := "../test_bugs.db"
	defer os.Remove(dbPath)

	mgr := topology.New()
	mgr.Load(dbPath)

	bug1, err := mgr.CreateBug("node-1", "nil dereference in function X")
	if err != nil {
		t.Fatalf("CreateBug: %v", err)
	}
	if bug1.State != domain.BugPending {
		t.Fatalf("expected pending state, got %s", bug1.State)
	}

	bug2, err := mgr.CreateBug("node-1", "missing error check in function Y")
	if err != nil {
		t.Fatalf("CreateBug: %v", err)
	}

	bug3, err := mgr.CreateBug("node-2", "race condition in struct Z")
	if err != nil {
		t.Fatalf("CreateBug: %v", err)
	}

	all, err := mgr.ListBugs("", "")
	if err != nil {
		t.Fatalf("ListBugs: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 bugs, got %d", len(all))
	}

	node1Bugs, err := mgr.ListBugs("node-1", "")
	if err != nil {
		t.Fatalf("ListBugs: %v", err)
	}
	if len(node1Bugs) != 2 {
		t.Fatalf("expected 2 bugs for node-1, got %d", len(node1Bugs))
	}

	pending, err := mgr.ListBugs("", domain.BugPending)
	if err != nil {
		t.Fatalf("ListBugs: %v", err)
	}
	if len(pending) != 3 {
		t.Fatalf("expected 3 pending bugs, got %d", len(pending))
	}

	_ = bug2
	_ = bug3
}

func TestBugStateTransitions(t *testing.T) {
	dbPath := "../test_bug_states.db"
	defer os.Remove(dbPath)

	mgr := topology.New()
	mgr.Load(dbPath)

	bug, err := mgr.CreateBug("node-1", "test bug")
	if err != nil {
		t.Fatalf("CreateBug: %v", err)
	}

	if err := mgr.AcknowledgeBug(bug.ID); err != nil {
		t.Fatalf("AcknowledgeBug: %v", err)
	}

	ack, err := mgr.ListBugs("", domain.BugAcknowledged)
	if err != nil {
		t.Fatalf("ListBugs: %v", err)
	}
	if len(ack) != 1 || ack[0].ID != bug.ID {
		t.Fatalf("expected acknowledged bug %s", bug.ID)
	}

	if err := mgr.DismissBug(bug.ID); err != nil {
		t.Fatalf("DismissBug: %v", err)
	}

	dismissed, err := mgr.ListBugs("", domain.BugDismissed)
	if err != nil {
		t.Fatalf("ListBugs: %v", err)
	}
	if len(dismissed) != 1 {
		t.Fatalf("expected 1 dismissed bug, got %d", len(dismissed))
	}
}

func TestDeleteBug(t *testing.T) {
	dbPath := "../test_bug_delete.db"
	defer os.Remove(dbPath)

	mgr := topology.New()
	mgr.Load(dbPath)

	bug, err := mgr.CreateBug("node-1", "bug to delete")
	if err != nil {
		t.Fatalf("CreateBug: %v", err)
	}

	if err := mgr.DeleteBug(bug.ID); err != nil {
		t.Fatalf("DeleteBug: %v", err)
	}

	all, err := mgr.ListBugs("", "")
	if err != nil {
		t.Fatalf("ListBugs: %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("expected 0 bugs after delete, got %d", len(all))
	}
}

func TestDeleteAllBugs(t *testing.T) {
	dbPath := "../test_bug_deleteall.db"
	defer os.Remove(dbPath)

	mgr := topology.New()
	mgr.Load(dbPath)

	for i := 0; i < 5; i++ {
		if _, err := mgr.CreateBug("node-1", "bug"); err != nil {
			t.Fatalf("CreateBug: %v", err)
		}
	}

	if err := mgr.DeleteAllBugs(); err != nil {
		t.Fatalf("DeleteAllBugs: %v", err)
	}

	all, err := mgr.ListBugs("", "")
	if err != nil {
		t.Fatalf("ListBugs: %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("expected 0 bugs after delete all, got %d", len(all))
	}
}

func TestBugSurvivesScan(t *testing.T) {
	dbPath := "../test_bug_scan_survival.db"
	defer os.Remove(dbPath)

	reg := newTestRegistry()
	mgr := topology.New()
	mgr.Load(dbPath)

	if err := mgr.FullScan("../..", reg); err != nil {
		t.Fatal(err)
	}

	topo, err := helper.ReadDb(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	var firstID string
	for id := range topo.Resources {
		firstID = id
		break
	}

	bug, err := mgr.CreateBug(firstID, "test bug survival")
	if err != nil {
		t.Fatalf("CreateBug: %v", err)
	}

	if err := mgr.FullScan("../..", reg); err != nil {
		t.Fatal(err)
	}

	all, err := mgr.ListBugs("", "")
	if err != nil {
		t.Fatalf("ListBugs: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("expected bug to survive scan, got %d bugs", len(all))
	}
	if all[0].ID != bug.ID {
		t.Fatalf("expected bug %s to survive, got %s", bug.ID, all[0].ID)
	}

	if err := mgr.DeleteAllBugs(); err != nil {
		t.Fatalf("DeleteAllBugs: %v", err)
	}
	if err := mgr.FullScan("../..", reg); err != nil {
		t.Fatal(err)
	}
	after, err := mgr.ListBugs("", "")
	if err != nil {
		t.Fatalf("ListBugs: %v", err)
	}
	if len(after) != 0 {
		t.Fatalf("expected bugs to be gone after explicit delete, got %d", len(after))
	}
}

func newPythonTestRegistry() *scanner.Registry {
	reg := scanner.NewRegistry()
	reg.Register(pyscanner.NewPythonScanner())
	return reg
}

func TestPyCut(t *testing.T) {
	dbPath := "../test_py_cut.db"
	defer os.Remove(dbPath)

	reg := newPythonTestRegistry()
	mgr := topology.New()
	mgr.Load(dbPath)
	if err := mgr.FullScan("py_testdata", reg); err != nil {
		t.Fatal(err)
	}

	topo, err := helper.ReadDb(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	gt := python.FromGeneric(topo)
	for id, fn := range gt.Functions {
		entry, err := mgr.Cut(fn.Loc)
		if err != nil {
			t.Fatalf("Cut(%s): %v", id, err)
		}
		if entry.Cut == "" {
			t.Fatalf("Cut(%s): empty cut", id)
		}
		if entry.Location.Path != fn.Loc.Path {
			t.Fatalf("Cut(%s): path mismatch", id)
		}
		break
	}
}

func TestPyReadFunction(t *testing.T) {
	dbPath := "../test_py_readfn.db"
	defer os.Remove(dbPath)

	reg := newPythonTestRegistry()
	mgr := topology.New()
	mgr.Load(dbPath)
	if err := mgr.FullScan("py_testdata", reg); err != nil {
		t.Fatal(err)
	}

	gm := python.NewPythonManager(mgr)
	topo, err := helper.ReadDb(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	gt := python.FromGeneric(topo)
	for id := range gt.Functions {
		ctx, err := gm.ReadFunction(string(id))
		if err != nil {
			t.Fatalf("ReadFunction(%s): %v", id, err)
		}

		if ctx.Function == nil {
			t.Fatal("expected non-nil Function")
		}
		if ctx.Function.Cut == "" {
			t.Fatal("expected non-empty function cut")
		}
		if len(ctx.Blocks) == 0 {
			t.Fatal("expected at least 1 block (the function)")
		}

		for _, b := range ctx.Blocks {
			if b.FileID == "" {
				t.Errorf("block %q has empty FileID", b.Title)
			}
			if b.Line < 1 {
				t.Errorf("block %q has invalid Line %d", b.Title, b.Line)
			}
		}

		b, _ := json.MarshalIndent(ctx, "", "  ")
		t.Logf("ReadFunction output for %s:\n%s", id, string(b))

		break
	}
}

func TestPyReadClass(t *testing.T) {
	dbPath := "../test_py_readclass.db"
	defer os.Remove(dbPath)

	reg := newPythonTestRegistry()
	mgr := topology.New()
	mgr.Load(dbPath)
	if err := mgr.FullScan("py_testdata", reg); err != nil {
		t.Fatal(err)
	}

	gm := python.NewPythonManager(mgr)
	topo, err := helper.ReadDb(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	gt := python.FromGeneric(topo)
	for id := range gt.Classes {
		ctx, err := gm.ReadClass(string(id))
		if err != nil {
			t.Fatalf("ReadClass(%s): %v", id, err)
		}

		if ctx.Class == nil {
			t.Fatal("expected non-nil Class")
		}
		if ctx.Class.Cut == "" {
			t.Fatal("expected non-empty class cut")
		}
		if len(ctx.Blocks) == 0 {
			t.Fatal("expected at least 1 block (the class)")
		}

		for _, b := range ctx.Blocks {
			if b.FileID == "" {
				t.Errorf("block %q has empty FileID", b.Title)
			}
			if b.Line < 1 {
				t.Errorf("block %q has invalid Line %d", b.Title, b.Line)
			}
		}

		if len(ctx.Methods) > 0 {
			t.Logf("Class %s has %d methods", id, len(ctx.Methods))
		}
		if ctx.Constructor != nil {
			t.Logf("Class %s has constructor: %s", id, ctx.Constructor.Name)
		}
		if len(ctx.BaseClasses) > 0 {
			t.Logf("Class %s has %d base classes", id, len(ctx.BaseClasses))
			for _, bc := range ctx.BaseClasses {
				t.Logf("  base: %s (need_impl=%v)", bc.Name, bc.NeedToImplement)
			}
		}

		b, _ := json.MarshalIndent(ctx, "", "  ")
		t.Logf("ReadClass output for %s:\n%s", id, string(b))

		break
	}
}

func TestReadFunction(t *testing.T) {
	dbPath := "../test_readfn.db"
	defer os.Remove(dbPath)

	reg := newTestRegistry()
	mgr := topology.New()
	mgr.Load(dbPath)
	if err := mgr.FullScan("../..", reg); err != nil {
		t.Fatal(err)
	}

	gm := golang.NewGoManager(mgr)
	topo, err := helper.ReadDb(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	gt := golang.FromGeneric(topo)
	for id := range gt.Functions {
		ctx, err := gm.ReadFunction(string(id))
		if err != nil {
			t.Fatalf("ReadFunction(%s): %v", id, err)
		}

		if ctx.Function == nil {
			t.Fatal("expected non-nil Function")
		}
		if ctx.Function.Cut == "" {
			t.Fatal("expected non-empty function cut")
		}
		if len(ctx.Blocks) == 0 {
			t.Fatal("expected at least 1 block (the function)")
		}
		if ctx.Blocks[0].Kind != "function" && ctx.Blocks[0].Kind != "parent_struct" {
			t.Logf("first block kind: %s (may appear later due to file-line sort)", ctx.Blocks[0].Kind)
		}

		for _, b := range ctx.Blocks {
			if b.FileID == "" {
				t.Errorf("block %q has empty FileID", b.Title)
			}
			if b.Line < 1 {
				t.Errorf("block %q has invalid Line %d", b.Title, b.Line)
			}
		}

		b, _ := json.MarshalIndent(ctx, "", "  ")
		t.Logf("ReadFunction output for %s:\n%s", id, string(b))

		break
	}
}

func TestReadFunctionCalledFuncs(t *testing.T) {
	dbPath := "../test_readfn2.db"
	defer os.Remove(dbPath)

	reg := newTestRegistry()
	mgr := topology.New()
	mgr.Load(dbPath)
	if err := mgr.FullScan("../..", reg); err != nil {
		t.Fatal(err)
	}

	gm := golang.NewGoManager(mgr)
	topo, err := helper.ReadDb(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	gt := golang.FromGeneric(topo)
	for id, fn := range gt.Functions {
		if len(fn.Calls()) == 0 {
			continue
		}
		ctx, err := gm.ReadFunction(string(id))
		if err != nil {
			t.Fatal(err)
		}
		if len(ctx.CalledFunctions) > 0 {
			t.Logf("Found %d called functions for %s", len(ctx.CalledFunctions), id)
			for _, cf := range ctx.CalledFunctions {
				t.Logf("  calls: %s", cf.Name)
			}
		}
		if len(ctx.StructsUsed) > 0 {
			t.Logf("Found %d structs used by %s", len(ctx.StructsUsed), id)
			for _, su := range ctx.StructsUsed {
				t.Logf("  struct: %s (%d methods)", su.Name, len(su.Methods))
			}
		}
		if len(ctx.ExtVarsUsed) > 0 {
			t.Logf("Found %d ext vars used by %s", len(ctx.ExtVarsUsed), id)
			for _, ev := range ctx.ExtVarsUsed {
				t.Logf("  var: %s", ev.Name)
			}
		}
		break
	}
}

func TestReadStruct(t *testing.T) {
	dbPath := "../test_readstruct.db"
	defer os.Remove(dbPath)

	reg := newTestRegistry()
	mgr := topology.New()
	mgr.Load(dbPath)
	if err := mgr.FullScan("../..", reg); err != nil {
		t.Fatal(err)
	}

	gm := golang.NewGoManager(mgr)
	topo, err := helper.ReadDb(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	gt := golang.FromGeneric(topo)
	for id := range gt.Structs {
		ctx, err := gm.ReadStruct(string(id))
		if err != nil {
			t.Fatalf("ReadStruct(%s): %v", id, err)
		}

		if ctx.Struct == nil {
			t.Fatal("expected non-nil Struct")
		}
		if ctx.Struct.Cut == "" {
			t.Fatal("expected non-empty struct cut")
		}
		if len(ctx.Blocks) == 0 {
			t.Fatal("expected at least 1 block (the struct)")
		}

		for _, b := range ctx.Blocks {
			if b.FileID == "" {
				t.Errorf("block %q has empty FileID", b.Title)
			}
			if b.Line < 1 {
				t.Errorf("block %q has invalid Line %d", b.Title, b.Line)
			}
		}

		if len(ctx.Methods) > 0 {
			t.Logf("Struct %s has %d methods", id, len(ctx.Methods))
		}
		if ctx.Constructor != nil {
			t.Logf("Struct %s has constructor: %s", id, ctx.Constructor.Name)
		}
		if len(ctx.Interfaces) > 0 {
			t.Logf("Struct %s implements %d interfaces", id, len(ctx.Interfaces))
		}

		b, _ := json.MarshalIndent(ctx, "", "  ")
		t.Logf("ReadStruct output for %s:\n%s", id, string(b))

		break
	}
}
