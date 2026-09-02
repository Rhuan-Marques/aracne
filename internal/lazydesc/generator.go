package lazydesc

import (
	"context"
	"fmt"
	"os"
	"strings"

	"aracne/internal/helper"
	"aracne/internal/llm"
	"aracne/internal/llm/providers"
	"aracne/internal/prompts"
	"aracne/internal/topology/domain"
)

// Generator turns one batch of targets into descriptions, keyed by resource id.
//
// It returns what it managed to write, not an all-or-nothing result: a batch of five where the
// model answered for four is four descriptions the project did not have a moment ago, and
// throwing them away to report a clean failure would be strictly worse.
type Generator interface {
	Describe(ctx context.Context, batch Batch) (map[string]string, error)
}

// Batch is one unit of work: the resources to describe, plus the already-written descriptions
// that anchor the house style for them.
//
// The exemplars ride with the batch rather than with the generator because they are chosen
// from the topology FOR these resources -- same kind first, same file as the tiebreak -- and
// the generator is built once, before any of that is known.
type Batch struct {
	Resources []Request
	Exemplars []prompts.DescriptionExemplar
}

// Request is one resource handed to a generator, with its source already cut.
type Request struct {
	ID     string
	Name   string
	Kind   domain.ResourceKind
	Source string
}

// GeneratorFactory builds the generator a Filler uses. It is a variable so tests can install a
// deterministic fake without an API key, a network call or a build tag; production code never
// assigns it.
//
// It returns (nil, nil) when no provider is configured. That is not an error -- an
// unconfigured project is simply a project where lazy generation does nothing, and reporting
// it as a failure on every read would turn a switched-off feature into noise.
var GeneratorFactory = func(cfg helper.ResolvedLazyDescriptions) (Generator, error) {
	// An explicit `provider: "claude_cli"` skips the key probe entirely.
	if strings.EqualFold(strings.TrimSpace(cfg.Provider), ProviderClaudeCLI) {
		if !claudeCLIAvailable() {
			return nil, nil
		}
		return &cliGenerator{bin: claudeCLIBinary(), model: claudeCLIModel(cfg.Model)}, nil
	}
	if provider, model, ok := resolveProvider(cfg); ok {
		return &llmGenerator{provider: provider, model: model}, nil
	}
	// Deliberately NOT a fallback. `provider: "claude_cli"` has to be asked for, because it
	// spends the user's interactive Claude Code quota and adds a process launch to a read --
	// surprising a project that simply has no API key with both, on every cold read, is worse
	// than the feature staying off. TestGeneratorFactoryReturnsNothingWhenUnconfigured pins
	// that contract.
	return nil, nil
}

// llmGenerator is the production generator: one completion per batch, no tools.
type llmGenerator struct {
	provider llm.Provider
	model    string
}

// Describe sends one batch and parses the reply.
func (g *llmGenerator) Describe(ctx context.Context, batch Batch) (map[string]string, error) {
	if len(batch.Resources) == 0 {
		return nil, nil
	}
	resources := make([]prompts.DescriptionResource, 0, len(batch.Resources))
	wanted := make(map[string]domain.ResourceKind, len(batch.Resources))
	for _, req := range batch.Resources {
		resources = append(resources, prompts.DescriptionResource{
			ID: req.ID, Name: req.Name, Kind: req.Kind, ReadOutput: req.Source,
		})
		wanted[req.ID] = req.Kind
	}

	messages := []llm.Message{
		{Role: "system", Content: prompts.LazyDescriptionsPrompt()},
		{Role: "user", Content: prompts.LazyDescriptionsInput(resources, batch.Exemplars)},
	}

	resp, err := g.chat(ctx, messages)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, fmt.Errorf("empty response")
	}
	return prompts.ParseLazyDescriptions(resp.Content, wanted), nil
}

// chat prefers the context-aware entry point so the fill's deadline actually cancels an
// in-flight request, and falls back to the plain one for a provider that has none.
func (g *llmGenerator) chat(ctx context.Context, messages []llm.Message) (*llm.ChatResponse, error) {
	if cp, ok := g.provider.(llm.ContextProvider); ok {
		return cp.StreamChatContext(ctx, messages, nil, func(llm.StreamEvent) {})
	}
	// Without cancellation the deadline still holds, because the Filler stops waiting; the
	// request is simply abandoned rather than aborted.
	return g.provider.Chat(messages, nil)
}

// Provider names accepted in descriptions.lazy.provider.
const (
	providerAnthropic = "anthropic"
	providerOpenAI    = "openai"
	providerDeepSeek  = "deepseek"
)

// modelAliases maps the short names a project actually writes in its config onto the model ids
// the APIs want. `llm.<harness>.agents.descriptions-generation-executor.model` is "haiku" out
// of the box -- a Claude Code sub-agent alias -- and that string has to mean something here
// too, or the one place a project already states its description model would be unusable.
var modelAliases = map[string]struct{ provider, model string }{
	"haiku":  {providerAnthropic, "claude-haiku-4-5"},
	"sonnet": {providerAnthropic, "claude-sonnet-4-6"},
	"opus":   {providerAnthropic, "claude-opus-4-8"},
	"fable":  {providerAnthropic, "fable-5"},
	"mini":   {providerOpenAI, "gpt-5.4-mini"},
	"flash":  {providerDeepSeek, "deepseek-v4-flash"},
}

// providerKeyEnv is the environment variable each provider's key comes from.
var providerKeyEnv = map[string]string{
	providerAnthropic: "ANTHROPIC_API_KEY",
	providerOpenAI:    "OPENAI_API_KEY",
	providerDeepSeek:  "DEEPSEEK_API_KEY",
}

// providerFallbackModel is what a provider named without a model runs.
var providerFallbackModel = map[string]string{
	providerAnthropic: "claude-haiku-4-5",
	providerOpenAI:    "gpt-5.4-mini",
	providerDeepSeek:  "deepseek-v4-flash",
}

// providerProbeOrder is the order an unconfigured project is probed in: cheapest capable model
// first, since a lazy fill is a small, mechanical, latency-sensitive job.
var providerProbeOrder = []string{providerAnthropic, providerOpenAI, providerDeepSeek}

// resolveProvider works out what to call, in three tiers: an explicit provider, a provider
// inferred from the model name, or whichever provider has a key in the environment.
//
// It reports ok=false rather than an error when nothing is configured. A project with no API
// key has not misconfigured anything -- it has simply not opted into a feature that needs one,
// and it must keep getting its reads unchanged and unmentioned.
func resolveProvider(cfg helper.ResolvedLazyDescriptions) (llm.Provider, string, bool) {
	name, model := cfg.Provider, cfg.Model
	if alias, ok := modelAliases[strings.ToLower(strings.TrimSpace(model))]; ok {
		if name == "" {
			name = alias.provider
		}
		model = alias.model
	}
	if name == "" {
		name = inferProvider(model)
	}
	if name == "" {
		for _, candidate := range providerProbeOrder {
			if os.Getenv(providerKeyEnv[candidate]) != "" {
				name = candidate
				break
			}
		}
	}
	if name == "" {
		return nil, "", false
	}
	key := os.Getenv(providerKeyEnv[name])
	if key == "" {
		return nil, "", false
	}
	if strings.TrimSpace(model) == "" {
		model = providerFallbackModel[name]
	}
	switch name {
	case providerAnthropic:
		return providers.NewAnthropic(key, model, cfg.BaseURL), model, true
	case providerOpenAI:
		return providers.NewOpenAI(key, model, cfg.BaseURL), model, true
	case providerDeepSeek:
		return providers.NewDeepSeekWithConfig(key, model, cfg.BaseURL), model, true
	}
	return nil, "", false
}

// inferProvider guesses the API from a full model id, so naming a model is normally enough.
func inferProvider(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	switch {
	case m == "":
		return ""
	case strings.Contains(m, "claude"), strings.Contains(m, "fable"),
		strings.Contains(m, "haiku"), strings.Contains(m, "sonnet"), strings.Contains(m, "opus"):
		return providerAnthropic
	case strings.Contains(m, "deepseek"):
		return providerDeepSeek
	case strings.HasPrefix(m, "gpt"), strings.HasPrefix(m, "o1"), strings.HasPrefix(m, "o3"),
		strings.HasPrefix(m, "o4"), strings.Contains(m, "codex"):
		return providerOpenAI
	}
	return ""
}
