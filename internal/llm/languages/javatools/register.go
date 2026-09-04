package javatools

import (
	"github.com/Rhuan-Marques/aracne/internal/llm/tools"
	"github.com/Rhuan-Marques/aracne/internal/topology/java"
)

type Parameter = tools.Parameter

// RegisterJavaTools registers Java tools (UpdateDescriptionTool and
// NodeListNoDescription) with the tool registry.
func RegisterJavaTools(registry *tools.Registry, mgr *java.JavaManager) {
	registry.Register(NewUpdateDescriptionTool(mgr))
	registry.Register(NewNodeListNoDescription(mgr))
}
