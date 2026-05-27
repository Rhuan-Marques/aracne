package gotools

import (
	"llm-topology/internal/llm/tools"
	"llm-topology/internal/topology/golang"
)

type Parameter = tools.Parameter

func RegisterGoTools(registry *tools.Registry, mgr *golang.GoManager) {
	registry.Register(NewReadFunction(mgr))
	registry.Register(NewReadStruct(mgr))
	registry.Register(NewReadResourceAndCut(mgr))
	registry.Register(NewUpdateDescriptionTool(mgr))
	registry.Register(NewListUndocumented(mgr))
}
