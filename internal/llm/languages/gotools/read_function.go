package gotools

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"aracne/internal/topology/golang"
)

// A tool struct wrapping GoManager that provides the read_function tool for MCP/agent integration, enabling function source lookup with interconnected context.
type ReadFunction struct {
	mgr *golang.GoManager
}

// Creates a new ReadFunction tool instance. Takes a *GoManager as parameter. Returns a *ReadFunction initialized with the given manager for looking up function context from the topology.
func NewReadFunction(mgr *golang.GoManager) *ReadFunction {
	return &ReadFunction{mgr: mgr}
}

// Returns the tool name "read_function" used to register the ReadFunction tool in the tool registry.
func (r *ReadFunction) Name() string {
	return "read_function"
}

// Returns the help text describing the ReadFunction tool, explaining it reads a Go function's source code with interconnected context.
func (r *ReadFunction) Description() string {
	return "Read a Go function's full source code and its interconnected context (called functions, structs, interfaces, external variables) from the project topology. Prefer this over 'read' when investigating a specific function."
}

// Returns the parameter definitions for the ReadFunction tool, accepting a required "name" string for the function to look up.
func (r *ReadFunction) Parameters() []Parameter {
	return []Parameter{
		{Name: "name", Type: "string", Description: "The function name (e.g. 'ReadFunction', 'New', 'Scan')", Required: true},
	}
}

// Executes the read_function MCP tool: looks up a function by name via GoManager, returns a formatted GoFunctionContext on single match, lists ambiguous matches if multiple are found, or returns an error if not found.
func (r *ReadFunction) Run(args json.RawMessage) (string, error) {
	var params struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if params.Name == "" {
		return "", fmt.Errorf("missing required argument: name")
	}

	ctx, err := r.mgr.ReadFunction(params.Name)
	if err == nil {
		return FormatGoFunctionContext(ctx), nil
	}

	ids, err := r.mgr.FindFunctionsByName(params.Name)
	if err != nil {
		return "", fmt.Errorf("search functions: %w", err)
	}
	if len(ids) == 0 {
		return "", fmt.Errorf("function %q not found in topology", params.Name)
	}
	if len(ids) > 1 {
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		var b strings.Builder
		b.WriteString(fmt.Sprintf("Multiple functions named %q found:\n", params.Name))
		for _, id := range ids {
			b.WriteString(fmt.Sprintf("- %s\n", id))
		}
		return b.String(), nil
	}

	ctx, err = r.mgr.ReadFunction(string(ids[0]))
	if err != nil {
		return "", fmt.Errorf("read function: %w", err)
	}

	return FormatGoFunctionContext(ctx), nil
}
