package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/lazydesc"
	"github.com/Rhuan-Marques/aracne/internal/topogrep"
	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// LLM tool to search code contents and return topology resource matches.
type Grep struct {
	mgr *topology.TopologyManager
	// lazy describes the nodes a search found and could not describe. Nil is the
	// switched-off state and every call on it is a no-op.
	lazy *lazydesc.Filler
}

// Creates a Grep tool for searching topology content with regex patterns.
func NewGrep(mgr *topology.TopologyManager) *Grep {
	return &Grep{mgr: mgr, lazy: lazydesc.New(mgr, helper.LoadConfig(helper.ConfigPath(mgr.DbPath())), "")}
}

// WithFiller replaces the lazy-description filler, which is how a test installs a generator
// that does not need an API key.
func (g *Grep) WithFiller(f *lazydesc.Filler) *Grep {
	g.lazy = f
	return g
}

// Returns the tool name "grep".
func (g *Grep) Name() string {
	return "grep"
}

// Returns the tool description for grep.
func (g *Grep) Description() string {
	return "Regex search over topology node names, node descriptions and file contents, " +
		"ranked in that order. Descriptions are searchable only here, so plain English " +
		"finds a node whose code never says it. Returns `path:line:match`, naming the " +
		"enclosing node above its matches -- which often answers the question with no " +
		"follow-up read. Results are capped; the reply says what was withheld."
}

// Returns parameter definitions for the grep tool.
func (g *Grep) Parameters() []Parameter {
	return []Parameter{
		{Name: "pattern", Type: "string", Description: "Regex to match", Required: true},
		{Name: "path", Type: "string", Description: "File or directory to search (default '.')", Required: false},
		{Name: "glob", Type: "string", Description: "Filename glob, e.g. '*.go', '**/*_test.ts'", Required: false},
		{Name: "type", Type: "string", Description: "Language filter: go, py, js, ts, rust, java, c, cpp, md, json, yaml, toml, sh", Required: false},
		{Name: "case_insensitive", Type: "boolean", Description: "Match case-insensitively", Required: false},
		{Name: "output_mode", Type: "string", Description: "'content' (default) | 'files_with_matches' | 'count'", Required: false},
		{Name: "head_limit", Type: "integer", Description: "Max matching lines (default 200; -1 = unlimited)", Required: false},
		{Name: "before", Type: "integer", Description: "Context lines before each match", Required: false},
		{Name: "after", Type: "integer", Description: "Context lines after each match", Required: false},
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
		Globs:      splitGlobs(params.Glob),
		Type:       params.Type,
		IgnoreCase: params.CaseInsensitive,
		Mode:       topogrep.OutputMode(params.OutputMode),
		Before:     params.Before,
		After:      params.After,
	}
	cfg := helper.LoadConfig(helper.ConfigPath(g.mgr.DbPath()))
	opt.DescriptionKinds, opt.LineRange = grepConfig(cfg, topo)
	if params.HeadLimit != nil {
		opt.HeadLimit = *params.HeadLimit
	}

	res, err := topogrep.SearchWith(opt, topo)
	if err != nil {
		return "", err
	}
	// A node found by its name or by a line in its body gets its description written now, so
	// the header this result is about to print carries one. See lazydesc.Filler.FillSearch.
	g.lazy.FillSearch(topo, res)
	out := topogrep.FormatResult(res, opt)
	// The over-serve ceiling this tool shares with every other surface that answers in
	// something else's place. Past it the node rows and headers are no longer what makes a
	// topology search worth having, so what a plain grep would have printed goes out instead.
	if topogrep.HasTextualMatch(res) &&
		!cfg.WithinOverserve(out, topogrep.RawBytes(res, opt), helper.OverserveSearchFree) {
		out = topogrep.FormatResult(topogrep.WithoutTopology(res), opt)
	}
	// Always a message, never "": an empty tool result reads as a broken tool rather than
	// as an honest "nothing matched".
	return out, nil
}

// splitGlobs turns the tool's single glob parameter into the list topogrep takes. A caller
// may still pass several, comma-separated, the way ripgrep's own `-g` is repeatable.
func splitGlobs(glob string) []string {
	var out []string
	for _, g := range strings.Split(glob, ",") {
		if g = strings.TrimSpace(g); g != "" {
			out = append(out, g)
		}
	}
	return out
}

// grepConfig reads the project settings the search honours: grep.description_kinds, which
// limits the kinds whose description may match, and the identification mode.
//
// SCAN.IGNORE IS NOT AMONG THEM, THOUGH IT USED TO BE. It is the scanner's rule, and it
// answers a different question -- what earns a place in the topology, not what exists on
// disk. Borrowing it here meant a directory deliberately left out of the graph could not be
// grepped either, which is the opposite of what a caller wants from the one tool that reads
// raw files. Pruning is the .gitignore hierarchy's job now; see topogrep/gitignore.go.
//
// A nil kind slice means "not configured" and lets topogrep apply its defaults, so a
// missing or unreadable config still gets description matching rather than silently
// losing it.
func grepConfig(cfg *helper.Config, topo *domain.Topology) ([]domain.ResourceKind, bool) {
	if cfg == nil {
		return nil, false
	}
	return cfg.Grep.DescriptionKinds, cfg.LineRangeIdentification()
}
