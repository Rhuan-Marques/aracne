package gotools

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	"github.com/Rhuan-Marques/aracne/internal/topology/golang"
)

// MCP/agent tool that updates a resource's description in the topology database. Wraps GoManager.UpdateDescription with JSON argument parsing.
type UpdateDescriptionTool struct {
	mgr *golang.GoManager
}

// Constructs a new UpdateDescriptionTool instance with the given GoManager.
func NewUpdateDescriptionTool(mgr *golang.GoManager) *UpdateDescriptionTool {
	return &UpdateDescriptionTool{mgr: mgr}
}

// Returns the tool name "update_description" for the UpdateDescriptionTool registration.
func (u *UpdateDescriptionTool) Name() string {
	return "update_description"
}

// Returns the description string for the update_description tool, explaining its purpose to the LLM.
func (u *UpdateDescriptionTool) Description() string {
	return "Update the description of a resource in the topology database"
}

// Returns the parameter definitions for the update_description tool: id (Resource ID), resource_name (resource kind), and description (new description text), all required.
func (u *UpdateDescriptionTool) Parameters() []Parameter {
	return []Parameter{
		{Name: "id", Type: "string", Description: "Resource ID", Required: true},
		{Name: "resource_name", Type: "string", Description: "Resource kind: Function, Method, Struct(ype), Interface, NamedType, Variable, File, Package", Required: true},
		{Name: "description", Type: "string", Description: "The new description text", Required: true},
	}
}

// Executes the "update_description" tool. Unmarshals JSON arguments (id, resource_name, description), maps the resource_name string to the appropriate domain.ResourceKind, and delegates to GoManager.UpdateDescription. Returns "description updated" on success or an error.
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
	// A func with a receiver is stored as kind `method`, and the write filters on kind.
	case "Method":
		kind = domain.ResourceMethod
	case "Struct", "Type":
		kind = domain.ResourceStruct
	case "NamedType":
		kind = domain.ResourceNamedType
	case "Interface":
		kind = domain.ResourceInterface
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
