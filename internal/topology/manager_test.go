package topology

import (
	"encoding/json"
	"os"
	"testing"

	"llm-topology/internal/helper"
)

func TestCut(t *testing.T) {
	dbPath := "../test_cut.db"
	defer os.Remove(dbPath)

	mgr := New()
	mgr.Load(dbPath)
	if err := mgr.FullScan("../.."); err != nil {
		t.Fatal(err)
	}

	topo, err := helper.ReadDb(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	for id, fn := range topo.Functions {
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
		break // just test one
	}
}

func TestReadFunction(t *testing.T) {
	dbPath := "../test_readfn.db"
	defer os.Remove(dbPath)

	mgr := New()
	mgr.Load(dbPath)
	if err := mgr.FullScan("../.."); err != nil {
		t.Fatal(err)
	}

	topo, err := helper.ReadDb(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	for id := range topo.Functions {
		ctx, err := mgr.ReadFunction(string(id))
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

	mgr := New()
	mgr.Load(dbPath)
	if err := mgr.FullScan("../.."); err != nil {
		t.Fatal(err)
	}

	topo, err := helper.ReadDb(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	for id, fn := range topo.Functions {
		if len(fn.FunctionsUsed) == 0 {
			continue
		}
		ctx, err := mgr.ReadFunction(string(id))
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

	mgr := New()
	mgr.Load(dbPath)
	if err := mgr.FullScan("../.."); err != nil {
		t.Fatal(err)
	}

	topo, err := helper.ReadDb(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	for id := range topo.Struct {
		ctx, err := mgr.ReadStruct(string(id))
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
