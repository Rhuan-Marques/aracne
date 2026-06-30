package jstools

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"aracne/internal/helper"
	"aracne/internal/topology/domain"
	"aracne/internal/topology/javascript"
)

// Tool that lists JavaScript resources without descriptions, filtered by resource kind and processed in batches.
type NodeListNoDescription struct {
	mgr               *javascript.JavaScriptManager
	targets           []domain.ResourceKind
	batchSize         int
	filter            domain.ContextFilter
	includeNotVisible bool
}

// Creates a NodeListNoDescription tool for listing JavaScript resources without generating descriptions.
func NewNodeListNoDescription(mgr *javascript.JavaScriptManager, targets ...[]domain.ResourceKind) *NodeListNoDescription {
	describeTargets := helper.DefaultDescribeTargets()
	if len(targets) > 0 {
		describeTargets = targets[0]
	}
	return &NodeListNoDescription{mgr: mgr, targets: describeTargets, batchSize: helper.DefaultDescriptionBatchSize, filter: domain.DefaultContextFilter()}
}

// Sets the batch size for processing node lists, returning the receiver for chaining.
func (l *NodeListNoDescription) SetBatchSize(batchSize int) *NodeListNoDescription {
	if batchSize > 0 {
		l.batchSize = batchSize
	}
	return l
}

// SetVisibility configures the read context filter and include-not-visible flag
// used to skip resources the read tools would not render as a normal line.
func (l *NodeListNoDescription) SetVisibility(filter domain.ContextFilter, includeNotVisible bool) *NodeListNoDescription {
	l.filter = filter
	l.includeNotVisible = includeNotVisible
	return l
}

// Returns the command name "node_list_no_description".
func (l *NodeListNoDescription) Name() string {
	return "node_list_no_description"
}

// Returns the description explaining that NodeListNoDescription lists undocumented resources.
func (l *NodeListNoDescription) Description() string {
	return "List targeted resources that still need descriptions. Returns each resource's ID, name, and kind for batching into description executor tasks."
}

// Returns an empty parameter list for the node_list_no_description command.
func (l *NodeListNoDescription) Parameters() []Parameter {
	return nil
}

// Scans topology for undocumented JS/TS resources matching targets and formats them for batch assignment to description executors.
func (l *NodeListNoDescription) Run(args json.RawMessage) (string, error) {
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
		if res.Language != "" && res.Language != "javascript" && res.Language != "typescript" {
			continue
		}
		if helper.ShouldDescribe(res, targetSet, l.filter, l.includeNotVisible) {
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
	b.WriteString(fmt.Sprintf("## Orchestration Guidance\n\nThe main session should split these resources into batches of at most %d and assign each batch to a descriptions-generation-executor subagent. Do not assign the same resource ID to more than one active executor. After executor batches finish, call this tool again and retry any resources that are still listed.\n\n", l.batchSize))
	b.WriteString("## Resources\n\n")
	for _, e := range entries {
		b.WriteString(fmt.Sprintf("  - ID: %s\n    Name: %s\n    Kind: %s\n\n", e.ID, e.Name, e.Kind))
	}

	return b.String(), nil
}
