package gotools

import (
	"github.com/Rhuan-Marques/aracne/internal/llm/tools"
	"github.com/Rhuan-Marques/aracne/internal/topology/golang"
)

type Parameter = tools.Parameter

// Registers all Go-specific topology tools (update_description, node_list_no_description, list_warnings) into the given tool registry.
func RegisterGoTools(registry *tools.Registry, mgr *golang.GoManager) {
	registry.Register(NewUpdateDescriptionTool(mgr))
	registry.Register(NewNodeListNoDescription(mgr))
	registry.Register(NewListWarnings(mgr))
}
