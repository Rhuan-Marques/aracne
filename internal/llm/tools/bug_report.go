package tools

import (
	"encoding/json"
	"fmt"

	"aracne/internal/topology"
)

type BugReport struct {
	mgr *topology.TopologyManager
}

func NewBugReport(mgr *topology.TopologyManager) *BugReport {
	return &BugReport{mgr: mgr}
}

func (b *BugReport) Name() string {
	return "bug_report"
}

func (b *BugReport) Description() string {
	return "Report a bug on a resource node. The bug starts in 'pending' state and will be triaged by the Bug Judge."
}

func (b *BugReport) Parameters() []Parameter {
	return []Parameter{
		{Name: "node_id", Type: "string", Description: "The resource ID of the node containing the bug", Required: true},
		{Name: "description", Type: "string", Description: "Clear description of the bug", Required: true},
	}
}

func (b *BugReport) Run(args json.RawMessage) (string, error) {
	var params struct {
		NodeID      string `json:"node_id"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	if params.NodeID == "" || params.Description == "" {
		return "", fmt.Errorf("missing required arguments: node_id, description")
	}

	bug, err := b.mgr.CreateBug(params.NodeID, params.Description)
	if err != nil {
		return "", fmt.Errorf("error reporting bug: %w", err)
	}

	return fmt.Sprintf("Bug reported successfully.\n  ID: %s\n  Node: %s\n  State: %s", bug.ID, bug.NodeID, bug.State), nil
}
