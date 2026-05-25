package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"llm-topology/internal/topology"
	"llm-topology/internal/topology/domain"
)

type ReadResourceAndCut struct {
	mgr *topology.TopologyManager
}

func NewReadResourceAndCut(mgr *topology.TopologyManager) *ReadResourceAndCut {
	return &ReadResourceAndCut{mgr: mgr}
}

func (r *ReadResourceAndCut) Name() string {
	return "read_resource_and_cut"
}

func (r *ReadResourceAndCut) Description() string {
	return "Read a resource's full source code and receive specific instructions for generating its description. Use this to get the code you need to describe."
}

func (r *ReadResourceAndCut) Parameters() []Parameter {
	return []Parameter{
		{Name: "id", Type: "string", Description: "The resource ID (e.g. 'llm-topology/main.go:main')", Required: true},
		{Name: "resource_name", Type: "string", Description: "The resource type: Function, Struct, Interface, ExternalVar, File, or Package", Required: true},
	}
}

func (r *ReadResourceAndCut) Run(args json.RawMessage) (string, error) {
	var params struct {
		ID           string `json:"id"`
		ResourceName string `json:"resource_name"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if params.ID == "" || params.ResourceName == "" {
		return "", fmt.Errorf("missing required arguments: id, resource_name")
	}

	rn := domain.ResourceName(params.ResourceName)
	entry, err := r.mgr.ReadResourceAndCut(params.ID, rn)
	if err != nil {
		return "", fmt.Errorf("read resource: %w", err)
	}

	var b strings.Builder
	b.WriteString("```go\n")
	b.WriteString(entry.Cut)
	b.WriteString("\n```\n\n")

	b.WriteString(buildResourceInstructions(rn))

	return b.String(), nil
}

func buildResourceInstructions(rn domain.ResourceName) string {
	switch rn {
	case domain.FUNCTION_RESOURCE:
		return "Generate a concise description (1-3 lines) for this function. Include its main purpose, what parameters it takes, what it returns, and any notable behavior or side effects. Write ONLY the description text, nothing else."
	case domain.STRUCT_RESOURCE:
		return "Generate a concise description (1-3 lines) for this struct. Include what it represents, its key fields and their purpose, and how it is typically used. If a constructor exists, mention its relationship. Write ONLY the description text, nothing else."
	case domain.INTERFACE_RESOURCE:
		return "Generate a concise description (1-3 lines) for this interface. Include what contract it defines, what behavior it abstracts, and the key methods it requires. Write ONLY the description text, nothing else."
	case domain.EXTERNAL_VAR_RESOURCE:
		return "Generate a concise description (1 line) for this external variable. Include what it stores and its purpose in the codebase. Write ONLY the description text, nothing else."
	case domain.FILE_RESOURCE:
		return "Generate a concise description (1 line) for this file. Include what it contains and its role within its package. Write ONLY the description text, nothing else."
	case domain.PACKAGE_RESOURCE:
		return "Generate a concise description (1-2 lines) for this package. Include its overall purpose and what functionality it provides. Write ONLY the description text, nothing else."
	default:
		return "Generate a concise description for this resource. Write ONLY the description text, nothing else."
	}
}
