package pythontools

import (
	"github.com/Rhuan-Marques/aracne/internal/llm/tools"
	"github.com/Rhuan-Marques/aracne/internal/topology/python"
)

type Parameter = tools.Parameter

// Registers Python LLM tools (update_description, node_list) to the provided tool registry for topology interactions.
func RegisterPythonTools(registry *tools.Registry, mgr *python.PythonManager) {
	registry.Register(NewUpdateDescriptionTool(mgr))
	registry.Register(NewNodeListNoDescription(mgr))
}
