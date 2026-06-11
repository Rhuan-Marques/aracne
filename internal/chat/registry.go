package chat

import (
	"aracne/internal/helper"
	"aracne/internal/llm/languages/gotools"
	"aracne/internal/llm/languages/pythontools"
	"aracne/internal/llm/languages/universaltools"
	"aracne/internal/llm/tools"
	"aracne/internal/topology"
	"aracne/internal/topology/domain"
	"aracne/internal/topology/golang"
	"aracne/internal/topology/python"
	"aracne/internal/topology/scanner"
	"aracne/internal/topology/scanner/goscanner"
	"aracne/internal/topology/scanner/pyscanner"
)

func NewScannerRegistry() *scanner.Registry {
	reg := scanner.NewRegistry()
	reg.Register(goscanner.NewGoScanner())
	reg.Register(pyscanner.NewPythonScanner())
	return reg
}

func BuildToolRegistry(manager *topology.TopologyManager, scannerReg *scanner.Registry, cfg *helper.Config, workspace string) *tools.Registry {
	registry := tools.NewRegistry()
	registry.Register(&tools.Ls{})
	registry.Register(NewBashTool(workspace, manager, scannerReg))
	registry.Register(NewGlobTool(workspace))
	registry.Register(&AskUserQuestionTool{})
	registry.Register(tools.NewGrep(manager))
	registry.Register(tools.NewEdit(manager, scannerReg))
	registry.Register(tools.NewWrite(manager, scannerReg))
	registry.Register(tools.NewWarningsList(manager))
	registry.Register(tools.NewBugReport(manager))
	registry.Register(tools.NewBugList(manager))
	registry.Register(tools.NewBugAcknowledge(manager))
	registry.Register(tools.NewBugDismiss(manager))
	registry.Register(tools.NewBugDelete(manager))

	lang := getLanguage(manager)
	useSplit := cfg != nil && len(cfg.ReadSplit) > 0
	if !useSplit {
		registry.Register(tools.NewRead(manager))
		registerLanguageMaintenanceTools(registry, manager, lang, nil)
		return registry
	}

	for kind := range cfg.ReadSplit {
		switch kind {
		case domain.ResourceFunction, domain.ResourceMethod:
			registry.Register(universaltools.NewReadFunction(manager))
		case domain.ResourceType:
			registry.Register(universaltools.NewReadStruct(manager))
		case domain.ResourceInterface:
			registry.Register(universaltools.NewReadInterface(manager))
		case domain.ResourceNamedType:
			registry.Register(universaltools.NewReadNamedType(manager))
		case domain.ResourceFile:
			registry.Register(universaltools.NewReadFile(manager))
		case domain.ResourcePackage:
			registry.Register(universaltools.NewReadPackage(manager))
		case domain.ResourceDependency:
			registry.Register(universaltools.NewReadDependency(manager))
		}
	}
	registerLanguageMaintenanceTools(registry, manager, lang, cfg.DescribeTargets)
	return registry
}

func registerLanguageMaintenanceTools(registry *tools.Registry, manager *topology.TopologyManager, lang string, targets []domain.ResourceKind) {
	if lang == "python" {
		pyMgr := python.NewPythonManager(manager)
		registry.Register(pythontools.NewUpdateDescriptionTool(pyMgr))
		registry.Register(pythontools.NewNodeListNoDescription(pyMgr, targets))
		return
	}
	goMgr := golang.NewGoManager(manager)
	registry.Register(gotools.NewUpdateDescriptionTool(goMgr))
	registry.Register(gotools.NewNodeListNoDescription(goMgr, targets))
}

func toolMap(registry *tools.Registry, allowed map[string]bool) map[string]tools.Tool {
	result := make(map[string]tools.Tool)
	for _, tool := range registry.List() {
		if allowed == nil || allowed[tool.Name()] {
			result[tool.Name()] = tool
		}
	}
	return result
}

func getLanguage(manager *topology.TopologyManager) string {
	topo, err := manager.ReadAll()
	if err == nil && topo != nil && topo.Language != "" {
		return topo.Language
	}
	return "go"
}
