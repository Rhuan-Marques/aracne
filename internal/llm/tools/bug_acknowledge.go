package tools

import (
	"encoding/json"
	"fmt"

	"llm-topology/internal/topology"
)

type BugAcknowledge struct {
	mgr *topology.TopologyManager
}

func NewBugAcknowledge(mgr *topology.TopologyManager) *BugAcknowledge {
	return &BugAcknowledge{mgr: mgr}
}

func (b *BugAcknowledge) Name() string {
	return "bug_acknowledge"
}

func (b *BugAcknowledge) Description() string {
	return "Mark a bug as 'acknowledged' -- a confirmed real bug that needs fixing."
}

func (b *BugAcknowledge) Parameters() []Parameter {
	return []Parameter{
		{Name: "bug_id", Type: "string", Description: "The bug ID to acknowledge", Required: true},
	}
}

func (b *BugAcknowledge) Run(args json.RawMessage) (string, error) {
	var params struct {
		BugID string `json:"bug_id"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	if params.BugID == "" {
		return "", fmt.Errorf("missing required argument: bug_id")
	}

	if err := b.mgr.AcknowledgeBug(params.BugID); err != nil {
		return "", fmt.Errorf("error acknowledging bug: %w", err)
	}

	return fmt.Sprintf("Bug %s acknowledged.", params.BugID), nil
}
