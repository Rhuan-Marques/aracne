package cli

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
)

// What is left of the description setup once `arac init` owns the asking: two pure functions
// over a config -- "has this been answered" and "what do I say when it has not". The questions
// themselves are tested in init_questions_test.go, against the wizard that asks them.

// The stock config answers nothing. It used to answer "anthropic" by accident, through a
// pinned haiku the provider was inferred from, which is the whole reason these questions
// exist -- so this is the assertion the feature rests on.
func TestStockConfigNamesNoDescriptionProvider(t *testing.T) {
	cfg := helper.DefaultConfig()
	if got := cfg.Descriptions.Provider; got != "" {
		t.Errorf("descriptions.provider = %q, want blank out of the box", got)
	}
	if got := cfg.EffectiveLazyDescriptions("claude_code").Provider; got != "" {
		t.Errorf("resolved provider = %q, want blank out of the box", got)
	}
	if got := missingDescriptionAnswers(&cfg.Descriptions); !reflect.DeepEqual(got, []string{answerProvider}) {
		t.Errorf("missing = %v, want the provider question", got)
	}
}

// What counts as answered, and what is still outstanding. The provider is reported alone when
// it is missing, because which of the two follow-ups applies is not known until it is given.
func TestMissingDescriptionAnswers(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*helper.DescriptionsSection)
		want []string
	}{
		{"nothing at all", func(*helper.DescriptionsSection) {}, []string{answerProvider}},
		{
			// Answered: an absent api_key_env falls back to the provider's own variable
			// (DEEPSEEK_API_KEY here), as configuration.md documents and the lazy fill does.
			"an API provider with no variable",
			func(d *helper.DescriptionsSection) { d.Provider = helper.ProviderNameDeepSeek },
			nil,
		},
		{
			"an API provider, fully answered",
			func(d *helper.DescriptionsSection) {
				d.Provider = helper.ProviderNameDeepSeek
				d.APIKeyEnv = "DS_KEY"
			},
			nil,
		},
		{
			"cli with no command",
			func(d *helper.DescriptionsSection) { d.Provider = helper.ProviderNameCLI },
			[]string{answerCLICommand},
		},
		{
			"cli with an unparseable command",
			func(d *helper.DescriptionsSection) {
				d.Provider = helper.ProviderNameCLI
				d.CLIProviderCommand = `claude -p "unbalanced`
			},
			[]string{answerCLICommand},
		},
		{
			"cli, fully answered",
			func(d *helper.DescriptionsSection) {
				d.Provider = helper.ProviderNameCLI
				d.CLIProviderCommand = "my-describer --print"
			},
			nil,
		},
		{
			// The old `descriptions.lazy.provider` spelling is an answer. Re-asking
			// would punish exactly the projects that configured this before the key
			// moved onto the section.
			"the legacy provider spelling counts",
			func(d *helper.DescriptionsSection) { d.Lazy.Provider = helper.ProviderNameAnthropic },
			nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var d helper.DescriptionsSection
			tc.set(&d)
			if got := missingDescriptionAnswers(&d); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("missing = %v, want %v", got, tc.want)
			}
		})
	}
}

// The unconfigured sweep names the command that answers these questions properly, and then the
// keys for everything that will never run a wizard.
func TestUnansweredSetupErrorNamesInitAndTheKeys(t *testing.T) {
	var d helper.DescriptionsSection
	err := unansweredSetupError(&d)
	if err == nil {
		t.Fatal("an unconfigured section must produce an error")
	}
	for _, want := range []string{"arac init", "provider", "cli_provider_command", "anthropic"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should name %q, got:\n%v", want, err)
		}
	}
}

// A half-answered config is asked about the half that is missing, and not about the half it
// already settled: a project that has chosen cli and lost only cli_provider_command should not
// be told to pick a vendor again.
func TestUnansweredSetupErrorAsksOnlyForWhatIsMissing(t *testing.T) {
	d := helper.DescriptionsSection{Provider: helper.ProviderNameCLI}
	err := unansweredSetupError(&d)
	if err == nil {
		t.Fatal("a cli provider with no command must produce an error")
	}
	if !strings.Contains(err.Error(), "cli_provider_command") {
		t.Errorf("the error should name the missing command key, got:\n%v", err)
	}
	if strings.Contains(err.Error(), "deepseek") {
		t.Errorf("a chosen provider's error should not list the others, got:\n%v", err)
	}
}

// DE-6: a provider named without api_key_env is a complete answer. configuration.md says an
// absent api_key_env falls back to the provider's own variable, and the lazy fill already
// resolves it that way; the sweep refused the same config as "not configured".
func TestAPIProviderWithoutKeyEnvIsAnswered(t *testing.T) {
	for _, p := range helper.APIProviderNames() {
		d := helper.DescriptionsSection{Provider: p, BaseURL: "http://127.0.0.1:1"}
		if got := missingDescriptionAnswers(&d); len(got) != 0 {
			t.Errorf("provider %q without api_key_env: missing = %v, want nothing", p, got)
		}
	}
}

// The error a project sees when it answered the questions and the variable is empty names THAT
// variable, not three vendors' keys -- the provider is not in question any more.
func TestUnresolvedProviderErrorNamesTheConfiguredVariable(t *testing.T) {
	err := unresolvedProviderError(helper.ResolvedLazyDescriptions{
		Provider: helper.ProviderNameOpenAI, APIKeyEnv: "MY_GATEWAY_KEY",
	})
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "MY_GATEWAY_KEY") {
		t.Errorf("the error should name the configured variable, got:\n%v", err)
	}
	if strings.Contains(err.Error(), "DEEPSEEK_API_KEY") {
		t.Errorf("a chosen provider's error should not list the others, got:\n%v", err)
	}
}
