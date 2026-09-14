package tools

import (
	"encoding/json"
	"fmt"

	"github.com/Rhuan-Marques/aracne/internal/llm/toolapi"
	"github.com/Rhuan-Marques/aracne/internal/topology"
)

// LLM tool to permanently delete a bug report from the topology database.
type BugDelete struct {
	mgr *topology.TopologyManager
}

// Constructs a BugDelete tool for permanently removing topology issues.
func NewBugDelete(mgr *topology.TopologyManager) *BugDelete {
	return &BugDelete{mgr: mgr}
}

// Returns the tool name "bug_delete".
func (b *BugDelete) Name() string {
	return "bug_delete"
}

// Returns the description of the bug_delete tool: deletes bugs from the database after fixing or for removing false positives.
func (b *BugDelete) Description() string {
	return "Delete a bug from the database. Used by the Bug Solver after fixing, or by the Bug Judge for duplicate false positives."
}

// Returns the parameter schema for deleting a bug: bug_id (required string).
func (b *BugDelete) Parameters() []toolapi.Parameter {
	return []toolapi.Parameter{
		{Name: "bug_id", Type: "string", Description: "The bug ID to delete", Required: true},
	}
}

// Parses bug_id argument and deletes the specified bug from the manager.
func (b *BugDelete) Run(args json.RawMessage) (string, error) {
	var params struct {
		BugID string `json:"bug_id"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	if params.BugID == "" {
		return "", fmt.Errorf("missing required argument: bug_id")
	}

	if err := b.mgr.DeleteBug(params.BugID); err != nil {
		return "", fmt.Errorf("error deleting bug: %w", err)
	}

	return fmt.Sprintf("Bug %s deleted.", params.BugID), nil
}
