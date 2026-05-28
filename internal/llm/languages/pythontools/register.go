package pythontools

import (
	"llm-topology/internal/llm/tools"
	"llm-topology/internal/topology/python"
)

type Parameter = tools.Parameter

func RegisterPythonTools(registry *tools.Registry, mgr *python.PythonManager) {
	registry.Register(NewReadFunction(mgr))
	registry.Register(NewReadStruct(mgr))
	registry.Register(NewReadResourceAndCut(mgr))
	registry.Register(NewUpdateDescriptionTool(mgr))
	registry.Register(NewListUndocumented(mgr))
}
