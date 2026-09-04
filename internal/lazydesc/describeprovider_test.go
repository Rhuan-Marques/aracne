package lazydesc

import (
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
)

// TestResolveDescriptionProviderFallsBackToEnvironment pins the difference between the two
// callers of the same resolution.
//
// The stock config pins the descriptions executor to "haiku", so a project that configured
// nothing still resolves to Anthropic. `arac descriptions generate` must not refuse such a
// project when the only key it holds is a different vendor's -- that was the exact case the
// command supported before the DeepSeek hardcoding was generalised, and losing it would have
// been a regression dressed up as a feature.
func TestResolveDescriptionProviderFallsBackToEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name, env, wantModel string
	}{
		{"anthropic", "ANTHROPIC_API_KEY", "claude-haiku-4-5"},
		{"deepseek", "DEEPSEEK_API_KEY", "deepseek-v4-flash"},
		{"openai", "OPENAI_API_KEY", "gpt-5.4-mini"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, k := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "DEEPSEEK_API_KEY"} {
				t.Setenv(k, "")
			}
			t.Setenv(tc.env, "k")

			// Model "haiku" is what DefaultConfig resolves to, i.e. Anthropic.
			cfg := helper.ResolvedLazyDescriptions{Model: "haiku"}
			provider, model, ok := ResolveDescriptionProvider(cfg)
			if !ok {
				t.Fatalf("holding %s, description generation must resolve a provider", tc.env)
			}
			if provider == nil {
				t.Fatal("resolved ok but returned a nil provider")
			}
			if model != tc.wantModel {
				t.Errorf("model = %q, want %q", model, tc.wantModel)
			}
		})
	}
}

// TestResolveDescriptionProviderNeedsSomeKey pins the other half: with no key anywhere the
// command must refuse, not fall through to a provider it cannot authenticate.
func TestResolveDescriptionProviderNeedsSomeKey(t *testing.T) {
	for _, k := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "DEEPSEEK_API_KEY"} {
		t.Setenv(k, "")
	}
	if _, _, ok := ResolveDescriptionProvider(helper.ResolvedLazyDescriptions{Model: "haiku"}); ok {
		t.Fatal("with no provider key set, resolution must report ok=false")
	}
}

// TestLazyFillDoesNotFallBack guards the asymmetry itself. A lazy fill is a side effect of a
// read: if a project configured Anthropic and holds only a DeepSeek key, the read must go out
// undescribed rather than quietly bill a vendor the project did not name.
func TestLazyFillDoesNotFallBack(t *testing.T) {
	for _, k := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "DEEPSEEK_API_KEY"} {
		t.Setenv(k, "")
	}
	t.Setenv("DEEPSEEK_API_KEY", "k")

	cfg := helper.ResolvedLazyDescriptions{Provider: "anthropic"}
	if _, _, ok := resolveProvider(cfg); ok {
		t.Fatal("a lazy fill must not silently switch to a provider the project did not name")
	}
}
