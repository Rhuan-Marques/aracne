package helper

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"aracne/internal/toolspec"
	"aracne/internal/topology/domain"
)

type ScanMode string

const (
	ScanModeDefault ScanMode = "default"
	ScanModeHard    ScanMode = "hard"
	ScanModeAll     ScanMode = "all"
)

// ReadScanMode controls whether a topology scan runs before read/grep
// operations (CLI commands, MCP tools, and viz chat tools). "none" keeps the
// current behavior (no scan); any other value triggers a scan first:
// - "default" = incremental scan (only changed files)
// - "full"    = re-scan all files (preserves descriptions)
// - "hard"    = rebuild from scratch (clears descriptions and bugs)
type ReadScanMode string

const (
	ReadScanNone    ReadScanMode = "none"
	ReadScanDefault ReadScanMode = "default"
	ReadScanFull    ReadScanMode = "full"
	ReadScanHard    ReadScanMode = "hard"
)

const DefaultDescriptionBatchSize = 5

// InheritsModel is the sentinel a sub-agent uses to copy the main agent's model.
const InheritsModel = "<inherits>"

// Configuration for the one-shot `arac scan` command, specifying the default scan mode.

type ScanSection struct {
	// Mode is the default mode for the one-shot `arac scan` command.
	Mode ScanMode `json:"mode"`
}

// Configuration for file read operations, including max file size, scan mode, context filtering, and shell command passthrough behavior.
type ReadSection struct {
	MaxFileSize   int64                `json:"max_file_size"`
	Scan          ReadScanMode         `json:"scan"`
	ContextFilter ContextFilterSection `json:"context_filter"`
	// PipePassthrough exempts read/grep shell commands that consume piped
	// stdin (e.g. `cmd | tail`) from the tool guard: such commands operate on
	// command output, which the aracne MCP tools cannot serve. Direct file
	// reads (`cat foo.go`) are still gated. Absent means the default (true).
	PipePassthrough *bool `json:"pipe_passthrough"`
}

// ContextFilterSection tunes the "# CONTEXT:" block emitted by the read tools:
// how verbosely each neighbor kind is rendered and whether incoming
// (caller/user) connections are shown. Visibility fields take
// "hidden" | "normal" | "full".
type ContextFilterSection struct {
	IncludeIncoming          bool   `json:"include_incoming"`
	ExternalVarsVisibility   string `json:"external_vars_visibility"`
	SmallFunctionsVisibility string `json:"small_functions_visibility"`
	SmallFunctionThreshold   int    `json:"small_function_threshold"`
	HideNoDescription        bool   `json:"hide_no_description"`
}

// Configuration for the live/incremental scanner, including default scan mode and update frequency.
type ScannerSection struct {
	// Mode is the default mode for the live/incremental scanner.
	Mode            ScanMode `json:"mode"`
	UpdateFrequency int      `json:"update_frequency"`
}

// Configuration for description generation specifying which resource kinds to describe and how many style exemplars to use as house-style anchors.
type DescriptionsSection struct {
	Kinds []domain.ResourceKind `json:"kinds"`
	// StyleExemplars is how many already-written neighbor descriptions to feed
	// the executor as house-style anchors. 0 disables (saves tokens).
	StyleExemplars int `json:"style_exemplars,omitempty"`
}

// AgentConfig is a main_agent or sub-agent entry under llm.<harness>. Absent
// slices and an empty/"<inherits>" model mean "inherit from the main agent".
type AgentConfig struct {
	Model        string         `json:"model,omitempty"`
	MCPTools     []string       `json:"mcp_tools,omitempty"`
	BlockedTools []string       `json:"blocked_tools,omitempty"`
	Plugins      []string       `json:"plugins,omitempty"`
	Params       map[string]int `json:"params,omitempty"`
}

// Configuration struct containing main and subordinate LLM agent configs.
type LLMHarness struct {
	MainAgent AgentConfig            `json:"main_agent"`
	Agents    map[string]AgentConfig `json:"agents,omitempty"`
}

// Struct holding LLM harness configs with per-harness overrides for OpenCode and ClaudeCode.
type LLMSection struct {
	// Any applies to both harnesses; the per-harness blocks take priority.
	Any        LLMHarness `json:"<any>"`
	OpenCode   LLMHarness `json:"opencode"`
	ClaudeCode LLMHarness `json:"claude_code"`
}

// Config struct for graph visualization settings, containing optimization rules.
type VizGraph struct {
	OptimizationRules string `json:"optimization_rules"`
}

// ChatAgentConfig is the proprietary-chat agent shape: a flat tool list plus
// optional model override and params (no mcp/native split).
type ChatAgentConfig struct {
	Model  string         `json:"model,omitempty"`
	Tools  []string       `json:"tools,omitempty"`
	Params map[string]int `json:"params,omitempty"`
}

// VizChatAgents models the JSON shape where a bare "model" string sits beside
// named-agent objects. The custom (un)marshalling splits the two apart.
type VizChatAgents struct {
	Model  string
	Agents map[string]ChatAgentConfig
}

// Encodes VizChatAgents to JSON with optional model field and named agent configs.
func (v VizChatAgents) MarshalJSON() ([]byte, error) {
	out := make(map[string]json.RawMessage, len(v.Agents)+1)
	if v.Model != "" {
		raw, err := json.Marshal(v.Model)
		if err != nil {
			return nil, err
		}
		out["model"] = raw
	}
	for name, agent := range v.Agents {
		raw, err := json.Marshal(agent)
		if err != nil {
			return nil, err
		}
		out[name] = raw
	}
	return json.Marshal(out)
}

// Decodes JSON into VizChatAgents, routing "model" key separately from named agent configs.
func (v *VizChatAgents) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	v.Agents = make(map[string]ChatAgentConfig, len(raw))
	for key, val := range raw {
		if key == "model" {
			var s string
			if err := json.Unmarshal(val, &s); err != nil {
				return err
			}
			v.Model = s
			continue
		}
		var agent ChatAgentConfig
		if err := json.Unmarshal(val, &agent); err != nil {
			return err
		}
		v.Agents[key] = agent
	}
	return nil
}

// Configuration for visualization chat with main agent and agent pool settings.
type VizChat struct {
	MainAgent ChatAgentConfig `json:"main_agent"`
	Agents    VizChatAgents   `json:"agents"`
}

// Config struct grouping graph and chat visualization settings.
type VizSection struct {
	Graph VizGraph `json:"graph"`
	Chat  VizChat  `json:"chat"`
}

// Root configuration struct holding scan, read, scanner, descriptions, LLM, and viz settings.
type Config struct {
	Scan         ScanSection         `json:"scan"`
	Read         ReadSection         `json:"read"`
	Scanner      ScannerSection      `json:"scanner"`
	Descriptions DescriptionsSection `json:"descriptions"`
	LLM          LLMSection          `json:"llm"`
	Viz          VizSection          `json:"viz"`
}

// Returns the maximum file size for scanning, defaulting to 512KB if not set.
func (c *Config) EffectiveMaxFileSize() int64 {
	if c.Read.MaxFileSize <= 0 {
		return 512 * 1024
	}
	return c.Read.MaxFileSize
}

// normalizeReadScan coerces a raw read.scan string to a known ReadScanMode,
// defaulting to ReadScanNone (which preserves the no-scan behavior).
func normalizeReadScan(s string) ReadScanMode {
	switch ReadScanMode(strings.ToLower(strings.TrimSpace(s))) {
	case ReadScanDefault:
		return ReadScanDefault
	case ReadScanFull:
		return ReadScanFull
	case ReadScanHard:
		return ReadScanHard
	default:
		return ReadScanNone
	}
}

// EffectiveReadScan resolves read.scan, defaulting to ReadScanNone. A non-none
// value asks read/grep entry points to run a scan before serving results.
func (c *Config) EffectiveReadScan() ReadScanMode {
	return normalizeReadScan(string(c.Read.Scan))
}

// EffectivePipePassthrough reports whether the tool guard should treat a
// read/grep shell command that consumes piped stdin (e.g. `cmd | tail`) as
// operating on command output and leave it ungated. Defaults to true; only an
// explicit `read.pipe_passthrough: false` restores the strict behavior of
// gating piped reads alongside direct file reads.
func (c *Config) EffectivePipePassthrough() bool {
	if c.Read.PipePassthrough == nil {
		return true
	}
	return *c.Read.PipePassthrough
}

// normalizeVisibility coerces a raw visibility string to one of
// "hidden" | "normal" | "full", defaulting to "normal".
func normalizeVisibility(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "hidden":
		return "hidden"
	case "full":
		return "full"
	default:
		return "normal"
	}
}

// Returns whether to include incoming resource references in context output.
func (c *Config) EffectiveIncludeIncoming() bool {
	return c.Read.ContextFilter.IncludeIncoming
}

// Returns the normalized visibility level for external variables from context filter configuration.
func (c *Config) EffectiveExternalVarsVisibility() string {
	return normalizeVisibility(c.Read.ContextFilter.ExternalVarsVisibility)
}

// Returns the normalized visibility level for small functions in context output.
func (c *Config) EffectiveSmallFunctionsVisibility() string {
	return normalizeVisibility(c.Read.ContextFilter.SmallFunctionsVisibility)
}

// Returns the line-count threshold for identifying small functions, defaulting to 5 if not set.
func (c *Config) EffectiveSmallFunctionThreshold() int {
	if c.Read.ContextFilter.SmallFunctionThreshold <= 0 {
		return 5
	}
	return c.Read.ContextFilter.SmallFunctionThreshold
}

// Returns whether to hide resources without descriptions in context output.
func (c *Config) EffectiveHideNoDescription() bool {
	return c.Read.ContextFilter.HideNoDescription
}

// EffectiveContextFilter composes the resolved read.context_filter settings
// into a domain.ContextFilter used by the topology read managers.
func (c *Config) EffectiveContextFilter() domain.ContextFilter {
	return domain.ContextFilter{
		IncludeIncoming:   c.EffectiveIncludeIncoming(),
		ExtVarsVisibility: domain.ParseVisibility(c.EffectiveExternalVarsVisibility()),
		SmallFnVisibility: domain.ParseVisibility(c.EffectiveSmallFunctionsVisibility()),
		SmallFnThreshold:  c.EffectiveSmallFunctionThreshold(),
		HideNoDescription: c.EffectiveHideNoDescription(),
	}
}

// Returns the config.json path derived from the database directory.
func ConfigPath(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), "config.json")
}

// Validate checks every tool name referenced in the config against the tool
// catalog (toolspec). It returns an error naming the offending agent and tool
// so a typo fails fast at init time.
func (c *Config) Validate() error {
	checkAgent := func(path string, mcpTools, blockedTools []string) error {
		if err := toolspec.ValidateMCPTools(mcpTools); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if err := toolspec.ValidateNativeTools(blockedTools); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		return nil
	}
	for _, h := range []struct {
		name    string
		harness LLMHarness
	}{
		{"<any>", c.LLM.Any},
		{"opencode", c.LLM.OpenCode},
		{"claude_code", c.LLM.ClaudeCode},
	} {
		if err := checkAgent(fmt.Sprintf("llm.%s.main_agent", h.name), h.harness.MainAgent.MCPTools, h.harness.MainAgent.BlockedTools); err != nil {
			return err
		}
		for agentName, ag := range h.harness.Agents {
			if err := checkAgent(fmt.Sprintf("llm.%s.agents.%s", h.name, agentName), ag.MCPTools, ag.BlockedTools); err != nil {
				return err
			}
		}
	}
	if err := toolspec.ValidateChatTools(c.Viz.Chat.MainAgent.Tools); err != nil {
		return fmt.Errorf("viz.chat.main_agent: %w", err)
	}
	for agentName, ag := range c.Viz.Chat.Agents.Agents {
		if err := toolspec.ValidateChatTools(ag.Tools); err != nil {
			return fmt.Errorf("viz.chat.agents.%s: %w", agentName, err)
		}
	}
	return nil
}

// Returns the default set of resource kinds that require descriptions: Functions, Methods, Types, Interfaces, and Files.

func DefaultNeedDescription() []domain.ResourceKind {
	return []domain.ResourceKind{
		domain.ResourceFunction,
		domain.ResourceMethod,
		domain.ResourceType,
		domain.ResourceInterface,
		domain.ResourceFile,
	}
}

func DefaultDescribeTargets() []domain.ResourceKind {
// Returns the default description resource kinds to generate descriptions for.
	return DefaultNeedDescription()
}

// DefaultAgentMCPTools returns the canonical MCP tool list for a known agent.
// agentName "" / "main" / "default" returns the main-agent set. This is the
// single source of truth for both DefaultConfig and the MCP registry.
//
// The default uses the generic "read" tool. To split reads by resource kind,
// replace "read" with the per-kind tools (read_function, read_struct,
// read_interface, read_named_type, read_file, read_package, read_dependency).
func DefaultAgentMCPTools(agentName string) []string {
// DefaultAgentMCPTools returns the canonical MCP tool list for a known agent.
// agentName "" / "main" / "default" returns the main-agent set. This is the
// single source of truth for both DefaultConfig and the MCP registry.
// The default uses the generic "read" tool. To split reads by resource kind,
// replace "read" with the per-kind tools (read_function, read_struct,
// read_interface, read_named_type, read_file, read_package, read_dependency).
	switch agentName {
	case "descriptions-generation-executor", "descriptions-executor":
		return []string{"read", "grep", "update_description"}
	case "bug-hunter":
		return []string{"read_file", "read_function", "read_struct", "read_interface", "grep", "bug_report"}
	case "bug-judge":
		return []string{"read_file", "read_function", "read_struct", "read_interface", "grep", "bug_list", "bug_acknowledge", "bug_dismiss", "bug_delete"}
	case "bug-solver":
		return []string{"read_file", "read_function", "read_struct", "read_interface", "grep", "edit", "write", "warnings_list", "bug_delete"}
	default:
		// The main agent orchestrates the bug workflows, so it needs read-only
		// bug_list to enumerate pending/acknowledged/dismissed bugs and drive the
		// per-bug fan-out and the hunter dedup loop.
		return []string{"read_file", "read_function", "read_struct", "read_interface", "grep", "edit", "write", "warnings_list", "bug_report", "bug_list"}
	}
}

func defaultBlockedTools() []string {
// Returns the default set of blocked tools: read, grep, edit, and write.
	return []string{"read", "grep", "edit", "write"}
}

func defaultChatMainAgentTools() []string {
// Returns the default tool set for the chat main agent including bash, MCP lookups, file operations, and topology utilities.
	return []string{"ls", "bash", "glob", "ask_user_question", "CreateTasks", "grep", "read_file", "read_function", "read_struct", "read_interface", "edit", "write", "warnings_list", "bug_report", "bug_list", "bug_acknowledge", "bug_dismiss", "bug_delete", "update_description", "node_list_no_description"}
}

// DefaultChatAgentTools returns the default tool list for a proprietary-chat
// sub-agent (used by CreateTasks). These are independent from the llm section.
func DefaultChatAgentTools(agentName string) []string {
// DefaultChatAgentTools returns the default tool list for a proprietary-chat
// sub-agent (used by CreateTasks). These are independent from the llm section.
	switch agentName {
	case "explorer":
		return []string{"read", "read_function", "read_struct", "read_interface", "read_named_type", "read_file", "read_package", "read_dependency", "grep"}
	case "descriptions-generation-executor":
		return []string{"read", "grep", "update_description"}
	case "bug-hunter":
		return []string{"read", "read_function", "read_struct", "read_interface", "read_file", "grep", "bug_report"}
	case "bug-judge":
		return []string{"read", "read_function", "read_struct", "read_interface", "read_file", "grep", "bug_list", "bug_acknowledge", "bug_dismiss", "bug_delete"}
	case "bug-solver":
		return []string{"read", "edit", "write", "read_function", "read_struct", "read_interface", "read_file", "grep", "warnings_list", "bug_delete"}
	default:
		return nil
	}
}

// DefaultBugJudgeThinkingBudget is the default extended-reasoning token budget
// for the proprietary-chat bug-judge sub-agent. Triage is a judgment task, so
// it reasons harder than the default. It is a no-op for providers/models that
// do not support reasoning (see chat newProvider).
const DefaultBugJudgeThinkingBudget = 4096
// DefaultBugJudgeThinkingBudget is the default extended-reasoning token budget
// for the proprietary-chat bug-judge sub-agent. Triage is a judgment task, so
// it reasons harder than the default. It is a no-op for providers/models that
// do not support reasoning (see chat newProvider).

// DefaultDescriptionStyleExemplars is how many neighbor descriptions are fed to
// the description executor as house-style anchors by default. Set to 0 to
// disable and save tokens.
const DefaultDescriptionStyleExemplars = 3
// DefaultDescriptionStyleExemplars is how many neighbor descriptions are fed to
// the description executor as house-style anchors by default. Set to 0 to
// disable and save tokens.

// DefaultBugSolverThinkingBudget is the default extended-reasoning token budget
// for the proprietary-chat bug-solver sub-agent. Producing a minimal correct
// fix is reasoning-heavy, so it reasons harder than the default. It is a no-op
// for providers/models that do not support reasoning (see chat newProvider).
const DefaultBugSolverThinkingBudget = 4096
// DefaultBugSolverThinkingBudget is the default extended-reasoning token budget
// for the proprietary-chat bug-solver sub-agent. Producing a minimal correct
// fix is reasoning-heavy, so it reasons harder than the default. It is a no-op
// for providers/models that do not support reasoning (see chat newProvider).

// DefaultBugHunterThinkingBudget is the default extended-reasoning token budget
// for the proprietary-chat bug-hunter sub-agent. Spotting real defects across a
// slice of resources is reasoning-heavy, so it reasons harder than the default.
// It is a no-op for providers/models that do not support reasoning (see chat
// newProvider).
const DefaultBugHunterThinkingBudget = 4096
// DefaultBugHunterThinkingBudget is the default extended-reasoning token budget
// for the proprietary-chat bug-hunter sub-agent. Spotting real defects across a
// slice of resources is reasoning-heavy, so it reasons harder than the default.
// It is a no-op for providers/models that do not support reasoning (see chat
// newProvider).

func boolPtr(b bool) *bool { return &b }
// Helper function that converts a boolean value to a pointer to bool.

func DefaultConfig() *Config {
// Returns default Config with scan/read/scanner/LLM/viz defaults and agent configurations.
	subAgent := func(name string) AgentConfig {
		ac := AgentConfig{
			Model:        InheritsModel,
			MCPTools:     DefaultAgentMCPTools(name),
			BlockedTools: defaultBlockedTools(),
		}
		if name == "descriptions-generation-executor" {
			ac.Params = map[string]int{"max-batch-size": DefaultDescriptionBatchSize}
		}
		return ac
	}
	return &Config{
		Scan: ScanSection{Mode: ScanModeDefault},
		Read: ReadSection{
			MaxFileSize:     512 * 1024,
			Scan:            ReadScanNone,
			PipePassthrough: boolPtr(true),
			ContextFilter: ContextFilterSection{
				ExternalVarsVisibility:   "normal",
				SmallFunctionsVisibility: "normal",
				SmallFunctionThreshold:   5,
			},
		},
		Scanner:      ScannerSection{Mode: ScanModeDefault, UpdateFrequency: 200},
		Descriptions: DescriptionsSection{Kinds: DefaultNeedDescription(), StyleExemplars: DefaultDescriptionStyleExemplars},
		LLM: LLMSection{
			Any: LLMHarness{
				// main_agent uses MCP edit/write (which sync the topology DB
				// inline), blocks the native versions, and has no plugins.
				MainAgent: AgentConfig{
					MCPTools:     DefaultAgentMCPTools("main"),
					BlockedTools: defaultBlockedTools(),
				},
				Agents: map[string]AgentConfig{
					"descriptions-generation-executor": subAgent("descriptions-generation-executor"),
					"bug-hunter":                       subAgent("bug-hunter"),
					"bug-judge":                        subAgent("bug-judge"),
					"bug-solver":                       subAgent("bug-solver"),
				},
			},
			OpenCode: LLMHarness{Agents: map[string]AgentConfig{}},
			// Description generation is high-volume and low-difficulty: run the
			// Claude Code executor on the fast/cheap tier by default. Safe to
			// pin here because Claude Code runs Claude models; other harnesses
			// inherit and stay configurable.
			ClaudeCode: LLMHarness{Agents: map[string]AgentConfig{
				"descriptions-generation-executor": {Model: "haiku"},
			}},
		},
		Viz: VizSection{
			Graph: VizGraph{OptimizationRules: ".aracne/optimization_rules.json"},
			Chat: VizChat{
				MainAgent: ChatAgentConfig{Tools: defaultChatMainAgentTools()},
				// viz.chat is fully self-contained: chat sub-agents are listed
				// here with their own tools and never inherit from llm.<any>
				// (which applies only to claude_code / opencode).
				Agents: VizChatAgents{
					Agents: map[string]ChatAgentConfig{
						"explorer":   {Tools: DefaultChatAgentTools("explorer")},
						"bug-hunter": {Tools: DefaultChatAgentTools("bug-hunter"), Params: map[string]int{"thinking": DefaultBugHunterThinkingBudget}},
						"bug-judge":  {Tools: DefaultChatAgentTools("bug-judge"), Params: map[string]int{"thinking": DefaultBugJudgeThinkingBudget}},
						"bug-solver": {Tools: DefaultChatAgentTools("bug-solver"), Params: map[string]int{"thinking": DefaultBugSolverThinkingBudget}},
						"descriptions-generation-executor": {
							Tools:  DefaultChatAgentTools("descriptions-generation-executor"),
							Params: map[string]int{"max-batch-size": DefaultDescriptionBatchSize},
						},
					},
				},
			},
		},
	}
}

// ---------------------------------------------------------------------------
// Agent resolution (inheritance + per-harness merge)
// ---------------------------------------------------------------------------

func harnessBlock(c *Config, harness string) LLMHarness {
// Returns the LLM harness configuration block for the specified harness name.
	switch harness {
	case "opencode":
		return c.LLM.OpenCode
	case "claude_code":
		return c.LLM.ClaudeCode
	default:
		return LLMHarness{}
	}
}

// EffectiveAgent resolves the config for (harness, agentName). harness is
// "opencode" or "claude_code"; agentName "" / "main" / "default" resolves the
// main agent. The per-harness block wins over <any>, and "<inherits>"/absent
// fields fall back to the main agent.
func (c *Config) EffectiveAgent(harness, agentName string) AgentConfig {
// EffectiveAgent resolves the config for (harness, agentName). harness is
// "opencode" or "claude_code"; agentName "" / "main" / "default" resolves the
// main agent. The per-harness block wins over <any>, and "<inherits>"/absent
// fields fall back to the main agent.
	hb := harnessBlock(c, harness)
	main := mergeAgent(c.LLM.Any.MainAgent, hb.MainAgent)
	if agentName == "" || agentName == "main" || agentName == "default" {
		return resolveInherits(main, main)
	}
	var anyAg, hAg AgentConfig
	if c.LLM.Any.Agents != nil {
		anyAg = c.LLM.Any.Agents[agentName]
	}
	if hb.Agents != nil {
		hAg = hb.Agents[agentName]
	}
	merged := mergeAgent(anyAg, hAg)
	return resolveInherits(merged, main)
}

func mergeAgent(base, over AgentConfig) AgentConfig {
// Merges an override agent config into a base config, combining model, tools, plugins, and parameters.
	result := base
	if over.Model != "" {
		result.Model = over.Model
	}
	if over.MCPTools != nil {
		result.MCPTools = over.MCPTools
	}
	if over.BlockedTools != nil {
		result.BlockedTools = over.BlockedTools
	}
	if over.Plugins != nil {
		result.Plugins = over.Plugins
	}
	if over.Params != nil {
		merged := make(map[string]int, len(result.Params)+len(over.Params))
		for k, v := range result.Params {
			merged[k] = v
		}
		for k, v := range over.Params {
			merged[k] = v
		}
		result.Params = merged
	}
	return result
}

func resolveInherits(ag, main AgentConfig) AgentConfig {
// Resolves agent configuration by inheriting unset fields (Model, MCPTools, BlockedTools) from a main agent.
	if ag.Model == "" || ag.Model == InheritsModel {
		ag.Model = main.Model
	}
	if ag.MCPTools == nil {
		ag.MCPTools = main.MCPTools
	}
	if ag.BlockedTools == nil {
		ag.BlockedTools = main.BlockedTools
	}
	return ag
}

// AgentParam returns the integer param for (harness, agentName, key), or
// fallback when unset or non-positive.
func (c *Config) AgentParam(harness, agentName, key string, fallback int) int {
// AgentParam returns the integer param for (harness, agentName, key), or
// fallback when unset or non-positive.
	eff := c.EffectiveAgent(harness, agentName)
	if eff.Params != nil {
		if v, ok := eff.Params[key]; ok && v > 0 {
			return v
		}
	}
	return fallback
}

// ---------------------------------------------------------------------------
// Load / save
// ---------------------------------------------------------------------------

// validConfig reports whether a decoded config looks like the new schema. An
// old-format file decodes to all-zero new-schema fields and is rejected so
// EnsureConfig can clean-break migrate it.
func validConfig(c *Config) bool {
// validConfig reports whether a decoded config looks like the new schema. An
// old-format file decodes to all-zero new-schema fields and is rejected so
// EnsureConfig can clean-break migrate it.
	return c.LLM.Any.MainAgent.MCPTools != nil ||
		len(c.LLM.Any.Agents) > 0 ||
		c.Scan.Mode != "" ||
		c.Read.MaxFileSize != 0 ||
		len(c.Descriptions.Kinds) > 0 ||
		c.Viz.Chat.MainAgent.Tools != nil ||
		c.Scanner.UpdateFrequency != 0
}

func normalizeConfig(c *Config) {
// Applies defaults to config fields for scan modes, file limits, visibility filters, descriptions, optimization rules, and LLM agents.
	switch c.Scan.Mode {
	case ScanModeDefault, ScanModeHard, ScanModeAll:
	default:
		c.Scan.Mode = ScanModeDefault
	}
	switch c.Scanner.Mode {
	case ScanModeDefault, ScanModeHard, ScanModeAll:
	default:
		c.Scanner.Mode = ScanModeDefault
	}
	if c.Scanner.UpdateFrequency <= 0 {
		c.Scanner.UpdateFrequency = 200
	}
	if c.Read.MaxFileSize <= 0 {
		c.Read.MaxFileSize = 512 * 1024
	}
	c.Read.Scan = normalizeReadScan(string(c.Read.Scan))
	c.Read.ContextFilter.ExternalVarsVisibility = normalizeVisibility(c.Read.ContextFilter.ExternalVarsVisibility)
	c.Read.ContextFilter.SmallFunctionsVisibility = normalizeVisibility(c.Read.ContextFilter.SmallFunctionsVisibility)
	if c.Read.ContextFilter.SmallFunctionThreshold <= 0 {
		c.Read.ContextFilter.SmallFunctionThreshold = 5
	}
	if len(c.Descriptions.Kinds) == 0 {
		c.Descriptions.Kinds = DefaultNeedDescription()
	} else if kinds, err := NormalizeDescribeTargets(c.Descriptions.Kinds); err == nil {
		c.Descriptions.Kinds = kinds
	}
	if strings.TrimSpace(c.Viz.Graph.OptimizationRules) == "" {
		c.Viz.Graph.OptimizationRules = ".aracne/optimization_rules.json"
	}
	if c.LLM.Any.Agents == nil {
		c.LLM.Any.Agents = map[string]AgentConfig{}
	}
	if c.LLM.OpenCode.Agents == nil {
		c.LLM.OpenCode.Agents = map[string]AgentConfig{}
	}
	if c.LLM.ClaudeCode.Agents == nil {
		c.LLM.ClaudeCode.Agents = map[string]AgentConfig{}
	}
	if c.Viz.Chat.Agents.Agents == nil {
		c.Viz.Chat.Agents.Agents = map[string]ChatAgentConfig{}
	}
}

// LoadConfigStrict loads the config and reports whether it parsed cleanly as
// the new schema. A missing file, a JSON error, or an old-format file all
// return (DefaultConfig(), false).
func LoadConfigStrict(path string) (*Config, bool) {
// LoadConfigStrict loads the config and reports whether it parsed cleanly as
// the new schema. A missing file, a JSON error, or an old-format file all
// return (DefaultConfig(), false).
	data, err := os.ReadFile(path)
	if err != nil {
		return DefaultConfig(), false
	}
	var loaded Config
	if err := json.Unmarshal(data, &loaded); err != nil {
		return DefaultConfig(), false
	}
	if !validConfig(&loaded) {
		return DefaultConfig(), false
	}
	normalizeConfig(&loaded)
	return &loaded, true
}

func LoadConfig(path string) *Config {
// Loads config from a path, returning the config without error handling.
	cfg, _ := LoadConfigStrict(path)
	return cfg
}

func SaveConfig(cfg *Config, path string) error {
// Writes a Config struct to a JSON file with pretty-printing, preserving special tokens like "<any>" and "<inherits>" as readable literals.
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// Keep "<any>" / "<inherits>" literal instead of <-escaped so the file
	// stays human-editable.
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(cfg); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0644)
}

// EnsureConfig returns the config at path, creating it with defaults when
// missing. If an existing file does not parse as the new schema (e.g. an
// old-format config), it is overwritten with fresh defaults (clean break).
func EnsureConfig(path string) *Config {
// EnsureConfig returns the config at path, creating it with defaults when
// missing. If an existing file does not parse as the new schema (e.g. an
// old-format config), it is overwritten with fresh defaults (clean break).
	if _, err := os.Stat(path); os.IsNotExist(err) {
		cfg := DefaultConfig()
		if saveErr := SaveConfig(cfg, path); saveErr != nil {
			return cfg
		}
		return cfg
	}
	cfg, ok := LoadConfigStrict(path)
	if !ok {
		_ = SaveConfig(cfg, path)
	}
	return cfg
}

// ---------------------------------------------------------------------------
// Describe-target helpers (unchanged)
// ---------------------------------------------------------------------------

func ParseDescribeTargets(value string) ([]domain.ResourceKind, error) {
// Parses comma-separated describe targets into a deduplicated slice of ResourceKinds.
	if strings.TrimSpace(value) == "" {
		return nil, fmt.Errorf("describe targets cannot be empty")
	}
	parts := strings.Split(value, ",")
	targets := make([]domain.ResourceKind, 0, len(parts))
	for _, part := range parts {
		kind, err := ParseDescribeTarget(part)
		if err != nil {
			return nil, err
		}
		targets = append(targets, kind)
	}
	return dedupeDescribeTargets(targets), nil
}

func NormalizeDescribeTargets(targets []domain.ResourceKind) ([]domain.ResourceKind, error) {
// Parses and dedupes describe targets into a normalized ResourceKind slice.
	normalized := make([]domain.ResourceKind, 0, len(targets))
	for _, target := range targets {
		kind, err := ParseDescribeTarget(string(target))
		if err != nil {
			return nil, err
		}
		normalized = append(normalized, kind)
	}
	return dedupeDescribeTargets(normalized), nil
}

func ParseDescribeTarget(value string) (domain.ResourceKind, error) {
// Parses a single describe target string into a ResourceKind, normalizing whitespace and hyphens.
	s := strings.ToLower(strings.TrimSpace(value))
	s = strings.ReplaceAll(s, "-", "_")
	s = strings.ReplaceAll(s, " ", "_")
	s = strings.TrimSuffix(s, "s")

	switch s {
	case "package":
		return domain.ResourcePackage, nil
	case "file":
		return domain.ResourceFile, nil
	case "function":
		return domain.ResourceFunction, nil
	case "method":
		return domain.ResourceMethod, nil
	case "type", "struct", "class", "classe":
		return domain.ResourceType, nil
	case "named_type":
		return domain.ResourceNamedType, nil
	case "interface":
		return domain.ResourceInterface, nil
	case "variable", "external_var", "externalvar":
		return domain.ResourceVariable, nil
	case "dependency", "dependencie":
		return domain.ResourceDependency, nil
	default:
		return "", fmt.Errorf("invalid describe target %q", value)
	}
}

func DescribeTargetSet(targets []domain.ResourceKind) map[domain.ResourceKind]bool {
// Converts a list of target resource kinds into a lookup map, defaulting to DefaultDescribeTargets if nil.
	if targets == nil {
		targets = DefaultDescribeTargets()
	}
	set := make(map[domain.ResourceKind]bool, len(targets))
	for _, target := range targets {
		set[target] = true
	}
	return set
}

func ShouldDescribeKind(kind domain.ResourceKind, targets []domain.ResourceKind) bool {
// Checks if a resource kind should be described based on configured describe targets.
	if targets == nil {
		targets = DefaultDescribeTargets()
	}
	return DescribeTargetSet(targets)[kind]
}

func FormatDescribeTargets(targets []domain.ResourceKind) string {
// Formats a slice of describe targets into a comma-separated string.
	if targets == nil {
		targets = DefaultDescribeTargets()
	}
	values := make([]string, 0, len(targets))
	for _, target := range targets {
		values = append(values, string(target))
	}
	return strings.Join(values, ", ")
}

func dedupeDescribeTargets(targets []domain.ResourceKind) []domain.ResourceKind {
// Removes duplicate ResourceKind targets, returning only the first occurrence of each unique kind.
	seen := make(map[domain.ResourceKind]bool, len(targets))
	result := make([]domain.ResourceKind, 0, len(targets))
	for _, target := range targets {
		if seen[target] {
			continue
		}
		seen[target] = true
		result = append(result, target)
	}
	return result
}
