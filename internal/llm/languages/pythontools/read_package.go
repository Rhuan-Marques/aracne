package pythontools

import (
	"encoding/json"
	"fmt"

	"ltp/internal/topology/python"
)

type ReadPackage struct {
	mgr *python.PythonManager
}

func NewReadPackage(mgr *python.PythonManager) *ReadPackage {
	return &ReadPackage{mgr: mgr}
}

func (r *ReadPackage) Name() string {
	return "read_package"
}

func (r *ReadPackage) Description() string {
	return "Read a Python package's full context (modules, functions, classes, dependencies) from the project topology."
}

func (r *ReadPackage) Parameters() []Parameter {
	return []Parameter{
		{Name: "name", Type: "string", Description: "The package path (e.g. 'package.subpackage')", Required: true},
	}
}

func (r *ReadPackage) Run(args json.RawMessage) (string, error) {
	var params struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if params.Name == "" {
		return "", fmt.Errorf("missing required argument: name")
	}

	ctx, err := r.mgr.ReadPackage(params.Name)
	if err != nil {
		return "", fmt.Errorf("read package: %w", err)
	}

	return FormatPythonPackageContext(ctx), nil
}
