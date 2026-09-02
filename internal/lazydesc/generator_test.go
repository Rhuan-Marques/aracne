package lazydesc

import (
	"testing"

	"aracne/internal/helper"
)

// clearKeys removes every provider key for the duration of a test, so the environment the
// suite happens to run in cannot decide the answer.
func clearKeys(t *testing.T) {
	t.Helper()
	for _, env := range providerKeyEnv {
		t.Setenv(env, "")
	}
}

func TestResolveProviderNeedsAKey(t *testing.T) {
	clearKeys(t)
	if _, _, ok := resolveProvider(helper.ResolvedLazyDescriptions{Model: "haiku"}); ok {
		t.Fatal("a model with no key in the environment must not resolve")
	}
	if _, _, ok := resolveProvider(helper.ResolvedLazyDescriptions{}); ok {
		t.Fatal("no model and no key must not resolve")
	}
}

// "haiku" is what DefaultConfig pins the description executor to, so it has to mean something
// here -- the whole model fallback rests on it.
func TestResolveProviderExpandsAliases(t *testing.T) {
	clearKeys(t)
	t.Setenv("ANTHROPIC_API_KEY", "k")
	_, model, ok := resolveProvider(helper.ResolvedLazyDescriptions{Model: "haiku"})
	if !ok {
		t.Fatal("haiku did not resolve with an Anthropic key present")
	}
	if model != "claude-haiku-4-5" {
		t.Fatalf("model = %q, want the expanded alias", model)
	}
}

func TestResolveProviderInfersFromModelName(t *testing.T) {
	for _, tc := range []struct{ model, want string }{
		{"claude-haiku-4-5", providerAnthropic},
		{"fable-5", providerAnthropic},
		{"gpt-5.4-mini", providerOpenAI},
		{"gpt-5.5-codex", providerOpenAI},
		{"deepseek-v4-flash", providerDeepSeek},
		{"", ""},
		{"llama-3", ""},
	} {
		if got := inferProvider(tc.model); got != tc.want {
			t.Errorf("inferProvider(%q) = %q, want %q", tc.model, got, tc.want)
		}
	}
}

// An explicit provider wins over what the model name suggests, so a project pointing a
// Claude-named model at a compatible endpoint is not overruled.
func TestResolveProviderExplicitProviderWins(t *testing.T) {
	clearKeys(t)
	t.Setenv("OPENAI_API_KEY", "k")
	_, model, ok := resolveProvider(helper.ResolvedLazyDescriptions{
		Provider: providerOpenAI, Model: "some-local-claude-ish",
	})
	if !ok {
		t.Fatal("an explicit provider with a key did not resolve")
	}
	if model != "some-local-claude-ish" {
		t.Fatalf("model = %q, want it left alone", model)
	}
}

// With nothing configured at all, whichever key is present decides -- which is what makes the
// feature work out of the box for a project that has an API key and has never read this doc.
func TestResolveProviderProbesTheEnvironment(t *testing.T) {
	clearKeys(t)
	t.Setenv("DEEPSEEK_API_KEY", "k")
	_, model, ok := resolveProvider(helper.ResolvedLazyDescriptions{})
	if !ok {
		t.Fatal("a bare config with a DeepSeek key did not resolve")
	}
	if model != providerFallbackModel[providerDeepSeek] {
		t.Fatalf("model = %q, want the provider's fallback", model)
	}
}

// A provider named without a key is not silently swapped for one that has a key: the project
// asked for that API, and answering with another would bill the wrong account.
func TestResolveProviderDoesNotSubstituteProviders(t *testing.T) {
	clearKeys(t)
	t.Setenv("OPENAI_API_KEY", "k")
	if _, _, ok := resolveProvider(helper.ResolvedLazyDescriptions{Provider: providerAnthropic}); ok {
		t.Fatal("an explicitly named provider with no key resolved to another provider")
	}
}

func TestGeneratorFactoryReturnsNothingWhenUnconfigured(t *testing.T) {
	clearKeys(t)
	gen, err := GeneratorFactory(helper.ResolvedLazyDescriptions{})
	if err != nil {
		t.Fatalf("factory error: %v", err)
	}
	if gen != nil {
		t.Fatal("an unconfigured project should get no generator, not a broken one")
	}
}
