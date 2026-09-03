package prompts

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"aracne/internal/topology/domain"
)

// Holds a resource's ID, name, kind, and pre-read source output for description generation.
type DescriptionResource struct {
	ID         string
	Name       string
	Kind       domain.ResourceKind
	ReadOutput string
	// CurrentDescription is set only when the resource is being RE-described because its
	// stored description overruns its kind's budget (`descriptions generate
	// --regen_oversized`). It is shown to the executor as the text to replace, which also
	// flips the input's framing from "describe this" to "rewrite this, shorter".
	CurrentDescription string
}

// currentDescriptionPreview caps how much of an over-budget description is quoted back to
// the executor. The source is already in the prompt and the rewrite must come from the
// code, not from compressing the old text, so a long offender is shown only far enough to
// recognise what is being replaced.
const currentDescriptionPreview = 200

// DescriptionExemplar is an already-written description fed to the executor as a
// house-style anchor so generated descriptions match the codebase's voice.
type DescriptionExemplar struct {
	Name        string
	Kind        domain.ResourceKind
	Description string
}

// Returns prompt instructions for the descriptions-generation executor sub-agent, directing it to write concise resource descriptions and update them via the topology database.
func DescriptionsGenerationExecutorPrompt() string {
	return `You write snappy, accurate descriptions for the resources in your assigned batch — nothing else.

## Loop (per assigned resource)
1. Use the source if it is already provided; otherwise call **read** with its ` + "`resource_id`" + `.
2. Glance at neighbors only when the resource alone is unclear.
3. Write the shortest description that is still accurate. One line, within the character budget in your task prompt — update_description rejects anything over it. Cut articles and filler before you cut facts.
4. Call **update_description** immediately.

## Rules
- Touch only assigned resources. Never write scripts to bulk-generate.
- When house-style examples are given, match their voice and brevity.
- The topology database is the source of truth. End with a one-line report: completed ids, then any failed ids with a brief reason.
`
}

// Formats executor subagent instructions for describing assigned resources, with per-kind limits and exemplars.
func DescriptionsGenerationExecutorInput(resources []DescriptionResource, exemplars []DescriptionExemplar) string {
	var b strings.Builder
	regen := hasCurrentDescriptions(resources)
	if len(resources) == 1 {
		res := resources[0]
		if regen {
			b.WriteString("Rewrite the description of the assigned resource below: it already has one, but it overruns the character budget for its kind. You may glance at neighbors, but update only this resource.\n\n")
		} else {
			b.WriteString("Describe only the assigned resource below. You may glance at neighbors, but update only this resource.\n\n")
		}
		b.WriteString(fmt.Sprintf("Resource ID: %s\nName: %s\nKind: %s\n", res.ID, res.Name, res.Kind))
		writeCurrentDescription(&b, "", res)
		b.WriteByte('\n')
		if strings.TrimSpace(res.ReadOutput) != "" {
			b.WriteString("Source (already read for you):\n\n```text\n")
			b.WriteString(strings.TrimSpace(res.ReadOutput))
			b.WriteString("\n```\n\n")
		} else {
			b.WriteString("Call read with the resource ID before writing its description.\n\n")
		}
		writeExemplars(&b, exemplars)
		b.WriteString(singleDescriptionInstruction(res.Kind))
		if regen {
			b.WriteString(" ")
			b.WriteString(rewriteInstruction)
		}
		b.WriteString(" Then call update_description immediately.\n")
		return b.String()
	}

	if regen {
		b.WriteString("Rewrite the descriptions of the assigned resources below: each already has one, but it overruns the character budget for its kind. You may glance at neighbors, but update only assigned resources.\n\n")
	} else {
		b.WriteString("Describe only the assigned resources below. You may glance at neighbors, but update only assigned resources.\n\n")
	}
	b.WriteString("Assigned resources:\n\n")
	for _, res := range resources {
		b.WriteString(fmt.Sprintf("- ID: %s\n  Name: %s\n  Kind: %s\n", res.ID, res.Name, res.Kind))
		writeCurrentDescription(&b, "  ", res)
		if strings.TrimSpace(res.ReadOutput) != "" {
			b.WriteString("  Source (already read):\n\n```text\n")
			b.WriteString(strings.TrimSpace(res.ReadOutput))
			b.WriteString("\n```\n")
		}
		b.WriteByte('\n')
	}
	b.WriteString("For any resource without source above, call read with its id first. Call update_description immediately after each description.\n\n")
	if regen {
		b.WriteString(rewriteInstruction)
		b.WriteByte('\n')
		b.WriteByte('\n')
	}
	writeExemplars(&b, exemplars)
	b.WriteString("## Per-kind limits\n\n")
	for _, line := range descriptionGuidelinesForKinds(resources) {
		b.WriteString("- ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// rewriteInstruction is the extra guidance a re-description needs and a first description
// does not: the old text is over budget, so shortening it is the whole job, and it must be
// re-derived from the code rather than trimmed word by word from a description that was
// already too long.
const rewriteInstruction = "Write the new description from the code, not by trimming the old one, and keep every fact that still fits."

// hasCurrentDescriptions reports whether any assigned resource is being re-described.
func hasCurrentDescriptions(resources []DescriptionResource) bool {
	for _, res := range resources {
		if strings.TrimSpace(res.CurrentDescription) != "" {
			return true
		}
	}
	return false
}

// writeCurrentDescription renders the over-budget description a resource is replacing,
// with its length and budget so the executor knows how much has to go; no-op for a
// resource that has no description yet. indent prefixes the line in list form.
func writeCurrentDescription(b *strings.Builder, indent string, res DescriptionResource) {
	trimmed := strings.TrimSpace(res.CurrentDescription)
	if trimmed == "" {
		return
	}
	// Counted the way domain.ValidateDescription counts it — the whole trimmed text in
	// runes — so the number quoted here is the one the budget was measured against, not
	// the length of the first line of a multi-line offender.
	n := utf8.RuneCountInString(trimmed)
	current := strings.Join(strings.Fields(trimmed), " ")
	if utf8.RuneCountInString(current) > currentDescriptionPreview {
		current = string([]rune(current)[:currentDescriptionPreview]) + "…"
	}
	b.WriteString(fmt.Sprintf("%sCurrent description (%d chars, over the %d-char budget) — replace it: %s\n", indent, n, domain.DescriptionBudget(res.Kind), current))
}

// writeExemplars renders a compact house-style block; no-op when empty.
func writeExemplars(b *strings.Builder, exemplars []DescriptionExemplar) {
	if len(exemplars) == 0 {
		return
	}
	b.WriteString("House style examples (match this voice and brevity):\n")
	for _, ex := range exemplars {
		b.WriteString(fmt.Sprintf("- %s: %s\n", ex.Name, oneLine(ex.Description)))
	}
	b.WriteByte('\n')
}

// Extracts the first line of a string, trimming whitespace.
func oneLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return s
}

// BuildDescriptionExemplars returns up to limit already-described resources to
// anchor house style for the assigned batch. Candidates are ranked Kind-first,
// file as tiebreaker: a resource of the same Kind as the batch is always
// preferred over a different Kind, and within a Kind one sharing a source file
// with the batch wins. Returns nil when limit <= 0 or nothing qualifies.
//
// A description that overruns its own kind's budget is never an exemplar. The block
// asks the executor to match the examples' "voice and brevity", so an over-budget one
// teaches a length update_description would reject — and in a --regen_oversized run,
// where over-budget descriptions are exactly what the database is full of, it would
// anchor the rewrite to the text being replaced.
func BuildDescriptionExemplars(topo *domain.Topology, batchIDs []string, limit int) []DescriptionExemplar {
	if topo == nil || limit <= 0 || len(batchIDs) == 0 {
		return nil
	}
	batch := make(map[string]bool, len(batchIDs))
	paths := make(map[string]bool)
	kinds := make(map[domain.ResourceKind]bool)
	for _, id := range batchIDs {
		batch[id] = true
		if res, ok := topo.Resources[id]; ok {
			if res.Location.Path != "" {
				paths[res.Location.Path] = true
			}
			kinds[res.Kind] = true
		}
	}

	ids := make([]string, 0, len(topo.Resources))
	for id := range topo.Resources {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	type candidate struct {
		ex   DescriptionExemplar
		rank int
	}
	var cands []candidate
	for _, id := range ids {
		if batch[id] {
			continue
		}
		res := topo.Resources[id]
		if strings.TrimSpace(res.Description) == "" {
			continue
		}
		if domain.ValidateDescription(res.Kind, res.Description) != nil {
			continue
		}
		sameKind := kinds[res.Kind]
		sameFile := res.Location.Path != "" && paths[res.Location.Path]
		// 0: same Kind + same file, 1: same Kind, 2: same file, 3: neither.
		rank := 3
		switch {
		case sameKind && sameFile:
			rank = 0
		case sameKind:
			rank = 1
		case sameFile:
			rank = 2
		}
		cands = append(cands, candidate{
			ex:   DescriptionExemplar{Name: res.Name, Kind: res.Kind, Description: res.Description},
			rank: rank,
		})
	}
	// Stable sort preserves the pre-sorted ID order within each rank.
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].rank < cands[j].rank })

	if len(cands) > limit {
		cands = cands[:limit]
	}
	out := make([]DescriptionExemplar, 0, len(cands))
	for _, c := range cands {
		out = append(out, c.ex)
	}
	return out
}

// Returns the per-resource-kind guideline for writing one-line descriptions in the topology database.
func singleDescriptionInstruction(kind domain.ResourceKind) string {
	budget := domain.StatedDescriptionBudget(kind)
	switch kind {
	case domain.ResourceFunction, domain.ResourceMethod:
		return fmt.Sprintf("One line, ≤%d chars: what it does, plus any notable params, returns, or side effects.", budget)
	case domain.ResourceStruct, domain.ResourceNamedType:
		return fmt.Sprintf("One line, ≤%d chars: what it represents and its key fields or methods.", budget)
	case domain.ResourceInterface:
		return fmt.Sprintf("One line, ≤%d chars: the contract and key methods.", budget)
	case domain.ResourceVariable:
		return fmt.Sprintf("One line, ≤%d chars: what it stores and why.", budget)
	case domain.ResourceFile:
		return fmt.Sprintf("One line, ≤%d chars: the file's role in its package.", budget)
	case domain.ResourcePackage:
		return fmt.Sprintf("One line, ≤%d chars: the package's purpose.", budget)
	case domain.ResourceDependency:
		return fmt.Sprintf("One line, ≤%d chars: what external dependency and why.", budget)
	default:
		return fmt.Sprintf("One line, ≤%d chars, on what this resource does.", budget)
	}
}

// Generates per-resource-kind description guidelines for description generator, formatted as instruction lines
func descriptionGuidelinesForKinds(resources []DescriptionResource) []string {
	seen := make(map[domain.ResourceKind]bool)
	for _, res := range resources {
		seen[res.Kind] = true
	}
	order := make([]domain.ResourceKind, 0, len(seen))
	for kind := range seen {
		order = append(order, kind)
	}
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })

	var lines []string
	functionLike := seen[domain.ResourceFunction] || seen[domain.ResourceMethod]
	if functionLike {
		lines = append(lines, fmt.Sprintf("Functions/methods: one line each, ≤%d chars — what it does + any notable params/returns/side effects", domain.StatedDescriptionBudget(domain.ResourceFunction)))
		delete(seen, domain.ResourceFunction)
		delete(seen, domain.ResourceMethod)
	}
	for _, kind := range order {
		if !seen[kind] {
			continue
		}
		lines = append(lines, pluralDescriptionInstruction(kind))
	}
	lines = append(lines, "Never exceed the character budget — update_description rejects an over-budget write: these descriptions are re-sent in every CONTEXT block, so an overlong one is paid for on every later lookup")
	return lines
}

// Returns description guidelines for a resource kind, specifying line limits and key details to include per type.
func pluralDescriptionInstruction(kind domain.ResourceKind) string {
	budget := domain.StatedDescriptionBudget(kind)
	switch kind {
	case domain.ResourceStruct, domain.ResourceNamedType:
		return fmt.Sprintf("Types: one line each, ≤%d chars — what they represent and key fields/methods", budget)
	case domain.ResourceInterface:
		return fmt.Sprintf("Interfaces: one line each, ≤%d chars — contract and key methods", budget)
	case domain.ResourceVariable:
		return fmt.Sprintf("Variables: one line each, ≤%d chars — what they store and why", budget)
	case domain.ResourceFile:
		return fmt.Sprintf("Files: one line each, ≤%d chars — role in the package", budget)
	case domain.ResourcePackage:
		return fmt.Sprintf("Packages: one line each, ≤%d chars — overall purpose", budget)
	case domain.ResourceDependency:
		return fmt.Sprintf("Dependencies: one line each, ≤%d chars — what and why", budget)
	default:
		return fmt.Sprintf("%s: one line each, ≤%d chars", kind, budget)
	}
}
