package gotools

import (
	"encoding/json"
	"fmt"
	"strings"

	"ltp/internal/topology/domain"
	"ltp/internal/topology/golang"
)

// MCP/agent tool that retrieves a resource's source code cut and type-specific instructions for generating its description.
type ReadResourceAndCut struct {
	mgr *golang.GoManager
}

// Constructs a new ReadResourceAndCut tool instance with the given GoManager.
func NewReadResourceAndCut(mgr *golang.GoManager) *ReadResourceAndCut {
	return &ReadResourceAndCut{mgr: mgr}
}

// Returns the tool name "read_resource_and_cut" for MCP/agent tool registration. No parameters. Returns the string constant identifying this tool.
func (r *ReadResourceAndCut) Name() string {
	return "read_resource_and_cut"
}

// Returns the description string for the ReadResourceAndCut MCP/agent tool.
func (r *ReadResourceAndCut) Description() string {
	return "Get a resource's source code cut and specific instructions for generating its description"
}

// Returns the parameter definitions for read_resource_and_cut (id and resource_name).
func (r *ReadResourceAndCut) Parameters() []Parameter {
	return []Parameter{
		{Name: "id", Type: "string", Description: "Resource ID", Required: true},
		{Name: "resource_name", Type: "string", Description: "Resource kind: function, method, type, named_type, interface, variable, file, or package", Required: true},
	}
}

// Executes the ReadResourceAndCut tool: unmarshals JSON args (id, resource_name), reads the resource source cut from the GoManager, and returns it with type-specific description instructions.
func (r *ReadResourceAndCut) Run(args json.RawMessage) (string, error) {
	var params struct {
		ID           string `json:"id"`
		ResourceName string `json:"resource_name"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	kind, instructions := goDescriptionKind(params.ResourceName)
	if kind == domain.ResourceFile {
		return fmt.Sprintf("File: %s\n\n%s\n\nWrite ONLY the description text, nothing else.", params.ID, instructions), nil
	}
	if kind == domain.ResourcePackage {
		return fmt.Sprintf("Package: %s\n\n%s\n\nWrite ONLY the description text, nothing else.", params.ID, instructions), nil
	}

	entry, err := r.mgr.ReadResourceAndCut(params.ID, kind)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%s\n\n```go\n%s\n```\n\nWrite ONLY the description text, nothing else.", instructions, entry.Cut), nil
}

func goDescriptionKind(resourceName string) (domain.ResourceKind, string) {
	normalized := strings.ToLower(strings.TrimSpace(resourceName))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	normalized = strings.ReplaceAll(normalized, " ", "_")

	switch normalized {
	case "function":
		return domain.ResourceFunction, "Generate a concise description (1-3 lines) for this Go function. Include its main purpose, what parameters it takes, what it returns, and any notable behavior or side effects."
	case "method":
		return domain.ResourceMethod, "Generate a concise description (1-3 lines) for this Go method. Include its receiver behavior, main purpose, parameters, return values, and any notable side effects."
	case "struct", "type":
		return domain.ResourceType, "Generate a concise description (1-3 lines) for this Go struct. Include what it represents, its key fields and their purpose, and how it is typically used."
	case "named_type", "namedtype":
		return domain.ResourceNamedType, "Generate a concise description (1-3 lines) for this Go named type. Include what it represents, its underlying kind, and how it is used."
	case "interface":
		return domain.ResourceInterface, "Generate a concise description (1-3 lines) for this Go interface. Include what contract it defines, what behavior it abstracts, and the key methods it requires."
	case "externalvar", "external_var", "variable":
		return domain.ResourceVariable, "Generate a concise description (1 line) for this Go external variable. Include what it stores and its purpose in the codebase."
	case "file":
		return domain.ResourceFile, "Generate a concise description (1 line) for this Go source file. What does it contain and what is its role within its package? The file name is all the context available."
	case "package":
		return domain.ResourcePackage, "Generate a concise description (1-2 lines) for this Go package. Include its overall purpose and what functionality it provides."
	default:
		return domain.ResourceKind(normalized), "Generate a concise description for this resource."
	}
}
