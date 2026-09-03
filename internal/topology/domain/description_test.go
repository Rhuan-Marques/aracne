package domain

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDescriptionBudgetPerKind(t *testing.T) {
	cases := []struct {
		kind ResourceKind
		want int
	}{
		{ResourceFunction, DescriptionBudgetFunction},
		{ResourceMethod, DescriptionBudgetFunction},
		{ResourceStruct, DescriptionBudgetType},
		{ResourceNamedType, DescriptionBudgetType},
		{ResourceInterface, DescriptionBudgetType},
		{ResourceFile, DescriptionBudgetType},
		{ResourcePackage, DescriptionBudgetType},
		{ResourceVariable, DescriptionBudgetVariable},
		{ResourceDependency, DescriptionBudgetVariable},
		// Unknown and unset kinds get the most permissive budget rather than a
		// rejection that would be wrong for the resource's real kind.
		{ResourceKind("trait"), DescriptionBudgetFunction},
		{ResourceKind(""), DescriptionBudgetFunction},
	}
	for _, c := range cases {
		if got := DescriptionBudget(c.kind); got != c.want {
			t.Errorf("DescriptionBudget(%q) = %d, want %d", c.kind, got, c.want)
		}
	}
}

func TestValidateDescriptionBoundary(t *testing.T) {
	if err := ValidateDescription(ResourceFunction, strings.Repeat("x", DescriptionBudgetFunction)); err != nil {
		t.Fatalf("a description exactly at the budget must pass: %v", err)
	}
	err := ValidateDescription(ResourceFunction, strings.Repeat("x", DescriptionBudgetFunction+1))
	if err == nil {
		t.Fatal("one char over the budget should be rejected")
	}
	// The message is read by the model that just called update_description, so it
	// has to carry both numbers and the fix.
	for _, want := range []string{"121 chars", "120-char budget", "function", "shorten"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
	if err := ValidateDescription(ResourceVariable, strings.Repeat("x", DescriptionBudgetVariable+1)); err == nil {
		t.Fatal("variables have the tightest budget and should reject sooner")
	}
}

func TestValidateDescriptionCountsCharsNotBytes(t *testing.T) {
	// Em dashes are three bytes each and the house style uses them: a budget
	// counted in bytes would reject a description well under its real length.
	if err := ValidateDescription(ResourceStruct, strings.Repeat("—", DescriptionBudgetType)); err != nil {
		t.Fatalf("multibyte runes must count as one char each: %v", err)
	}
}

func TestValidateDescriptionIgnoresSurroundingWhitespace(t *testing.T) {
	desc := "\n  " + strings.Repeat("x", DescriptionBudgetType) + "  \n"
	if err := ValidateDescription(ResourceFile, desc); err != nil {
		t.Fatalf("a stray trailing newline is not worth a rejection: %v", err)
	}
}

// The scanner harvests doc comments straight into the topology without ever meeting
// ValidateDescription, so the budget has to be enforced where resources are STORED as well as
// where they arrive through update_description. On the terraform-docs fixture that gap left
// 17% of stored descriptions over budget -- including every package description, the worst at
// 1,285 characters against a budget of 100 -- and each one is re-sent in every CONTEXT block
// that names the resource.
func TestCapDescriptionEnforcesTheBudget(t *testing.T) {
	long := strings.Repeat("word ", 400)
	for _, kind := range []ResourceKind{ResourceFunction, ResourceStruct, ResourceFile, ResourceVariable} {
		got := CapDescription(kind, long)
		if utf8.RuneCountInString(got) > DescriptionBudget(kind) {
			t.Errorf("%s: capped to %d runes, budget is %d", kind, utf8.RuneCountInString(got), DescriptionBudget(kind))
		}
		if !strings.HasSuffix(got, "…") {
			t.Errorf("%s: a truncated description must be marked, got %q", kind, got)
		}
	}
}

// A Go package comment is a paragraph. Eleven lines of it inside a four-entry CONTEXT block is
// how a 1,950-byte file read came back as 3,597 bytes.
func TestCapDescriptionCollapsesNewlines(t *testing.T) {
	got := CapDescription(ResourceFunction, "first line\n\nsecond line\n\tthird")
	if strings.ContainsAny(got, "\n\t") {
		t.Errorf("newlines and tabs must collapse, got %q", got)
	}
	if got != "first line second line third" {
		t.Errorf("got %q", got)
	}
}

// Within budget it must be returned untouched -- capping is not an excuse to reformat.
func TestCapDescriptionLeavesShortTextAlone(t *testing.T) {
	const s = "Normalize trims and lowercases a tag."
	if got := CapDescription(ResourceFunction, s); got != s {
		t.Errorf("got %q, want %q", got, s)
	}
	if got := CapDescription(ResourceFunction, ""); got != "" {
		t.Errorf("empty stays empty, got %q", got)
	}
	if got := RenderDescription(ResourceFunction, ""); got != "no description" {
		t.Errorf("render of empty = %q", got)
	}
}

// Harvested doc comments are ported only when they FIT. A doc comment is a paragraph written to
// be read as one, and its first 120 characters are not a summary of it -- truncating yields a
// severed clause that reads like a description without being one, and nothing downstream can
// tell the difference. On grafana/k6 that would have applied to 34% of harvested Go comments
// (52% of interfaces). An undescribed resource is the honest state: `descriptions generate` and
// node_list_no_description can both find it, while a stub looks described and is skipped.
func TestDescriptionForStorageDropsRatherThanTruncates(t *testing.T) {
	long := strings.Repeat("word ", 200)
	for _, kind := range []ResourceKind{ResourceFunction, ResourceStruct, ResourceInterface, ResourceVariable} {
		if got := DescriptionForStorage(kind, long); got != "" {
			t.Errorf("%s: over-budget text must not be stored, got %d chars", kind, len(got))
		}
	}
	fits := "Serve starts the HTTP listener and blocks until the context is cancelled."
	if got := DescriptionForStorage(ResourceFunction, fits); got != fits {
		t.Errorf("a description within budget must be stored verbatim, got %q", got)
	}
}

// Whitespace still collapses: a comment that fits the budget but spans eleven lines costs the
// same in a CONTEXT block as one that does not.
func TestDescriptionForStorageCollapsesWhitespace(t *testing.T) {
	got := DescriptionForStorage(ResourceFunction, "Serve starts\n\tthe listener.")
	if got != "Serve starts the listener." {
		t.Errorf("got %q", got)
	}
}

// A doc comment sitting exactly on the budget is kept; one rune more is dropped. Counted in
// runes, the way ValidateDescription counts.
func TestDescriptionForStorageBoundary(t *testing.T) {
	b := DescriptionBudget(ResourceFunction)
	if got := DescriptionForStorage(ResourceFunction, strings.Repeat("a", b)); got == "" {
		t.Error("exactly at budget must be kept")
	}
	if got := DescriptionForStorage(ResourceFunction, strings.Repeat("a", b+1)); got != "" {
		t.Error("one over budget must be dropped")
	}
}

// Prompts quote a budget BELOW the enforced one, because models count characters badly and the
// error is one-sided: a few characters over is rejected by update_description (a retry, or an
// undescribed resource), a few under costs nothing.
func TestStatedBudgetIsLowerThanEnforced(t *testing.T) {
	for _, kind := range []ResourceKind{ResourceFunction, ResourceMethod, ResourceStruct,
		ResourceInterface, ResourceFile, ResourceVariable} {
		stated, real := StatedDescriptionBudget(kind), DescriptionBudget(kind)
		if stated >= real {
			t.Errorf("%s: stated %d must be below enforced %d", kind, stated, real)
		}
		if real-stated != StatedBudgetMargin {
			t.Errorf("%s: margin is %d, want %d", kind, real-stated, StatedBudgetMargin)
		}
		// A description written to the stated budget must always pass the real gate.
		if err := ValidateDescription(kind, strings.Repeat("a", stated)); err != nil {
			t.Errorf("%s: text at the stated budget must validate: %v", kind, err)
		}
	}
	// The headline numbers, pinned so a budget change cannot silently invert the margin.
	if got := StatedDescriptionBudget(ResourceFunction); got != 100 {
		t.Errorf("function: stated %d, want 100 (enforced 120)", got)
	}
	if got := StatedDescriptionBudget(ResourceStruct); got != 80 {
		t.Errorf("struct: stated %d, want 80 (enforced 100)", got)
	}
}

// The margin must never drive a budget to something unusable.
func TestStatedBudgetHasAFloor(t *testing.T) {
	if got := StatedDescriptionBudget(ResourceKind("nonexistent")); got < 20 {
		t.Errorf("stated budget fell below the floor: %d", got)
	}
}
