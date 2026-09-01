package chat

import (
	"aracne/internal/helper"
	"aracne/internal/llm/languages/gotools"
	"aracne/internal/llm/languages/javatools"
	"aracne/internal/llm/languages/jstools"
	"aracne/internal/llm/languages/pythontools"
	"aracne/internal/llm/languages/rusttools"
	"aracne/internal/llm/languages/universaltools"
	"aracne/internal/llm/tools"
	"aracne/internal/topology"
	"aracne/internal/topology/domain"
	"aracne/internal/topology/golang"
	"aracne/internal/topology/java"
	"aracne/internal/topology/javascript"
	"aracne/internal/topology/python"
	"aracne/internal/topology/rust"
	"aracne/internal/topology/scanner"
	"aracne/internal/topology/scanner/goscanner"
	"aracne/internal/topology/scanner/javascanner"
	"aracne/internal/topology/scanner/jsscanner"
	"aracne/internal/topology/scanner/pyscanner"
	"aracne/internal/topology/scanner/rustscanner"
)

// chatNativeReadAvailable is false because the proprietary chat harness has no read tool of
// its own for aracne's to collide with, so the aracne tool keeps the short name `read`.
//
// This is NOT a placeholder: it is the same decision cli.NativeReadAvailable makes for the
// synthetic "all" profile, and it must stay in step with the `## Tools` listing that
// agent_registry.go renders into every generated chat agent file. A generated name that
// does not match the registered one silently denies the agent the tool.
const chatNativeReadAvailable = false

// Registers scanners for Go, Python, JavaScript, TypeScript, Rust, and Java into a scanner registry.
func NewScannerRegistry() *scanner.Registry {
	reg := scanner.NewRegistry()
	reg.Register(goscanner.NewGoScanner())
	reg.Register(pyscanner.NewPythonScanner())
	reg.Register(jsscanner.NewJavaScriptScanner())
	reg.Register(jsscanner.NewTypeScriptScanner())
	reg.Register(rustscanner.NewRustScanner())
	reg.Register(javascanner.NewJavaScanner())
	return reg
}

// Constructs a filtered tool registry for the main chat agent with config-based tool access control.
func BuildToolRegistry(manager *topology.TopologyManager, scannerReg *scanner.Registry, cfg *helper.Config, workspace string) *tools.Registry {
	registry := tools.NewRegistry()
	allowed := chatMainAgentToolSet(cfg)
	readScan := helper.ReadScanNone
	if cfg != nil {
		readScan = cfg.EffectiveReadScan()
	}
	add := func(t tools.Tool) {
		t = tools.WrapWithReadScan(t, manager, scannerReg, readScan)
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
	add(universaltools.NewRead(manager, cfg, chatNativeReadAvailable, scannerReg))

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

// Returns the list of resource kinds that should have descriptions generated from config.
func configDescribeTargets(cfg *helper.Config) []domain.ResourceKind {
	if cfg == nil {
		return nil
	}
	return cfg.Descriptions.Kinds
}

// Constructs a full tool registry for agent threads with topology, scanning, and language-specific tools.
func BuildAgentToolRegistry(manager *topology.TopologyManager, scannerReg *scanner.Registry, cfg *helper.Config, workspace string) *tools.Registry {
	registry := tools.NewRegistry()
	readScan := helper.ReadScanNone
	if cfg != nil {
		readScan = cfg.EffectiveReadScan()
	}
	add := func(t tools.Tool) {
		registry.Register(tools.WrapWithReadScan(t, manager, scannerReg, readScan))
	}
	add(&tools.Ls{})
	add(NewBashTool(workspace, manager, scannerReg))
	add(NewGlobTool(workspace))
	add(universaltools.NewRead(manager, cfg, false, scannerReg))
	add(tools.NewGrep(manager))
	add(tools.NewEdit(manager, scannerReg))
	add(tools.NewWrite(manager, scannerReg))
	add(tools.NewWarningsList(manager))
	add(tools.NewBugReport(manager))
	add(tools.NewBugList(manager))
	add(tools.NewBugAcknowledge(manager))
	add(tools.NewBugDismiss(manager))
	add(tools.NewBugDelete(manager))
	lang := getLanguage(manager)
	registerLanguageMaintenanceTools(registry, nil, manager, lang, configDescribeTargets(cfg), configDescriptionBatchSize(cfg))
	return registry
}

// Registers language-specific maintenance tools (description updates and node listing) in the tool registry.
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
	if lang == "rust" {
		rustMgr := rust.NewRustManager(manager)
		add(rusttools.NewUpdateDescriptionTool(rustMgr))
		add(rusttools.NewNodeListNoDescription(rustMgr, targets).SetBatchSize(descriptionBatchSize))
		return
	}
	if lang == "java" {
		javaMgr := java.NewJavaManager(manager)
		add(javatools.NewUpdateDescriptionTool(javaMgr))
		add(javatools.NewNodeListNoDescription(javaMgr, targets).SetBatchSize(descriptionBatchSize))
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

// Filters registry tools by allowlist and returns them as a map keyed by tool name.
func toolMap(registry *tools.Registry, allowed map[string]bool) map[string]tools.Tool {
	result := make(map[string]tools.Tool)
	for _, tool := range registry.List() {
		if allowed == nil || allowed[tool.Name()] {
			result[tool.Name()] = tool
		}
	}
	return result
}

// Returns the language from topology metadata, defaulting to "go" if not available.
func getLanguage(manager *topology.TopologyManager) string {
	topo, err := manager.ReadAll()
	if err == nil && topo != nil && topo.Language != "" {
		return topo.Language
	}
	return "go"
}
