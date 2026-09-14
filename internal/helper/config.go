package helper

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/toolspec"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

type ScanMode string

const (
	ScanModeDefault ScanMode = "default"
	ScanModeHard    ScanMode = "hard"
	ScanModeAll     ScanMode = "all"
)

// PreToolScanMode controls the topology scan the guard runs BEFORE a tool call
// -- the PreToolUse hook in Claude Code, the `tool.execute.before` plugin in
// OpenCode. It is what keeps the graph current for the call that is about to
// happen, whichever surface that call arrives on.
//
// The default is "default": an incremental scan, which diffs the manifest and
// re-parses only what changed, so the common case (nothing changed since the
// previous tool call) is a no-op. The heavier modes exist for the same reason
// `arac scan` has them, not because a hook should normally use them:
// - "none"    = no scan (nothing keeps the graph fresh between tool calls)
// - "default" = incremental scan (only changed files)
// - "full"    = re-scan all files (preserves descriptions)
// - "hard"    = rebuild from scratch (clears descriptions and bugs)
type PreToolScanMode string

const (
	PreToolScanNone    PreToolScanMode = "none"
	PreToolScanDefault PreToolScanMode = "default"
	PreToolScanFull    PreToolScanMode = "full"
	PreToolScanHard    PreToolScanMode = "hard"
)

const DefaultDescriptionBatchSize = 5

// InheritsModel is the sentinel a sub-agent uses to copy the main agent's model.
const InheritsModel = "<inherits>"

// Configuration for the one-shot `arac scan` command and for the scan the guard
// runs before each tool call.

type ScanSection struct {
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
	// PreTool is the scan the guard runs before every tool call it sees --
	// Claude Code's PreToolUse hook and OpenCode's `tool.execute.before`
	// plugin. Absent means "default" (incremental); see PreToolScanMode.
	PreTool PreToolScanMode `json:"pre_tool"`
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

// Configuration for file read operations, including max file size, context filtering, and shell command passthrough behavior.
type ReadSection struct {
	MaxFileSize int64 `json:"max_file_size"`
	// Kinds is the allow-list of resource kinds that may be READ, at every entrance.
	//
	// SCOPE: all of them. The MCP `read` tool, `arac read`, an intercepted shell read
	// (`cat`/`head`/`sed -n`, by path or by resource id), the windowed slice reader and the
	// denial proxy all narrow through this set.
	//
	// It used to gate the MCP tool ALONE -- every other path passed AllReadKinds() explicitly,
	// on the reasoning that the key existed to narrow what a MODEL is offered through a tool
	// schema and should not lock a person out of their own topology. That made it two policies
	// under one name, and left it with no effect at all in three of the four modes: the mode
	// where a project most wants to say "do not read named types here" is an intercepting one,
	// where there is no tool schema to narrow and every read arrives as a `cat`.
	//
	// A refused id is an ERROR, not a fallback. The shell surfaces do not hand the command
	// back to the real binary, because `cat` on a resource id answers "No such file or
	// directory" -- which reads as "you mistyped the id" when the truth is that the project
	// does not serve that kind. An operand that resolves to NOTHING is the other case and
	// still passes through: there is no policy in a typo.
	//
	// It replaced the per-agent read_function/read_struct/read_interface/... tool lists:
	// splitting one capability across eight tool names made models pick the wrong one. An ID
	// that resolves to a kind absent from this list returns an error naming the allowed
	// kinds. Absent (null) means the default set; an explicit [] would make read useless and
	// is rejected by Validate.
	Kinds []domain.ResourceKind `json:"kinds"`
	// ContextFilter is how verbosely the "# CONTEXT:" block renders a read's neighbours:
	// "off", "normal" (the default) or "full". See the ContextFilter* constants.
	//
	// This was six independent sub-keys -- include_incoming, external_vars_visibility,
	// small_functions_visibility, small_function_threshold, hide_no_description and
	// max_inline_parent_lines. They were never independent in practice: a project wants a
	// terser or a fuller context block, not one neighbour kind elevated while another is
	// suppressed. Six knobs made that one decision six ways, and five of the six accessors
	// had no reader outside the function that composed them back together.
	ContextFilter string `json:"context_filter"`
	// PipePassthrough exempts read/grep shell commands that consume piped
	// stdin (e.g. `cmd | tail`) from the tool guard: such commands operate on
	// command output, which the aracne MCP tools cannot serve. Direct file
	// reads (`cat foo.go`) are still gated. Absent means the default (true).
	PipePassthrough *bool `json:"pipe_passthrough"`
	// FileMode decides what a whole-FILE read returns: "full" is the file verbatim, "skeleton"
	// is each top-level declaration's signature with large bodies elided.
	//
	// THE DEFAULT DEPENDS ON THE SURFACE, and EffectiveFileMode is the only place that decides
	// it: "skeleton" everywhere except ModeMCP, which defaults to "full". A benchmark run
	// measured a whole-file read returning 1.00x the bytes on disk plus a context block on top
	// -- strictly more expensive than the `cat` it replaced -- which is why the intercepting
	// surfaces do not default to it. ModeMCP keeps "full" because there a file read is an
	// explicit tool call against a named id, already the deliberate choice skeleton mode
	// exists to force. Reading a SYMBOL is unaffected and already compact.
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

// Configuration for the live/incremental scanner (`arac scanner run`).
type ScannerSection struct {
	// UpdateFrequency is how often, in milliseconds, the watch loop re-checks the manifest.
	UpdateFrequency int `json:"update_frequency"`
}

// Configuration for description generation specifying which resource kinds to describe and how many style exemplars to use as house-style anchors.
type DescriptionsSection struct {
	Kinds []domain.ResourceKind `json:"kinds"`
	// StyleExemplars is how many already-written neighbor descriptions to feed
	// the executor as house-style anchors. 0 disables (saves tokens).
	//
	// Absent means DefaultDescriptionStyleExemplars (LoadConfigRead seeds it before decoding),
	// so it is written even when 0: with omitempty an explicit 0 vanished on save and came
	// back as the default.
	StyleExemplars int `json:"style_exemplars"`
	// IncludeNotVisible, when false (default), skips undocumented targets the
	// read context filter would not render as a normal line (small functions /
	// external vars configured full or hidden).
	IncludeNotVisible bool `json:"include_not_visible,omitempty"`
	// Lazy generates a missing description at the moment a read or a search is about to
	// show it, instead of only in an `arac descriptions generate` sweep. On by default.
	// See config_lazy.go; it accepts `true`/`false` or an object of tuning knobs.
	Lazy LazyDescriptions `json:"lazy"`

	// Provider, BaseURL and CLIProviderCommand say WHO writes a description. They sit on
	// the section rather than under `lazy` because both entry points -- the lazy fill on the
	// read path and the `arac descriptions generate` sweep -- describe the same resources
	// into the same database, and a project that has answered "which provider writes my
	// descriptions" has answered it for both. Two places to say it was two places for them
	// to disagree, and the sweep was the one that could not say "use the CLI" at all.
	//
	// There is deliberately no `model` here. WHICH model writes the descriptions is already
	// said by the descriptions-generation-executor agent
	// (`llm.<harness>.agents.descriptions-generation-executor.model`), and that agent is not
	// optional -- the sweep runs as it. A second spelling of one answer is a second place for
	// the two to disagree, and the one that lost was invisible. Out of the box it is blank,
	// and a blank model means the provider's own cheap tier rather than a vendor chosen by a
	// default nobody typed. See EffectiveLazyDescriptions.
	//
	// The old spelling (`lazy.provider` / `lazy.base_url`) is still read, and still means
	// what it meant, so an existing config keeps working; these win when both are set.

	// Provider names the transport: "anthropic", "openai" or "deepseek" for an API key, or
	// "cli" to run CLIProviderCommand.
	//
	// Absent means UNANSWERED, and nothing guesses on the project's behalf. `arac
	// descriptions generate` asks -- API key or a command, which wire format, which
	// variable holds the key -- and writes the answers back here, so the second run reads
	// what the first one was told. The lazy fill on the read path stays silent and simply
	// does nothing until that has happened: a read is not the place to ask a question.
	Provider string `json:"provider,omitempty"`
	// BaseURL points an API provider at a different endpoint -- a gateway, a proxy, or a
	// self-hosted model speaking one of the three wire formats. Ignored by "cli".
	BaseURL string `json:"base_url,omitempty"`
	// APIKeyEnv is the environment variable the API key is read from, e.g. a shared
	// "LLM_API_KEY" or a per-project one. Absent falls back to the provider's own name
	// (ANTHROPIC_API_KEY / OPENAI_API_KEY / DEEPSEEK_API_KEY), which is what every config
	// written before this key existed relies on.
	//
	// It is a separate question from `provider` because the two stopped being the same
	// answer the moment a gateway spoke one vendor's format while billing another's key:
	// "which format" and "which key" are only accidentally related, and a project pointing
	// `openai` at a proxy needs to say both. Ignored by "cli", which authenticates itself.
	APIKeyEnv string `json:"api_key_env,omitempty"`
	// CLIProviderCommand is the command `provider: "cli"` runs, e.g. "claude -p". It is
	// argv, not a shell line: words split on whitespace, with quoting honoured, and no
	// pipes or redirection. The prompt arrives on stdin, the descriptions are read from
	// stdout. Required by "cli" and ignored by every other provider.
	CLIProviderCommand string `json:"cli_provider_command,omitempty"`
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
	// BugManagement enables the bug pipeline as a whole: `arac setup` writes the
	// bug-hunter/judge/solver agents and their commands, the bug_* MCP tools become
	// servable, the `arac bug` usage block prints, and viz exposes /api/bugs and bug
	// counts. Off by default: the pipeline is unproven and its tool schemas are context
	// cost on every request for a workflow most projects never run.
	//
	// `arac bug` itself stays dispatchable in both states -- it is the orchestration
	// channel the generated commands use, and the debugging path.
	BugManagement bool `json:"bug_management"`

	// Chat enables the viz Chat tab: the /api/chat, /api/chat/ and /api/context-graph
	// routes, and the Chat nav item in the SPA. Off by default because the chat harness --
	// its own agent registry, permission policy and sub-agent runner -- is not part of 1.0.
	//
	// Unlike the bug pipeline this had no gate at all: the routes were registered
	// unconditionally, so the only way not to ship Chat was not to ship viz.
	Chat bool `json:"chat"`

	// Agent enables `arac agent`, the self-contained REPL that talks straight to an LLM
	// provider. Off by default: it is unproven next to the harnesses that do ship, and it
	// is the one surface that needs a provider API key of its own.
	//
	// This gates the COMMAND, not internal/llm/agent -- `arac descriptions generate` runs
	// its executors through the same package and is a shipping 1.0 feature.
	Agent bool `json:"agent"`

	// WarningReads attaches the FULL read of the code a topology warning names to the
	// warning report itself, instead of the one line naming the two ids.
	//
	// WHAT IT BUYS. The report an edit comes back with is a summary -- "[signature_changed]
	// f changed signature, verify caller g (source: ..., target: ...)". Acting on it means
	// reading g, which is another turn, and every turn re-sends the whole transcript. The
	// code is already resolvable from the ids the warning carries, so the expansion is one
	// batched `arac read` of the sites to fix: the model can go straight to the edit.
	//
	// Off by default because it is not free. A warning report is emitted after an edit, on
	// the agent's critical path, and expanding it spends bytes on code the model may
	// already have in context. WarningReadLimit is the ceiling that keeps a wide breakage
	// from dumping a subgraph.
	WarningReads bool `json:"warning_reads"`

	// WarningReadLimit caps how many warnings WarningReads expands. The rest still appear
	// in the summary above the reads, and `arac warnings list` still has all of them.
	//
	// THREE STATES, and 0 is not the default. Absent (nil) means
	// DefaultWarningReadLimit; a value of 0 or lower means NO limit, which is a deliberate
	// choice a project makes and not what an untouched config should silently mean.
	// EffectiveWarningReadLimit() is the only reader.
	WarningReadLimit *int `json:"warning_read_limit,omitempty"`
}

// DefaultWarningReadLimit is how many warnings features.warning_reads expands when
// features.warning_read_limit is absent.
//
// Five, because the expansion competes with the transcript it saves. A signature change with
// eleven callers is the case this feature exists for AND the case that would bury the report
// it is attached to; five sites is enough to fix the common breakage in one turn, and the
// summary above still names every one of them.
const DefaultWarningReadLimit = 5

// The four modes of Config.Mode.
//
// WHY FOUR MODES AND NOT A CROSS-PRODUCT. This was once two independent keys --
// `integration.mode` (terminal/mcp/both) and `identification_mode` (id/line_range) -- plus five
// booleans under `terminal`. Nothing folded them together, so combinations existed that made no
// sense: `mcp` with line-range addressing advertised spans in every grep header while the only
// reader available was an MCP `read` that takes ids and nothing else, and the contract for that
// project never mentioned a line range at all. The surfaces are not independent axes; they are
// four coherent products, and naming them as four is what stops the incoherent fifth from being
// reachable. Those keys are gone -- aracne has never had a release that wrote them.
//
// Each mode answers three questions at once: which tools exist, which shell commands aracne
// answers, and what vocabulary the contract teaches. Nothing else may re-decide any of them.
const (
	// ModeMCP serves capabilities as MCP tools: a single `read`, plus the non-read tools the
	// project enables. Shell READS are left alone; shell grep is still intercepted, and
	// blocked_tools may block or redirect a native/bash read into aracne -- the only mode in
	// which blocked_tools does anything at all.
	ModeMCP = "mcp"
	// ModeCLI ships no MCP tools. The contract points at `arac read <id>` for symbols
	// and asks the model to prefer it over opening files. Shell reads are left alone; shell
	// grep is intercepted. It is the default.
	ModeCLI = "cli"
	// ModeInterceptID intercepts the shell reads the model already types (`cat`, `head`,
	// `tail`, `sed -n`) and answers them from the topology, addressing declarations by
	// resource ID -- which those commands then accept where they accept a path.
	ModeInterceptID = "intercept_id"
	// ModeInterceptLineRanges intercepts the same commands and answers them the same way, but addresses
	// declarations by `path:start-end`: the vocabulary the model already uses for code.
	ModeInterceptLineRanges = "intercept_line_ranges"
)

// DefaultMode is what a config with no mode key resolves to.
const DefaultMode = ModeCLI

// The two contract verbosities.
//
// WHY A DIAL AND NOT TWO DOCUMENTS. There used to be two: the contract in this package, written
// into CLAUDE.md and AGENTS.md, and a second family of per-language system prompts under
// internal/llm/languages that aracne's own harness sent instead. They described the same
// topology, the same read output and the same discipline, in different words -- so every change
// to what a read RETURNS had to be made twice, and the copy nobody was looking at was the one
// that went stale. They are one document now, and this key is the only thing that differs
// between the two audiences that wanted them apart.
const (
	// ContractVerbosityLow is the terse contract: what the graph is, how it reaches this
	// surface, and the one or two facts the model cannot derive from what it is already
	// shown. It is the default because every byte of it is re-sent on every request, and on a
	// harness with tool schemas and a system prompt of its own most of the long version is
	// already said.
	ContractVerbosityLow = "low"
	// ContractVerbosityHigh is the long contract: the same mode-shaped skeleton, plus the
	// language's read output format, its ID vocabulary, its semantics, and the full
	// guidelines. It is what a harness with no prompt of its own needs, and what a project
	// running a weaker model may want on any surface.
	ContractVerbosityHigh = "high"
)

// DefaultContractVerbosity is what a config with no contract_verbosity key resolves to.
//
// Low, because the contract's cost is per-request and the long version's value is not: a
// harness that already tells the model how to read code pays for the long one twice.
const DefaultContractVerbosity = ContractVerbosityLow

// TerminalSection is what is left of the terminal knobs once the mode owns the decisions.
//
// It held five booleans -- intercept, enhance_files, enhance_resources, grep,
// prefer_resource_ids -- and every one of them has become a fact about the mode instead. A
// project does not want "interception on, files off": it wants one of the four products.
// MaxOverserve survives because it is genuinely orthogonal, a numeric ceiling that means the
// same thing whichever mode is answering, and on every surface that answers in something
// else's place. See OverserveBudget.
type TerminalSection struct {
	// MaxOverserve bounds an answer against the plain one it replaces: past this multiple of
	// what that would have cost, the surface falls back to it -- the real command where there
	// is one, and the source or the matches without aracne's additions where there is not.
	//
	// A narrow question answered disproportionately is not a cheaper read; it is a way to
	// spend the context window on one `head -1`. 0 or negative disables the check.
	MaxOverserve *int `json:"max_overserve"`
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
	Terminal     TerminalSection     `json:"terminal"`
	// Mode is the one dial: which tools exist, which shell commands aracne answers, and what
	// vocabulary the contract teaches. See the Mode* constants for what each one is.
	//
	// Absent, it resolves to whatever the legacy integration.mode/identification_mode pair
	// said, and to DefaultMode when neither is set. EffectiveMode() is the only reader.
	Mode string `json:"mode"`
	// ContractVerbosity is how much of the contract to render: "low" (the default) or
	// "high". It is orthogonal to Mode -- the mode decides WHICH capabilities the contract
	// may name, this decides how much is said about them -- and it is the same answer for
	// CLAUDE.md, AGENTS.md and aracne's own harness, which all render one document.
	// EffectiveContractVerbosity() is the only reader.
	ContractVerbosity string `json:"contract_verbosity"`
	// PreloadMCPTools asks Claude Code to put every tool schema in the model's context up
	// front instead of deferring it behind its tool-search, by writing
	// `env.ENABLE_TOOL_SEARCH: "false"` into .claude/settings.json.
	//
	// WHY THIS IS WORTH A KEY. A deferred tool reaches the model as a bare NAME with no
	// schema and no description, and calling it costs a lookup round-trip first. Against a
	// `grep` that is right there, fully described, that asymmetry decides which one gets
	// used -- so in ModeMCP the tools aracne exists to serve are exactly the ones the model
	// is most likely to talk itself out of reaching for. Preloading spends context on every
	// request to remove the friction.
	//
	// It is Claude-Code-only (OpenCode defers nothing) and ModeMCP-only (no other mode
	// serves the main agent an MCP tool at all), which is why `arac init` asks for it only
	// when both hold, and why setup withdraws the key when either stops holding.
	//
	// THREE STATES, and nil is not "false". nil means aracne does not manage the variable,
	// so a project that predates this key -- or one whose operator set ENABLE_TOOL_SEARCH
	// themselves -- is left exactly as it is. Only a value aracne itself wrote is ever
	// removed again.
	PreloadMCPTools *bool `json:"preload_mcp_tools"`

	// Paths marks directories/files (relative to the topology root) as hidden or
	// visible. Hidden paths are skipped by the indexing and scan stages in every
	// mode (default/all/hard). More specific (more internal) rules win, so a
	// parent can be hidden while a nested child stays visible.
	Paths []domain.PathRule `json:"paths"`

	// spelled is what the file said for the three enum keys normalizeConfig coerces, captured
	// before it coerced them. Nil on a config built in code rather than loaded. See Validate.
	spelled *spelledEnums

	// unknownFeatures is the keys the `features` object carried that FeaturesSection has no
	// field for, captured at decode time because nothing downstream can see them: an unknown
	// JSON key is dropped by Unmarshal without a word.
	//
	// WHY THIS ONE SECTION. Turning a feature on is the single edit the documentation asks a
	// user to make to this file by hand, and every feature is a bool that defaults to off --
	// so a misspelled key is indistinguishable from a key that is working and set to false.
	// Measured on a real session: `"warnings_reads": true` (plural) was written, silently
	// dropped, and the feature's absence was reported as a bug in the feature. See Validate.
	unknownFeatures []string
}

// featuresSchemaKeys is the key set FeaturesSection actually declares, read off its own tags so
// it cannot fall behind a field added later -- the same reflection configSchemaKeys uses.
var featuresSchemaKeys = func() map[string]bool {
	out := map[string]bool{}
	t := reflect.TypeOf(FeaturesSection{})
	for i := 0; i < t.NumField(); i++ {
		if name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ","); name != "" && name != "-" {
			out[name] = true
		}
	}
	return out
}()

// unknownFeatureKeys returns the keys under `features` that FeaturesSection does not declare.
func unknownFeatureKeys(raw []byte) []string {
	var doc struct {
		Features map[string]json.RawMessage `json:"features"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil
	}
	var unknown []string
	for key := range doc.Features {
		if !featuresSchemaKeys[key] {
			unknown = append(unknown, key)
		}
	}
	sort.Strings(unknown)
	return unknown
}

// quoteAll renders a key list for an error message.
func quoteAll(xs []string) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = fmt.Sprintf("%q", x)
	}
	return out
}

// spelledEnums pairs each coerced enum with the value it was coerced FROM and TO. The "to" half
// is what lets Validate tell a value still as loaded from one a caller has since set: the
// `arac init` wizard loads a config, overwrites Mode with the answer, then validates -- and a
// typo it has just corrected must not be reported.
type spelledEnums struct {
	mode, modeTo                   string
	verbosity, verbosityTo         string
	contextFilter, contextFilterTo string
}

// Returns the maximum file size for scanning, defaulting to 512KB if not set.
func (c *Config) EffectiveMaxFileSize() int64 {
	if c.Read.MaxFileSize <= 0 {
		return 512 * 1024
	}
	return c.Read.MaxFileSize
}

// normalizePreToolScan coerces a raw scan.pre_tool string to a known
// PreToolScanMode. Absent or unrecognized resolves to PreToolScanDefault: the
// incremental scan is the behavior a project gets without asking, and it is
// cheap enough to be the safe answer to a typo. "none" has to be spelled
// correctly to switch the freshness guarantee off.
func normalizePreToolScan(s string) PreToolScanMode {
	switch PreToolScanMode(strings.ToLower(strings.TrimSpace(s))) {
	case PreToolScanNone:
		return PreToolScanNone
	case PreToolScanFull:
		return PreToolScanFull
	case PreToolScanHard:
		// Recognized so Validate can REJECT it by name. The stamped value has to survive
		// normalization or the error would never fire; EffectivePreToolScan is what keeps a
		// config that bypassed Validate from actually running a rebuild per tool call.
		return PreToolScanHard
	default:
		return PreToolScanDefault
	}
}

// EffectivePreToolScan resolves scan.pre_tool, defaulting to
// PreToolScanDefault. A non-none value asks the guard to scan before the tool
// call it is about to let through.
func (c *Config) EffectivePreToolScan() PreToolScanMode {
	mode := normalizePreToolScan(string(c.Scan.PreTool))
	if mode == PreToolScanHard {
		// Validate refuses this, loudly, so the setting is a typo someone fixes. This is the
		// belt: a config that reached the guard without being validated gets the incremental
		// scan rather than a from-scratch rebuild -- which would drop every description and
		// bug before EVERY tool call.
		return PreToolScanDefault
	}
	return mode
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

// Context-filter presets for read.context_filter.
const (
	// ContextFilterOff renders no neighbours: the code asked for, and nothing around it.
	ContextFilterOff = "off"
	// ContextFilterNormal names each neighbour with its description. The default.
	ContextFilterNormal = "normal"
	// ContextFilterFull renders neighbours as fenced source cuts and adds "# USED BY:".
	ContextFilterFull = "full"
)

// ContextOff reports whether read.context_filter is "off": a read returns the code asked for
// and no "# CONTEXT:" section at all. EffectiveContextFilter cannot say this on its own -- it
// resolves to per-neighbour visibilities, and some context lines take none.
func (c *Config) ContextOff() bool {
	return strings.EqualFold(strings.TrimSpace(c.Read.ContextFilter), ContextFilterOff)
}

// EffectiveIncludeIncoming reports whether a read appends the "# USED BY:" section.
func (c *Config) EffectiveIncludeIncoming() bool {
	return c.EffectiveContextFilter().IncludeIncoming
}

// FileModeSkeleton renders a file as its declarations with large bodies elided;
// FileModeFull returns the file verbatim.
const (
	FileModeFull     = "full"
	FileModeSkeleton = "skeleton"
)

// EffectiveFileMode resolves read.file_mode. An explicit "full" or "skeleton" is honoured;
// anything else -- unset, or a typo -- resolves to "skeleton" everywhere except ModeMCP,
// which resolves to "full".
//
// WHY THE SURFACE CHANGES THE DEFAULT. Skeleton mode is opt-in on the MCP surface because a
// model that has only seen signatures must not build an `edit` old_string from them, and that
// risk was worth measuring first. On the terminal surface the same setting stops being an
// experiment and becomes a correctness matter: `cat f` is a command the model ALREADY runs,
// and answering it with the file verbatim plus a context block is strictly more expensive than
// the `cat` it replaced. Measured on the clap fixture, a whole-file read is 0.21x the bytes of
// `cat` under skeleton and above 1.0x under full -- so on this surface "full" makes aracne
// worse than doing nothing, for the one shape it should win most easily.
//
// The edit risk is answered here rather than dodged: every byte a skeleton shows is verbatim
// file text, and everything omitted carries an elision marker naming the id to read.
func (c *Config) EffectiveFileMode() string {
	switch strings.ToLower(strings.TrimSpace(c.Read.FileMode)) {
	case FileModeSkeleton:
		return FileModeSkeleton
	case FileModeFull:
		return FileModeFull
	}
	// Skeleton everywhere the read arrives as a whole-file request the model did not have to
	// think about -- an intercepted `cat`, or `arac read <file>`. ModeMCP keeps "full": there
	// a file read is an explicit tool call against a named id, which is already the deliberate
	// choice skeleton mode exists to make the model make.
	if c.EffectiveMode() != ModeMCP {
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

// normalizeContextFilter stamps a recognised preset, so a re-saved config states its context
// verbosity instead of leaving it implicit. An unrecognised value normalises to the default.
func normalizeContextFilter(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case ContextFilterOff:
		return ContextFilterOff
	case ContextFilterFull:
		return ContextFilterFull
	default:
		return ContextFilterNormal
	}
}

// EffectiveContextFilter resolves read.context_filter into the domain.ContextFilter the read
// managers apply. An unrecognised value resolves to the default rather than to an empty
// filter, which would silently render no context at all.
func (c *Config) EffectiveContextFilter() domain.ContextFilter {
	switch strings.ToLower(strings.TrimSpace(c.Read.ContextFilter)) {
	case ContextFilterOff:
		return domain.ContextFilter{
			ExtVarsVisibility:    domain.VisibilityHidden,
			SmallFnVisibility:    domain.VisibilityHidden,
			SmallFnThreshold:     domain.DefaultContextFilter().SmallFnThreshold,
			MaxInlineParentLines: domain.DefaultMaxInlineParentLines,
			HideNoDescription:    true,
		}
	case ContextFilterFull:
		f := domain.DefaultContextFilter()
		f.IncludeIncoming = true
		f.ExtVarsVisibility = domain.VisibilityFull
		f.SmallFnVisibility = domain.VisibilityFull
		f.HideNoDescription = false
		return f
	default:
		return domain.DefaultContextFilter()
	}
}

// Returns the config.json path derived from the database directory.
func ConfigPath(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), "config.json")
}

// spelledValues returns the mode, contract verbosity and context filter Validate should judge:
// the file's own spelling for each one still holding the value it was loaded with, and the
// current value for one a caller has set since (or for a config that was never loaded).
func (c *Config) spelledValues() (mode, verbosity, contextFilter string) {
	mode, verbosity, contextFilter = c.Mode, c.ContractVerbosity, c.Read.ContextFilter
	if s := c.spelled; s != nil {
		if c.Mode == s.modeTo {
			mode = s.mode
		}
		if c.ContractVerbosity == s.verbosityTo {
			verbosity = s.verbosity
		}
		if c.Read.ContextFilter == s.contextFilterTo {
			contextFilter = s.contextFilter
		}
	}
	return mode, verbosity, contextFilter
}

// Validate checks every tool name referenced in the config against the tool
// catalog (toolspec). It returns an error naming the offending agent and tool
// so a typo fails fast at init time.
func (c *Config) Validate() error {
	if err := ValidateReadKinds(c.Read.Kinds); err != nil {
		return fmt.Errorf("read.kinds: %w", err)
	}
	// Reported by NAME, with the nearest real key, because the alternative is what actually
	// happens: the flag is dropped, the feature stays off, and the user reads that as the
	// feature being broken.
	if len(c.unknownFeatures) > 0 {
		known := make([]string, 0, len(featuresSchemaKeys))
		for k := range featuresSchemaKeys {
			known = append(known, k)
		}
		sort.Strings(known)
		return fmt.Errorf("features: unknown key(s) %s -- a key this section does not declare is "+
			"dropped silently and its feature stays off (valid: %s)",
			strings.Join(quoteAll(c.unknownFeatures), ", "), strings.Join(known, ", "))
	}
	mode, verbosity, contextFilter := c.spelledValues()
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", ModeMCP, ModeCLI, ModeInterceptID, ModeInterceptLineRanges:
	default:
		return fmt.Errorf("mode: unknown mode %q (want %s, %s, %s or %s)",
			mode, ModeMCP, ModeCLI, ModeInterceptID, ModeInterceptLineRanges)
	}
	// `hard` is a valid `arac scan` mode and a catastrophic PRE-TOOL one: it rebuilds from
	// scratch, which drops every description and every bug -- before EVERY tool call the guard
	// sees. Nothing warned, and the loss is unrecoverable without a descriptions sidecar. The
	// heavier modes exist here for the same reason `arac scan` has them, but only the one that
	// preserves what a project has paid for.
	if PreToolScanMode(strings.ToLower(strings.TrimSpace(string(c.Scan.PreTool)))) == PreToolScanHard {
		return fmt.Errorf("scan.pre_tool: %q rebuilds from scratch and would clear every "+
			"description and bug before every tool call (want %s, %s or %s; use `arac scan --hard` "+
			"for a one-off rebuild)", PreToolScanHard, PreToolScanNone, PreToolScanDefault, PreToolScanFull)
	}
	switch strings.ToLower(strings.TrimSpace(verbosity)) {
	case "", ContractVerbosityLow, ContractVerbosityHigh:
	default:
		return fmt.Errorf("contract_verbosity: unknown value %q (want %s or %s)",
			verbosity, ContractVerbosityLow, ContractVerbosityHigh)
	}
	switch strings.ToLower(strings.TrimSpace(contextFilter)) {
	case "", ContextFilterOff, ContextFilterNormal, ContextFilterFull:
	default:
		return fmt.Errorf("read.context_filter: unknown value %q (want %s, %s or %s)",
			contextFilter, ContextFilterOff, ContextFilterNormal, ContextFilterFull)
	}
	if err := ValidateDescriptionProvider(c.Descriptions); err != nil {
		return err
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

// Returns the default description resource kinds to generate descriptions for.
func DefaultDescribeTargets() []domain.ResourceKind {
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
//
// grep, edit and write are gone too, for a different reason: aracne answers all three without
// a tool -- a search is intercepted wherever the model types it, and a mutation goes through
// the native edit (re-synced by the update-file hook) or `arac edit` / `arac write`. A tool for
// them offered a second way to ask one question and charged a schema block per request for the
// privilege. See toolspec.IsRetiredMCPTool. A config that still lists one is not an error --
// ServableMCPTools drops it -- but a config aracne writes should not.
func DefaultAgentMCPTools(agentName string) []string {
	switch agentName {
	case "descriptions-generation-executor", "descriptions-executor":
		return []string{"read", "update_description"}
	case "bug-hunter":
		return []string{"read", "bug_report"}
	case "bug-judge":
		return []string{"read", "bug_list", "bug_acknowledge", "bug_dismiss", "bug_delete"}
	case "bug-solver":
		return []string{"read", "warnings_list", "bug_delete"}
	default:
		// The bug pipeline is a v2 feature and is not part of the default surface, so
		// `bug_report`/`bug_list` are NOT here: their schemas cost roughly 260 tokens on
		// every single request for a workflow the shipped binary does not run. Projects
		// using the bug agents add them back via llm.<harness>.main_agent.mcp_tools.
		return []string{"read", "warnings_list"}
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

// Returns the default tool set for the chat main agent including bash, MCP lookups, file operations, and topology utilities.
func defaultChatMainAgentTools() []string {
	return []string{"ls", "bash", "glob", "ask_user_question", "CreateTasks", "grep", "read", "edit", "write", "warnings_list", "bug_report", "bug_list", "bug_acknowledge", "bug_dismiss", "bug_delete", "update_description", "node_list_no_description"}
}

// DefaultChatAgentTools returns the default tool list for a proprietary-chat
// sub-agent (used by CreateTasks). These are independent from the llm section.
func DefaultChatAgentTools(agentName string) []string {
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

// DefaultDescriptionStyleExemplars is how many neighbor descriptions are fed to
// the description executor as house-style anchors by default. Set to 0 to
// disable and save tokens.
const DefaultDescriptionStyleExemplars = 1

// DefaultBugSolverThinkingBudget is the default extended-reasoning token budget
// for the proprietary-chat bug-solver sub-agent. Producing a minimal correct
// fix is reasoning-heavy, so it reasons harder than the default. It is a no-op
// for providers/models that do not support reasoning (see chat newProvider).
const DefaultBugSolverThinkingBudget = 4096

// DefaultBugHunterThinkingBudget is the default extended-reasoning token budget
// for the proprietary-chat bug-hunter sub-agent. Spotting real defects across a
// slice of resources is reasoning-heavy, so it reasons harder than the default.
// It is a no-op for providers/models that do not support reasoning (see chat
// newProvider).
const DefaultBugHunterThinkingBudget = 4096

func boolPtr(b bool) *bool { return &b }

// intPtr returns a pointer to i, for config fields where 0 is a meaningful value and absence
// must be distinguishable from it.
func intPtr(i int) *int { return &i }

// Helper function that converts a boolean value to a pointer to bool.

// Returns default Config with scan/read/scanner/LLM/viz defaults and agent configurations.
func DefaultConfig() *Config {
	subAgent := func(name string) AgentConfig {
		ac := AgentConfig{
			Model:        InheritsModel,
			MCPTools:     DefaultAgentMCPTools(name),
			BlockedTools: defaultBlockedTools(),
		}
		if name == "descriptions-generation-executor" {
			ac.Params = map[string]int{"max-batch-size": DefaultDescriptionBatchSize}
		}
		// The hunter and the judge only ever read and record a verdict; the descriptions
		// executor only ever reads and calls update_description. Only the solver changes code.
		// Leaving the other three the native Edit/Write contradicted their own prompts
		// ("Touch only assigned resources. Never write scripts to bulk-generate.", "use only
		// its restricted tools") and handed three read-only agents the ability to rewrite the
		// codebase.
		//
		// Deliberately NOT "read": blocking read renames the aracne read tool
		// (toolspec.ResolveReadToolName), which is a separate decision from privilege.
		if name != "bug-solver" {
			ac.BlockedTools = []string{"edit", "write"}
		}
		return ac
	}
	return &Config{
		Scan:  ScanSection{Ignore: []string{}, Workers: 0, Progress: ProgressAuto, PreTool: PreToolScanDefault},
		Paths: []domain.PathRule{},
		// Stamped explicitly rather than left to the default: a written config should say
		// which of the four products it is, not make the reader know what absence means.
		Mode: DefaultMode,
		// Stamped for the same reason as Mode: the dial that decides how big every
		// request's contract is should be visible in the file, not inferred from a
		// missing key.
		ContractVerbosity: DefaultContractVerbosity,
		Terminal: TerminalSection{
			MaxOverserve: intPtr(DefaultTerminalMaxOverserve),
		},
		Read: ReadSection{
			MaxFileSize:     512 * 1024,
			Kinds:           DefaultReadKinds(),
			PipePassthrough: boolPtr(true),
			ContextFilter:   ContextFilterNormal,
		},
		Scanner:      ScannerSection{UpdateFrequency: 200},
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
			// No model pinned for the descriptions executor, deliberately.
			//
			// It used to be "haiku", which read as a harmless cheap-tier default and was
			// not one: the description provider is INFERRED from this model when nothing
			// names one, so a config that had answered nothing still resolved to
			// Anthropic. A project with no ANTHROPIC_API_KEY got silence from the lazy
			// fill and "no LLM provider configured" from the sweep, with no way to tell
			// that a default it never wrote had chosen a vendor for it. Blank here means
			// blank everywhere, which is what lets `arac descriptions generate` notice it
			// has never been told anything and ask. A project that wants a specific model
			// still writes it here; without one, the provider's own cheap tier is used
			// (lazydesc.providerFallbackModel).
			ClaudeCode: LLMHarness{Agents: map[string]AgentConfig{}},
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

// Returns the LLM harness configuration block for the specified harness name.
func harnessBlock(c *Config, harness string) LLMHarness {
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
	hb := harnessBlock(c, harness)
	main := mergeAgent(c.LLM.Any.MainAgent, hb.MainAgent)
	if IsMainAgentName(agentName) {
		out := resolveInherits(main, main)
		out.MCPTools = c.ServableMCPTools(out.MCPTools)
		return out
	}
	var anyAg, hAg AgentConfig
	if c.LLM.Any.Agents != nil {
		anyAg = c.LLM.Any.Agents[agentName]
	}
	if hb.Agents != nil {
		hAg = hb.Agents[agentName]
	}
	merged := mergeAgent(anyAg, hAg)
	out := resolveInherits(merged, main)
	// A SUB-AGENT is not on the main agent's surface, so the mode does not narrow it.
	//
	// The mode answers "which capabilities does the MAIN agent get, and how do they
	// reach it". A generated sub-agent gets its own scoped server declared inline in its
	// frontmatter precisely because it is outside that answer: it exists to call
	// update_description or bug_*, which have no shell equivalent worth teaching, and its
	// schema cost is paid inside a short-lived sub-agent instead of on every request.
	//
	// Running the mode filter here returned nil in three of the four modes, which left the
	// descriptions executor with no update_description, an `arac serve` that refused to
	// start, and a sweep that failed after max-retries -- while docs/architecture.md
	// promised the pipelines keep working on the terminal surface. They now do.
	out.MCPTools = c.SubAgentMCPTools(out.MCPTools)
	return out
}

// IsMainAgentName reports whether a tool-profile name resolves to the main agent. "" is the
// unnamed default; "main" and "default" are the two spellings --tool-profile accepts.
func IsMainAgentName(agentName string) bool {
	return agentName == "" || agentName == "main" || agentName == "default"
}

// ServableMCPTools narrows a configured mcp_tools list to what this mode's server actually
// registers, preserving order.
//
// EVERY consumer goes through it -- the server registry, the generated agent markdown, the
// Claude permission rules, the OpenCode permission block. That is the whole point: a name in
// one of those that the server does not register denies the agent a tool SILENTLY, which is the
// drift TestGeneratorMatchesServer exists to catch, and the cheapest way to never have it is to
// give the two one answer rather than two that agree today.
//
// Outside ModeMCP the answer is "none", which is what "no MCP tools" has to mean in the three
// modes that do not have them.
func (c *Config) ServableMCPTools(names []string) []string {
	if !c.MCPEnabled() || len(names) == 0 {
		return nil
	}
	out := make([]string, 0, len(names))
	for _, n := range names {
		if toolspec.IsRetiredMCPTool(n) {
			continue
		}
		out = append(out, n)
	}
	return out
}

// SubAgentMCPTools narrows a SUB-agent's mcp_tools to what its own scoped server registers.
//
// The same shell-served filter as ServableMCPTools, and deliberately without the mode gate:
// a sub-agent's server is declared in its own frontmatter and started per run, so it exists
// in every mode. See EffectiveAgent for what running the mode gate here used to cost.
func (c *Config) SubAgentMCPTools(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	out := make([]string, 0, len(names))
	for _, n := range names {
		if toolspec.IsRetiredMCPTool(n) {
			continue
		}
		out = append(out, n)
	}
	return out
}

// Merges an override agent config into a base config, combining model, tools, plugins, and parameters.
func mergeAgent(base, over AgentConfig) AgentConfig {
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

// Resolves agent configuration by inheriting unset fields (Model, MCPTools, BlockedTools) from a main agent.
func resolveInherits(ag, main AgentConfig) AgentConfig {
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

// configSchemaKeys is every top-level key the current schema defines. It is derived from the
// struct tags rather than typed out, so it cannot fall behind a field added to Config.
var configSchemaKeys = func() map[string]bool {
	out := map[string]bool{}
	t := reflect.TypeOf(Config{})
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("json")
		if name, _, _ := strings.Cut(tag, ","); name != "" && name != "-" {
			out[name] = true
		}
	}
	return out
}()

// validConfig reports whether a decoded config looks like the new schema.
//
// The test is PRESENCE OF A KNOWN KEY, not the value of a hand-picked sentinel. It used to be
// a list of "does this field look set" probes, extended once per hand-editable key -- and it
// fell behind six of them. A file saying only `{"scan": {"ignore": ["generated/**"]}}` or
// `{"contract_verbosity": "high"}` decoded to all-zero sentinels, failed every probe, was
// judged legacy, and EnsureConfig replaced it with defaults: the one setting the file existed
// to express was the one thing lost.
//
// Reading the raw keys cannot fall behind, because the key set comes from Config's own tags.
// A genuinely old-format file shares none of them and is still cleanly rejected; a file whose
// keys are all unknown is rejected for the same reason.
func validConfig(raw []byte) bool {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		return false
	}
	for key := range keys {
		if configSchemaKeys[key] {
			return true
		}
	}
	return false
}

// Terminal-surface defaults. Interception ships ON: it is the product, and every branch it
// takes is either an aracne answer or the command the caller typed.
const (
	// DefaultTerminalMaxOverserve was calibrated against real runs rather than picked: it is
	// the factor the guard's proxied read has always applied.
	DefaultTerminalMaxOverserve = 4
)

// EffectiveMode resolves the one dial, in three tiers: the explicit key, then the legacy
// integration.mode/identification_mode pair, then DefaultMode.
//
// An unrecognized value falls through to the same resolution rather than failing. A config
// aracne cannot read must never leave a project with no surface at all; Validate() is where a
// typo is reported, loudly and once.
func (c *Config) EffectiveMode() string {
	switch strings.ToLower(strings.TrimSpace(c.Mode)) {
	case ModeMCP:
		return ModeMCP
	case ModeCLI:
		return ModeCLI
	case ModeInterceptID:
		return ModeInterceptID
	case ModeInterceptLineRanges:
		return ModeInterceptLineRanges
	}
	return DefaultMode
}

// EffectiveContractVerbosity resolves contract_verbosity, defaulting to
// DefaultContractVerbosity.
//
// An unrecognized value falls through to the default rather than failing, for the same reason
// EffectiveMode does: a config aracne cannot read must never leave a project with no contract
// at all. Validate() is where a typo is reported.
func (c *Config) EffectiveContractVerbosity() string {
	if strings.ToLower(strings.TrimSpace(c.ContractVerbosity)) == ContractVerbosityHigh {
		return ContractVerbosityHigh
	}
	return ContractVerbosityLow
}

// MCPEnabled reports whether the MCP server is wired and its tools served.
func (c *Config) MCPEnabled() bool { return c.EffectiveMode() == ModeMCP }

// PreloadMCPToolsEnabled reports whether setup should write the tool-search opt-out into
// .claude/settings.json. See Config.PreloadMCPTools.
//
// The mode is folded in here rather than left to each caller: outside ModeMCP the main agent is
// served no MCP tool at all, so the variable would buy nothing and still pay for every other
// deferred tool's schema on every request. A config that says true keeps saying it while the
// project is elsewhere -- switching back to mcp restores the setting without asking again.
func (c *Config) PreloadMCPToolsEnabled() bool {
	return c.PreloadMCPTools != nil && *c.PreloadMCPTools && c.MCPEnabled()
}

// InterceptReads reports whether a shell read (`cat`, `head`, `tail`, `sed -n`) is answered
// from the topology instead of by the real command.
//
// Only the two intercepting modes do this. In ModeMCP the read capability is a tool, and in
// ModeCLI it is `arac read` -- in both, rewriting the model's `cat` as well would be a
// second answer to a question that already has one.
func (c *Config) InterceptReads() bool {
	m := c.EffectiveMode()
	return m == ModeInterceptID || m == ModeInterceptLineRanges
}

// InterceptGrep reports whether a shell search is answered by the topology-annotated grep.
//
// True in every mode, which is why it takes no config. Search is the one capability with no
// competing surface: the annotated grep finds node names and stored descriptions, which no
// plain grep reaches and no read tool answers, so there is never a reason to hand a search
// back to the real binary.
func (c *Config) InterceptGrep() bool { return true }

// InterceptShell reports whether the guard may rewrite a shell command at all.
func (c *Config) InterceptShell() bool { return c.InterceptReads() || c.InterceptGrep() }

// AdvertiseResourceIDs reports whether the contract teaches resource IDs as the way to address
// a declaration. Only ModeInterceptID does; ModeInterceptLineRanges deliberately stops advertising them
// (it still accepts them), and the two toolful modes name a tool instead.
func (c *Config) AdvertiseResourceIDs() bool { return c.EffectiveMode() == ModeInterceptID }

// LineRangeIdentification reports whether resources are NAMED by path and line span. Every
// consumer reads the mode through this, so the spellings cannot drift apart.
//
// It is deliberately not true in ModeMCP. A span is only worth printing where reading it is a
// move the model can make: the MCP `read` tool takes ids and has no line-range argument, and
// with shell reads unintercepted there is nothing to answer a `sed` with either. Printing a
// span there replaced the id in every grep header with a coordinate no available tool accepts.
func (c *Config) LineRangeIdentification() bool { return c.EffectiveMode() == ModeInterceptLineRanges }

// GuardBlocksNativeReads reports whether blocked_tools may deny a native or bash read and
// redirect it into aracne.
//
// The test is whether a denial has somewhere to SEND the model. ModeMCP does: an MCP `read`
// tool that is present in its tool list. ModeCLI does too: `arac read` is a real
// command there, and reads are not intercepted, so a refusal points at a capability the model
// has rather than refusing something aracne was about to answer anyway.
//
// The two intercepting modes do not, and must not: there the capability arrives AS the command
// the model already typed, so a block would refuse a call aracne was about to serve -- which is
// how the guard used to spend two turns on a question asked correctly the first time.
//
// What a mode may block is narrower than the set an operator writes; see BlockableInMode.
func (c *Config) GuardBlocksNativeReads() bool {
	m := c.EffectiveMode()
	return m == ModeMCP || m == ModeCLI
}

// BlockableInMode filters an operator's blocked_tools down to the ones this mode may actually
// refuse.
//
// Search is the entry that has to be dropped. InterceptGrep is true in EVERY mode, so a
// blocked `grep` in ModeCLI would deny a command the guard was one step away from
// answering itself -- the exact two-turns-for-one-question failure interception exists to end.
// The operator's config is not rejected for it: `blocked_tools: [read, grep]` is a reasonable
// thing to write when moving a project between modes, and silently keeping the half that
// applies is better than failing the whole config over the half that does not.
func (c *Config) BlockableInMode(blocked map[string]bool) map[string]bool {
	if len(blocked) == 0 || !c.GuardBlocksNativeReads() {
		return map[string]bool{}
	}
	if c.EffectiveMode() == ModeMCP {
		return blocked
	}
	out := make(map[string]bool, len(blocked))
	for name, on := range blocked {
		if !on || c.interceptsToolName(name) {
			continue
		}
		out[name] = true
	}
	return out
}

// interceptsToolName reports whether aracne answers this capability by rewriting the command,
// which is what makes blocking it counterproductive.
func (c *Config) interceptsToolName(name string) bool {
	switch name {
	case toolspec.GrepToolName:
		return c.InterceptGrep()
	case toolspec.ReadToolName:
		return c.InterceptReads()
	}
	return false
}

// Surface maps the mode onto the guidance table the guard's warnings come from.
func (c *Config) Surface() toolspec.Surface {
	switch c.EffectiveMode() {
	case ModeMCP:
		return toolspec.SurfaceMCP
	case ModeCLI:
		return toolspec.SurfaceAracneRead
	case ModeInterceptID:
		return toolspec.SurfaceInterceptID
	default:
		return toolspec.SurfaceLineRange
	}
}

// EffectiveTerminalMaxOverserve returns the over-serve factor; 0 means "no ceiling".
func (c *Config) EffectiveTerminalMaxOverserve() int {
	if c.Terminal.MaxOverserve == nil {
		return DefaultTerminalMaxOverserve
	}
	if *c.Terminal.MaxOverserve < 0 {
		return 0
	}
	return *c.Terminal.MaxOverserve
}

// boolOr reads an optional bool with a default, so an absent key and an explicit `false` stay
// distinguishable in the JSON.
func boolOr(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

// BugManagementEnabled reports whether the bug pipeline is turned on for this project.
// Every gate reads it through this method rather than the field, so the flag can grow
// siblings without a scattered rename.
func (c *Config) BugManagementEnabled() bool { return c.Features.BugManagement }

// ChatEnabled reports whether the viz Chat tab is turned on for this project.
func (c *Config) ChatEnabled() bool { return c.Features.Chat }

// AgentEnabled reports whether `arac agent` is turned on for this project.
func (c *Config) AgentEnabled() bool { return c.Features.Agent }

// WarningReadsEnabled reports whether a topology warning is reported with the full read of
// the code it names attached.
func (c *Config) WarningReadsEnabled() bool { return c.Features.WarningReads }

// EffectiveWarningReadLimit resolves features.warning_read_limit. It returns 0 for "no
// limit", so the one comparison a caller needs is `limit > 0 && len(ws) > limit`.
func (c *Config) EffectiveWarningReadLimit() int {
	if c.Features.WarningReadLimit == nil {
		return DefaultWarningReadLimit
	}
	if n := *c.Features.WarningReadLimit; n > 0 {
		return n
	}
	return 0
}

// Applies defaults to config fields for scan modes, file limits, visibility filters, descriptions, optimization rules, and LLM agents.
func normalizeConfig(c *Config) {
	// The spellings are kept BEFORE anything is coerced. Every Validate() caller loads through
	// here, so validating the stamped values meant a typo had already become a valid value by
	// the time it was checked: `"mode": "intercept_lineranges"` ran as cli with nothing
	// reported, while EffectiveMode's contract is that Validate reports it.
	spelled := &spelledEnums{
		mode: c.Mode, verbosity: c.ContractVerbosity, contextFilter: c.Read.ContextFilter,
	}
	// Resolve the mode once and stamp it, so a re-saved config states its surface instead of
	// leaving it implicit.
	c.Mode = c.EffectiveMode()
	// Stamped for the same reason as Mode, and previously only claimed to be: the dial that
	// decides how big every request's contract is should be visible in the file rather than
	// inferred from a missing key.
	c.ContractVerbosity = c.EffectiveContractVerbosity()
	spelled.modeTo, spelled.verbosityTo = c.Mode, c.ContractVerbosity
	defer func() {
		spelled.contextFilterTo = c.Read.ContextFilter
		if c.spelled == nil {
			c.spelled = spelled
		}
	}()
	if c.Scanner.UpdateFrequency <= 0 {
		c.Scanner.UpdateFrequency = 200
	}
	if c.Read.MaxFileSize <= 0 {
		c.Read.MaxFileSize = 512 * 1024
	}
	c.Scan.PreTool = normalizePreToolScan(string(c.Scan.PreTool))
	c.Read.ContextFilter = normalizeContextFilter(c.Read.ContextFilter)
	if c.Read.Kinds == nil {
		c.Read.Kinds = DefaultReadKinds()
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

// LoadConfigStrict loads the config and reports whether it parsed cleanly as the new
// schema. A missing file, a JSON error, or an old-format file all return
// (DefaultConfig(), false).
//
// It is LoadConfigRead without the read error, kept because almost every caller only
// wants "did I get a real config". The one caller that must distinguish an unreadable
// file from an unparseable one is EnsureConfig, which overwrites on false -- see
// LoadConfigRead for what conflating the two cost.
func LoadConfigStrict(path string) (*Config, bool) {
	cfg, ok, _ := LoadConfigRead(path)
	return cfg, ok
}

// LoadConfigRead is LoadConfigStrict with the third answer a writer needs.
//
// WHY THREE RETURNS AND NOT TWO. "I could not read this file" and "this file is not the
// current schema" are opposite facts with opposite remedies, and they used to be the same
// `false`. EnsureConfig treats false as licence to overwrite -- which is right for an
// old-format file and catastrophic for one that is merely unreadable right now: a
// permission change, a Windows lock, an NFS hiccup, a config written mode 0600 by another
// user. The project's mode, feature flags, ignore rules, agent tool lists and provider
// settings were then replaced by defaults for a reason that had nothing to do with their
// contents, under a message that said the schema was wrong when it had never been read.
//
// readErr is non-nil ONLY when the bytes could not be obtained. A file that was read and
// then failed validConfig or json.Unmarshal returns (defaults, false, nil), which is the
// clean-break case and the only one an overwrite is allowed for.
func LoadConfigRead(path string) (cfg *Config, ok bool, readErr error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return DefaultConfig(), false, err
	}
	// A UTF-8 byte order mark is how Notepad and several Windows editors save UTF-8. It is an
	// encoding artifact, not content (RFC 8259 lets a parser ignore it), but encoding/json
	// rejects it -- so a valid config saved that way was judged unreadable and EnsureConfig
	// replaced the project's mode and ignore rules with defaults.
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if !validConfig(data) {
		return DefaultConfig(), false, nil
	}
	// Seeded, not zero: Unmarshal keeps what the file does not mention, which is how an absent
	// style_exemplars means the documented 1 rather than a silent 0.
	loaded := Config{Descriptions: DescriptionsSection{StyleExemplars: DefaultDescriptionStyleExemplars}}
	if err := json.Unmarshal(data, &loaded); err != nil {
		return DefaultConfig(), false, nil
	}
	loaded.unknownFeatures = unknownFeatureKeys(data)
	normalizeConfig(&loaded)
	return &loaded, true, nil
}

// Loads config from a path, returning the config without error handling.
func LoadConfig(path string) *Config {
	cfg, _ := LoadConfigStrict(path)
	return cfg
}

// SaveConfig writes a Config to JSON with pretty-printing, preserving special tokens like
// "<any>" and "<inherits>" as readable literals.
//
// ATOMIC, because of what EnsureConfig does to a config it cannot read. os.WriteFile truncates
// first and writes second; interrupted in between -- a Ctrl-C during `arac init`, a hook whose
// timeout takes the process with it, a full disk -- it leaves a zero-byte or half-written
// config.json. On the next run validConfig finds no known key, LoadConfigStrict reports false,
// and EnsureConfig REPLACES the file with defaults. The project's mode, feature flags, ignore
// rules, agent tool lists and provider settings are then gone, for a reason that had nothing to
// do with their contents.
//
// That combination is what makes this the one file where a torn write is unrecoverable rather
// than merely inconvenient -- the same argument atomicwrite.go makes for the manifest, which
// was hardened after this repository's own was found empty.
func SaveConfig(cfg *Config, path string) error {
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
	return AtomicWriteFile(path, buf.Bytes(), 0644)
}

// EnsureConfig returns the config at path, creating it with defaults when
// missing. If an existing file does not parse as the new schema (e.g. an
// old-format config), it is overwritten with fresh defaults (clean break).
func EnsureConfig(path string) *Config {
	if _, err := os.Stat(path); err != nil {
		cfg := DefaultConfig()
		// Only ABSENCE gets a fresh config written for it. Any other stat error -- a
		// permission denial, a broken symlink, an unmounted share -- means the file may
		// well be there and readable later, and writing defaults over it would destroy a
		// config for a reason that had nothing to do with its contents.
		if !errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "aracne: %s could not be read (%v); using defaults for this run "+
				"without changing the file.\n", path, err)
			return cfg
		}
		if saveErr := SaveConfig(cfg, path); saveErr != nil {
			return cfg
		}
		return cfg
	}
	cfg, ok, readErr := LoadConfigRead(path)
	if ok {
		return cfg
	}
	// The file is there and could not be READ. That says nothing about its contents, so it
	// keeps them: run on defaults for this process and leave the file exactly as it is. The
	// stat above already guards the same case for a path that will not stat; this is the
	// same rule for one that stats and then refuses to open.
	if readErr != nil {
		fmt.Fprintf(os.Stderr, "aracne: %s could not be read (%v); using defaults for this run "+
			"without changing the file.\n", path, readErr)
		return cfg
	}
	// Read, and not this schema. Say so. A clean break is the right call for a file that
	// cannot be read as this schema, but doing it silently means a project loses hand-set
	// keys -- a feature it switched on, an agent's tool list -- and finds out later, from
	// the behaviour. On stderr because this runs inside hooks whose stdout is parsed as JSON.
	fmt.Fprintf(os.Stderr,
		"aracne: %s could not be read as the current config schema and was replaced with "+
			"defaults. Any keys it set are gone; re-apply them if you need them.\n", path)
	_ = SaveConfig(cfg, path)
	return cfg
}

// ---------------------------------------------------------------------------
// Describe-target helpers (unchanged)
// ---------------------------------------------------------------------------

// Parses comma-separated describe targets into a deduplicated slice of ResourceKinds.
func ParseDescribeTargets(value string) ([]domain.ResourceKind, error) {
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

// Parses and dedupes describe targets into a normalized ResourceKind slice.
func NormalizeDescribeTargets(targets []domain.ResourceKind) ([]domain.ResourceKind, error) {
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

// Parses a single describe target string into a ResourceKind, normalizing whitespace and hyphens.
func ParseDescribeTarget(value string) (domain.ResourceKind, error) {
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

// Converts a list of target resource kinds into a lookup map, defaulting to DefaultDescribeTargets if nil.
func DescribeTargetSet(targets []domain.ResourceKind) map[domain.ResourceKind]bool {
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

// Formats a slice of describe targets into a comma-separated string.
func FormatDescribeTargets(targets []domain.ResourceKind) string {
	if targets == nil {
		targets = DefaultDescribeTargets()
	}
	values := make([]string, 0, len(targets))
	for _, target := range targets {
		values = append(values, string(target))
	}
	return strings.Join(values, ", ")
}

// Removes duplicate ResourceKind targets, returning only the first occurrence of each unique kind.
func dedupeDescribeTargets(targets []domain.ResourceKind) []domain.ResourceKind {
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
