package domain

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Description budgets, in characters, per resource kind. Descriptions ride along in
// every CONTEXT block, so an overlong one is paid for on every later lookup — these
// are hard caps on the write path, not style suggestions. The description-generation
// prompts quote these same constants, so a budget change moves guidance and
// enforcement together.
const (
	DescriptionBudgetFunction = 120
	DescriptionBudgetType     = 100
	DescriptionBudgetVariable = 80
)

// DescriptionBudget returns the character cap for a kind's description. An unknown or
// unset kind falls back to the most permissive budget: the cap exists to stop runaway
// descriptions, and guessing low would reject text that is legal for the resource's
// real kind.
func DescriptionBudget(kind ResourceKind) int {
	switch kind {
	case ResourceFunction, ResourceMethod:
		return DescriptionBudgetFunction
	case ResourceStruct, ResourceNamedType, ResourceInterface, ResourceFile, ResourcePackage:
		return DescriptionBudgetType
	case ResourceVariable, ResourceDependency:
		return DescriptionBudgetVariable
	default:
		return DescriptionBudgetFunction
	}
}

// ValidateDescription rejects a description that overruns its kind's budget. It gates
// descriptions on the way IN only — whatever is already stored stays as it is, however
// long, so this must never be run as a sweep over existing rows. Surrounding whitespace
// does not count: a stray trailing newline is not worth a rejection. The error text is
// written to be read by the model that just called update_description, so it names the
// overrun and the fix.
func ValidateDescription(kind ResourceKind, description string) error {
	budget := DescriptionBudget(kind)
	n := utf8.RuneCountInString(strings.TrimSpace(description))
	if n <= budget {
		return nil
	}
	label := string(kind)
	if label == "" {
		label = "resource"
	}
	return fmt.Errorf("description is %d chars, over the %d-char budget for a %s: shorten it to one line and retry", n, budget, label)
}
