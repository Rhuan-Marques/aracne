package tools

import (
	"encoding/json"
	"fmt"

	"github.com/Rhuan-Marques/aracne/internal/llm/toolapi"
	"github.com/Rhuan-Marques/aracne/internal/topology"
)

// LLM tool to dismiss a bug report without deleting it.
type BugDismiss struct {
	mgr *topology.TopologyManager
}

// Constructs a BugDismiss tool for temporarily hiding topology issues.
func NewBugDismiss(mgr *topology.TopologyManager) *BugDismiss {
	return &BugDismiss{mgr: mgr}
}

// Returns the tool name 'bug_dismiss'.
func (b *BugDismiss) Name() string {
	return "bug_dismiss"
}

// Returns the tool description explaining that dismissed bugs are false positives kept as examples for the Bug Judge.
func (b *BugDismiss) Description() string {
	return "Mark a bug as 'dismissed' -- a false positive kept for reference. Dismissed bugs serve as examples for the Bug Judge."
}

// Returns the required bug_id parameter definition for the bug_dismiss tool.
func (b *BugDismiss) Parameters() []toolapi.Parameter {
	return []toolapi.Parameter{
		{Name: "bug_id", Type: "string", Description: "The bug ID to dismiss", Required: true},
	}
}

// Parses bug_id argument and dismisses the specified bug via the manager.
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
