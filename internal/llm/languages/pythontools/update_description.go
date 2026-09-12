package pythontools

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	"github.com/Rhuan-Marques/aracne/internal/topology/python"
)

// LLM tool handler that updates a resource's description in the topology database by id and resource kind.
type UpdateDescriptionTool struct {
	mgr *python.PythonManager
}

// Creates an UpdateDescriptionTool handler for modifying resource descriptions in the topology.
func NewUpdateDescriptionTool(mgr *python.PythonManager) *UpdateDescriptionTool {
	return &UpdateDescriptionTool{mgr: mgr}
}

// Returns the tool's name for the LLM: "update_description"
func (u *UpdateDescriptionTool) Name() string {
	return "update_description"
}

// Returns the tool's description for the LLM: "Update the description of a resource in the topology database"
func (u *UpdateDescriptionTool) Description() string {
	return "Update the description of a resource in the topology database"
}

// Returns the tool's parameters: id (resource ID), resource_name (kind), and description (new text), all required strings
func (u *UpdateDescriptionTool) Parameters() []Parameter {
	return []Parameter{
		{Name: "id", Type: "string", Description: "Resource ID", Required: true},
		{Name: "resource_name", Type: "string", Description: "Resource kind: Function, Method, Struct(ype), Interface, Variable, File, Package", Required: true},
		{Name: "description", Type: "string", Description: "The new description text", Required: true},
	}
}

// Parses arguments and updates a resource's description in the topology database with the given id, kind, and text
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
	// A def inside a class is stored as kind `method`, and the write filters on kind.
	case "Method":
		kind = domain.ResourceMethod
	case "Struct", "Type":
		kind = domain.ResourceStruct
	case "Interface":
		kind = domain.ResourceStruct
	case "ExternalVar", "Variable":
		kind = domain.ResourceVariable
	case "File":
		kind = domain.ResourceFile
	case "Package":
		kind = domain.ResourcePackage
	}

	if err := u.mgr.UpdateDescription(params.ID, kind, params.Description); err != nil {
		return "", err
	}

	return "description updated", nil
}
