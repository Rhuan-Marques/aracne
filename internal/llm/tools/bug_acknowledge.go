package tools

import (
	"encoding/json"
	"fmt"

	"aracne/internal/topology"
)

// LLM tool for acknowledging reported bugs in the codebase.
type BugAcknowledge struct {
	mgr *topology.TopologyManager
}

// Constructs a BugAcknowledge tool for marking topology issues as acknowledged.
func NewBugAcknowledge(mgr *topology.TopologyManager) *BugAcknowledge {
	return &BugAcknowledge{mgr: mgr}
}

// Returns the tool identifier "bug_acknowledge".
func (b *BugAcknowledge) Name() string {
	return "bug_acknowledge"
}

// Returns the user-facing description for the bug acknowledgment tool.
func (b *BugAcknowledge) Description() string {
	return "Mark a bug as 'acknowledged' -- a confirmed real bug that needs fixing."
}

// Returns the parameter schema for acknowledging a bug: bug_id (required string).
func (b *BugAcknowledge) Parameters() []Parameter {
	return []Parameter{
		{Name: "bug_id", Type: "string", Description: "The bug ID to acknowledge", Required: true},
	}
}

// Executes the bug acknowledge command by parsing bug_id and calling the manager to mark it acknowledged.
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
