package tools

import (
	"encoding/json"
	"fmt"

	"aracne/internal/topology"
)

type BugDelete struct {
	mgr *topology.TopologyManager
}

func NewBugDelete(mgr *topology.TopologyManager) *BugDelete {
	return &BugDelete{mgr: mgr}
}

func (b *BugDelete) Name() string {
	return "bug_delete"
}

func (b *BugDelete) Description() string {
	return "Delete a bug from the database. Used by the Bug Solver after fixing, or by the Bug Judge for duplicate false positives."
}

func (b *BugDelete) Parameters() []Parameter {
	return []Parameter{
		{Name: "bug_id", Type: "string", Description: "The bug ID to delete", Required: true},
	}
}

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
