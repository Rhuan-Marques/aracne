package cli

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
)

// allMCPToolNames is the full universe of registerable MCP tools (the special
// "all" profile used by the chat MCP server).
func allMCPToolNames() []string {
	return []string{
		"read", "grep", "edit", "write",
		"read_struct", "read_function", "read_interface", "read_named_type",
		"read_file", "read_package", "read_dependency",
		"warnings_list", "bug_report", "bug_list", "bug_acknowledge",
		"bug_dismiss", "bug_delete", "node_list_no_description", "update_description",
	}
}

// ValidAgentProfile reports whether name is usable as a --tool-profile value:
// the synthetic "main"/"default"/"all" or an agent defined in the config.
func ValidAgentProfile(cfg *helper.Config, name string) bool {
	switch name {
	case "", "main", "default", "all":
		return true
	}
	if _, ok := cfg.LLM.Any.Agents[name]; ok {
		return true
	}
	if _, ok := cfg.LLM.ClaudeCode.Agents[name]; ok {
		return true
	}
	if _, ok := cfg.LLM.OpenCode.Agents[name]; ok {
		return true
	}
	return false
}

// effectiveMCPToolSet returns the set of MCP tool names the (harness, agent)
// is allowed to use. The synthetic "all" agent yields the full universe.
func effectiveMCPToolSet(cfg *helper.Config, harness, agentName string) map[string]bool {
	var names []string
	if agentName == "all" {
		names = allMCPToolNames()
	} else {
		names = cfg.EffectiveAgent(harness, agentName).MCPTools
	}
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	return set
}

// BuildToolRegistry builds the MCP tool registry for an agent by registering
// exactly the tools listed in its effective mcp_tools.
func BuildToolRegistry(manager *topology.TopologyManager, scannerReg *scanner.Registry, cfg *helper.Config, harness, agentName string) *tools.Registry {
	registry := tools.NewRegistry()
	allowed := effectiveMCPToolSet(cfg, harness, agentName)

	if allowed["read"] {
		registry.Register(tools.NewRead(manager))
	}
	if allowed["read_function"] {
		registry.Register(universaltools.NewReadFunction(manager))
	}
	if allowed["read_struct"] {
		registry.Register(universaltools.NewReadStruct(manager))
	}
	if allowed["read_interface"] {
		registry.Register(universaltools.NewReadInterface(manager))
	}
	if allowed["read_named_type"] {
		registry.Register(universaltools.NewReadNamedType(manager))
	}
	if allowed["read_file"] {
		registry.Register(universaltools.NewReadFile(manager))
	}
	if allowed["read_package"] {
		registry.Register(universaltools.NewReadPackage(manager))
	}
	if allowed["read_dependency"] {
		registry.Register(universaltools.NewReadDependency(manager))
	}
	if allowed["grep"] {
		registry.Register(tools.NewGrep(manager))
	}
	if allowed["edit"] {
		registry.Register(tools.NewEdit(manager, scannerReg))
	}
	if allowed["write"] {
		registry.Register(tools.NewWrite(manager, scannerReg))
	}
	if allowed["warnings_list"] {
		registry.Register(tools.NewWarningsList(manager))
	}
	if allowed["bug_report"] {
		registry.Register(tools.NewBugReport(manager))
	}
	if allowed["bug_list"] {
		registry.Register(tools.NewBugList(manager))
	}
	if allowed["bug_acknowledge"] {
		registry.Register(tools.NewBugAcknowledge(manager))
	}
	if allowed["bug_dismiss"] {
		registry.Register(tools.NewBugDismiss(manager))
	}
	if allowed["bug_delete"] {
		registry.Register(tools.NewBugDelete(manager))
	}

	batchSize := cfg.AgentParam(harness, agentName, "max-batch-size", helper.DefaultDescriptionBatchSize)
	targets := cfg.Descriptions.Kinds
	switch GetLanguage(manager) {
	case "python":
		registerPythonTopologyTools(registry, python.NewPythonManager(manager), allowed, targets, batchSize)
	case "javascript", "typescript":
		registerJavaScriptTopologyTools(registry, javascript.NewJavaScriptManager(manager), allowed, targets, batchSize)
	default:
		registerGoTopologyTools(registry, golang.NewGoManager(manager), allowed, targets, batchSize)
	}
	return registry
}

func registerGoTopologyTools(registry *tools.Registry, mgr *golang.GoManager, allowed map[string]bool, describeTargets []domain.ResourceKind, descriptionBatchSize int) {
	if allowed["update_description"] {
		registry.Register(gotools.NewUpdateDescriptionTool(mgr))
	}
	if allowed["node_list_no_description"] {
		registry.Register(gotools.NewNodeListNoDescription(mgr, describeTargets).SetBatchSize(descriptionBatchSize))
	}
}

func registerPythonTopologyTools(registry *tools.Registry, mgr *python.PythonManager, allowed map[string]bool, describeTargets []domain.ResourceKind, descriptionBatchSize int) {
	if allowed["update_description"] {
		registry.Register(pythontools.NewUpdateDescriptionTool(mgr))
	}
	if allowed["node_list_no_description"] {
		registry.Register(pythontools.NewNodeListNoDescription(mgr, describeTargets).SetBatchSize(descriptionBatchSize))
	}
}

func registerJavaScriptTopologyTools(registry *tools.Registry, mgr *javascript.JavaScriptManager, allowed map[string]bool, describeTargets []domain.ResourceKind, descriptionBatchSize int) {
	if allowed["update_description"] {
		registry.Register(jstools.NewUpdateDescriptionTool(mgr))
	}
	if allowed["node_list_no_description"] {
		registry.Register(jstools.NewNodeListNoDescription(mgr, describeTargets).SetBatchSize(descriptionBatchSize))
	}
}
