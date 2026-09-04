package jstools

import (
	"github.com/Rhuan-Marques/aracne/internal/llm/tools"
	"github.com/Rhuan-Marques/aracne/internal/topology/javascript"
)

type Parameter = tools.Parameter

// Registers JavaScript tools (UpdateDescriptionTool and NodeListNoDescription) with the tool registry.
func RegisterJavaScriptTools(registry *tools.Registry, mgr *javascript.JavaScriptManager) {
	registry.Register(NewUpdateDescriptionTool(mgr))
	registry.Register(NewNodeListNoDescription(mgr))
}
