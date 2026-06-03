package tools

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"ltp/internal/topology"
	"ltp/internal/topology/domain"
)

type WarningsList struct {
	mgr *topology.TopologyManager
}

func NewWarningsList(mgr *topology.TopologyManager) *WarningsList {
	return &WarningsList{mgr: mgr}
}

func (w *WarningsList) Name() string {
	return "warnings_list"
}

func (w *WarningsList) Description() string {
	return "List all outstanding topology warnings. Warnings track missing references (UseMissingNode), removed resources (NodeRemoved), and signature changes (SignatureChanged). Use optional filters to narrow by source ID, target ID, or warning kind."
}

func (w *WarningsList) Parameters() []Parameter {
	return []Parameter{
		{Name: "source_id", Type: "string", Description: "Filter warnings by source resource ID", Required: false},
		{Name: "target_id", Type: "string", Description: "Filter warnings by target resource ID", Required: false},
		{Name: "kind", Type: "string", Description: "Filter by warning kind: use_missing_node, node_removed, signature_changed", Required: false},
	}
}

func (w *WarningsList) Run(args json.RawMessage) (string, error) {
	var params struct {
		SourceID string `json:"source_id"`
		TargetID string `json:"target_id"`
		Kind     string `json:"kind"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	var kind domain.WarningKind
	if params.Kind != "" {
		kind = domain.WarningKind(params.Kind)
	}

	warnings, err := w.mgr.ListWarnings(params.SourceID, params.TargetID, kind)
	if err != nil {
		return "", fmt.Errorf("error reading warnings: %w", err)
	}

	if len(warnings) == 0 {
		return "No outstanding warnings.", nil
	}

	sort.SliceStable(warnings, func(i, j int) bool {
		if warnings[i].Kind != warnings[j].Kind {
			return warnings[i].Kind < warnings[j].Kind
		}
		return warnings[i].SourceID < warnings[j].SourceID
	})

	counts := make(map[domain.WarningKind]int)
	for _, warning := range warnings {
		counts[warning.Kind]++
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Found %d warning(s):\n\n", len(warnings)))
	b.WriteString("Summary:\n")
	for _, kind := range []domain.WarningKind{domain.WarnUseMissingNode, domain.WarnNodeRemoved, domain.WarnSignatureChanged} {
		if count := counts[kind]; count > 0 {
			b.WriteString(fmt.Sprintf("  - %s: %d\n", kind, count))
		}
	}
	b.WriteString("\nDetails:\n")
	for _, warning := range warnings {
		b.WriteString(fmt.Sprintf("  [%s] %s\n    source: %s", warning.Kind, warning.Message, warning.SourceID))
		if warning.TargetID != "" {
			b.WriteString(fmt.Sprintf("\n    target: %s", warning.TargetID))
		}
		b.WriteString("\n\n")
	}

	return b.String(), nil
}
