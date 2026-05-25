package mermaid

import (
	"os"
	"strings"
	"testing"

	"llm-topology/internal/helper"
	"llm-topology/internal/topology/domain"
)

func TestGenerate(t *testing.T) {
	topoPath := "../../.ltp/topology.db"
	if _, err := os.Stat(topoPath); os.IsNotExist(err) {
		t.Skip("test database not found at", topoPath)
	}

	topo, err := helper.ReadDb(topoPath)
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
	topoPath := "../../.ltp/topology.db"
	if _, err := os.Stat(topoPath); os.IsNotExist(err) {
		t.Skip("test database not found at", topoPath)
	}

	topo, err := helper.ReadDb(topoPath)
	if err != nil {
		t.Fatal(err)
	}

	out := Generate(topo, domain.FUNCTION_RESOURCE, domain.STRUCT_RESOURCE, domain.INTERFACE_RESOURCE)
	if out == "" {
		t.Fatal("expected non-empty output")
	}

	if strings.Contains(out, "classDef Package") {
		t.Log("Package classDef not present (filtered out)")
	}
	if !strings.Contains(out, "classDef Function") {
		t.Fatal("expected Function classDef")
	}
	if !strings.Contains(out, "classDef Struct") {
		t.Fatal("expected Struct classDef")
	}
	if !strings.Contains(out, "classDef Interface") {
		t.Fatal("expected Interface classDef")
	}
}
