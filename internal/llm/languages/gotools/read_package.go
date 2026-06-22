package gotools

import (
	"encoding/json"
	"fmt"

	"aracne/internal/topology/golang"
)

// Reads package information from Go code via a GoManager instance
type ReadPackage struct {
	mgr *golang.GoManager
}

// Constructor that creates a ReadPackage tool instance for reading Go packages with topology context.
func NewReadPackage(mgr *golang.GoManager) *ReadPackage {
	return &ReadPackage{mgr: mgr}
}

// Returns the tool name "read_package" for LLM tool invocation.
func (r *ReadPackage) Name() string {
	return "read_package"
}

// Returns the human-readable description of the read_package tool for LLM consumption.
func (r *ReadPackage) Description() string {
	return "Read a Go package's full context (files, functions, structs, interfaces, dependencies) from the project topology."
}

// Returns the parameter schema for read_package: a required package name string.
func (r *ReadPackage) Parameters() []Parameter {
	return []Parameter{
		{Name: "name", Type: "string", Description: "The package path (e.g. 'aracne/internal/cli', 'fmt')", Required: true},
	}
}

// Executes read_package by parsing the package name argument and returning formatted topology context.
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
