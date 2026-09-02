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
	// DefaultLazyTimeoutSeconds bounds the whole fill, not one batch. A read that has
	// already waited two minutes has stopped being a read.
	DefaultLazyTimeoutSeconds = 120
	// DefaultLazyBatchSize is how many resources one completion describes.
	DefaultLazyBatchSize = DefaultDescriptionBatchSize
	// DefaultLazyParallel is how many batches run at once.
	DefaultLazyParallel = 4
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
	// TimeoutSeconds bounds one fill end to end. <= 0 disables the deadline.
	TimeoutSeconds *int `json:"timeout_seconds,omitempty"`
	// BatchSize is how many resources go into one completion.
	BatchSize *int `json:"batch_size,omitempty"`
	// Parallel is how many batches are in flight at once.
	Parallel *int `json:"parallel,omitempty"`
	// Model overrides the model the fill runs on. Absent falls back to the model configured
	// for the descriptions-generation-executor agent, which is where a project already says
	// what it wants descriptions written by.
	Model string `json:"model,omitempty"`
	// Provider names the API to call ("anthropic", "openai", "deepseek"). Absent is inferred
	// from the model, so naming a model is normally enough.
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
		l.Parallel != nil || strings.TrimSpace(l.Model) != "" ||
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
	Model          string
	Provider       string
	BaseURL        string
}

// Resolve applies the defaults.
func (l LazyDescriptions) Resolve() ResolvedLazyDescriptions {
	out := ResolvedLazyDescriptions{
		Enabled:        boolOr(l.Enabled, true),
		MaxNodes:       DefaultLazyMaxNodes,
		TimeoutSeconds: DefaultLazyTimeoutSeconds,
		BatchSize:      DefaultLazyBatchSize,
		Parallel:       DefaultLazyParallel,
		Model:          strings.TrimSpace(l.Model),
		Provider:       strings.ToLower(strings.TrimSpace(l.Provider)),
		BaseURL:        strings.TrimSpace(l.BaseURL),
	}
	// MaxNodes and TimeoutSeconds take a non-positive value as "no limit", so they are
	// copied whenever the key is present at all. BatchSize and Parallel have no such
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

// EffectiveLazyDescriptions resolves the lazy settings for a harness, filling the model in
// from the descriptions-generation-executor agent when the lazy block does not name one.
//
// That fallback is the whole reason the executor's model is not restated here: a project that
// already said "write my descriptions with haiku" has said it once, and a second place to say
// it again is a second place for the two to disagree.
func (c *Config) EffectiveLazyDescriptions(harness string) ResolvedLazyDescriptions {
	if c == nil {
		return LazyDescriptions{Enabled: boolPtr(false)}.Resolve()
	}
	if strings.TrimSpace(harness) == "" {
		harness = DefaultLazyHarness
	}
	out := c.Descriptions.Lazy.Resolve()
	if out.Model == "" {
		if agent := c.EffectiveAgent(harness, DescriptionsExecutorAgent); agent.Model != "" &&
			agent.Model != InheritsModel {
			out.Model = strings.TrimSpace(agent.Model)
		}
	}
	return out
}

// DescriptionsExecutorAgent is the configured agent whose model, and whose house style, the
// lazy fill borrows.
const DescriptionsExecutorAgent = "descriptions-generation-executor"

// DefaultLazyHarness is the harness block a fill resolves its model against when the caller
// has no harness of its own to name -- which is most of them: `arac read`, `arac grep` and
// `arac cmd` are the shell, not a harness.
//
// claude_code, because that is the block DefaultConfig pins the executor's model in ("haiku"),
// and a fill that resolved against <any> would ignore the one place a project has already
// said which model writes its descriptions.
const DefaultLazyHarness = "claude_code"

// ValidateLazyDescriptions rejects a provider name nothing can serve. A typo here would
// otherwise be invisible: the fill would find no provider, decline silently, and the project
// would conclude the feature does not work.
func ValidateLazyDescriptions(l LazyDescriptions) error {
	switch strings.ToLower(strings.TrimSpace(l.Provider)) {
	case "", "anthropic", "openai", "deepseek":
		return nil
	default:
		return fmt.Errorf("descriptions.lazy.provider: unknown provider %q (want anthropic, openai or deepseek)", l.Provider)
	}
}
