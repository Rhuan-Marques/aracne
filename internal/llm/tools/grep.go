package tools

import (
	"encoding/json"
	"fmt"

	"ltp/internal/topogrep"
	"ltp/internal/topology"
)

type Grep struct {
	mgr *topology.TopologyManager
}

func NewGrep(mgr *topology.TopologyManager) *Grep {
	return &Grep{mgr: mgr}
}

func (g *Grep) Name() string {
	return "grep"
}

func (g *Grep) Description() string {
	return "Search file contents with a regular expression. Returns path:line:match output, plus ResourceID and description when the matching line belongs to a topology resource."
}

func (g *Grep) Parameters() []Parameter {
	return []Parameter{
		{Name: "pattern", Type: "string", Description: "Regular expression pattern to search for", Required: true},
		{Name: "path", Type: "string", Description: "File or directory to search (default '.')", Required: false},
	}
}

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
