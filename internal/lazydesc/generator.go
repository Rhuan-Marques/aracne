package lazydesc

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/llm"
	"github.com/Rhuan-Marques/aracne/internal/llm/providers"
	"github.com/Rhuan-Marques/aracne/internal/prompts"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
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
	// An explicit CLI provider skips the key probe entirely.
	if IsCLIProvider(cfg) {
		// The error is dropped for the same reason the caller drops one: a fill runs
		// inside a read, and "claude is not on PATH" printed into a read's output would
		// be noise in the answer to a different question. `arac descriptions generate`
		// calls NewCLIGenerator directly and does report it.
		gen, err := NewCLIGenerator(cfg)
		if err != nil {
			return nil, nil
		}
		return gen, nil
	}
	if provider, model, ok := resolveProvider(cfg); ok {
		return &llmGenerator{provider: provider, model: model}, nil
	}
	// Deliberately NOT a fallback. A CLI provider has to be asked for, because it spends the
	// user's interactive Claude Code quota and adds a process launch to a read -- surprising
	// a project that simply has no API key with both, on every cold read, is worse than the
	// feature staying off. TestGeneratorFactoryReturnsNothingWhenUnconfigured pins that
	// contract.
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

// Provider names accepted in descriptions.provider. Aliases of the helper constants, which is
// where a config validates them, so the dispatch below and the validation cannot drift.
const (
	providerAnthropic = helper.ProviderNameAnthropic
	providerOpenAI    = helper.ProviderNameOpenAI
	providerDeepSeek  = helper.ProviderNameDeepSeek
)

// modelAliases maps the short names a project actually writes in its config onto the model ids
// the APIs want. `llm.<harness>.agents.descriptions-generation-executor.model` is written as a
// Claude Code sub-agent alias ("haiku", "sonnet"), and those strings have to mean something
// here too, or the one place a project states its description model would be unusable.
//
// EVERY VALUE HERE MUST BE A MODEL ID THAT EXISTS, and two were not: `fable` resolved to
// "fable-5" (the ids are claude-fable-5 / claude-fable-5-1) and the wizard's own default for
// the Anthropic branch was "claude-haiku-5" (there is no such model). A bad id does not fail
// loudly -- generator() drops the error by design, because a fill runs inside a read whose
// output answers a different question -- so the symptom is descriptions that simply never
// appear. This table is a snapshot of someone else's release schedule and no test can tell
// when it goes stale; re-check it when a model generation ships.
var modelAliases = map[string]struct{ provider, model string }{
	"haiku":  {providerAnthropic, "claude-haiku-4-5"},
	"sonnet": {providerAnthropic, "claude-sonnet-5"},
	"opus":   {providerAnthropic, "claude-opus-5"},
	"fable":  {providerAnthropic, "claude-fable-5-1"},
	"mini":   {providerOpenAI, "gpt-5.4-mini"},
	"flash":  {providerDeepSeek, "deepseek-v4-flash"},
}

// providerKeyEnv is the environment variable each provider's key comes from when the config
// names none of its own. `descriptions.api_key_env` overrides it -- see helper.APIKeyEnvFor.
func providerKeyEnv(provider string) string { return helper.DefaultAPIKeyEnv(provider) }

// providerFallbackModel is what a provider named without a model runs.
var providerFallbackModel = map[string]string{
	providerAnthropic: "claude-haiku-4-5",
	providerOpenAI:    "gpt-5.4-mini",
	providerDeepSeek:  "deepseek-v4-flash",
}

// providerProbeOrder is the order an unconfigured project is probed in: cheapest capable model
// first, since a lazy fill is a small, mechanical, latency-sensitive job.
var providerProbeOrder = helper.APIProviderNames()

// ResolveDescriptionProvider works out which LLM to describe with, for any caller that needs
// one -- the lazy filler and the `arac descriptions generate` sweep both go through it.
//
// It exists exported because the sweep used to construct providers.NewDeepSeek() outright and
// exit if DEEPSEEK_API_KEY was unset. Description generation is a headline feature, so having
// it work with exactly one vendor's key -- while the lazy path on the same descriptions
// already accepted four -- made the product's answer to "how do I describe my repo?" depend
// on which of two entry points you happened to find.
//
// Unlike the lazy fill it falls back to probing the environment when an INFERRED provider has
// no key. The two want different things from the same resolution: a lazy fill is a side effect
// of a read and must decline silently rather than surprise anyone, while `arac descriptions
// generate` is an explicit request, so reaching for the key the user actually has beats
// refusing over one nobody chose. An explicitly named provider is exempt -- see below.
//
// Returns ok=false when nothing at all is configured; see resolveProvider for why that is not
// an error.
func ResolveDescriptionProvider(cfg helper.ResolvedLazyDescriptions) (llm.Provider, string, bool) {
	// A named CLI provider is not an API provider that failed to resolve, so it must not
	// fall through to the environment probe below: a project that asked to describe through
	// `claude -p` and happens to hold an OpenAI key asked for `claude -p`. Callers check
	// IsCLIProvider first; this is the guard for the ones that forget.
	if IsCLIProvider(cfg) {
		return nil, "", false
	}
	if provider, model, ok := resolveProvider(cfg); ok {
		return provider, model, true
	}
	// A provider the project NAMED is an answer, not a guess, so it is never swapped for
	// another vendor behind the user's back -- they were asked which one, and this is what
	// they said. The fallback below is for the other case: a provider that was only ever
	// inferred (from the executor's model, or from nothing at all), where reaching for the
	// key the machine actually holds beats refusing over one nobody chose.
	if strings.TrimSpace(cfg.Provider) != "" {
		return nil, "", false
	}
	// Keep the caller's base URL: it is transport, not provider choice, and a project that
	// proxies its LLM traffic still proxies it when the key came from the environment.
	return resolveProvider(helper.ResolvedLazyDescriptions{BaseURL: cfg.BaseURL})
}

// ProviderKeyEnvNames lists the environment variables any provider key may come from, for
// error messages that name every option instead of the one the caller happened to prefer.
func ProviderKeyEnvNames() []string {
	names := make([]string, 0, len(providerProbeOrder))
	for _, p := range providerProbeOrder {
		names = append(names, providerKeyEnv(p))
	}
	return names
}

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
	// The variable the project named, where it named one. A gateway that speaks the OpenAI
	// format while billing its own key is the case this exists for: the wire format and the
	// key are separate answers, and tying the second to the first meant such a project had
	// to export OPENAI_API_KEY holding a credential OpenAI never issued.
	keyEnv := ""
	if name != "" {
		keyEnv = helper.APIKeyEnvFor(helper.ResolvedLazyDescriptions{
			Provider: name, APIKeyEnv: cfg.APIKeyEnv,
		})
	} else {
		// Nothing named a provider, so the environment picks one -- and it picks by the
		// STANDARD variable names, not by a configured one: api_key_env answers "where is
		// the key for the provider I chose", and there is no chosen provider here.
		for _, candidate := range providerProbeOrder {
			if os.Getenv(providerKeyEnv(candidate)) != "" {
				name, keyEnv = candidate, providerKeyEnv(candidate)
				break
			}
		}
	}
	if name == "" || keyEnv == "" {
		return nil, "", false
	}
	key := os.Getenv(keyEnv)
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
