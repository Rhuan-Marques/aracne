package tools

import (
	"encoding/json"
	"fmt"

	"aracne/internal/topology"
)

// LLM tool to report a bug detected in the codebase to the topology database.
type BugReport struct {
	mgr *topology.TopologyManager
}

// Constructs a BugReport tool for logging new topology issues.
func NewBugReport(mgr *topology.TopologyManager) *BugReport {
	return &BugReport{mgr: mgr}
}

// Returns the tool name "bug_report".
func (b *BugReport) Name() string {
	return "bug_report"
}

// Returns the description for the bug_report tool: reports a bug on a resource in pending state for triage.
func (b *BugReport) Description() string {
	return "Report a bug on a resource node. Target the root of the issue: file the bug on the node whose code must change to fix it (the root cause), not a node that merely exhibits the symptom. The bug starts in 'pending' state and will be triaged by the Bug Judge."
}

// Defines bug report parameters: node_id and description, both required strings.
func (b *BugReport) Parameters() []Parameter {
	return []Parameter{
		{Name: "node_id", Type: "string", Description: "The resource ID of the node at the root of the bug — the resource whose code must change to fix it, not a node that merely exhibits the symptom", Required: true},
		{Name: "description", Type: "string", Description: "Clear description of the bug", Required: true},
	}
}

// Parses arguments, validates node_id and description, creates a bug via manager, and returns confirmation with bug ID, node, and state.
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
