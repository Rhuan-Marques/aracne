package helper

import (
	"strings"
	"testing"

	"aracne/internal/topology/domain"
)

func TestEffectiveReadKindsDefaults(t *testing.T) {
	// Absent means the default four, not "nothing readable".
	c := &Config{}
	if got := c.EffectiveReadKinds(); len(got) != len(DefaultReadKinds()) {
		t.Fatalf("absent read.kinds should default, got %v", got)
	}
	c.Read.Kinds = []domain.ResourceKind{domain.ResourceFunction}
	if got := c.EffectiveReadKinds(); len(got) != 1 || got[0] != domain.ResourceFunction {
		t.Fatalf("explicit read.kinds should win, got %v", got)
	}
}

func TestValidateReadKinds(t *testing.T) {
	if err := ValidateReadKinds(nil); err != nil {
		t.Fatalf("absent is valid: %v", err)
	}
	if err := ValidateReadKinds(AllReadKinds()); err != nil {
		t.Fatalf("every accepted kind should validate: %v", err)
	}
	// An empty list would make read reject everything, which is never what someone meant.
	if err := ValidateReadKinds([]domain.ResourceKind{}); err == nil {
		t.Fatal("empty read.kinds should be rejected")
	}
	// "method" is not a separate kind: methods resolve as functions.
	err := ValidateReadKinds([]domain.ResourceKind{domain.ResourceFunction, "method", "bogus"})
	if err == nil {
		t.Fatal("expected an error for unknown kinds")
	}
	for _, want := range []string{"method", "bogus", "named_type"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error should name the offender and the valid set, got: %v", err)
		}
	}
}

func TestConfigValidateRejectsBadReadKinds(t *testing.T) {
	c := DefaultConfig()
	c.Read.Kinds = []domain.ResourceKind{"nonsense"}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "read.kinds") {
		t.Fatalf("Validate should surface a bad read.kinds, got: %v", err)
	}
}

func TestEffectiveMaxInlineParentLines(t *testing.T) {
	c := &Config{}
	if got := c.EffectiveMaxInlineParentLines(); got != domain.DefaultMaxInlineParentLines {
		t.Fatalf("absent should default to %d, got %d", domain.DefaultMaxInlineParentLines, got)
	}
	// 0 is meaningful (disable inlining), so it must be distinguishable from absent.
	c.Read.ContextFilter.MaxInlineParentLines = intPtr(0)
	if got := c.EffectiveMaxInlineParentLines(); got != 0 {
		t.Fatalf("explicit 0 should survive, got %d", got)
	}
	if got := c.EffectiveContextFilter().MaxInlineParentLines; got != 0 {
		t.Fatalf("EffectiveContextFilter should carry it, got %d", got)
	}
}

func TestDefaultAgentToolsHaveOneReadEntry(t *testing.T) {
	// The per-kind read tools are gone; every default profile names "read" once or not at all.
	gone := map[string]bool{
		"read_function": true, "read_struct": true, "read_interface": true,
		"read_named_type": true, "read_file": true, "read_package": true, "read_dependency": true,
	}
	for _, agent := range []string{"", "main", "bug-hunter", "bug-judge", "bug-solver", "descriptions-generation-executor"} {
		for _, name := range DefaultAgentMCPTools(agent) {
			if gone[name] {
				t.Fatalf("agent %q still defaults to removed tool %q", agent, name)
			}
		}
	}
	for _, agent := range []string{"explorer", "bug-hunter", "bug-judge", "bug-solver"} {
		for _, name := range DefaultChatAgentTools(agent) {
			if gone[name] {
				t.Fatalf("chat agent %q still defaults to removed tool %q", agent, name)
			}
		}
	}
}
