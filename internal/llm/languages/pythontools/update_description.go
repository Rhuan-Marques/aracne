package pythontools

import (
	"encoding/json"
	"fmt"
	"strings"

	"llm-topology/internal/topology/domain"
	"llm-topology/internal/topology/python"
)

type UpdateDescriptionTool struct {
	mgr *python.PythonManager
}

func NewUpdateDescriptionTool(mgr *python.PythonManager) *UpdateDescriptionTool {
	return &UpdateDescriptionTool{mgr: mgr}
}

func (u *UpdateDescriptionTool) Name() string {
	return "update_description"
}

func (u *UpdateDescriptionTool) Description() string {
	return "Update the description of a resource in the topology database"
}

func (u *UpdateDescriptionTool) Parameters() []Parameter {
	return []Parameter{
		{Name: "id", Type: "string", Description: "Resource ID", Required: true},
		{Name: "resource_name", Type: "string", Description: "Resource kind: Function, Struct(ype), Interface, Variable, File, Package", Required: true},
		{Name: "description", Type: "string", Description: "The new description text", Required: true},
	}
}

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
	case "Struct", "Type":
		kind = domain.ResourceType
	case "Interface":
		kind = domain.ResourceType
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
