package prompts

import (
	"fmt"
	"sort"
	"strings"

	"aracne/internal/topology/domain"
)

type DescriptionResource struct {
	ID         string
	Name       string
	Kind       domain.ResourceKind
	ReadOutput string
}

func DescriptionsGenerationExecutorPrompt() string {
	return `You are a description generation executor for the project topology database.

Your goal is to generate careful, concise descriptions for one assigned batch of undocumented resources.

You are not the orchestrator. Do not call node_list_no_description. Process only the resources explicitly assigned in your task prompt.

You may and should look around neighboring resources when the assigned resource's source and immediate context are not enough to understand what it does. Use that context to improve accuracy, but never update resources outside your assigned batch.

## Workflow

1. Read the assigned resource list from the task prompt
2. For each assigned resource, one at a time:
   a. Use the provided resource read, if one is included
   b. Otherwise call **read** with its ` + "`resource_id`" + `
   c. Study the returned source code and nearby topology context when needed
   d. Manually write a description based on what the resource actually does
   e. Immediately call **update_description** with ` + "`id`" + `, ` + "`resource_name`" + `, and your generated description
3. Continue until every assigned resource has either been updated or has a clear failure reason
4. Return a concise completion report listing completed IDs and failed IDs

## General Rules

- Be concise and accurate
- Do not automate description generation by writing scripts or bulk transformation code
- Do not update resources outside your assigned batch
- Do not skip any assigned resource unless a tool error prevents completion

## Final Report Format

completed:
- <id>

failed:
- <id>: <reason>
`
}

func DescriptionsGenerationExecutorInput(resources []DescriptionResource) string {
	var b strings.Builder
	if len(resources) == 1 {
		res := resources[0]
		b.WriteString("Process only the assigned main resource. You may inspect neighboring resources if needed, but update only this main resource.\n\n")
		b.WriteString(fmt.Sprintf("Main resource ID: %s\nName: %s\nKind: %s\n\n", res.ID, res.Name, res.Kind))
		if strings.TrimSpace(res.ReadOutput) != "" {
			b.WriteString("The main resource has already been read for you:\n\n```text\n")
			b.WriteString(strings.TrimSpace(res.ReadOutput))
			b.WriteString("\n```\n\n")
		} else {
			b.WriteString("Call read with the main resource ID before writing its description.\n\n")
		}
		b.WriteString(singleDescriptionInstruction(res.Kind))
		b.WriteString(" Call update_description immediately after writing the description.\n")
		return b.String()
	}

	b.WriteString("Process only the assigned resources below. You may inspect neighboring resources if needed, but update only assigned resources.\n\n")
	b.WriteString("For each assigned resource, call read with the resource_id, manually write a concise description, then call update_description immediately.\n\n")
	b.WriteString("Assigned resources:\n\n")
	for _, res := range resources {
		b.WriteString(fmt.Sprintf("- ID: %s\n  Name: %s\n  Kind: %s\n\n", res.ID, res.Name, res.Kind))
	}
	b.WriteString("## Guidelines\n\n")
	for _, line := range descriptionGuidelinesForKinds(resources) {
		b.WriteString("- ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

func singleDescriptionInstruction(kind domain.ResourceKind) string {
	switch kind {
	case domain.ResourceFunction, domain.ResourceMethod:
		return "Write 1-3 lines covering purpose, parameters, return values, and side effects."
	case domain.ResourceType, domain.ResourceNamedType:
		return "Write 1-3 lines covering what it represents, key fields or methods, and usage."
	case domain.ResourceInterface:
		return "Write 1-3 lines covering the contract and key methods."
	case domain.ResourceVariable:
		return "Write 1 line covering what it stores and why it exists."
	case domain.ResourceFile:
		return "Write 1 line covering the file's role in its package."
	case domain.ResourcePackage:
		return "Write 1-2 lines covering the package's overall purpose."
	case domain.ResourceDependency:
		return "Write 1 line covering what external dependency is referenced and why."
	default:
		return "Write a concise, accurate description of what this resource does."
	}
}

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
		lines = append(lines, "Functions/methods: 1-3 lines covering purpose, parameters, return values, and side effects")
		delete(seen, domain.ResourceFunction)
		delete(seen, domain.ResourceMethod)
	}
	for _, kind := range order {
		if !seen[kind] {
			continue
		}
		lines = append(lines, pluralDescriptionInstruction(kind))
	}
	lines = append(lines, "Be concise and accurate")
	return lines
}

func pluralDescriptionInstruction(kind domain.ResourceKind) string {
	switch kind {
	case domain.ResourceType, domain.ResourceNamedType:
		return "Structs/classes/types: 1-3 lines covering what they represent, key fields or methods, and usage"
	case domain.ResourceInterface:
		return "Interfaces/ABCs/protocols: 1-3 lines covering the contract and key methods"
	case domain.ResourceVariable:
		return "Variables: 1 line covering what they store and their purpose"
	case domain.ResourceFile:
		return "Files: 1 line covering each file's role in its package"
	case domain.ResourcePackage:
		return "Packages: 1-2 lines covering overall purpose"
	case domain.ResourceDependency:
		return "Dependencies: 1 line covering what external dependency is referenced and why"
	default:
		return fmt.Sprintf("%s resources: write concise, accurate descriptions", kind)
	}
}

func DescriptionsGenerationExecutorContent() string {
	return `---
name: descriptions-generation-executor
description: Generates descriptions for one assigned batch of undocumented topology resources
tools: read, grep, update_description
---

` + DescriptionsGenerationExecutorPrompt()
}
