package pythontools

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"ltp/internal/helper"
	"ltp/internal/topology/domain"
	"ltp/internal/topology/python"
)

type ListUndocumented struct {
	mgr     *python.PythonManager
	targets []domain.ResourceKind
}

func NewListUndocumented(mgr *python.PythonManager, targets ...[]domain.ResourceKind) *ListUndocumented {
	describeTargets := helper.DefaultDescribeTargets()
	if len(targets) > 0 {
		describeTargets = targets[0]
	}
	return &ListUndocumented{mgr: mgr, targets: describeTargets}
}

func (l *ListUndocumented) Name() string {
	return "list_undocumented_resources"
}

func (l *ListUndocumented) Description() string {
	return "List targeted resources that still need descriptions. Returns each resource's ID, name, and kind for batching into description executor tasks."
}

func (l *ListUndocumented) Parameters() []Parameter {
	return nil
}

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
