package rusttools

import (
	"encoding/json"
	"fmt"
	"strings"

	"aracne/internal/topology/domain"
	"aracne/internal/topology/rust"
)

// UpdateDescriptionTool updates topology descriptions for Rust resources via LLM
// interaction.
type UpdateDescriptionTool struct {
	mgr *rust.RustManager
}

// NewUpdateDescriptionTool creates an UpdateDescriptionTool for updating
// descriptions of Rust resources.
func NewUpdateDescriptionTool(mgr *rust.RustManager) *UpdateDescriptionTool {
	return &UpdateDescriptionTool{mgr: mgr}
}

// Name returns the tool name "update_description".
func (u *UpdateDescriptionTool) Name() string {
	return "update_description"
}

// Description returns the description of the update_description LLM tool.
func (u *UpdateDescriptionTool) Description() string {
	return "Update the description of a resource in the topology database"
}

// Parameters returns the parameter schema for the update_description tool: id,
// resource_name, and description.
func (u *UpdateDescriptionTool) Parameters() []Parameter {
	return []Parameter{
		{Name: "id", Type: "string", Description: "Resource ID", Required: true},
		{Name: "resource_name", Type: "string", Description: "Resource kind: Function, Struct(Enum/Type), Trait, NamedType, Variable, File", Required: true},
		{Name: "description", Type: "string", Description: "The new description text", Required: true},
	}
}

// Run executes the update_description tool to modify a resource's description in
// the topology database.
func (u *UpdateDescriptionTool) Run(args json.RawMessage) (string, error) {
	var params struct {
		ID           string `json:"id"`
		ResourceName string `json:"resource_name"`
		Description  string `json:"description"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	kind := domain.ResourceKind(strings.ToLower(params.ResourceName))
	switch params.ResourceName {
	case "Function", "Method":
		kind = domain.ResourceFunction
	case "Struct", "Enum", "Union", "Type", "Class":
		kind = domain.ResourceStruct
	case "Trait", "Interface":
		kind = domain.ResourceInterface
	case "NamedType":
		kind = domain.ResourceNamedType
	case "Variable", "Const", "Static", "ExternalVar":
		kind = domain.ResourceVariable
	case "File", "Module":
		kind = domain.ResourceFile
	}

	if err := u.mgr.UpdateDescription(params.ID, kind, params.Description); err != nil {
		return "", err
	}

	return "description updated", nil
}
