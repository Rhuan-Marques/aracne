package tools

import (
	"encoding/json"
	"fmt"

	"llm-topology/internal/topology"
	"llm-topology/internal/topology/domain"
)

type UpdateDescriptionTool struct {
	mgr *topology.TopologyManager
}

func NewUpdateDescriptionTool(mgr *topology.TopologyManager) *UpdateDescriptionTool {
	return &UpdateDescriptionTool{mgr: mgr}
}

func (u *UpdateDescriptionTool) Name() string {
	return "update_description"
}

func (u *UpdateDescriptionTool) Description() string {
	return "Save a generated description for a resource in the topology database."
}

func (u *UpdateDescriptionTool) Parameters() []Parameter {
	return []Parameter{
		{Name: "id", Type: "string", Description: "The resource ID", Required: true},
		{Name: "resource_name", Type: "string", Description: "The resource type (Function, Struct, Interface, ExternalVar, File, Package)", Required: true},
		{Name: "description", Type: "string", Description: "The generated description text", Required: true},
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
	if params.ID == "" || params.ResourceName == "" || params.Description == "" {
		return "", fmt.Errorf("missing required arguments: id, resource_name, description")
	}

	rn := domain.ResourceName(params.ResourceName)
	if err := u.mgr.UpdateDescription(params.ID, rn, params.Description); err != nil {
		return "", fmt.Errorf("update description: %w", err)
	}

	return "ok", nil
}
