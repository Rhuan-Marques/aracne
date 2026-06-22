package pythontools

import (
	"aracne/internal/llm/tools"
	"aracne/internal/topology/python"
)

type Parameter = tools.Parameter

// Registers Python LLM tools (update_description, node_list) to the provided tool registry for topology interactions.
func RegisterPythonTools(registry *tools.Registry, mgr *python.PythonManager) {
	registry.Register(NewUpdateDescriptionTool(mgr))
	registry.Register(NewNodeListNoDescription(mgr))
}
