package gotools

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"llm-topology/internal/llm/tools"
	"llm-topology/internal/topology/domain"
	"llm-topology/internal/topology/golang"
)

type ListWarnings struct {
	mgr *golang.GoManager
}

func NewListWarnings(mgr *golang.GoManager) *ListWarnings {
	return &ListWarnings{mgr: mgr}
}

func (l *ListWarnings) Name() string {
	return "list_warnings"
}

func (l *ListWarnings) Description() string {
	return "List all outstanding topology warnings. Warnings track missing references (UseMissingNode), removed resources (NodeRemoved), and signature changes (SignatureChanged). Use optional filters to narrow by source ID, target ID, or warning kind."
}

func (l *ListWarnings) Parameters() []tools.Parameter {
	return []tools.Parameter{
		{Name: "source_id", Type: "string", Description: "Filter warnings by source resource ID", Required: false},
		{Name: "target_id", Type: "string", Description: "Filter warnings by target resource ID", Required: false},
		{Name: "kind", Type: "string", Description: "Filter by warning kind: use_missing_node, node_removed, signature_changed", Required: false},
	}
}

func (l *ListWarnings) Run(args json.RawMessage) (string, error) {
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

	warnings, err := l.mgr.Generic().ListWarnings(params.SourceID, params.TargetID, kind)
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
	for _, w := range warnings {
		counts[w.Kind]++
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Found %d warning(s):\n\n", len(warnings)))
	b.WriteString("Summary:\n")
	for _, k := range []domain.WarningKind{domain.WarnUseMissingNode, domain.WarnNodeRemoved, domain.WarnSignatureChanged} {
		if c := counts[k]; c > 0 {
			b.WriteString(fmt.Sprintf("  - %s: %d\n", k, c))
		}
	}
	b.WriteString("\nDetails:\n")
	for _, w := range warnings {
		b.WriteString(fmt.Sprintf("  [%s] %s\n    source: %s", w.Kind, w.Message, w.SourceID))
		if w.TargetID != "" {
			b.WriteString(fmt.Sprintf("\n    target: %s", w.TargetID))
		}
		b.WriteString("\n\n")
	}

	b.WriteString("To resolve warnings:\n")
	b.WriteString("  - UseMissingNode / NodeRemoved: if the target was recently created or restored, the connection auto-resolves on edit. Otherwise, update or remove the reference in the source.\n")
	b.WriteString("  - SignatureChanged: verify the caller's call site matches the new signature and update if needed.\n")

	return b.String(), nil
}
