package chat

import (
	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/llm/languages/gotools"
	"github.com/Rhuan-Marques/aracne/internal/llm/languages/javatools"
	"github.com/Rhuan-Marques/aracne/internal/llm/languages/jstools"
	"github.com/Rhuan-Marques/aracne/internal/llm/languages/pythontools"
	"github.com/Rhuan-Marques/aracne/internal/llm/languages/rusttools"
	"github.com/Rhuan-Marques/aracne/internal/llm/languages/universaltools"
	"github.com/Rhuan-Marques/aracne/internal/llm/toolapi"
	"github.com/Rhuan-Marques/aracne/internal/llm/tools"
	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	"github.com/Rhuan-Marques/aracne/internal/topology/golang"
	"github.com/Rhuan-Marques/aracne/internal/topology/java"
	"github.com/Rhuan-Marques/aracne/internal/topology/javascript"
	"github.com/Rhuan-Marques/aracne/internal/topology/python"
	"github.com/Rhuan-Marques/aracne/internal/topology/rust"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner/goscanner"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner/javascanner"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner/jsscanner"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner/pyscanner"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner/rustscanner"
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
func BuildToolRegistry(manager *topology.TopologyManager, scannerReg *scanner.Registry, cfg *helper.Config, workspace string) *toolapi.Registry {
	registry := toolapi.NewRegistry()
	allowed := chatMainAgentToolSet(cfg)
	// No pre-tool scan here. scan.pre_tool is a GUARD setting: it belongs to the surfaces
	// where an outside harness runs the tools and nothing else keeps the graph current. The
	// chat owns its own loop -- its bash tool re-scans after every command and its
	// edit/write tools sync the file they touched -- so a scan on the way IN would re-walk
	// the project for a change the loop has already applied.
	add := func(t toolapi.Tool) {
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
	add(tools.NewWarningsList(manager, cfg, scannerReg))
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
func BuildAgentToolRegistry(manager *topology.TopologyManager, scannerReg *scanner.Registry, cfg *helper.Config, workspace string) *toolapi.Registry {
	registry := toolapi.NewRegistry()
	// See BuildToolRegistry: the chat loop keeps itself fresh, so scan.pre_tool does not
	// apply to it.
	add := func(t toolapi.Tool) {
		registry.Register(t)
	}
	add(&tools.Ls{})
	add(NewBashTool(workspace, manager, scannerReg))
	add(NewGlobTool(workspace))
	add(universaltools.NewRead(manager, cfg, false, scannerReg))
	add(tools.NewGrep(manager))
	add(tools.NewEdit(manager, scannerReg))
	add(tools.NewWrite(manager, scannerReg))
	add(tools.NewWarningsList(manager, cfg, scannerReg))
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
func registerLanguageMaintenanceTools(registry *toolapi.Registry, allowed map[string]bool, manager *topology.TopologyManager, lang string, targets []domain.ResourceKind, descriptionBatchSize int) {
	add := func(t toolapi.Tool) {
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
func toolMap(registry *toolapi.Registry, allowed map[string]bool) map[string]toolapi.Tool {
	result := make(map[string]toolapi.Tool)
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
