package domain

import (
	"strings"
	"testing"
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
