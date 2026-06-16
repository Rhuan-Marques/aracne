package chat

import (
	"aracne/internal/helper"
	"aracne/internal/llm/languages/gotools"
	"aracne/internal/llm/languages/jstools"
	"aracne/internal/llm/languages/pythontools"
	"aracne/internal/llm/languages/universaltools"
	"aracne/internal/llm/tools"
	"aracne/internal/topology"
	"aracne/internal/topology/domain"
	"aracne/internal/topology/golang"
	"aracne/internal/topology/javascript"
	"aracne/internal/topology/python"
	"aracne/internal/topology/scanner"
	"aracne/internal/topology/scanner/goscanner"
	"aracne/internal/topology/scanner/jsscanner"
	"aracne/internal/topology/scanner/pyscanner"
)

func NewScannerRegistry() *scanner.Registry {
	reg := scanner.NewRegistry()
	reg.Register(goscanner.NewGoScanner())
	reg.Register(pyscanner.NewPythonScanner())
	reg.Register(jsscanner.NewJavaScriptScanner())
	reg.Register(jsscanner.NewTypeScriptScanner())
	return reg
}

func BuildToolRegistry(manager *topology.TopologyManager, scannerReg *scanner.Registry, cfg *helper.Config, workspace string) *tools.Registry {
	registry := tools.NewRegistry()
	allowed := chatMainAgentToolSet(cfg)
	add := func(t tools.Tool) {
		if allowed == nil || allowed[t.Name()] {
			registry.Register(t)
		}
	}

	add(&tools.Ls{})
	add(NewBashTool(workspace, manager, scannerReg))
	add(NewGlobTool(workspace))
	add(&AskUserQuestionTool{})
	add(&CreateTasksTool{})
	add(tools.NewGrep(manager))
	add(tools.NewEdit(manager, scannerReg))
	add(tools.NewWrite(manager, scannerReg))
	add(tools.NewWarningsList(manager))
	add(tools.NewBugReport(manager))
	add(tools.NewBugList(manager))
	add(tools.NewBugAcknowledge(manager))
	add(tools.NewBugDismiss(manager))
	add(tools.NewBugDelete(manager))
	add(tools.NewRead(manager))
	add(universaltools.NewReadFunction(manager))
	add(universaltools.NewReadStruct(manager))
	add(universaltools.NewReadInterface(manager))
	add(universaltools.NewReadNamedType(manager))
	add(universaltools.NewReadFile(manager))
	add(universaltools.NewReadPackage(manager))
	add(universaltools.NewReadDependency(manager))

	registerLanguageMaintenanceTools(registry, allowed, manager, getLanguage(manager), configDescribeTargets(cfg), configDescriptionBatchSize(cfg))
	return registry
}

// chatMainAgentToolSet returns the set of tool names the proprietary chat main
// agent may use, or nil when unconfigured (meaning "all tools").
func chatMainAgentToolSet(cfg *helper.Config) map[string]bool {
	if cfg == nil || len(cfg.Viz.Chat.MainAgent.Tools) == 0 {
		return nil
	}
	set := make(map[string]bool, len(cfg.Viz.Chat.MainAgent.Tools))
	for _, name := range cfg.Viz.Chat.MainAgent.Tools {
		set[name] = true
	}
	return set
}

func configDescribeTargets(cfg *helper.Config) []domain.ResourceKind {
	if cfg == nil {
		return nil
	}
	return cfg.Descriptions.Kinds
}

func BuildAgentToolRegistry(manager *topology.TopologyManager, scannerReg *scanner.Registry, cfg *helper.Config, workspace string) *tools.Registry {
	registry := tools.NewRegistry()
	registry.Register(&tools.Ls{})
	registry.Register(NewBashTool(workspace, manager, scannerReg))
	registry.Register(NewGlobTool(workspace))
	registry.Register(tools.NewRead(manager))
	registry.Register(universaltools.NewReadFunction(manager))
	registry.Register(universaltools.NewReadStruct(manager))
	registry.Register(universaltools.NewReadInterface(manager))
	registry.Register(universaltools.NewReadNamedType(manager))
	registry.Register(universaltools.NewReadFile(manager))
	registry.Register(universaltools.NewReadPackage(manager))
	registry.Register(universaltools.NewReadDependency(manager))
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
	registerLanguageMaintenanceTools(registry, nil, manager, lang, configDescribeTargets(cfg), configDescriptionBatchSize(cfg))
	return registry
}

func registerLanguageMaintenanceTools(registry *tools.Registry, allowed map[string]bool, manager *topology.TopologyManager, lang string, targets []domain.ResourceKind, descriptionBatchSize int) {
	add := func(t tools.Tool) {
		if allowed == nil || allowed[t.Name()] {
			registry.Register(t)
		}
	}
	if lang == "python" {
		pyMgr := python.NewPythonManager(manager)
		add(pythontools.NewUpdateDescriptionTool(pyMgr))
		add(pythontools.NewNodeListNoDescription(pyMgr, targets).SetBatchSize(descriptionBatchSize))
		return
	}
	if lang == "javascript" || lang == "typescript" {
		jsMgr := javascript.NewJavaScriptManager(manager)
		add(jstools.NewUpdateDescriptionTool(jsMgr))
		add(jstools.NewNodeListNoDescription(jsMgr, targets).SetBatchSize(descriptionBatchSize))
		return
	}
	goMgr := golang.NewGoManager(manager)
	add(gotools.NewUpdateDescriptionTool(goMgr))
	add(gotools.NewNodeListNoDescription(goMgr, targets).SetBatchSize(descriptionBatchSize))
}

// configDescriptionBatchSize reads the chat descriptions-executor's
// max-batch-size param, falling back to the default.
func configDescriptionBatchSize(cfg *helper.Config) int {
	if cfg != nil {
		if ag, ok := cfg.Viz.Chat.Agents.Agents["descriptions-generation-executor"]; ok {
			if v, ok := ag.Params["max-batch-size"]; ok && v > 0 {
				return v
			}
		}
	}
	return helper.DefaultDescriptionBatchSize
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
