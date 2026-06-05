package pythontools

import (
	"encoding/json"
	"fmt"

	"ltp/internal/topology/python"
)

type ReadDependency struct {
	mgr *python.PythonManager
}

func NewReadDependency(mgr *python.PythonManager) *ReadDependency {
	return &ReadDependency{mgr: mgr}
}

func (r *ReadDependency) Name() string {
	return "read_dependency"
}

func (r *ReadDependency) Description() string {
	return "Read a Python dependency's context (all functions and classes that reference it) from the project topology."
}

func (r *ReadDependency) Parameters() []Parameter {
	return []Parameter{
		{Name: "name", Type: "string", Description: "The dependency path (e.g. 'numpy', 'requests')", Required: true},
	}
}

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
