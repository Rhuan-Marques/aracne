package gotools

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"aracne/internal/topology/golang"
)

// Reads named type definitions from Go code via a GoManager instance
type ReadNamedType struct {
	mgr *golang.GoManager
}

// Constructor that creates a ReadNamedType tool instance for reading Go named types with topology context.
func NewReadNamedType(mgr *golang.GoManager) *ReadNamedType {
	return &ReadNamedType{mgr: mgr}
}

// Returns the tool name "read_named_type" for the ReadNamedType tool.
func (r *ReadNamedType) Name() string {
	return "read_named_type"
}

// Returns the tool description for reading a Go named type with its full context from the topology.
func (r *ReadNamedType) Description() string {
	return "Read a Go named type's full source code and its interconnected context (resources that use it, dependencies) from the project topology."
}

// Defines the "name" parameter for the ReadNamedType tool, accepting a Go named type name as required input.
func (r *ReadNamedType) Parameters() []Parameter {
	return []Parameter{
		{Name: "name", Type: "string", Description: "The named type name (e.g. 'HandlerFunc', 'Bytes')", Required: true},
	}
}

// Retrieves a Go named type's full source and context from topology by name, handling ambiguous results by listing all matches.
func (r *ReadNamedType) Run(args json.RawMessage) (string, error) {
	var params struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if params.Name == "" {
		return "", fmt.Errorf("missing required argument: name")
	}

	ctx, err := r.mgr.ReadNamedType(params.Name)
	if err == nil {
		return FormatGoNamedTypeContext(ctx), nil
	}

	ids, err := r.mgr.FindNamedTypesByName(params.Name)
	if err != nil {
		return "", fmt.Errorf("search named types: %w", err)
	}
	if len(ids) == 0 {
		return "", fmt.Errorf("named type %q not found in topology", params.Name)
	}
	if len(ids) > 1 {
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		var b strings.Builder
		b.WriteString(fmt.Sprintf("Multiple named types %q found:\n", params.Name))
		for _, id := range ids {
			b.WriteString(fmt.Sprintf("- %s\n", id))
		}
		return b.String(), nil
	}

	ctx, err = r.mgr.ReadNamedType(string(ids[0]))
	if err != nil {
		return "", fmt.Errorf("read named type: %w", err)
	}

	return FormatGoNamedTypeContext(ctx), nil
}
