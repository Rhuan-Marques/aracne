package tools

import (
	"encoding/json"
	"fmt"

	"ltp/internal/topology"
)

type BugDismiss struct {
	mgr *topology.TopologyManager
}

func NewBugDismiss(mgr *topology.TopologyManager) *BugDismiss {
	return &BugDismiss{mgr: mgr}
}

func (b *BugDismiss) Name() string {
	return "bug_dismiss"
}

func (b *BugDismiss) Description() string {
	return "Mark a bug as 'dismissed' -- a false positive kept for reference. Dismissed bugs serve as examples for the Bug Judge."
}

func (b *BugDismiss) Parameters() []Parameter {
	return []Parameter{
		{Name: "bug_id", Type: "string", Description: "The bug ID to dismiss", Required: true},
	}
}

func (b *BugDismiss) Run(args json.RawMessage) (string, error) {
	var params struct {
		BugID string `json:"bug_id"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	if params.BugID == "" {
		return "", fmt.Errorf("missing required argument: bug_id")
	}

	if err := b.mgr.DismissBug(params.BugID); err != nil {
		return "", fmt.Errorf("error dismissing bug: %w", err)
	}

	return fmt.Sprintf("Bug %s dismissed.", params.BugID), nil
}
