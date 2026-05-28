package gotools

import (
	"llm-topology/internal/llm/tools"
	"llm-topology/internal/topology/golang"
)

type Parameter = tools.Parameter

// Registers all Go-specific topology tools (read_function, read_struct, read_resource_and_cut, update_description, list_undocumented) into the given tool registry.
func RegisterGoTools(registry *tools.Registry, mgr *golang.GoManager) {
	registry.Register(NewReadFunction(mgr))
	registry.Register(NewReadStruct(mgr))
	registry.Register(NewReadResourceAndCut(mgr))
	registry.Register(NewUpdateDescriptionTool(mgr))
	registry.Register(NewListUndocumented(mgr))
	registry.Register(NewListWarnings(mgr))
}
