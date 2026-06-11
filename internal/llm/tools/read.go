package tools

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"aracne/internal/helper"
	"aracne/internal/topology"
)

type Read struct {
	mgr *topology.TopologyManager
}

func NewRead(mgr *topology.TopologyManager) *Read {
	return &Read{mgr: mgr}
}

func (r *Read) Name() string {
	return "read"
}

func (r *Read) Description() string {
	return "Read any resource by its ID (function, struct, interface, file, package, variable, etc.) and return its source code. Same behavior as `arac read {resource_id}`."
}

func (r *Read) Parameters() []Parameter {
	return []Parameter{
		{Name: "resource_id", Type: "string", Description: "The resource ID to read", Required: true},
	}
}

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

	topo, err := r.mgr.ReadAll()
	if err != nil {
		return "", fmt.Errorf("read topology: %w", err)
	}

	id := helper.NormalizeResourceID(params.ResourceID)

	res, ok := topo.Resources[id]
	if !ok {
		absPath, absErr := filepath.Abs(id)
		if absErr == nil {
			res, ok = topo.Resources[absPath]
		}
	}
	if !ok {
		return "", fmt.Errorf("resource %q not found in topology", params.ResourceID)
	}

	loc := res.Location
	entry, err := r.mgr.Cut(loc)
	if err != nil {
		return "", fmt.Errorf("read resource: %w", err)
	}

	name := filepath.Base(loc.Path)
	return fmt.Sprintf("%s\n%s", name, entry.Cut), nil
}
