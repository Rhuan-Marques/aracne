package cli

import (
	"sort"

	"aracne/internal/helper"
	"aracne/internal/llm/languages/gotools"
	"aracne/internal/llm/languages/javatools"
	"aracne/internal/llm/languages/jstools"
	"aracne/internal/llm/languages/pythontools"
	"aracne/internal/llm/languages/rusttools"
	"aracne/internal/llm/languages/universaltools"
	"aracne/internal/llm/tools"
	"aracne/internal/toolspec"
	"aracne/internal/topology"
	"aracne/internal/topology/domain"
	"aracne/internal/topology/golang"
	"aracne/internal/topology/java"
	"aracne/internal/topology/javascript"
	"aracne/internal/topology/python"
	"aracne/internal/topology/rust"
	"aracne/internal/topology/scanner"
)

// toolDeps carries everything an MCP tool constructor may need.
type toolDeps struct {
	manager           *topology.TopologyManager
	scannerReg        *scanner.Registry
	lang              string
	targets           []domain.ResourceKind
	batchSize         int
	filter            domain.ContextFilter
	includeNotVisible bool
	cfg               *helper.Config
	// nativeReadAvailable reports whether this agent still has the harness's own read tool,
	// which decides whether the aracne one registers as "read" or "read_resource".
	nativeReadAvailable bool
}

// mcpToolConstructors maps each MCP tool name to its constructor. It is the
// single source of truth for which tools can be registered: allMCPToolNames and
// BuildToolRegistry both derive from it, and a test asserts its key set matches
// the toolspec catalog so config names, validation, and registration can't drift.
var mcpToolConstructors = map[string]func(toolDeps) tools.Tool{
	// One constructor for the whole read family. The registered tool's NAME is decided at
	// build time from the agent's blocked_tools, so the catalog key "read" and the runtime
	// name can differ -- see BuildToolRegistry.
	"read": func(d toolDeps) tools.Tool {
		return universaltools.NewRead(d.manager, d.cfg, d.nativeReadAvailable, d.scannerReg)
	},
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
	case "rust":
		return rusttools.NewUpdateDescriptionTool(rust.NewRustManager(d.manager))
	case "java":
		return javatools.NewUpdateDescriptionTool(java.NewJavaManager(d.manager))
	default:
		return gotools.NewUpdateDescriptionTool(golang.NewGoManager(d.manager))
	}
}

// Builds a language-specific tool for generating node lists without descriptions.
func buildNodeListNoDescriptionTool(d toolDeps) tools.Tool {
	switch d.lang {
	case "python":
		return pythontools.NewNodeListNoDescription(python.NewPythonManager(d.manager), d.targets).SetBatchSize(d.batchSize).SetVisibility(d.filter, d.includeNotVisible)
	case "javascript", "typescript":
		return jstools.NewNodeListNoDescription(javascript.NewJavaScriptManager(d.manager), d.targets).SetBatchSize(d.batchSize).SetVisibility(d.filter, d.includeNotVisible)
	case "rust":
		return rusttools.NewNodeListNoDescription(rust.NewRustManager(d.manager), d.targets).SetBatchSize(d.batchSize).SetVisibility(d.filter, d.includeNotVisible)
	case "java":
		return javatools.NewNodeListNoDescription(java.NewJavaManager(d.manager), d.targets).SetBatchSize(d.batchSize).SetVisibility(d.filter, d.includeNotVisible)
	default:
		return gotools.NewNodeListNoDescription(golang.NewGoManager(d.manager), d.targets).SetBatchSize(d.batchSize).SetVisibility(d.filter, d.includeNotVisible)
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
		// The bug pipeline is gated as a whole. Filtering HERE rather than in
		// mcpToolConstructors keeps the catalog bijection intact (see
		// TestMCPConstructorsMatchToolspecCatalog) and covers every path into the registry:
		// the synthetic "all" profile, and a hand-written mcp_tools that names a bug tool.
		// A config that grants bug_report with the feature off gets nothing rather than
		// half a pipeline.
		if toolspec.IsBugTool(n) && !cfg.BugManagementEnabled() {
			continue
		}
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
		manager:             manager,
		scannerReg:          scannerReg,
		lang:                GetLanguage(manager),
		targets:             cfg.Descriptions.Kinds,
		batchSize:           cfg.AgentParam(harness, agentName, "max-batch-size", helper.DefaultDescriptionBatchSize),
		filter:              cfg.EffectiveContextFilter(),
		includeNotVisible:   cfg.Descriptions.IncludeNotVisible,
		cfg:                 cfg,
		nativeReadAvailable: NativeReadAvailable(cfg, harness, agentName),
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

// NativeReadAvailable reports whether the MCP server built for this (harness, agent) pair
// still sees a native read tool it could be confused with -- which is the one input that
// decides the aracne read tool's runtime name (toolspec.ResolveReadToolName).
//
// This is the single source of truth. It is exported because the generators that write
// tool names into agent files and permission maps must reach the SAME answer as the server:
// a generated name that does not match the registered one silently denies the agent the
// tool rather than failing loudly.
func NativeReadAvailable(cfg *helper.Config, harness, agentName string) bool {
	// The synthetic "all" profile serves the chat MCP server, which has no native read.
	return agentName != "all" && !blocksNativeRead(cfg, harness, agentName)
}

// blocksNativeRead reports whether the agent's blocked_tools denies the harness's own read
// tool. When it does, the aracne read tool takes the short name; otherwise it registers as
// "read_resource" so the two are never confusable in one session.
func blocksNativeRead(cfg *helper.Config, harness, agentName string) bool {
	for _, t := range cfg.EffectiveAgent(harness, agentName).BlockedTools {
		if t == "read" {
			return true
		}
	}
	return false
}
