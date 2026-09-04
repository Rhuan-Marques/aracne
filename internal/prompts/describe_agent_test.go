package prompts

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
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

// A --regen_oversized batch has to tell the executor that the resource is already
// described, how far over budget that description is, and that the replacement comes from
// the code rather than from trimming the old text.
func TestDescriptionsExecutorInputRewritesOverBudgetDescriptions(t *testing.T) {
	current := strings.Repeat("very wordy ", 20) // well over the function budget
	single := DescriptionsGenerationExecutorInput([]DescriptionResource{
		{ID: "fn:a", Name: "A", Kind: domain.ResourceFunction, ReadOutput: "func A() {}", CurrentDescription: current},
	}, nil)
	for _, want := range []string{
		"Rewrite the description",
		"Current description (",
		fmt.Sprintf("over the %d-char budget", domain.DescriptionBudgetFunction),
		"not by trimming the old one",
		"update_description",
	} {
		if !strings.Contains(single, want) {
			t.Fatalf("regen input missing %q:\n%s", want, single)
		}
	}
	if strings.Contains(single, "Describe only the assigned resource") {
		t.Fatalf("a rewrite must not be framed as a first description:\n%s", single)
	}

	batch := DescriptionsGenerationExecutorInput([]DescriptionResource{
		{ID: "fn:a", Name: "A", Kind: domain.ResourceFunction, CurrentDescription: current},
		{ID: "st:b", Name: "B", Kind: domain.ResourceStruct, CurrentDescription: current},
	}, nil)
	for _, want := range []string{
		"Rewrite the descriptions",
		fmt.Sprintf("over the %d-char budget", domain.DescriptionBudgetFunction),
		fmt.Sprintf("over the %d-char budget", domain.DescriptionBudgetType),
		"not by trimming the old one",
	} {
		if !strings.Contains(batch, want) {
			t.Fatalf("regen batch input missing %q:\n%s", want, batch)
		}
	}
}

// The quoted length is the one the budget was measured against -- the whole trimmed text,
// not the first line -- and a long offender is shown only in preview.
func TestDescriptionsExecutorInputQuotesFullLengthAndTruncatesPreview(t *testing.T) {
	current := "first line\n" + strings.Repeat("z", 400)
	input := DescriptionsGenerationExecutorInput([]DescriptionResource{
		{ID: "fn:a", Name: "A", Kind: domain.ResourceFunction, CurrentDescription: current},
	}, nil)
	if !strings.Contains(input, fmt.Sprintf("Current description (%d chars", len(strings.TrimSpace(current)))) {
		t.Fatalf("length must count the whole trimmed description:\n%s", input)
	}
	if !strings.Contains(input, "…") {
		t.Fatalf("an over-long description should be previewed, not pasted whole:\n%s", input)
	}
	if strings.Contains(input, strings.Repeat("z", currentDescriptionPreview+1)) {
		t.Fatalf("preview should stop at %d chars:\n%s", currentDescriptionPreview, input)
	}
}

// Nothing changes for a normal generation run: no stored description, no rewrite framing.
func TestDescriptionsExecutorInputKeepsGenerateFramingWithoutCurrent(t *testing.T) {
	input := DescriptionsGenerationExecutorInput([]DescriptionResource{
		{ID: "fn:a", Name: "A", Kind: domain.ResourceFunction},
	}, nil)
	if !strings.Contains(input, "Describe only the assigned resource") {
		t.Fatalf("generate framing lost:\n%s", input)
	}
	for _, unwanted := range []string{"Current description (", "Rewrite the description"} {
		if strings.Contains(input, unwanted) {
			t.Fatalf("generate input should not mention %q:\n%s", unwanted, input)
		}
	}
}

// An exemplar teaches "voice and brevity", so one that update_description would itself
// reject must never be offered -- least of all in a --regen_oversized run, where the
// database is full of exactly such descriptions.
func TestBuildDescriptionExemplarsSkipsOverBudget(t *testing.T) {
	long := strings.Repeat("x", domain.DescriptionBudgetFunction+1)
	topo := &domain.Topology{Resources: map[string]domain.Resource{
		"fn:target": {ID: "fn:target", Name: "Target", Kind: domain.ResourceFunction, Location: domain.Location{Path: "x.go"}},
		"fn:long":   {ID: "fn:long", Name: "Long", Kind: domain.ResourceFunction, Description: long, Location: domain.Location{Path: "x.go"}},
		"fn:short":  {ID: "fn:short", Name: "Short", Kind: domain.ResourceFunction, Description: "Does a short thing.", Location: domain.Location{Path: "y.go"}},
	}}

	got := BuildDescriptionExemplars(topo, []string{"fn:target"}, 5)
	if len(got) != 1 || got[0].Name != "Short" {
		t.Fatalf("only the within-budget description qualifies, got %+v", got)
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
