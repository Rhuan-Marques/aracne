package cli

import (
	"sort"

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

// toolDeps carries everything an MCP tool constructor may need.
type toolDeps struct {
	manager    *topology.TopologyManager
	scannerReg *scanner.Registry
	lang       string
	targets    []domain.ResourceKind
	batchSize  int
}

// mcpToolConstructors maps each MCP tool name to its constructor. It is the
// single source of truth for which tools can be registered: allMCPToolNames and
// BuildToolRegistry both derive from it, and a test asserts its key set matches
// the toolspec catalog so config names, validation, and registration can't drift.
var mcpToolConstructors = map[string]func(toolDeps) tools.Tool{
	"read":                     func(d toolDeps) tools.Tool { return tools.NewRead(d.manager) },
	"read_function":            func(d toolDeps) tools.Tool { return universaltools.NewReadFunction(d.manager) },
	"read_struct":              func(d toolDeps) tools.Tool { return universaltools.NewReadStruct(d.manager) },
	"read_interface":           func(d toolDeps) tools.Tool { return universaltools.NewReadInterface(d.manager) },
	"read_named_type":          func(d toolDeps) tools.Tool { return universaltools.NewReadNamedType(d.manager) },
	"read_file":                func(d toolDeps) tools.Tool { return universaltools.NewReadFile(d.manager) },
	"read_package":             func(d toolDeps) tools.Tool { return universaltools.NewReadPackage(d.manager) },
	"read_dependency":          func(d toolDeps) tools.Tool { return universaltools.NewReadDependency(d.manager) },
	"grep":                     func(d toolDeps) tools.Tool { return tools.NewGrep(d.manager) },
	"edit":                     func(d toolDeps) tools.Tool { return tools.NewEdit(d.manager, d.scannerReg) },
	"write":                    func(d toolDeps) tools.Tool { return tools.NewWrite(d.manager, d.scannerReg) },
	"warnings_list":            func(d toolDeps) tools.Tool { return tools.NewWarningsList(d.manager) },
	"bug_report":               func(d toolDeps) tools.Tool { return tools.NewBugReport(d.manager) },
	"bug_list":                 func(d toolDeps) tools.Tool { return tools.NewBugList(d.manager) },
	"bug_acknowledge":          func(d toolDeps) tools.Tool { return tools.NewBugAcknowledge(d.manager) },
	"bug_dismiss":              func(d toolDeps) tools.Tool { return tools.NewBugDismiss(d.manager) },
	"bug_delete":               func(d toolDeps) tools.Tool { return tools.NewBugDelete(d.manager) },
	"update_description":       buildUpdateDescriptionTool,
	"node_list_no_description": buildNodeListNoDescriptionTool,
}

// buildUpdateDescriptionTool and buildNodeListNoDescriptionTool are the only
// language-dispatched constructors: the language maintenance tools differ per
// topology language.
func buildUpdateDescriptionTool(d toolDeps) tools.Tool {
	switch d.lang {
	case "python":
		return pythontools.NewUpdateDescriptionTool(python.NewPythonManager(d.manager))
	case "javascript", "typescript":
		return jstools.NewUpdateDescriptionTool(javascript.NewJavaScriptManager(d.manager))
	default:
		return gotools.NewUpdateDescriptionTool(golang.NewGoManager(d.manager))
	}
}

// Builds a language-specific tool for generating node lists without descriptions.
func buildNodeListNoDescriptionTool(d toolDeps) tools.Tool {
	switch d.lang {
	case "python":
		return pythontools.NewNodeListNoDescription(python.NewPythonManager(d.manager), d.targets).SetBatchSize(d.batchSize)
	case "javascript", "typescript":
		return jstools.NewNodeListNoDescription(javascript.NewJavaScriptManager(d.manager), d.targets).SetBatchSize(d.batchSize)
	default:
		return gotools.NewNodeListNoDescription(golang.NewGoManager(d.manager), d.targets).SetBatchSize(d.batchSize)
	}
}

// allMCPToolNames returns every registerable MCP tool name, sorted. Used by the
// synthetic "all" profile (the chat MCP server).
func allMCPToolNames() []string {
	names := make([]string, 0, len(mcpToolConstructors))
	for n := range mcpToolConstructors {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
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
	deps := toolDeps{
		manager:    manager,
		scannerReg: scannerReg,
		lang:       GetLanguage(manager),
		targets:    cfg.Descriptions.Kinds,
		batchSize:  cfg.AgentParam(harness, agentName, "max-batch-size", helper.DefaultDescriptionBatchSize),
	}
	readScan := cfg.EffectiveReadScan()
	for _, name := range allMCPToolNames() {
		if allowed[name] {
			t := tools.WrapWithReadScan(mcpToolConstructors[name](deps), manager, scannerReg, readScan)
			registry.Register(t)
		}
	}
	return registry
}
