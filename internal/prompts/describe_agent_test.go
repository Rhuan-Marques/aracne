package prompts

import (
	"strings"
	"testing"

	"aracne/internal/topology/domain"
)

func TestDescriptionsExecutorPromptIsSnappy(t *testing.T) {
	p := DescriptionsGenerationExecutorPrompt()
	if len(p) > 1200 {
		t.Fatalf("executor prompt should stay snappy, got %d chars", len(p))
	}
	for _, want := range []string{"shortest description", "update_description", "match their voice and brevity"} {
		if !strings.Contains(p, want) {
			t.Fatalf("executor prompt missing %q", want)
		}
	}
}

func TestDescriptionsExecutorInputExemplarsToggle(t *testing.T) {
	resources := []DescriptionResource{{ID: "fn:one", Name: "One", Kind: domain.ResourceFunction}}

	off := DescriptionsGenerationExecutorInput(resources, nil)
	if strings.Contains(off, "House style examples") {
		t.Fatalf("no exemplars should mean no house-style block:\n%s", off)
	}

	on := DescriptionsGenerationExecutorInput(resources, []DescriptionExemplar{
		{Name: "Neighbor", Kind: domain.ResourceFunction, Description: "Does a neighborly thing.\nIgnored second line."},
	})
	if !strings.Contains(on, "House style examples") || !strings.Contains(on, "Neighbor: Does a neighborly thing.") {
		t.Fatalf("expected a compact one-line exemplar block:\n%s", on)
	}
	if strings.Contains(on, "Ignored second line") {
		t.Fatalf("exemplar descriptions should be collapsed to one line:\n%s", on)
	}
}

func TestDescriptionsExecutorInputRendersPreReadSource(t *testing.T) {
	resources := []DescriptionResource{
		{ID: "fn:a", Name: "A", Kind: domain.ResourceFunction, ReadOutput: "func A() {}"},
		{ID: "fn:b", Name: "B", Kind: domain.ResourceFunction},
	}
	input := DescriptionsGenerationExecutorInput(resources, nil)
	if !strings.Contains(input, "func A() {}") {
		t.Fatalf("pre-read source should be embedded for fn:a:\n%s", input)
	}
	if !strings.Contains(input, "call read with its id first") {
		t.Fatalf("resources without source should be told to read:\n%s", input)
	}
}

func TestBuildDescriptionExemplars(t *testing.T) {
	topo := &domain.Topology{Resources: map[string]domain.Resource{
		"fn:a": {ID: "fn:a", Name: "A", Kind: domain.ResourceFunction, Description: "Does A.", Location: domain.Location{Path: "x.go"}}, // same kind + same file -> rank 0
		"fn:b": {ID: "fn:b", Name: "B", Kind: domain.ResourceFunction, Location: domain.Location{Path: "x.go"}},                         // batch member (undescribed)
		"fn:c": {ID: "fn:c", Name: "C", Kind: domain.ResourceFunction, Description: "Does C.", Location: domain.Location{Path: "y.go"}}, // same kind, other file -> rank 1
		"st:d": {ID: "st:d", Name: "D", Kind: domain.ResourceStruct, Description: "Holds D.", Location: domain.Location{Path: "x.go"}},  // other kind, same file -> rank 2
	}}

	// Kind is the dominant signal: with a single slot the same-kind same-file
	// neighbor wins.
	if got := BuildDescriptionExemplars(topo, []string{"fn:b"}, 1); len(got) != 1 || got[0].Name != "A" {
		t.Fatalf("limit 1 should pick same-kind same-file neighbor A, got %+v", got)
	}

	// With room for all, the order is A (same kind + file), then C (same kind,
	// other file), then D (other kind, same file): same-kind C outranks
	// same-file D.
	got := BuildDescriptionExemplars(topo, []string{"fn:b"}, 3)
	names := make([]string, 0, len(got))
	for _, e := range got {
		names = append(names, e.Name)
	}
	if strings.Join(names, ",") != "A,C,D" {
		t.Fatalf("expected Kind-first ordering [A C D], got %v", names)
	}

	if ex := BuildDescriptionExemplars(topo, []string{"fn:b"}, 0); ex != nil {
		t.Fatalf("limit 0 should disable exemplars, got %+v", ex)
	}
}
