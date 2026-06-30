package prompts

import (
	"fmt"
	"sort"
	"strings"

	"aracne/internal/topology/domain"
)

// Holds a resource's ID, name, kind, and pre-read source output for description generation.
type DescriptionResource struct {
	ID         string
	Name       string
	Kind       domain.ResourceKind
	ReadOutput string
}

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
3. Write the shortest description that is still accurate — one line is ideal, never exceed the per-kind limit in your task prompt.
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
	if len(resources) == 1 {
		res := resources[0]
		b.WriteString("Describe only the assigned resource below. You may glance at neighbors, but update only this resource.\n\n")
		b.WriteString(fmt.Sprintf("Resource ID: %s\nName: %s\nKind: %s\n\n", res.ID, res.Name, res.Kind))
		if strings.TrimSpace(res.ReadOutput) != "" {
			b.WriteString("Source (already read for you):\n\n```text\n")
			b.WriteString(strings.TrimSpace(res.ReadOutput))
			b.WriteString("\n```\n\n")
		} else {
			b.WriteString("Call read with the resource ID before writing its description.\n\n")
		}
		writeExemplars(&b, exemplars)
		b.WriteString(singleDescriptionInstruction(res.Kind))
		b.WriteString(" Then call update_description immediately.\n")
		return b.String()
	}

	b.WriteString("Describe only the assigned resources below. You may glance at neighbors, but update only assigned resources.\n\n")
	b.WriteString("Assigned resources:\n\n")
	for _, res := range resources {
		b.WriteString(fmt.Sprintf("- ID: %s\n  Name: %s\n  Kind: %s\n", res.ID, res.Name, res.Kind))
		if strings.TrimSpace(res.ReadOutput) != "" {
			b.WriteString("  Source (already read):\n\n```text\n")
			b.WriteString(strings.TrimSpace(res.ReadOutput))
			b.WriteString("\n```\n")
		}
		b.WriteByte('\n')
	}
	b.WriteString("For any resource without source above, call read with its id first. Call update_description immediately after each description.\n\n")
	writeExemplars(&b, exemplars)
	b.WriteString("## Per-kind limits\n\n")
	for _, line := range descriptionGuidelinesForKinds(resources) {
		b.WriteString("- ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
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
	switch kind {
	case domain.ResourceFunction, domain.ResourceMethod:
		return "1 line: what it does, plus any notable params, returns, or side effects."
	case domain.ResourceStruct, domain.ResourceNamedType:
		return "1 line: what it represents and its key fields or methods."
	case domain.ResourceInterface:
		return "1 line: the contract and key methods."
	case domain.ResourceVariable:
		return "≤1 line: what it stores and why."
	case domain.ResourceFile:
		return "≤1 line: the file's role in its package."
	case domain.ResourcePackage:
		return "1 line: the package's purpose."
	case domain.ResourceDependency:
		return "≤1 line: what external dependency and why."
	default:
		return "1 short line on what this resource does."
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
		lines = append(lines, "Functions/methods: 1 line each — what it does + any notable params/returns/side effects")
		delete(seen, domain.ResourceFunction)
		delete(seen, domain.ResourceMethod)
	}
	for _, kind := range order {
		if !seen[kind] {
			continue
		}
		lines = append(lines, pluralDescriptionInstruction(kind))
	}
	lines = append(lines, "Keep every description to one line where possible")
	return lines
}

// Returns description guidelines for a resource kind, specifying line limits and key details to include per type.
func pluralDescriptionInstruction(kind domain.ResourceKind) string {
	switch kind {
	case domain.ResourceStruct, domain.ResourceNamedType:
		return "Types: 1 line each — what they represent and key fields/methods"
	case domain.ResourceInterface:
		return "Interfaces: 1 line each — contract and key methods"
	case domain.ResourceVariable:
		return "Variables: ≤1 line each — what they store and why"
	case domain.ResourceFile:
		return "Files: ≤1 line each — role in the package"
	case domain.ResourcePackage:
		return "Packages: 1 line each — overall purpose"
	case domain.ResourceDependency:
		return "Dependencies: ≤1 line each — what and why"
	default:
		return fmt.Sprintf("%s: 1 short line each", kind)
	}
}
