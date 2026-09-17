package helper

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Lazy-description defaults.
//
// The node cap mirrors renderstate.DefaultMaxEntries, which is the ceiling on how many
// entries one CONTEXT or USED BY section can print: describing more nodes than a section can
// show buys nothing for the read that paid for it. (The constant is restated rather than
// imported because helper sits underneath renderstate; TestLazyMaxNodesTracksRenderBudget
// keeps the two in step.)
const (
	// DefaultLazyMaxNodes caps how many nodes one lazy fill may describe.
	DefaultLazyMaxNodes = 40
	// DefaultLazyTimeoutSeconds is how long a READ WAITS, and nothing more.
	//
	// It used to bound the whole fill, because the fill ran inline: when it expired, the
	// provider call was killed and every token spent on the batch in flight was thrown away.
	// That made the deadline a floor rather than a preference -- anything shorter than the
	// slowest provider landed nothing, and the read paid the full wait to render exactly what
	// it would have rendered with the feature off.
	//
	// That is no longer true. Generation happens in a detached worker that outlives the read
	// (see lazydesc.Filler.fill and `arac descriptions worker`), so this deadline ends the
	// WAIT, never the work. Whatever has not landed by then is rendered as undescribed and
	// arrives in the database a moment later, for the next read that names it. Nothing is
	// discarded and nothing is paid for twice.
	//
	// 45 stays as the default because a first read on a cold repository is the one most likely
	// to be able to show what it started, and waiting costs nothing but latency now. Lower it
	// freely; the only thing a short value gives up is seeing a description in THIS answer.
	//
	// THREE READINGS, and the sign is what picks between them:
	//
	//   > 0  wait this long, capped at MaxLazyTimeoutSeconds so a stray 36000 cannot hang every
	//        intercepted read in a project;
	//     0  do not wait at all -- claim, start the work, render now, collect it next time;
	//   < 0  wait until the work is finished.
	//
	// Zero is the safe reading of "no limit" because zero is what a typo produces, and what a
	// half-written config leaves behind. Waiting forever has to be ASKED for, in a way nobody
	// types by accident, which is what the negative is.
	//
	// And "forever" is shorter than it sounds. The wait ends when every resource it is watching
	// has landed, settled or lost its worker, and a worker cannot outlive worker_timeout_seconds
	// plus its watchdog -- with the lease in the claim row expiring after that whatever the
	// process does. So a negative value waits for the work, not for eternity; the ceiling is
	// worker_timeout_seconds, which is a number the same config sets.
	DefaultLazyTimeoutSeconds = 45
	// DefaultLazyBatchSize is how many resources one completion describes.
	DefaultLazyBatchSize = DefaultDescriptionBatchSize
	// DefaultLazyParallel is how many batches run at once.
	DefaultLazyParallel = 4
	// MaxLazyTimeoutSeconds caps how long a read may wait, however the project spells it.
	MaxLazyTimeoutSeconds = 120

	// DefaultLazyBackground puts generation in a detached worker process. Off restores the
	// inline fill, which is the right answer only where spawning is impossible or unwanted --
	// a locked-down CI image, a sandbox, the benchmark harness, which deliberately refuses to
	// leave a process behind between runs.
	DefaultLazyBackground = true
	// DefaultLazyWorkerTimeoutSeconds is the ceiling on ONE worker process.
	//
	// It is generous because a worker is not on anybody's critical path: ten minutes of
	// background generation costs latency to nobody, where the same ten minutes inline would
	// have been ten minutes of a hanging `cat`. It is not unbounded because a detached process
	// nothing is waiting for is exactly the thing that must never be able to run forever.
	DefaultLazyWorkerTimeoutSeconds = 600
	// DefaultLazyMaxWorkers is how many description workers one project may have at once.
	//
	// The sweep's parallelism, by reference rather than by value: a worker runs the sweep's
	// machinery under the sweep's limits, so "how much description work may be in flight" has
	// one answer, and raising it raises both.
	DefaultLazyMaxWorkers = DefaultDescriptionParallel
)

// LazyDescriptions is `descriptions.lazy`: generate a missing description at the moment
// something is about to show it, rather than only in an `arac descriptions generate` sweep.
//
// It accepts BOTH JSON shapes for one key. `"lazy": true` is the setting as most projects
// will ever write it -- a switch -- and `"lazy": {"enabled": true, "max_nodes": 8}` is the
// same switch with its tuning exposed. Two keys (a bool plus a sibling object) would have made
// "on" expressible twice and let them disagree; one key with two shapes cannot.
type LazyDescriptions struct {
	// Enabled is the switch. Absent means the default, which is ON: a project that has never
	// heard of this key gets descriptions as it reads, which is the point of the feature.
	Enabled *bool `json:"enabled,omitempty"`
	// MaxNodes caps the nodes one fill describes. Absent uses DefaultLazyMaxNodes; <= 0 is
	// read as "no cap", for a project that would rather pay once than converge over reads.
	MaxNodes *int `json:"max_nodes,omitempty"`
	// TimeoutSeconds is how long a read waits for the descriptions it started. Positive waits
	// that long, zero does not wait at all, and negative waits until the work is done. See
	// DefaultLazyTimeoutSeconds.
	TimeoutSeconds *int `json:"timeout_seconds,omitempty"`
	// BatchSize is how many resources go into one completion.
	BatchSize *int `json:"batch_size,omitempty"`
	// Parallel is how many batches are in flight at once.
	Parallel *int `json:"parallel,omitempty"`
	// Background generates in a detached worker process instead of inside the read. Absent
	// means the default, which is ON. See DefaultLazyBackground.
	Background *bool `json:"background,omitempty"`
	// WorkerTimeoutSeconds bounds one worker process end to end.
	//
	// Unlike TimeoutSeconds, `<= 0` here is NOT "no limit": it falls back to the default. An
	// unbounded detached process is the one thing this design must never be able to create, and
	// a config typo is not a good enough reason to create one.
	WorkerTimeoutSeconds *int `json:"worker_timeout_seconds,omitempty"`
	// MaxWorkers caps concurrent worker processes for this project.
	MaxWorkers *int `json:"max_workers,omitempty"`
	// MaxRetries is how many times a worker re-attempts one resource before recording it as
	// failed. Straight from the sweep; see DefaultDescriptionMaxRetries.
	MaxRetries *int `json:"max_retries,omitempty"`
	// MaxAgentTurns caps the agent transport's iterations. Absent, or <= 0, keeps the sweep's
	// own len(batch)*4+10 formula, which scales with how much there is to describe.
	MaxAgentTurns *int `json:"max_agent_turns,omitempty"`
	// Provider and BaseURL are the OLD spelling of settings that now live on the
	// descriptions section itself, because the sweep needs them too (see
	// DescriptionsSection). They are still read, and still mean exactly what they meant, so
	// a config written before the move keeps describing with what it named -- the section
	// keys simply win where both are set. New configs should use the section keys; nothing
	// writes these.
	//
	// There was a `model` here too, and it is gone from both spellings: the model is the
	// descriptions-generation-executor agent's, and only its.

	// Provider names the API to call ("anthropic", "openai", "deepseek"), or "cli" for a
	// command. Absent is inferred from the executor's model, so naming a model there is
	// normally enough.
	Provider string `json:"provider,omitempty"`
	// BaseURL points the provider at a different endpoint -- a gateway, a proxy, or a
	// self-hosted model speaking one of the three wire formats. Absent uses the provider's
	// own. The same knob viz.chat's ProviderSettings already exposes, for the same reason.
	BaseURL string `json:"base_url,omitempty"`
}

// lazyDescriptionsFields is the object form, without the custom unmarshaller, so decoding an
// object cannot recurse into LazyDescriptions.UnmarshalJSON.
type lazyDescriptionsFields LazyDescriptions

// UnmarshalJSON accepts `true`, `false`, `null` or the object form.
func (l *LazyDescriptions) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		*l = LazyDescriptions{}
		return nil
	}
	if trimmed[0] == 't' || trimmed[0] == 'f' {
		var b bool
		if err := json.Unmarshal(trimmed, &b); err != nil {
			return err
		}
		*l = LazyDescriptions{Enabled: boolPtr(b)}
		return nil
	}
	var fields lazyDescriptionsFields
	if err := json.Unmarshal(trimmed, &fields); err != nil {
		return fmt.Errorf("descriptions.lazy: want a boolean or an object: %w", err)
	}
	*l = LazyDescriptions(fields)
	return nil
}

// MarshalJSON writes back the shape the setting was expressed in: a bare boolean when nothing
// but the switch is set, the object otherwise. A config that said `"lazy": true` and is saved
// again still says `"lazy": true`.
func (l LazyDescriptions) MarshalJSON() ([]byte, error) {
	if l.tuned() {
		return json.Marshal(lazyDescriptionsFields(l))
	}
	return json.Marshal(l.Resolve().Enabled)
}

// tuned reports whether any knob beyond the switch carries a value.
func (l LazyDescriptions) tuned() bool {
	return l.MaxNodes != nil || l.TimeoutSeconds != nil || l.BatchSize != nil ||
		l.Parallel != nil || l.Background != nil || l.WorkerTimeoutSeconds != nil ||
		l.MaxWorkers != nil || l.MaxRetries != nil || l.MaxAgentTurns != nil ||
		strings.TrimSpace(l.Provider) != "" || strings.TrimSpace(l.BaseURL) != ""
}

// ResolvedLazyDescriptions is LazyDescriptions with every default applied, so the fill path
// reads plain values and never re-decides a default of its own.
type ResolvedLazyDescriptions struct {
	Enabled        bool
	MaxNodes       int
	TimeoutSeconds int
	BatchSize      int
	Parallel       int
	Background     bool
	// WorkerTimeoutSeconds is always positive: see LazyDescriptions.WorkerTimeoutSeconds.
	WorkerTimeoutSeconds int
	MaxWorkers           int
	MaxRetries           int
	MaxAgentTurns        int
	Model                string
	Provider             string
	BaseURL              string
	// APIKeyEnv is the environment variable the API key is read from, or "" to use the
	// provider's own default name. Empty for "cli", which authenticates itself.
	APIKeyEnv string
	// CLICommand is the argv `provider: "cli"` runs, already split into words. Empty for
	// every other provider, and for "cli" it is what makes the generator buildable at all.
	CLICommand []string
}

// Resolve applies the defaults.
func (l LazyDescriptions) Resolve() ResolvedLazyDescriptions {
	out := ResolvedLazyDescriptions{
		Enabled:        boolOr(l.Enabled, true),
		MaxNodes:       DefaultLazyMaxNodes,
		TimeoutSeconds: DefaultLazyTimeoutSeconds,
		BatchSize:      DefaultLazyBatchSize,
		Parallel:       DefaultLazyParallel,
		Provider:       strings.ToLower(strings.TrimSpace(l.Provider)),
		BaseURL:        strings.TrimSpace(l.BaseURL),

		Background:           boolOr(l.Background, DefaultLazyBackground),
		WorkerTimeoutSeconds: DefaultLazyWorkerTimeoutSeconds,
		MaxWorkers:           DefaultLazyMaxWorkers,
		MaxRetries:           DefaultDescriptionMaxRetries,
	}
	// MaxNodes takes a non-positive value as "no limit", and TimeoutSeconds gives the two
	// signs of non-positive different meanings (0 do not wait, negative wait it out), so both
	// are copied whenever the key is present at all. BatchSize and Parallel have no such
	// reading -- a batch of zero describes nothing -- so a non-positive value there keeps
	// the default rather than disabling the feature by arithmetic.
	if l.MaxNodes != nil {
		out.MaxNodes = *l.MaxNodes
	}
	if l.TimeoutSeconds != nil {
		out.TimeoutSeconds = *l.TimeoutSeconds
	}
	if l.BatchSize != nil && *l.BatchSize > 0 {
		out.BatchSize = *l.BatchSize
	}
	if l.Parallel != nil && *l.Parallel > 0 {
		out.Parallel = *l.Parallel
	}
	// A non-positive worker timeout or worker cap keeps the default rather than meaning "no
	// limit". Both bound a process nothing is waiting for, and "unbounded" is not a thing a
	// project should be able to ask for by typing a zero.
	if l.WorkerTimeoutSeconds != nil && *l.WorkerTimeoutSeconds > 0 {
		out.WorkerTimeoutSeconds = *l.WorkerTimeoutSeconds
	}
	if l.MaxWorkers != nil && *l.MaxWorkers > 0 {
		out.MaxWorkers = *l.MaxWorkers
	}
	if l.MaxRetries != nil && *l.MaxRetries > 0 {
		out.MaxRetries = *l.MaxRetries
	}
	if l.MaxAgentTurns != nil && *l.MaxAgentTurns > 0 {
		out.MaxAgentTurns = *l.MaxAgentTurns
	}
	// Only a POSITIVE wait is capped. A negative one is the project asking to wait the work out,
	// and clamping that to two minutes would answer a question nobody asked -- it is already
	// bounded by the worker's own ceiling, which is the honest bound for "until it is done".
	if out.TimeoutSeconds > MaxLazyTimeoutSeconds {
		out.TimeoutSeconds = MaxLazyTimeoutSeconds
	}
	// A worker that stops before the read waiting on it does is strictly worse than no worker:
	// the read would time out on work that had already been abandoned. (A negative wait is
	// already bounded BY the worker, so there is nothing to reconcile.)
	if out.TimeoutSeconds > 0 && out.WorkerTimeoutSeconds < out.TimeoutSeconds {
		out.WorkerTimeoutSeconds = out.TimeoutSeconds
	}
	return out
}

// LazyDescriptionsEnabled reports whether descriptions may be generated on the read path.
// Every gate reads it through this method rather than the field, so the switch keeps one
// meaning across the read, slice and grep surfaces.
func (c *Config) LazyDescriptionsEnabled() bool {
	if c == nil {
		return false
	}
	return c.Descriptions.Lazy.Resolve().Enabled
}

// EffectiveLazyDescriptions resolves the description settings for a harness: the lazy
// tuning knobs, plus the one provider choice both entry points share.
//
// The TRANSPORT has two tiers, and each exists for a reason.
//
//  1. `descriptions.provider` / `.base_url` / `.cli_provider_command` -- the current
//     spelling, and the only one the sweep ever had a way to honour.
//  2. `descriptions.lazy.{provider,base_url}` -- where those settings used to live. Read so
//     a config written before the move keeps describing with what it named; a silently
//     ignored key would have looked like the feature breaking.
//
// The MODEL has exactly one tier: the descriptions-generation-executor agent
// (`llm.<harness>.agents.descriptions-generation-executor.model`). It is not a fallback and
// there is nothing to override it with. That agent is where the sweep already runs, so a
// project that said "write my descriptions with haiku" has said it once -- and a config key
// that said it a second time was a second place for the two to disagree, with no way to see
// which one won. Out of the box it is unset, and stays unset: a model pinned there also picks
// the PROVIDER by inference, so a default here is a vendor chosen for a project that has not
// been asked yet.
//
// The command is split here, once, rather than at each transport: a config carrying an
// unparseable command resolves to no command at all, which the generator reports as "not
// configured" instead of running half an argv.
func (c *Config) EffectiveLazyDescriptions(harness string) ResolvedLazyDescriptions {
	if c == nil {
		return LazyDescriptions{Enabled: boolPtr(false)}.Resolve()
	}
	if strings.TrimSpace(harness) == "" {
		harness = DefaultLazyHarness
	}
	out := c.Descriptions.Lazy.Resolve()
	if p := strings.ToLower(strings.TrimSpace(c.Descriptions.Provider)); p != "" {
		out.Provider = p
	}
	if u := strings.TrimSpace(c.Descriptions.BaseURL); u != "" {
		out.BaseURL = u
	}
	if env := strings.TrimSpace(c.Descriptions.APIKeyEnv); env != "" {
		out.APIKeyEnv = env
	}
	out.CLICommand, _ = SplitCommand(c.Descriptions.CLIProviderCommand)
	if agent := c.EffectiveAgent(harness, DescriptionsExecutorAgent); agent.Model != "" &&
		agent.Model != InheritsModel {
		out.Model = strings.TrimSpace(agent.Model)
	}
	return out
}

// DescriptionsExecutorAgent is the configured agent whose model, and whose house style, both
// description entry points use. Its model is THE description model: the sweep runs as this
// agent, and the lazy fill reads the same field so the two cannot describe with different
// models.
const DescriptionsExecutorAgent = "descriptions-generation-executor"

// DefaultLazyHarness is the harness block a fill resolves its model against when the caller
// has no harness of its own to name -- which is most of them: `arac read`, `arac grep` and
// `arac cmd` are the shell, not a harness.
//
// claude_code, because that is the block a project pins the executor's model in, and a fill
// that resolved against <any> would ignore the one place a project has already said which
// model writes its descriptions.
const DefaultLazyHarness = "claude_code"

// The API transports, by the name a config writes in `descriptions.provider`.
//
// They live here, next to ProviderNameCLI and underneath lazydesc, for the same reason it
// does: the name a config validates against, the name a prompt offers, and the name the
// transport dispatches on are one string and cannot be allowed to drift into three.
const (
	ProviderNameAnthropic = "anthropic"
	ProviderNameOpenAI    = "openai"
	ProviderNameDeepSeek  = "deepseek"
)

// APIProviderNames lists the API transports in the order a chooser offers them.
//
// Anthropic first because it is the one aracne's own default model belongs to; OpenAI second
// because its wire format is the one most third-party vendors serve, so it is the answer for
// far more endpoints than the company it is named after.
func APIProviderNames() []string {
	return []string{ProviderNameAnthropic, ProviderNameOpenAI, ProviderNameDeepSeek}
}

// IsAPIProvider reports whether a name is one of the HTTP transports (as opposed to
// ProviderNameCLI, or nothing at all).
func IsAPIProvider(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case ProviderNameAnthropic, ProviderNameOpenAI, ProviderNameDeepSeek:
		return true
	}
	return false
}

// DefaultAPIKeyEnv is the environment variable a provider reads its key from when
// `descriptions.api_key_env` names none. Empty for a provider with no key of its own.
func DefaultAPIKeyEnv(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case ProviderNameAnthropic:
		return "ANTHROPIC_API_KEY"
	case ProviderNameOpenAI:
		return "OPENAI_API_KEY"
	case ProviderNameDeepSeek:
		return "DEEPSEEK_API_KEY"
	}
	return ""
}

// APIKeyEnvFor is the variable a resolved config actually reads its key from: what the
// project named, or the provider's own default. One function so the prompt that asks for it,
// the resolver that reads it, and any error that names it cannot disagree.
func APIKeyEnvFor(cfg ResolvedLazyDescriptions) string {
	if env := strings.TrimSpace(cfg.APIKeyEnv); env != "" {
		return env
	}
	return DefaultAPIKeyEnv(cfg.Provider)
}

// ProviderNameCLI runs a command of the project's choosing -- see
// DescriptionsSection.CLIProviderCommand -- instead of calling an API.
//
// Declared here, underneath lazydesc, and aliased by lazydesc.ProviderCLI, so the name a
// config validates against and the name the transport dispatches on are one string and cannot
// drift.
const ProviderNameCLI = "cli"

// ProviderNameClaudeCLI is the retired name for what "cli" now does. It is recognised only to
// be rejected with instructions: a project carrying it would otherwise be told "unknown
// provider" about a key it was told to write, and the fix -- one command it has to guess -- is
// exactly what the error can spell out.
const ProviderNameClaudeCLI = "claude_cli"

// ClaudeCLIReplacementCommand is what `provider: "claude_cli"` used to run, as a command the
// project can paste into cli_provider_command.
const ClaudeCLIReplacementCommand = "claude --print --max-turns 1"

// ValidateDescriptionProvider rejects a provider name nothing can serve, and a "cli" that
// names no command.
//
// A typo here would otherwise be invisible: generation would find no provider, decline
// silently, and the project would conclude the feature does not work. The empty-command case
// is the same failure with a shorter fuse -- `provider: "cli"` alone is a project that has
// asked for a transport and forgotten to say what to run.
func ValidateDescriptionProvider(d DescriptionsSection) error {
	provider := strings.ToLower(strings.TrimSpace(d.Provider))
	key := "descriptions.provider"
	if provider == "" {
		// Fall back to the old spelling so a legacy typo is still caught, and named as the
		// key the project actually wrote.
		provider = strings.ToLower(strings.TrimSpace(d.Lazy.Provider))
		key = "descriptions.lazy.provider"
	}
	switch provider {
	case "", ProviderNameAnthropic, ProviderNameOpenAI, ProviderNameDeepSeek:
	case ProviderNameClaudeCLI:
		return fmt.Errorf("%s: %q was replaced by %q; write:\n"+
			"  \"descriptions\": {\"provider\": %q, \"cli_provider_command\": %q}",
			key, ProviderNameClaudeCLI, ProviderNameCLI,
			ProviderNameCLI, ClaudeCLIReplacementCommand)
	case ProviderNameCLI:
		if argv, err := SplitCommand(d.CLIProviderCommand); err != nil {
			return fmt.Errorf("descriptions.cli_provider_command: %w", err)
		} else if len(argv) == 0 {
			return fmt.Errorf("%s: %q needs descriptions.cli_provider_command, "+
				"e.g. \"claude -p\"", key, ProviderNameCLI)
		}
	default:
		return fmt.Errorf("%s: unknown provider %q (want %s or %s)",
			key, provider, strings.Join(APIProviderNames(), ", "), ProviderNameCLI)
	}
	return nil
}

// SplitCommand splits a configured command line into argv.
//
// It is argv, not a shell line: single and double quotes group words, a backslash escapes the
// next character, and nothing else is special. Running it through `sh -c` instead would buy
// pipes and variable expansion nobody asked for, hand a config file the power to run arbitrary
// shell, and stop working on a machine without a shell -- while the thing actually wanted here
// is "claude -p" with its flags.
func SplitCommand(command string) ([]string, error) {
	var (
		argv    []string
		word    strings.Builder
		started bool
		quote   rune
		escaped bool
	)
	flush := func() {
		if started {
			argv = append(argv, word.String())
			word.Reset()
			started = false
		}
	}
	for _, r := range command {
		switch {
		case escaped:
			word.WriteRune(r)
			escaped = false
			started = true
		case r == '\\' && quote != '\'':
			escaped = true
			started = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				word.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
			started = true
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			flush()
		default:
			word.WriteRune(r)
			started = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unbalanced %c quote in %q", quote, command)
	}
	if escaped {
		return nil, fmt.Errorf("trailing backslash in %q", command)
	}
	flush()
	return argv, nil
}
