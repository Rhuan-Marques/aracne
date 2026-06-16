package jstools

import (
	"aracne/internal/llm/tools"
	"aracne/internal/topology/javascript"
)

type Parameter = tools.Parameter

func RegisterJavaScriptTools(registry *tools.Registry, mgr *javascript.JavaScriptManager) {
	registry.Register(NewUpdateDescriptionTool(mgr))
	registry.Register(NewNodeListNoDescription(mgr))
}
