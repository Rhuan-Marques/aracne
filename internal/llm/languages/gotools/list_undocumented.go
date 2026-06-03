package gotools

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"ltp/internal/helper"
	"ltp/internal/topology/domain"
	"ltp/internal/topology/golang"
)

// Holds a GoManager reference and serves as the receiver for methods that list undocumented resources from the topology database. Used as a tool for the LLM agent to discover resources needing descriptions.
type ListUndocumented struct {
	mgr     *golang.GoManager
	targets []domain.ResourceKind
}

// Creates a new ListUndocumented tool instance with the given GoManager. Returns a pointer to the initialized ListUndocumented struct.
func NewListUndocumented(mgr *golang.GoManager, targets ...[]domain.ResourceKind) *ListUndocumented {
	describeTargets := helper.DefaultDescribeTargets()
	if len(targets) > 0 {
		describeTargets = targets[0]
	}
	return &ListUndocumented{mgr: mgr, targets: describeTargets}
}

// Returns the tool name "list_undocumented_resources" for MCP/agent tool registration. No parameters. Returns the string constant identifying this tool.
func (l *ListUndocumented) Name() string {
	return "list_undocumented_resources"
}

// Returns the description string for the ListUndocumented tool, explaining it lists all resources needing descriptions with their ID, name, and kind.
func (l *ListUndocumented) Description() string {
	return "List targeted resources that still need descriptions. Returns each resource's ID, name, and kind for batching into description executor tasks."
}

// Returns the parameter schema for the ListUndocumented tool, which takes no parameters (returns nil).
func (l *ListUndocumented) Parameters() []Parameter {
	return nil
}

// Lists all resources in the topology that lack descriptions, formatting output for main-session batching.
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

	targetSet := helper.DescribeTargetSet(l.targets)
	for id, res := range topo.Resources {
		if res.Description == "" && targetSet[res.Kind] {
			entries = append(entries, entry{
				ID:   id,
				Name: res.Name,
				Kind: string(res.Kind),
			})
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Kind != entries[j].Kind {
			return entries[i].Kind < entries[j].Kind
		}
		if entries[i].Name != entries[j].Name {
			return entries[i].Name < entries[j].Name
		}
		return entries[i].ID < entries[j].ID
	})

	if len(entries) == 0 {
		return "All targeted resources already have descriptions. Nothing to generate.", nil
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Found %d undocumented resources for targets: %s.\n\n", len(entries), helper.FormatDescribeTargets(l.targets)))
	b.WriteString(`## Orchestration Guidance

The main session should split these resources into batches of at most 20 and assign each batch to a descriptions-generation-executor subagent. Do not assign the same resource ID to more than one active executor. After executor batches finish, call this tool again and retry any resources that are still listed.

`)
	b.WriteString("## Resources\n\n")
	for _, e := range entries {
		b.WriteString(fmt.Sprintf("  - ID: %s\n    Name: %s\n    Kind: %s\n\n", e.ID, e.Name, e.Kind))
	}

	return b.String(), nil
}
