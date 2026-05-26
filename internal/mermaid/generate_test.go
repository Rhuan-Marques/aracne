package mermaid

import (
	"os"
	"strings"
	"testing"

	"llm-topology/internal/helper"
	"llm-topology/internal/topology"
	"llm-topology/internal/topology/domain"
	"llm-topology/internal/topology/scanner"
	"llm-topology/internal/topology/scanner/goscanner"
)

func buildTestDb(t *testing.T, dbPath string) {
	t.Helper()
	reg := scanner.NewRegistry()
	reg.Register(goscanner.NewGoScanner())
	mgr := topology.New()
	mgr.Load(dbPath)
	if err := mgr.FullScan("../../", reg); err != nil {
		t.Fatalf("scan failed: %v", err)
	}
}

func TestGenerate(t *testing.T) {
	dbPath := "../../.ltp/mermaid_test.db"
	os.MkdirAll("../../.ltp", 0755)
	defer os.Remove(dbPath)
	buildTestDb(t, dbPath)

	topo, err := helper.ReadDb(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	out := Generate(topo)
	if out == "" {
		t.Fatal("expected non-empty output")
	}

	if !strings.HasPrefix(out, "flowchart TD\n") {
		t.Fatalf("expected to start with 'flowchart TD', got: %q", out[:15])
	}
	if !strings.Contains(out, "classDef") {
		t.Fatal("expected classDef declarations")
	}
	if !strings.Contains(out, "subgraph") {
		t.Fatal("expected subgraph declarations")
	}
	if !strings.Contains(out, "-->") {
		t.Fatal("expected arrow relationships")
	}
}

func TestGenerateFiltered(t *testing.T) {
	dbPath := "../../.ltp/mermaid_filter_test.db"
	os.MkdirAll("../../.ltp", 0755)
	defer os.Remove(dbPath)
	buildTestDb(t, dbPath)

	topo, err := helper.ReadDb(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	out := Generate(topo, domain.ResourceFunction, domain.ResourceType, domain.ResourceInterface)
	if out == "" {
		t.Fatal("expected non-empty output")
	}

	if strings.Contains(out, "classDef package") {
		t.Log("Package classDef not present (filtered out)")
	}
	if !strings.Contains(out, "classDef function") && !strings.Contains(out, "classDef method") {
		t.Fatal("expected Function classDef")
	}
	if !strings.Contains(out, "classDef type") {
		t.Fatal("expected Type classDef")
	}
	if !strings.Contains(out, "classDef interface") {
		t.Fatal("expected Interface classDef")
	}
}
