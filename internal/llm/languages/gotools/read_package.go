package gotools

import (
	"encoding/json"
	"fmt"

	"ltp/internal/topology/golang"
)

type ReadPackage struct {
	mgr *golang.GoManager
}

func NewReadPackage(mgr *golang.GoManager) *ReadPackage {
	return &ReadPackage{mgr: mgr}
}

func (r *ReadPackage) Name() string {
	return "read_package"
}

func (r *ReadPackage) Description() string {
	return "Read a Go package's full context (files, functions, structs, interfaces, dependencies) from the project topology."
}

func (r *ReadPackage) Parameters() []Parameter {
	return []Parameter{
		{Name: "name", Type: "string", Description: "The package path (e.g. 'ltp/internal/cli', 'fmt')", Required: true},
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

	return FormatGoPackageContext(ctx), nil
}
