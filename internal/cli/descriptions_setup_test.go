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
			"an API provider with no variable",
			func(d *helper.DescriptionsSection) { d.Provider = helper.ProviderNameDeepSeek },
			[]string{answerAPIKeyEnv},
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
			[]string{answerAPIKeyEnv},
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
// already settled: a project that has chosen openai and lost only api_key_env should not be
// told to pick a vendor again.
func TestUnansweredSetupErrorAsksOnlyForWhatIsMissing(t *testing.T) {
	d := helper.DescriptionsSection{Provider: helper.ProviderNameOpenAI}
	err := unansweredSetupError(&d)
	if err == nil {
		t.Fatal("a provider with no key variable must produce an error")
	}
	if !strings.Contains(err.Error(), "OPENAI_API_KEY") {
		t.Errorf("the error should offer the provider's usual variable, got:\n%v", err)
	}
	if strings.Contains(err.Error(), "deepseek") {
		t.Errorf("a chosen provider's error should not list the others, got:\n%v", err)
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
