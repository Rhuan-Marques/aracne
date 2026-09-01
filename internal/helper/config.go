package helper

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	// Ignore is a list of .gitignore-style glob patterns. Any path matching a
	// pattern is skipped by the scanner on every front (file discovery, manifest,
	// and parsing in every mode), so ignored files never enter the topology.
	// Patterns support *, **, ?, a trailing "/" (directory-only), and float at any
	// depth unless they contain a "/". There is no "!" negation.
	Ignore []string `json:"ignore"`
	// Workers is the maximum number of files parsed concurrently during a full
	// scan. 0 (the default) means auto = runtime.NumCPU(). Lower it to cap peak
	// RAM on large projects; the `--workers` flag overrides this per run.
	Workers int `json:"workers"`
	// Progress controls the scan progress bar: "auto" (default; shown on a
	// terminal when the project has more than ProgressFileThreshold files),
	// "always", or "never". The `--progress` flag overrides this per run.
	Progress string `json:"progress"`
}

// ProgressFileThreshold is the file count above which an "auto" scan progress
// bar is shown.
const ProgressFileThreshold = 15

// Scan progress modes for ScanSection.Progress / the `--progress` flag.
const (
	ProgressAuto   = "auto"
	ProgressAlways = "always"
	ProgressNever  = "never"
)

// Configuration for file read operations, including max file size, scan mode, context filtering, and shell command passthrough behavior.
type ReadSection struct {
	MaxFileSize int64        `json:"max_file_size"`
	Scan        ReadScanMode `json:"scan"`
	// Kinds is the global allow-list of resource kinds the `read` tool will return.
	//
	// This replaced the per-agent read_function/read_struct/read_interface/... tool lists.
	// Which KINDS are readable is a property of the project, not of an agent, and splitting
	// one capability across eight tool names made models pick the wrong one. An ID that
	// resolves to a kind absent from this list returns an error naming the allowed kinds.
	// Absent (null) means the default set; an explicit [] would make read useless and is
	// rejected by Validate.
	Kinds         []domain.ResourceKind `json:"kinds"`
	ContextFilter ContextFilterSection  `json:"context_filter"`
	// PipePassthrough exempts read/grep shell commands that consume piped
	// stdin (e.g. `cmd | tail`) from the tool guard: such commands operate on
	// command output, which the aracne MCP tools cannot serve. Direct file
	// reads (`cat foo.go`) are still gated. Absent means the default (true).
	PipePassthrough *bool `json:"pipe_passthrough"`
	// FileMode decides what a whole-FILE read returns: "full" (default) is the file verbatim,
	// "skeleton" is each top-level declaration's signature with large bodies elided.
	//
	// A benchmark run measured a file read returning 1.00x the bytes on disk, plus the context
	// block on top -- strictly more expensive than `cat`, with the context block as the only
	// thing bought. Skeleton mode is the answer, but it is opt-in: a model that has only seen
	// a skeleton must not build an `edit` old_string from it, and that risk is worth measuring
	// before it is anyone's default. Reading a SYMBOL is unaffected and already compact.
	FileMode string `json:"file_mode"`
	// SkeletonThreshold is how many lines a declaration may span before file_mode "skeleton"
	// replaces its body with an elision marker. Absent uses the default.
	//
	// It exists because skeleton mode originally borrowed
	// read.context_filter.small_function_threshold, whose real job is deciding how verbosely
	// a NEIGHBOUR is rendered in the "# CONTEXT:" block. The benchmark config set that to 40
	// for good reasons of its own, and at 40 lines almost nothing in real code elides:
	// skeleton mode fired on 2 of 39 reads in compact-blocked-after-bs-20260830c and the
	// median whole-file read still returned 1.00x the bytes on disk. Two different questions
	// deserve two different knobs.
	SkeletonThreshold *int `json:"skeleton_threshold,omitempty"`
	// MaxSymbolLines caps a SYMBOL body the same way SkeletonThreshold caps a file's
	// declarations: past it the read returns the signature plus an elision marker naming the
	// id, and the model can ask again with `full: true` for the exact bytes.
	//
	// Symbol reads were the one path with no ceiling at all. Reading
	// ['into_config', 'EngineState.merge_env'] out of nushell returned 75,893 bytes -- the
	// largest single tool result across two benchmark runs -- because `into_config` is one
	// enormous function. 0 or negative disables the cap.
	MaxSymbolLines *int `json:"max_symbol_lines,omitempty"`
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
	HideNoDescription        *bool  `json:"hide_no_description"`
	// MaxInlineParentLines caps how large a method's enclosing type may be before it stops
	// being printed above the method and becomes an ordinary context entry. 0 disables
	// inlining, negative means no cap. Absent uses the default.
	MaxInlineParentLines *int `json:"max_inline_parent_lines,omitempty"`
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
	// IncludeNotVisible, when false (default), skips undocumented targets the
	// read context filter would not render as a normal line (small functions /
	// external vars configured full or hidden).
	IncludeNotVisible bool `json:"include_not_visible,omitempty"`
}

// GrepSection tunes `grep` / `arac grep`.
type GrepSection struct {
	// DescriptionKinds lists the resource kinds whose stored description may
	// match the search pattern. A node found this way is returned even when its
	// source contains no matching line, which is the whole point: descriptions
	// live only in the topology DB.
	//
	// Absent (null) means the default set; an explicit [] disables description
	// matching entirely. Kinds whose descriptions are thin or auto-seeded from
	// doc comments (file, package, variable) are excluded by default because
	// they flood results without answering anything.
	DescriptionKinds []domain.ResourceKind `json:"description_kinds"`
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

// FeaturesSection gates optional surfaces that are not part of the default product. An
// absent section means every feature is off, which is what an existing project's config
// decodes to -- so adding a feature here never turns something on for an existing user.
type FeaturesSection struct {
	// BugManagement enables the bug pipeline as a whole: `arac init` writes the
	// bug-hunter/judge/solver agents and their commands, the bug_* MCP tools become
	// servable, the `arac bug` usage block prints, and viz exposes /api/bugs and bug
	// counts. Off by default: the pipeline is unproven and its tool schemas are context
	// cost on every request for a workflow most projects never run.
	//
	// `arac bug` itself stays dispatchable in both states -- it is the orchestration
	// channel the generated commands use, and the debugging path.
	BugManagement bool `json:"bug_management"`
}

// Root configuration struct holding scan, read, scanner, descriptions, LLM, and viz settings.
type Config struct {
	Scan         ScanSection         `json:"scan"`
	Read         ReadSection         `json:"read"`
	Scanner      ScannerSection      `json:"scanner"`
	Descriptions DescriptionsSection `json:"descriptions"`
	Grep         GrepSection         `json:"grep"`
	LLM          LLMSection          `json:"llm"`
	Viz          VizSection          `json:"viz"`
	Features     FeaturesSection     `json:"features"`
	// Paths marks directories/files (relative to the topology root) as hidden or
	// visible. Hidden paths are skipped by the indexing and scan stages in every
	// mode (default/all/hard). More specific (more internal) rules win, so a
	// parent can be hidden while a nested child stays visible.
	Paths []domain.PathRule `json:"paths"`
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
// FileModeSkeleton renders a file as its declarations with large bodies elided;
// FileModeFull returns the file verbatim.
const (
	FileModeFull     = "full"
	FileModeSkeleton = "skeleton"
)

// EffectiveFileMode resolves read.file_mode, defaulting to "full" for anything unrecognized
// so a typo degrades to today's behaviour rather than to a surprising one.
func (c *Config) EffectiveFileMode() string {
	if strings.ToLower(strings.TrimSpace(c.Read.FileMode)) == FileModeSkeleton {
		return FileModeSkeleton
	}
	return FileModeFull
}

// DefaultSkeletonThreshold is the declaration size, in lines, past which file_mode "skeleton"
// elides a body. Twelve keeps small helpers, constructors and one-line accessors intact --
// which is most of what a reader wants a file's shape for -- while eliding the bodies that
// make a whole-file read cost the same as `cat`.
const DefaultSkeletonThreshold = 12

// DefaultMaxSymbolLines is the body size, in lines, past which a symbol read elides. Generous
// on purpose: reading a symbol is the habit the tool wants to encourage, and truncating an
// ordinary 100-line function would punish exactly the behaviour it is trying to buy.
const DefaultMaxSymbolLines = 160

// EffectiveSkeletonThreshold resolves read.skeleton_threshold.
func (c *Config) EffectiveSkeletonThreshold() int {
	if c.Read.SkeletonThreshold == nil || *c.Read.SkeletonThreshold <= 0 {
		return DefaultSkeletonThreshold
	}
	return *c.Read.SkeletonThreshold
}

// EffectiveMaxSymbolLines resolves read.max_symbol_lines. A configured 0 or negative disables
// the cap, which is why absence and zero must stay distinguishable.
func (c *Config) EffectiveMaxSymbolLines() int {
	if c.Read.MaxSymbolLines == nil {
		return DefaultMaxSymbolLines
	}
	if *c.Read.MaxSymbolLines <= 0 {
		return 0
	}
	return *c.Read.MaxSymbolLines
}

// EffectiveHideNoDescription resolves read.context_filter.hide_no_description, defaulting to
// TRUE when the key is absent -- hence the pointer, which is how this file already
// distinguishes "unset" from a meaningful false (see EffectiveMaxInlineParentLines).
//
// The default flipped after a benchmark run measured what an undescribed neighbour actually
// buys. The CONTEXT section earns its tokens by letting the model skip a read: a description
// says whether the neighbour matters. An entry with no description makes that promise and
// does not keep it, so it costs a line and answers nothing. Constants are exempt upstream --
// a value shown is a description (see applyExtVars).
func (c *Config) EffectiveHideNoDescription() bool {
	if c.Read.ContextFilter.HideNoDescription == nil {
		return true
	}
	return *c.Read.ContextFilter.HideNoDescription
}

// EffectiveMaxInlineParentLines resolves read.context_filter.max_inline_parent_lines,
// defaulting when the key is absent. 0 and negative values are meaningful (disable inlining /
// no cap), so absence is signalled by a nil pointer rather than by a zero.
func (c *Config) EffectiveMaxInlineParentLines() int {
	if c.Read.ContextFilter.MaxInlineParentLines == nil {
		return domain.DefaultMaxInlineParentLines
	}
	return *c.Read.ContextFilter.MaxInlineParentLines
}

// EffectiveReadKinds resolves read.kinds, defaulting when absent.
func (c *Config) EffectiveReadKinds() []domain.ResourceKind {
	if c.Read.Kinds == nil {
		return DefaultReadKinds()
	}
	return c.Read.Kinds
}

// DefaultReadKinds is what `read` resolves out of the box: the kinds a model actually
// navigates by. named_type, package, dependency and variable are supported but off by default
// -- they are reachable through the context section of a read that matters, and listing them
// widens the surface for little gain.
func DefaultReadKinds() []domain.ResourceKind {
	return []domain.ResourceKind{
		domain.ResourceFile,
		domain.ResourceFunction,
		domain.ResourceStruct,
		domain.ResourceInterface,
	}
}

// AllReadKinds is every kind valid in read.kinds. Note there is no "method": methods resolve
// as functions, exactly as they did under read_function.
func AllReadKinds() []domain.ResourceKind {
	return []domain.ResourceKind{
		domain.ResourceFile,
		domain.ResourceFunction,
		domain.ResourceStruct,
		domain.ResourceInterface,
		domain.ResourceNamedType,
		domain.ResourcePackage,
		domain.ResourceDependency,
		domain.ResourceVariable,
	}
}

// ValidateReadKinds reports any entry that is not a valid read.kinds value.
func ValidateReadKinds(kinds []domain.ResourceKind) error {
	if kinds == nil {
		return nil
	}
	if len(kinds) == 0 {
		return fmt.Errorf("read.kinds is empty: read would reject every resource")
	}
	allowed := map[domain.ResourceKind]bool{}
	for _, k := range AllReadKinds() {
		allowed[k] = true
	}
	var unknown []string
	for _, k := range kinds {
		if !allowed[k] {
			unknown = append(unknown, string(k))
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	names := make([]string, 0, len(AllReadKinds()))
	for _, k := range AllReadKinds() {
		names = append(names, string(k))
	}
	return fmt.Errorf("unknown read.kinds value(s): %s (valid: %s)",
		strings.Join(unknown, ", "), strings.Join(names, ", "))
}

// EffectiveContextFilter composes the resolved read.context_filter settings
// into a domain.ContextFilter used by the topology read managers.
func (c *Config) EffectiveContextFilter() domain.ContextFilter {
	return domain.ContextFilter{
		IncludeIncoming:      c.EffectiveIncludeIncoming(),
		ExtVarsVisibility:    domain.ParseVisibility(c.EffectiveExternalVarsVisibility()),
		SmallFnVisibility:    domain.ParseVisibility(c.EffectiveSmallFunctionsVisibility()),
		SmallFnThreshold:     c.EffectiveSmallFunctionThreshold(),
		HideNoDescription:    c.EffectiveHideNoDescription(),
		MaxInlineParentLines: c.EffectiveMaxInlineParentLines(),
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
	if err := ValidateReadKinds(c.Read.Kinds); err != nil {
		return fmt.Errorf("read.kinds: %w", err)
	}
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

// Returns the default set of resource kinds that require descriptions: Functions, Methods, Types, and Interfaces.

func DefaultNeedDescription() []domain.ResourceKind {
	return []domain.ResourceKind{
		domain.ResourceFunction,
		domain.ResourceMethod,
		domain.ResourceStruct,
		domain.ResourceInterface,
	}
}

func DefaultDescribeTargets() []domain.ResourceKind {
	// Returns the default description resource kinds to generate descriptions for.
	return DefaultNeedDescription()
}

// DefaultGrepDescriptionKinds returns the resource kinds whose description grep
// may match. It is deliberately the same set aracne writes descriptions for:
// those are the nodes whose prose is authored rather than scraped from a doc
// comment, so a match on them means something.
func DefaultGrepDescriptionKinds() []domain.ResourceKind {
	return DefaultNeedDescription()
}

// DefaultAgentMCPTools returns the canonical MCP tool list for a known agent.
// agentName "" / "main" / "default" returns the main-agent set. This is the
// single source of truth for both DefaultConfig and the MCP registry.
//
// There is exactly one read entry. The per-kind tools (read_function, read_struct,
// read_interface, read_named_type, read_file, read_package, read_dependency) are gone:
// listing an agent's readable KINDS is now read.kinds, which is a project-wide setting, and
// "read" here only says whether this agent may read at all.
func DefaultAgentMCPTools(agentName string) []string {
	switch agentName {
	case "descriptions-generation-executor", "descriptions-executor":
		return []string{"read", "grep", "update_description"}
	case "bug-hunter":
		return []string{"read", "grep", "bug_report"}
	case "bug-judge":
		return []string{"read", "grep", "bug_list", "bug_acknowledge", "bug_dismiss", "bug_delete"}
	case "bug-solver":
		return []string{"read", "grep", "edit", "write", "warnings_list", "bug_delete"}
	default:
		// The bug pipeline is a v2 feature and is not part of the default surface, so
		// `bug_report`/`bug_list` are NOT here: their schemas cost roughly 260 tokens on
		// every single request for a workflow the shipped binary does not run. Projects
		// using the bug agents add them back via llm.<harness>.main_agent.mcp_tools.
		return []string{"read", "grep", "edit", "write", "warnings_list"}
	}
}

func defaultBlockedTools() []string {
	// Ship WARN-ONLY: the guard tells the agent which aracne tool matches the native one
	// it just used, and does not deny anything. Hard-blocking is opt-in per project by
	// listing tools here.
	//
	// Blocking used to be the default, on the theory that it forced agents onto the
	// topology-aware tools. In practice it forced them off a capable native grep onto a
	// less capable one, and every denial cost a wasted turn — which is a large part of why
	// the aracne arm spent MORE context than the baseline. Denial is also unnecessary for
	// graph consistency: the `arac update-file` PostToolUse hook re-syncs the topology
	// after a native edit (see internal/cli/native_hooks.go).
	//
	// A tool has to earn its use by being better, not by being the only one allowed.
	return []string{}
}

func defaultChatMainAgentTools() []string {
	// Returns the default tool set for the chat main agent including bash, MCP lookups, file operations, and topology utilities.
	return []string{"ls", "bash", "glob", "ask_user_question", "CreateTasks", "grep", "read", "edit", "write", "warnings_list", "bug_report", "bug_list", "bug_acknowledge", "bug_dismiss", "bug_delete", "update_description", "node_list_no_description"}
}

// DefaultChatAgentTools returns the default tool list for a proprietary-chat
// sub-agent (used by CreateTasks). These are independent from the llm section.
func DefaultChatAgentTools(agentName string) []string {
	// DefaultChatAgentTools returns the default tool list for a proprietary-chat
	// sub-agent (used by CreateTasks). These are independent from the llm section.
	switch agentName {
	case "explorer":
		return []string{"read", "grep"}
	case "descriptions-generation-executor":
		return []string{"read", "grep", "update_description"}
	case "bug-hunter":
		return []string{"read", "grep", "bug_report"}
	case "bug-judge":
		return []string{"read", "grep", "bug_list", "bug_acknowledge", "bug_dismiss", "bug_delete"}
	case "bug-solver":
		return []string{"read", "edit", "write", "grep", "warnings_list", "bug_delete"}
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
const DefaultDescriptionStyleExemplars = 1

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

// intPtr returns a pointer to i, for config fields where 0 is a meaningful value and absence
// must be distinguishable from it.
func intPtr(i int) *int { return &i }

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
		// The hunter and the judge only ever read and record a verdict; only the solver
		// changes code. Leaving them the native Edit/Write contradicted their own prompts
		// ("use only its restricted tools") and handed two read-only agents the ability to
		// rewrite the codebase.
		//
		// Deliberately NOT "read": blocking read renames the aracne read tool
		// (toolspec.ResolveReadToolName), which is a separate decision from privilege.
		if name == "bug-hunter" || name == "bug-judge" {
			ac.BlockedTools = []string{"edit", "write"}
		}
		return ac
	}
	return &Config{
		Scan:  ScanSection{Mode: ScanModeDefault, Ignore: []string{}, Workers: 0, Progress: ProgressAuto},
		Paths: []domain.PathRule{},
		Read: ReadSection{
			MaxFileSize:     512 * 1024,
			Scan:            ReadScanNone,
			Kinds:           DefaultReadKinds(),
			PipePassthrough: boolPtr(true),
			ContextFilter: ContextFilterSection{
				ExternalVarsVisibility:   "normal",
				SmallFunctionsVisibility: "normal",
				SmallFunctionThreshold:   5,
				MaxInlineParentLines:     intPtr(domain.DefaultMaxInlineParentLines),
			},
		},
		Scanner:      ScannerSection{Mode: ScanModeDefault, UpdateFrequency: 200},
		Descriptions: DescriptionsSection{Kinds: DefaultNeedDescription(), StyleExemplars: DefaultDescriptionStyleExemplars},
		Grep:         GrepSection{DescriptionKinds: DefaultGrepDescriptionKinds()},
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
		c.Scanner.UpdateFrequency != 0 ||
		// A features-only file is a legitimate new-schema config: turning a feature on is
		// the one edit a user makes by hand. Without this line the file decodes to all-zero
		// sentinels, is judged legacy, and EnsureConfig overwrites it with defaults -- so
		// enabling the feature would silently turn it back off.
		c.Features.BugManagement
}

// BugManagementEnabled reports whether the bug pipeline is turned on for this project.
// Every gate reads it through this method rather than the field, so the flag can grow
// siblings without a scattered rename.
func (c *Config) BugManagementEnabled() bool { return c.Features.BugManagement }

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
	if c.Read.Kinds == nil {
		c.Read.Kinds = DefaultReadKinds()
	}
	if c.Read.ContextFilter.MaxInlineParentLines == nil {
		c.Read.ContextFilter.MaxInlineParentLines = intPtr(domain.DefaultMaxInlineParentLines)
	}
	if c.Read.ContextFilter.SmallFunctionThreshold <= 0 {
		c.Read.ContextFilter.SmallFunctionThreshold = 5
	}
	if len(c.Descriptions.Kinds) == 0 {
		c.Descriptions.Kinds = DefaultNeedDescription()
	} else if kinds, err := NormalizeDescribeTargets(c.Descriptions.Kinds); err == nil {
		c.Descriptions.Kinds = kinds
	}
	// nil (key absent) means "use the defaults"; an explicit [] is a deliberate
	// "never match on description" and must survive normalization.
	if c.Grep.DescriptionKinds == nil {
		c.Grep.DescriptionKinds = DefaultGrepDescriptionKinds()
	} else if kinds, err := NormalizeDescribeTargets(c.Grep.DescriptionKinds); err == nil {
		c.Grep.DescriptionKinds = kinds
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
	if c.Paths == nil {
		c.Paths = []domain.PathRule{}
	}
	if c.Scan.Ignore == nil {
		c.Scan.Ignore = []string{}
	}
	if c.Scan.Workers < 0 {
		c.Scan.Workers = 0
	}
	switch c.Scan.Progress {
	case ProgressAuto, ProgressAlways, ProgressNever:
	default:
		c.Scan.Progress = ProgressAuto
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
	case "struct":
		return domain.ResourceStruct, nil
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

// locLineSpan returns the inclusive line count of a location, or 0 when unknown.
func locLineSpan(loc domain.Location) int {
	if loc.EndsAt < loc.StartsAt {
		return 0
	}
	return loc.EndsAt - loc.StartsAt + 1
}

// ShouldDescribe reports whether res is an undocumented description target worth
// listing/counting/generating. targetSet is the set of describable kinds. When
// includeNotVisible is false, targets whose read-context visibility is not
// Normal (small functions / external vars configured full or hidden) are
// skipped, since the read tools would render them as full code or hide them
// rather than as a "ID: description" line.
func ShouldDescribe(res domain.Resource, targetSet map[domain.ResourceKind]bool, filter domain.ContextFilter, includeNotVisible bool) bool {
	if strings.TrimSpace(res.Description) != "" || !targetSet[res.Kind] {
		return false
	}
	if includeNotVisible {
		return true
	}
	// hasDescription=true so hide_no_description never fires here -- only
	// small_functions_visibility/threshold and external_vars_visibility decide.
	return filter.For(res.Kind, locLineSpan(res.Location), true) == domain.VisibilityNormal
}

// ShouldRegenerateDescription is the mirror of ShouldDescribe for
// `descriptions generate --regen_oversized`: same target-kind and read-context
// visibility gates, but it selects resources that ALREADY have a description whose
// stored text overruns its kind's budget (domain.ValidateDescription). The budget is
// enforced on the write path only, so descriptions written before it existed are
// grandfathered in the database and this is the one place that re-checks them.
func ShouldRegenerateDescription(res domain.Resource, targetSet map[domain.ResourceKind]bool, filter domain.ContextFilter, includeNotVisible bool) bool {
	if strings.TrimSpace(res.Description) == "" || !targetSet[res.Kind] {
		return false
	}
	if domain.ValidateDescription(res.Kind, res.Description) == nil {
		return false
	}
	if includeNotVisible {
		return true
	}
	return filter.For(res.Kind, locLineSpan(res.Location), true) == domain.VisibilityNormal
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
