package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// LLM tool to list all open bug reports from the topology database.
type BugList struct {
	mgr *topology.TopologyManager
}

// Constructs a BugList tool for retrieving all topology issues.
func NewBugList(mgr *topology.TopologyManager) *BugList {
	return &BugList{mgr: mgr}
}

// Returns the tool name "bug_list" for the bug listing tool.
func (b *BugList) Name() string {
	return "bug_list"
}

// Returns the description for the bug_list tool: lists known bugs with optional filtering by node ID or state.
func (b *BugList) Description() string {
	return "List all known bugs. Optionally filter by node ID or state (pending, acknowledged, dismissed)."
}

// Returns tool parameters: optional node_id and state filters for querying bugs.
func (b *BugList) Parameters() []Parameter {
	return []Parameter{
		{Name: "node_id", Type: "string", Description: "Filter bugs by resource node ID", Required: false},
		{Name: "state", Type: "string", Description: "Filter by state: pending, acknowledged, dismissed", Required: false},
	}
}

// Executes bug listing by querying the bug manager with optional filters and formats results as a readable string.
func (b *BugList) Run(args json.RawMessage) (string, error) {
	var params struct {
		NodeID string `json:"node_id"`
		State  string `json:"state"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	bugs, err := b.mgr.ListBugs(params.NodeID, domain.BugState(params.State))
	if err != nil {
		return "", fmt.Errorf("error listing bugs: %w", err)
	}

	if len(bugs) == 0 {
		return "No bugs found.", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d bug(s):\n\n", len(bugs)))
	for _, bug := range bugs {
		sb.WriteString(fmt.Sprintf("[%s] %s\n", bug.State, bug.ID))
		sb.WriteString(fmt.Sprintf("  Node: %s\n", bug.NodeID))
		sb.WriteString(fmt.Sprintf("  Description: %s\n\n", bug.Description))
	}
	return sb.String(), nil
}
