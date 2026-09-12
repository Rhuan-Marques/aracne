package jstools

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	"github.com/Rhuan-Marques/aracne/internal/topology/javascript"
)

// Tool that updates topology descriptions for JavaScript resources via LLM interaction.
type UpdateDescriptionTool struct {
	mgr *javascript.JavaScriptManager
}

// Creates an UpdateDescriptionTool for updating descriptions of JavaScript resources.
func NewUpdateDescriptionTool(mgr *javascript.JavaScriptManager) *UpdateDescriptionTool {
	return &UpdateDescriptionTool{mgr: mgr}
}

// Returns the name "update_description" for the LLM tool.
func (u *UpdateDescriptionTool) Name() string {
	return "update_description"
}

// Returns the description of the update_description LLM tool.
func (u *UpdateDescriptionTool) Description() string {
	return "Update the description of a resource in the topology database"
}

// Returns the parameter schema for the update_description tool: id, resource_name, and description.
func (u *UpdateDescriptionTool) Parameters() []Parameter {
	return []Parameter{
		{Name: "id", Type: "string", Description: "Resource ID", Required: true},
		{Name: "resource_name", Type: "string", Description: "Resource kind: Function, Method, Class(Type), Interface, NamedType, Variable, File, Package", Required: true},
		{Name: "description", Type: "string", Description: "The new description text", Required: true},
	}
}

// Executes the update_description tool to modify a resource's description in the topology database.
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
	case "Function":
		kind = domain.ResourceFunction
	// A class member is stored as kind `method`, and the write filters on kind.
	case "Method":
		kind = domain.ResourceMethod
	case "Class", "Struct", "Type":
		kind = domain.ResourceStruct
	case "Interface":
		kind = domain.ResourceInterface
	case "NamedType":
		kind = domain.ResourceNamedType
	case "ExternalVar", "Variable":
		kind = domain.ResourceVariable
	case "File", "Module":
		kind = domain.ResourceFile
	case "Package":
		kind = domain.ResourcePackage
	}

	if err := u.mgr.UpdateDescription(params.ID, kind, params.Description); err != nil {
		return "", err
	}

	return "description updated", nil
}
