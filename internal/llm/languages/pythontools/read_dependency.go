package pythontools

import (
	"encoding/json"
	"fmt"

	"aracne/internal/topology/python"
)

// Handler for reading and resolving Python module dependencies from the topology.
type ReadDependency struct {
	mgr *python.PythonManager
}

// Creates a ReadDependency handler for reading Python dependencies.
func NewReadDependency(mgr *python.PythonManager) *ReadDependency {
	return &ReadDependency{mgr: mgr}
}

// Returns the tool name 'read_dependency'.
func (r *ReadDependency) Name() string {
	return "read_dependency"
}

// Returns the description of the read_dependency tool.
func (r *ReadDependency) Description() string {
	return "Read a Python dependency's context (all functions and classes that reference it) from the project topology."
}

// Returns parameter schema: a required 'name' string for the dependency path.
func (r *ReadDependency) Parameters() []Parameter {
	return []Parameter{
		{Name: "name", Type: "string", Description: "The dependency path (e.g. 'numpy', 'requests')", Required: true},
	}
}

// Executes the read_dependency tool by parsing arguments, validating the name, reading dependency context, and formatting the output.
func (r *ReadDependency) Run(args json.RawMessage) (string, error) {
	var params struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if params.Name == "" {
		return "", fmt.Errorf("missing required argument: name")
	}

	ctx, err := r.mgr.ReadDependency(params.Name)
	if err != nil {
		return "", fmt.Errorf("read dependency: %w", err)
	}

	return FormatPythonDependencyContext(ctx), nil
}
