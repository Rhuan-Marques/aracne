package gotools

import (
	"encoding/json"
	"fmt"
	"strings"

	"llm-topology/internal/topology/golang"
)

// Holds a GoManager reference and serves as the receiver for methods that list undocumented resources from the topology database. Used as a tool for the LLM agent to discover resources needing descriptions.
type ListUndocumented struct {
	mgr *golang.GoManager
}

// Creates a new ListUndocumented tool instance with the given GoManager. Returns a pointer to the initialized ListUndocumented struct.
func NewListUndocumented(mgr *golang.GoManager) *ListUndocumented {
	return &ListUndocumented{mgr: mgr}
}

// Returns the tool name "list_undocumented_resources" for MCP/agent tool registration. No parameters. Returns the string constant identifying this tool.
func (l *ListUndocumented) Name() string {
	return "list_undocumented_resources"
}

// Returns the description string for the ListUndocumented tool, explaining it lists all resources needing descriptions with their ID, name, and kind.
func (l *ListUndocumented) Description() string {
	return "List all resources that need descriptions. Returns each resource's ID, name, and kind. After receiving this list, dispatch descriptor sub-agents — one per resource — that each call read_resource_and_cut then update_description."
}

// Returns the parameter schema for the ListUndocumented tool, which takes no parameters (returns nil).
func (l *ListUndocumented) Parameters() []Parameter {
	return nil
}

// Lists all resources in the topology that lack descriptions, formatting output with instructions for sub-agents to generate descriptions.
func (l *ListUndocumented) Run(args json.RawMessage) (string, error) {
	topo, err := l.mgr.Generic().ReadAll()
	if err != nil {
		return "", fmt.Errorf("error reading topology: %w", err)
	}

	type entry struct {
		ID   string
		Name string
		Kind string
	}
	var entries []entry

	for id, res := range topo.Resources {
		if res.Description == "" {
			entries = append(entries, entry{
				ID:   id,
				Name: res.Name,
				Kind: string(res.Kind),
			})
		}
	}

	if len(entries) == 0 {
		return "All resources already have descriptions. Nothing to generate.", nil
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Found %d undocumented resources.\n\n", len(entries)))
	b.WriteString(`## INSTRUCTIONS

Dispatch a descriptor sub-agent for EACH resource below. Each sub-agent receives:
- A system prompt instructing it to generate a description
- Two exclusive tools: **read_resource_and_cut** and **update_description**

Each sub-agent workflow:
1. Call **read_resource_and_cut** with the resource's ID and resource_name
2. Read the returned source code and the type-specific instructions
3. Generate a concise description (1-3 lines for functions/structs/interfaces, 1 line for variables/files/packages)
4. Call **update_description** with the generated description (parameters: id, resource_name, description)
5. Return "done"

Process all resources below. Do not skip any.

`)
	b.WriteString("## Resources\n\n")
	for _, e := range entries {
		b.WriteString(fmt.Sprintf("  - ID: %s\n    Name: %s\n    Kind: %s\n\n", e.ID, e.Name, strings.ToUpper(e.Kind)))
	}

	return b.String(), nil
}
