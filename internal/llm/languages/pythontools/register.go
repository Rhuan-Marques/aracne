package pythontools

import (
	"ltp/internal/llm/tools"
	"ltp/internal/topology/python"
)

type Parameter = tools.Parameter

func RegisterPythonTools(registry *tools.Registry, mgr *python.PythonManager) {
	registry.Register(NewUpdateDescriptionTool(mgr))
	registry.Register(NewNodeListNoDescription(mgr))
}
