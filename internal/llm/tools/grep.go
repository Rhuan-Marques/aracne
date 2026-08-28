package tools

import (
	"encoding/json"
	"fmt"

	"aracne/internal/helper"
	"aracne/internal/topogrep"
	"aracne/internal/topology"
	"aracne/internal/topology/domain"
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

// Returns the tool description for grep.
func (g *Grep) Description() string {
	return "Search file contents with a regular expression. Returns `path:line:match`, " +
		"with the enclosing topology resource named once above its matches. Narrow with " +
		"`glob`/`type`/`path` and use `output_mode` to get just filenames or counts; " +
		"results are capped, and the reply says how many were withheld."
}

// Returns parameter definitions for the grep tool.
func (g *Grep) Parameters() []Parameter {
	return []Parameter{
		{Name: "pattern", Type: "string", Description: "Regular expression pattern to search for", Required: true},
		{Name: "path", Type: "string", Description: "File or directory to search (default '.')", Required: false},
		{Name: "glob", Type: "string", Description: "Filename glob to restrict the search, e.g. '*.go' or '**/*_test.ts'", Required: false},
		{Name: "type", Type: "string", Description: "Language shorthand to restrict the search: go, py, js, ts, rust, java, c, cpp, md, json, yaml, toml, sh", Required: false},
		{Name: "case_insensitive", Type: "boolean", Description: "Match case-insensitively", Required: false},
		{Name: "output_mode", Type: "string", Description: "'content' (default, matching lines), 'files_with_matches' (paths only), or 'count' (per-file counts)", Required: false},
		{Name: "head_limit", Type: "integer", Description: "Maximum matching lines to return (default 200). Use -1 for no limit.", Required: false},
		{Name: "before", Type: "integer", Description: "Lines of context to show before each match", Required: false},
		{Name: "after", Type: "integer", Description: "Lines of context to show after each match", Required: false},
	}
}

// Executes a regex search across files or directories, leveraging topology metadata when available.
func (g *Grep) Run(args json.RawMessage) (string, error) {
	var params struct {
		Pattern         string `json:"pattern"`
		Path            string `json:"path"`
		Glob            string `json:"glob"`
		Type            string `json:"type"`
		CaseInsensitive bool   `json:"case_insensitive"`
		OutputMode      string `json:"output_mode"`
		HeadLimit       *int   `json:"head_limit"`
		Before          int    `json:"before"`
		After           int    `json:"after"`
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

	opt := topogrep.Options{
		Pattern:    params.Pattern,
		Root:       params.Path,
		Glob:       params.Glob,
		Type:       params.Type,
		IgnoreCase: params.CaseInsensitive,
		Mode:       topogrep.OutputMode(params.OutputMode),
		Before:     params.Before,
		After:      params.After,
		Ignore:     grepIgnore(g.mgr, topo),
	}
	if params.HeadLimit != nil {
		opt.HeadLimit = *params.HeadLimit
	}

	res, err := topogrep.SearchWith(opt, topo)
	if err != nil {
		return "", err
	}
	// Always a message, never "": an empty tool result reads as a broken tool rather than
	// as an honest "nothing matched".
	return topogrep.FormatResult(res, opt), nil
}

// grepIgnore builds the project's scan.ignore matcher so search honours the same
// exclusions as the scanner. Without it a search descends into build output the project
// has explicitly told aracne to skip.
func grepIgnore(mgr *topology.TopologyManager, topo *domain.Topology) *domain.IgnoreMatcher {
	root := ""
	if topo != nil {
		root = topo.Root
	}
	if root == "" {
		return nil
	}
	cfg := helper.LoadConfig(helper.ConfigPath(mgr.DbPath()))
	if cfg == nil {
		return nil
	}
	return domain.BuildIgnoreMatcher(root, cfg.Scan.Ignore)
}
