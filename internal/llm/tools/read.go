package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"aracne/internal/helper"
	"aracne/internal/topology"
)

// Struct that holds a topology manager for reading resources by ID.
type Read struct {
	mgr *topology.TopologyManager
}

// Creates a Read tool for fetching topology resources by ID.
func NewRead(mgr *topology.TopologyManager) *Read {
	return &Read{mgr: mgr}
}

// Returns the tool name "read".
func (r *Read) Name() string {
	return "read"
}

// Returns the full description of the Read tool explaining its ability to fetch resource code or raw file text.
func (r *Read) Description() string {
	return "Read any resource by its ID (function, struct, interface, file, package, variable, etc.) and return its source code. Also accepts a file path (absolute or relative) and falls back to returning the raw text of any file, even one not in the topology. Same behavior as `arac read {resource_id}`."
}

// Returns parameter definitions for read tool: resource_id or file path (required).
func (r *Read) Parameters() []Parameter {
	return []Parameter{
		{Name: "resource_id", Type: "string", Description: "The resource ID, or a file path (absolute or relative), to read", Required: true},
	}
}

// Executes a resource read by ID, returning source code from topology or raw file content with fallback to filesystem lookup.
func (r *Read) Run(args json.RawMessage) (string, error) {
	var params struct {
		ResourceID string `json:"resource_id"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if params.ResourceID == "" {
		return "", fmt.Errorf("missing required argument: resource_id")
	}

	id := helper.NormalizeResourceID(params.ResourceID)

	// The topology may not be readable (e.g. not scanned yet); we still fall
	// back to reading the input as a raw file below, mirroring read_file and
	// `arac read`.
	root := ""
	topo, topoErr := r.mgr.ReadAll()
	if topoErr == nil {
		root = topo.Root
	}

	candidates := readPathCandidates(id, root)

	if topoErr == nil {
		for _, cand := range candidates {
			res, ok := topo.Resources[cand]
			if !ok {
				continue
			}
			loc := res.Location
			entry, err := r.mgr.Cut(loc)
			if err != nil {
				return "", fmt.Errorf("read resource: %w", err)
			}
			name := filepath.Base(loc.Path)
			return fmt.Sprintf("%s\n%s", name, entry.Cut), nil
		}
	}

	// Not a known topology resource: read the input as a raw text file. Try
	// each candidate path so a relative path resolves against both the working
	// directory and the topology root.
	maxSize := helper.LoadConfig(helper.ConfigPath(r.mgr.DbPath())).EffectiveMaxFileSize()
	for _, cand := range candidates {
		if info, err := os.Stat(cand); err == nil && !info.IsDir() {
			return helper.ReadRawFile(cand, maxSize)
		}
	}

	return "", fmt.Errorf("resource %q not found in topology", params.ResourceID)
}

// readPathCandidates returns the input followed by alternative path forms to try
// when resolving a file: its absolute form, and (for a relative input) its form
// joined onto the topology root. Duplicates are removed while preserving order.
func readPathCandidates(id, root string) []string {
	candidates := []string{id}
	seen := map[string]bool{id: true}
	add := func(p string) {
		if p != "" && !seen[p] {
			seen[p] = true
			candidates = append(candidates, p)
		}
	}
	if abs, err := filepath.Abs(id); err == nil {
		add(abs)
	}
	if root != "" && !filepath.IsAbs(id) {
		add(filepath.Join(root, id))
	}
	return candidates
}
