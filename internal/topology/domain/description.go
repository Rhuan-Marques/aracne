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

// RenderDescription is what a CONTEXT or USED BY line should print for a stored description.
//
// WHY CAPPING HAPPENS HERE AND NOT ON THE WRITE PATH. ValidateDescription gates descriptions
// arriving through update_description, and deliberately never sweeps what is already stored.
// But the scanner does not go through that gate at all: it harvests doc comments straight into
// the topology when it builds it. In a doc-comment language that is most of the database --
// measured on the terraform-docs fixture, 48 of 275 stored descriptions (17%) overrun their
// kind's budget, every one of the 5 file/package descriptions does, and the worst is 1,285
// characters against a budget of 100.
//
// The budget's stated reason is render cost: "these descriptions are re-sent in every CONTEXT
// block, so an overlong one is paid for on every later lookup". That is an argument about what
// is PRINTED, so printing is where it can be enforced without destroying anything -- the full
// text stays in the database for `descriptions generate --regen_oversized` to rewrite properly.
//
// Newlines collapse first. A Go package comment is a paragraph, and eleven lines of it inside a
// four-entry CONTEXT block is how a 1,950-byte file read came back as 3,597 bytes.
func RenderDescription(kind ResourceKind, s string) string {
	if capped := CapDescription(kind, s); capped != "" {
		return capped
	}
	return "no description"
}

// DescriptionForStorage is what may be STORED for a harvested description: the text itself if
// it fits its kind's budget, or nothing at all.
//
// It does NOT truncate. A doc comment is written to be read as a paragraph, and its first 120
// characters are not a summary of it -- cutting there yields a severed clause that reads like a
// description without being one, and nothing downstream can tell the difference. Measured on
// grafana/k6, 34% of harvested Go doc comments overrun their budget (52% of interfaces, whose
// comments explain a contract rather than an action), so truncation would have applied to a
// third of the database.
//
// Leaving the resource undescribed is the honest state: `descriptions generate` then writes a
// real one-line description for it, and `node_list_no_description` can find it. A truncated
// stub would look described and be skipped.
//
// Whitespace still collapses, because a description that fits the budget but spans eleven lines
// costs the same in a CONTEXT block either way.
func DescriptionForStorage(kind ResourceKind, s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" || utf8.RuneCountInString(s) > DescriptionBudget(kind) {
		return ""
	}
	return s
}

// CapDescription normalises a description to what its kind's budget allows, TRUNCATING when it
// overruns. Kept for the RENDER path only: rows written before DescriptionForStorage existed
// are grandfathered in the database, and printing 1,285 characters of package comment in a
// CONTEXT block is worse than printing its first hundred. New writes never reach this.
func CapDescription(kind ResourceKind, s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return ""
	}
	budget := DescriptionBudget(kind)
	// Runes, not bytes -- ValidateDescription counts with utf8.RuneCountInString, and a cap
	// that disagreed with the validator would store text the validator then rejects.
	if utf8.RuneCountInString(s) <= budget {
		return s
	}
	runes := []rune(s)
	// One rune of the budget belongs to the ellipsis, or the result overruns by exactly the
	// marker that says it was shortened.
	cut := string(runes[:budget-1])
	if i := strings.LastIndexByte(cut, ' '); i > len(cut)/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " .,;:") + "…"
}
