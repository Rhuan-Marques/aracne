package tools

import (
	"encoding/json"
	"fmt"

	"aracne/internal/topogrep"
	"aracne/internal/topology"
)

// LLM tool to search code contents and return topology resource matches.
type Grep struct {
	mgr *topology.TopologyManager
}

// Creates a Grep tool for searching topology content with regex patterns.
func NewGrep(mgr *topology.TopologyManager) *Grep {
	return &Grep{mgr: mgr}
}

// Returns the tool name "grep".
func (g *Grep) Name() string {
	return "grep"
}

// Returns the tool description for grep: regex search returning path:line:match output with topology resource metadata.
func (g *Grep) Description() string {
	return "Search file contents with a regular expression. Returns path:line:match output, plus ResourceID and description when the matching line belongs to a topology resource."
}

// Returns parameter definitions for grep tool: pattern (required) and path (optional, defaults to current directory).
func (g *Grep) Parameters() []Parameter {
	return []Parameter{
		{Name: "pattern", Type: "string", Description: "Regular expression pattern to search for", Required: true},
		{Name: "path", Type: "string", Description: "File or directory to search (default '.')", Required: false},
	}
}

// Executes a regex search across files or directories, leveraging topology metadata when available.
func (g *Grep) Run(args json.RawMessage) (string, error) {
	var params struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if params.Path == "" {
		params.Path = "."
	}
	topo, err := g.mgr.ReadAll()
	if err != nil {
		topo = nil
	}
	matches, err := topogrep.Search(params.Pattern, params.Path, topo)
	if err != nil {
		return "", err
	}
	return topogrep.Format(matches), nil
}
