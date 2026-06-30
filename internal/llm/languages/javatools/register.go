package javatools

import (
	"aracne/internal/llm/tools"
	"aracne/internal/topology/java"
)

type Parameter = tools.Parameter

// RegisterJavaTools registers Java tools (UpdateDescriptionTool and
// NodeListNoDescription) with the tool registry.
func RegisterJavaTools(registry *tools.Registry, mgr *java.JavaManager) {
	registry.Register(NewUpdateDescriptionTool(mgr))
	registry.Register(NewNodeListNoDescription(mgr))
}
