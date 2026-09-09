package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/tui"
)

// The keystrokes the wizard's tests press. Enter alone takes whatever is under the cursor,
// which is how most of these read: a run is mostly "the default, the default, this one".
const (
	enter = "\r"
	down  = "\x1b[B"
	esc   = "\x1b"
)

// ask drives the whole wizard with a scripted keyboard.
func ask(t *testing.T, cfg *helper.Config, sourceFiles int, keys string) (initAnswers, error) {
	t.Helper()
	s := tui.NewScriptedSession(strings.NewReader(keys), &bytes.Buffer{})
	return runInitQuestions(s, cfg, sourceFiles)
}

// mustAsk fails the test if the wizard did not complete.
func mustAsk(t *testing.T, keys string) initAnswers {
	t.Helper()
	a, err := ask(t, helper.DefaultConfig(), 10, keys)
	if err != nil {
		t.Fatalf("runInitQuestions(%q): %v", keys, err)
	}
	return a
}

// Enter through the whole flow is the path most first runs take, so it is the one whose
// answers have to be right without anybody having chosen them.
func TestTheDefaultRunIsClaudeCodeCliAnthropic(t *testing.T) {
	// harness, mode, describer, format, key variable, model, now/lazily, verbosity.
	a := mustAsk(t, strings.Repeat(enter, 8))

	if a.Harness != harnessClaudeCode {
		t.Errorf("harness = %q, want claude_code", a.Harness)
	}
	if a.Mode != helper.ModeCLI {
		t.Errorf("mode = %q, want cli", a.Mode)
	}
	if a.Provider != helper.ProviderNameAnthropic {
		t.Errorf("provider = %q, want anthropic", a.Provider)
	}
	if a.APIKeyEnv != "ANTHROPIC_API_KEY" {
		t.Errorf("api_key_env = %q, want ANTHROPIC_API_KEY", a.APIKeyEnv)
	}
	if a.Model != "claude-haiku-4-5" {
		t.Errorf("model = %q, want claude-haiku-4-5", a.Model)
	}
	if a.Verbosity != helper.ContractVerbosityLow {
		t.Errorf("contract_verbosity = %q, want low", a.Verbosity)
	}
	if !a.DescribeNow {
		t.Error("a ten-file repository should default to describing now")
	}
	if a.CLICommand != "" {
		t.Errorf("the API branch must leave cli_provider_command unset, got %q", a.CLICommand)
	}
}

func TestEachHarnessAndModeIsReachable(t *testing.T) {
	for _, tc := range []struct {
		keys        string
		harness     string
		mode        string
		wantClaude  bool
		wantOpencod bool
	}{
		{enter, harnessClaudeCode, helper.ModeCLI, true, false},
		{down, harnessOpenCode, helper.ModeMCP, false, true},
		{down + down, harnessBoth, helper.ModeInterceptID, true, true},
	} {
		// The same number of downs on question one and question two, then defaults.
		a := mustAsk(t, tc.keys+enter+tc.keys+enter+strings.Repeat(enter, 6))
		if a.Harness != tc.harness {
			t.Errorf("harness = %q, want %q", a.Harness, tc.harness)
		}
		if a.Mode != tc.mode {
			t.Errorf("mode = %q, want %q", a.Mode, tc.mode)
		}
		if a.writesClaude() != tc.wantClaude || a.writesOpenCode() != tc.wantOpencod {
			t.Errorf("%q writes claude=%v opencode=%v, want %v/%v",
				tc.harness, a.writesClaude(), a.writesOpenCode(), tc.wantClaude, tc.wantOpencod)
		}
	}
}

// The fourth mode is the last row, and a list that stopped one short of it would be a mode
// nobody could choose.
func TestTheLastModeIsReachable(t *testing.T) {
	a := mustAsk(t, enter+strings.Repeat(down, 3)+enter+strings.Repeat(enter, 6))
	if a.Mode != helper.ModeInterceptLineRanges {
		t.Errorf("mode = %q, want intercept_line_ranges", a.Mode)
	}
}

// A re-run opens on the answers the project already has, so pressing Enter through a
// configured repository changes nothing.
func TestTheQuestionsOpenOnTheCurrentConfig(t *testing.T) {
	cfg := helper.DefaultConfig()
	cfg.Mode = helper.ModeInterceptLineRanges
	cfg.ContractVerbosity = helper.ContractVerbosityHigh

	a, err := ask(t, cfg, 10, strings.Repeat(enter, 8))
	if err != nil {
		t.Fatalf("runInitQuestions: %v", err)
	}
	if a.Mode != helper.ModeInterceptLineRanges {
		t.Errorf("mode = %q, want the configured one", a.Mode)
	}
	if a.Verbosity != helper.ContractVerbosityHigh {
		t.Errorf("contract_verbosity = %q, want the configured one", a.Verbosity)
	}
}

// The API branch: the format decides the key variable offered and the model offered, and both
// are one keystroke.
func TestAPIBranchDefaultsFollowTheFormat(t *testing.T) {
	for _, tc := range []struct{ moves, provider, env, model string }{
		{"", helper.ProviderNameAnthropic, "ANTHROPIC_API_KEY", "claude-haiku-4-5"},
		{down, helper.ProviderNameOpenAI, "OPENAI_API_KEY", "gpt-5.4-mini"},
		{down + down, helper.ProviderNameDeepSeek, "DEEPSEEK_API_KEY", "deepseek-v4-flash"},
	} {
		a := mustAsk(t, enter+enter+enter+tc.moves+enter+enter+enter+enter+enter)
		if a.Provider != tc.provider || a.APIKeyEnv != tc.env || a.Model != tc.model {
			t.Errorf("format %q gave (%q, %q, %q), want (%q, %q, %q)",
				tc.moves, a.Provider, a.APIKeyEnv, a.Model, tc.provider, tc.env, tc.model)
		}
	}
}

// "Other:" is the second row of every open question, and what is typed there is the answer.
func TestTypedAnswersWin(t *testing.T) {
	a := mustAsk(t, enter+enter+enter+enter+
		down+"MY_GATEWAY_KEY"+enter+
		down+"gpt-5.4"+enter+
		enter+enter)

	if a.APIKeyEnv != "MY_GATEWAY_KEY" {
		t.Errorf("api_key_env = %q, want the typed value", a.APIKeyEnv)
	}
	if a.Model != "gpt-5.4" {
		t.Errorf("model = %q, want the typed value", a.Model)
	}
}

// The CLI branch, with no model in the command: the model is asked for AND appended to the
// command, because the CLI transport reads its model from the command and from nowhere else.
func TestCLIBranchAppendsTheModelToTheClaudeCLI(t *testing.T) {
	a := mustAsk(t, enter+enter+down+enter+enter+enter+enter+enter)

	if a.Provider != helper.ProviderNameCLI {
		t.Fatalf("provider = %q, want cli", a.Provider)
	}
	// The CLI branch offers the alias the Claude CLI resolves, not the published id an
	// HTTP endpoint would want -- this answer ends up on a command line.
	if a.CLICommand != "claude -p --model haiku" {
		t.Errorf("cli_provider_command = %q, want the model appended", a.CLICommand)
	}
	if a.Model != "haiku" {
		t.Errorf("model = %q, want it recorded as well", a.Model)
	}
	if a.APIKeyEnv != "" {
		t.Errorf("the CLI branch must not collect a key variable, got %q", a.APIKeyEnv)
	}
}

// A command that already names a model has answered question four. Asking again would collect
// a second model and write it to a key the CLI transport never reads.
func TestACommandNamingAModelSkipsTheModelQuestion(t *testing.T) {
	// After the command, only "now/lazily" and "verbosity" are left -- two Enters. A third
	// would be consumed by a model question that should not be there, and the run would end
	// having answered one question too few.
	a := mustAsk(t, enter+enter+down+enter+
		down+"claude -p --model sonnet"+enter+
		enter+enter)

	if a.Model != "sonnet" {
		t.Errorf("model = %q, want the one already in the command", a.Model)
	}
	if a.CLICommand != "claude -p --model sonnet" {
		t.Errorf("cli_provider_command = %q, want it untouched", a.CLICommand)
	}
	if a.Verbosity == "" {
		t.Error("the flow ran out of answers early -- the model question was asked anyway")
	}
}

// Escape is a cancel at every question, and a cancel writes nothing. Nothing here can write
// anything -- apply comes after the last answer -- and this is the test that keeps it that way.
func TestEscapeCancelsAtEveryQuestion(t *testing.T) {
	for i := 0; i < 8; i++ {
		keys := strings.Repeat(enter, i) + esc
		if _, err := ask(t, helper.DefaultConfig(), 10, keys); !errors.Is(err, tui.ErrCancelled) {
			t.Errorf("escape after %d answers: err = %v, want ErrCancelled", i, err)
		}
	}
}

// The offered default for the sweep flips with the size of the repository: small enough to
// describe in one sitting defaults to doing it, large enough to be an unattended job defaults
// to warming up as it is read. Either answer is available at either size.
func TestTheSweepDefaultFollowsTheRepositorySize(t *testing.T) {
	for _, tc := range []struct {
		files int
		now   bool
	}{
		{10, true},
		{describeEverythingFileCap, true},
		{describeEverythingFileCap + 1, false},
		{90_000, false},
	} {
		a, err := ask(t, helper.DefaultConfig(), tc.files, strings.Repeat(enter, 8))
		if err != nil {
			t.Fatalf("runInitQuestions: %v", err)
		}
		if a.DescribeNow != tc.now {
			t.Errorf("%d files defaulted to now=%v, want %v", tc.files, a.DescribeNow, tc.now)
		}
	}
}

func TestTheSweepAnswerCanBeOverridden(t *testing.T) {
	// Six defaults, then "lazily", then verbosity.
	a, err := ask(t, helper.DefaultConfig(), 10, strings.Repeat(enter, 6)+down+enter+enter)
	if err != nil {
		t.Fatalf("runInitQuestions: %v", err)
	}
	if a.DescribeNow {
		t.Error("a small repository must still be allowed to answer lazily")
	}
}

// A run that asked for an environment variable it cannot see says so -- after the alternate
// screen is down, which is why it is a note and not a print.
func TestAnUnsetKeyVariableIsNoted(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	a := mustAsk(t, strings.Repeat(enter, 8))
	if len(a.Notes) != 1 || !strings.Contains(a.Notes[0], "ANTHROPIC_API_KEY") {
		t.Fatalf("notes = %v, want one naming the unset variable", a.Notes)
	}

	t.Setenv("ANTHROPIC_API_KEY", "sk-something")
	if a := mustAsk(t, strings.Repeat(enter, 8)); len(a.Notes) != 0 {
		t.Errorf("a key that IS set should draw no note, got %v", a.Notes)
	}
}
