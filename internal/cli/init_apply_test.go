package cli

import (
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
)

// What the wizard's answers do to a config, and the two rules about the description model that
// are easy to get subtly wrong: finding one already named inside a CLI command, and putting one
// where the CLI transport will actually read it.

func TestApplyWritesTheAPIBranch(t *testing.T) {
	cfg := helper.DefaultConfig()
	initAnswers{
		Harness:   harnessClaudeCode,
		Mode:      helper.ModeInterceptID,
		Provider:  helper.ProviderNameOpenAI,
		APIKeyEnv: "MY_GATEWAY_KEY",
		Model:     "gpt-5.4-mini",
		Verbosity: helper.ContractVerbosityHigh,
	}.apply(cfg)

	if cfg.Mode != helper.ModeInterceptID {
		t.Errorf("mode = %q", cfg.Mode)
	}
	if cfg.ContractVerbosity != helper.ContractVerbosityHigh {
		t.Errorf("contract_verbosity = %q", cfg.ContractVerbosity)
	}
	if cfg.Descriptions.Provider != helper.ProviderNameOpenAI {
		t.Errorf("provider = %q", cfg.Descriptions.Provider)
	}
	if cfg.Descriptions.APIKeyEnv != "MY_GATEWAY_KEY" {
		t.Errorf("api_key_env = %q", cfg.Descriptions.APIKeyEnv)
	}
	if cfg.Descriptions.CLIProviderCommand != "" {
		t.Errorf("the API branch left a command behind: %q", cfg.Descriptions.CLIProviderCommand)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("the wizard produced an invalid config: %v", err)
	}
}

// The two describer answers are mutually exclusive in the file as well as in the flow: a config
// carrying both a key variable and a command says two things about who writes its descriptions,
// and the next reader has no way to tell which one was meant.
func TestApplyClearsTheOtherBranch(t *testing.T) {
	cfg := helper.DefaultConfig()
	cfg.Descriptions.APIKeyEnv = "LEFT_OVER_KEY"
	initAnswers{
		Provider:   helper.ProviderNameCLI,
		CLICommand: "claude -p",
		Mode:       helper.ModeCLI,
		Verbosity:  helper.ContractVerbosityLow,
	}.apply(cfg)

	if cfg.Descriptions.CLIProviderCommand != "claude -p" {
		t.Errorf("cli_provider_command = %q", cfg.Descriptions.CLIProviderCommand)
	}
	if cfg.Descriptions.APIKeyEnv != "" {
		t.Errorf("switching to cli left api_key_env = %q", cfg.Descriptions.APIKeyEnv)
	}
	if len(missingDescriptionAnswers(&cfg.Descriptions)) != 0 {
		t.Error("a config the wizard wrote must not still be missing an answer")
	}
}

// The model goes in llm.<any>, which is what both harnesses and the read-path lazy fill all
// resolve through. A per-harness block would be a model the other harness cannot see.
func TestApplyPinsTheModelWhereBothHarnessesSeeIt(t *testing.T) {
	cfg := helper.DefaultConfig()
	initAnswers{
		Provider:  helper.ProviderNameAnthropic,
		APIKeyEnv: "ANTHROPIC_API_KEY",
		Model:     "claude-haiku-4-5",
		Mode:      helper.ModeCLI,
		Verbosity: helper.ContractVerbosityLow,
	}.apply(cfg)

	for _, harness := range []string{"claude_code", "opencode"} {
		if got := cfg.EffectiveAgent(harness, helper.DescriptionsExecutorAgent).Model; got != "claude-haiku-4-5" {
			t.Errorf("%s resolves the executor model to %q", harness, got)
		}
	}
	if got := cfg.EffectiveLazyDescriptions(helper.DefaultLazyHarness).Model; got != "claude-haiku-4-5" {
		t.Errorf("the lazy fill resolves the model to %q", got)
	}
	// It must survive a round trip through the file, or the next command reads the
	// placeholder it replaced.
	path := t.TempDir() + "/config.json"
	if err := helper.SaveConfig(cfg, path); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	if got := helper.LoadConfig(path).EffectiveLazyDescriptions(helper.DefaultLazyHarness).Model; got != "claude-haiku-4-5" {
		t.Errorf("after a save and load the model is %q", got)
	}
}

func TestModelInCommand(t *testing.T) {
	for _, tc := range []struct{ command, want string }{
		{"claude -p", ""},
		{"claude -p --model sonnet", "sonnet"},
		{"claude --model=haiku -p", "haiku"},
		{"codex exec", ""},
		{"claude -p --model", ""},        // a flag with nothing after it names nothing
		{`claude -p "unbalanced`, ""},    // unparseable is not an answer
		{"claude -p --model-name x", ""}, // a different flag that starts the same way
	} {
		if got := modelInCommand(tc.command); got != tc.want {
			t.Errorf("modelInCommand(%q) = %q, want %q", tc.command, got, tc.want)
		}
	}
}

// The flag is appended only where aracne actually knows it. Guessing --model onto someone
// else's program turns a working command into one that exits 2.
func TestCommandWithModel(t *testing.T) {
	for _, tc := range []struct{ command, model, want string }{
		{"claude -p", "haiku", "claude -p --model haiku"},
		{"claude --print", "haiku", "claude --print --model haiku"},
		{"claude -p --model sonnet", "haiku", "claude -p --model sonnet"},
		{"codex exec", "haiku", "codex exec"},
		{"./scripts/describe.sh", "haiku", "./scripts/describe.sh"},
		{"claude -p", "", "claude -p"},
		// DE-12: any Claude CLI in print mode takes the flag, whatever else it was given --
		// `claude -p --max-turns 1` is the command the docs recommend.
		{"claude -p --max-turns 1", "haiku", "claude -p --max-turns 1 --model haiku"},
		{"claude --print --max-turns 1", "haiku", "claude --print --max-turns 1 --model haiku"},
		{"claude --max-turns 1 -p", "haiku", "claude --max-turns 1 -p --model haiku"},
		{"/usr/local/bin/claude -p", "haiku", "/usr/local/bin/claude -p --model haiku"},
		// ...and it stays narrow about the program and its mode.
		{"claude --max-turns 1", "haiku", "claude --max-turns 1"},
		{"claude -p -- describe these", "haiku", "claude -p -- describe these"},
		{"claudette -p", "haiku", "claudette -p"},
		{"codex exec -p", "haiku", "codex exec -p"},
	} {
		if got := commandWithModel(tc.command, tc.model); got != tc.want {
			t.Errorf("commandWithModel(%q, %q) = %q, want %q", tc.command, tc.model, got, tc.want)
		}
	}
}

// An appended flag has to survive the split the transport does, or the command aracne runs is
// not the command the config shows.
func TestAnAppendedModelSplitsBackOut(t *testing.T) {
	command := commandWithModel(defaultDescribeCLICommand, "haiku")
	argv, err := helper.SplitCommand(command)
	if err != nil {
		t.Fatalf("SplitCommand(%q): %v", command, err)
	}
	want := []string{"claude", "-p", "--model", "haiku"}
	if len(argv) != len(want) {
		t.Fatalf("argv = %q, want %q", argv, want)
	}
	for i := range want {
		if argv[i] != want[i] {
			t.Fatalf("argv = %q, want %q", argv, want)
		}
	}
}

func TestDefaultModelFollowsTheProvider(t *testing.T) {
	for provider, want := range map[string]string{
		helper.ProviderNameAnthropic: "claude-haiku-4-5",
		helper.ProviderNameOpenAI:    "gpt-5.4-mini",
		helper.ProviderNameDeepSeek:  "deepseek-v4-flash",
		// A different string for the same model: this one goes on a command line,
		// where the Claude CLI resolves its own aliases, and the published id is
		// what an HTTP endpoint wants instead.
		helper.ProviderNameCLI: "haiku",
	} {
		if got := defaultModelFor(provider); got != want {
			t.Errorf("defaultModelFor(%q) = %q, want %q", provider, got, want)
		}
	}
}
