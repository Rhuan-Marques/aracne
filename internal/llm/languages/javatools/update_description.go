package javatools

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	"github.com/Rhuan-Marques/aracne/internal/topology/java"
)

// UpdateDescriptionTool updates topology descriptions for Java resources via LLM
// interaction.
type UpdateDescriptionTool struct {
	mgr *java.JavaManager
}

// NewUpdateDescriptionTool creates an UpdateDescriptionTool for updating
// descriptions of Java resources.
func NewUpdateDescriptionTool(mgr *java.JavaManager) *UpdateDescriptionTool {
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
		{Name: "resource_name", Type: "string", Description: "Resource kind: Class, Enum, Record, AbstractClass, Interface, Annotation, Method, Constructor, Field, File", Required: true},
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
	case "Class", "Enum", "Record", "AbstractClass", "Struct":
		kind = domain.ResourceStruct
	case "Interface", "Annotation":
		kind = domain.ResourceInterface
	// The scanner stores a method or constructor -- anything with a declaring class -- as kind
	// `method`, and the write filters on kind, so mapping them to `function` rejected every one.
	case "Method", "Constructor":
		kind = domain.ResourceMethod
	case "Function":
		kind = domain.ResourceFunction
	case "NamedType":
		kind = domain.ResourceNamedType
	case "Variable", "Field":
		kind = domain.ResourceVariable
	case "File", "Module":
		kind = domain.ResourceFile
	}

	if err := u.mgr.UpdateDescription(params.ID, kind, params.Description); err != nil {
		return "", err
	}

	return "description updated", nil
}
