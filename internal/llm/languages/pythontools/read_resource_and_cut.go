package pythontools

import (
	"encoding/json"
	"fmt"
	"strings"

	"ltp/internal/topology/domain"
	"ltp/internal/topology/python"
)

type ReadResourceAndCut struct {
	mgr *python.PythonManager
}

func NewReadResourceAndCut(mgr *python.PythonManager) *ReadResourceAndCut {
	return &ReadResourceAndCut{mgr: mgr}
}

func (r *ReadResourceAndCut) Name() string {
	return "read_resource_and_cut"
}

func (r *ReadResourceAndCut) Description() string {
	return "Get a resource's source code cut and specific instructions for generating its description"
}

func (r *ReadResourceAndCut) Parameters() []Parameter {
	return []Parameter{
		{Name: "id", Type: "string", Description: "Resource ID", Required: true},
		{Name: "resource_name", Type: "string", Description: "Resource kind: function, method, type, interface, variable, file, or package", Required: true},
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

	kind, instructions := pythonDescriptionKind(params.ResourceName)
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

	return fmt.Sprintf("%s\n\n```python\n%s\n```\n\nWrite ONLY the description text, nothing else.", instructions, entry.Cut), nil
}

func pythonDescriptionKind(resourceName string) (domain.ResourceKind, string) {
	normalized := strings.ToLower(strings.TrimSpace(resourceName))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	normalized = strings.ReplaceAll(normalized, " ", "_")

	switch normalized {
	case "function":
		return domain.ResourceFunction, "Generate a concise description (1-3 lines) for this Python function. Include its main purpose, what parameters it takes, what it returns, and any notable behavior or decorators."
	case "method":
		return domain.ResourceMethod, "Generate a concise description (1-3 lines) for this Python method. Include its receiver/class behavior, purpose, parameters, return value, and notable decorators or side effects."
	case "struct", "type", "class":
		return domain.ResourceType, "Generate a concise description (1-3 lines) for this Python class. Include what it represents, its key attributes and methods, how it is typically used, and its inheritance hierarchy."
	case "interface", "abc", "protocol":
		return domain.ResourceType, "Generate a concise description (1-3 lines) for this Python ABC/Protocol class. Include what contract it defines and the key abstract methods it requires."
	case "externalvar", "external_var", "variable":
		return domain.ResourceVariable, "Generate a concise description (1 line) for this Python module-level variable. Include what it stores and its purpose in the codebase."
	case "file":
		return domain.ResourceFile, "Generate a concise description (1 line) for this Python source file. What does it contain and what is its role within its module? The file name is all the context available."
	case "package":
		return domain.ResourcePackage, "Generate a concise description (1-2 lines) for this Python package. Include its overall purpose and what functionality it provides."
	default:
		return domain.ResourceKind(normalized), "Generate a concise description for this resource."
	}
}
