package gotools

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"ltp/internal/topology/golang"
)

// Tool implementation wrapping GoManager to expose the "read_struct" MCP/agent tool. Holds a reference to GoManager for looking up struct context by name.
type ReadStruct struct {
	mgr *golang.GoManager
}

// Creates a new ReadStruct tool instance. Takes a *GoManager as parameter. Returns a *ReadStruct initialized with the given manager for looking up struct context from the topology.
func NewReadStruct(mgr *golang.GoManager) *ReadStruct {
	return &ReadStruct{mgr: mgr}
}

// Returns the tool name "read_struct" for MCP/agent registration.
func (r *ReadStruct) Name() string {
	return "read_struct"
}

// Returns the description string for the read_struct MCP/agent tool.
func (r *ReadStruct) Description() string {
	return "Read a Go struct's full source code and its interconnected context (interfaces, methods, constructor, used types) from the project topology. Prefer this over 'read' when investigating a specific struct."
}

// Returns the parameter definition for read_struct (a single "name" parameter).
func (r *ReadStruct) Parameters() []Parameter {
	return []Parameter{
		{Name: "name", Type: "string", Description: "The struct name (e.g. 'TopologyManager', 'ReadFunction')", Required: true},
	}
}

// Executes the read_struct tool: unmarshals a JSON name argument, looks up the struct via GoManager, and returns a formatted context with source cut and all relationships. Supports disambiguation when multiple structs share the same name.
func (r *ReadStruct) Run(args json.RawMessage) (string, error) {
	var params struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if params.Name == "" {
		return "", fmt.Errorf("missing required argument: name")
	}

	ctx, err := r.mgr.ReadStruct(params.Name)
	if err == nil {
		return FormatGoStructContext(ctx), nil
	}

	ids, err := r.mgr.FindStructsByName(params.Name)
	if err != nil {
		return "", fmt.Errorf("search structs: %w", err)
	}
	if len(ids) == 0 {
		return "", fmt.Errorf("struct %q not found in topology", params.Name)
	}
	if len(ids) > 1 {
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		var b strings.Builder
		b.WriteString(fmt.Sprintf("Multiple structs named %q found:\n", params.Name))
		for _, id := range ids {
			b.WriteString(fmt.Sprintf("- %s\n", id))
		}
		return b.String(), nil
	}

	ctx, err = r.mgr.ReadStruct(string(ids[0]))
	if err != nil {
		return "", fmt.Errorf("read struct: %w", err)
	}

	return FormatGoStructContext(ctx), nil
}
