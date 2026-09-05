package helper

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// A config that has never heard of the key gets the feature. That is the whole point of the
// default, and it is the one thing about it that must not regress silently.
func TestLazyDescriptionsDefaultsOn(t *testing.T) {
	var cfg Config
	if err := json.Unmarshal([]byte(`{"descriptions":{"kinds":["function"]}}`), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !cfg.LazyDescriptionsEnabled() {
		t.Fatal("descriptions.lazy should default to on")
	}
	if got := DefaultConfig().LazyDescriptionsEnabled(); !got {
		t.Fatal("DefaultConfig should have lazy descriptions on")
	}
}

func TestLazyDescriptionsBooleanForm(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want bool
	}{
		{`{"descriptions":{"lazy":true}}`, true},
		{`{"descriptions":{"lazy":false}}`, false},
		{`{"descriptions":{"lazy":null}}`, true},
		{`{"descriptions":{}}`, true},
	} {
		var cfg Config
		if err := json.Unmarshal([]byte(tc.raw), &cfg); err != nil {
			t.Fatalf("%s: unmarshal: %v", tc.raw, err)
		}
		if got := cfg.LazyDescriptionsEnabled(); got != tc.want {
			t.Errorf("%s: enabled = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

func TestLazyDescriptionsObjectForm(t *testing.T) {
	var cfg Config
	raw := `{"descriptions":{"lazy":{"enabled":true,"max_nodes":3,"timeout_seconds":9,` +
		`"batch_size":2,"parallel":1,"provider":"anthropic"}}}`
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got := cfg.EffectiveLazyDescriptions("claude_code")
	// No model: this config names no executor agent, and the model has no other source.
	want := ResolvedLazyDescriptions{
		Enabled: true, MaxNodes: 3, TimeoutSeconds: 9, BatchSize: 2, Parallel: 1,
		Provider: "anthropic",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resolved = %+v, want %+v", got, want)
	}
}

// The object form must be able to turn the feature OFF too -- otherwise a project that wants
// the tuning knobs cannot also want it disabled, and would have to delete the block to do it.
func TestLazyDescriptionsObjectCanDisable(t *testing.T) {
	var cfg Config
	if err := json.Unmarshal([]byte(`{"descriptions":{"lazy":{"enabled":false,"max_nodes":5}}}`), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cfg.LazyDescriptionsEnabled() {
		t.Fatal("object form with enabled:false should be off")
	}
}

// A config round-trips in the shape it was written in: the switch stays a switch, the tuned
// block stays an object. A `"lazy": true` that came back as an object would rewrite every
// user's config the first time aracne saved one.
func TestLazyDescriptionsRoundTripsItsShape(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`true`, `true`},
		{`false`, `false`},
		{`{"enabled":false}`, `false`},
		{`{"max_nodes":7}`, `{"max_nodes":7}`},
		{`{"enabled":true,"provider":"anthropic"}`, `{"enabled":true,"provider":"anthropic"}`},
	} {
		var l LazyDescriptions
		if err := json.Unmarshal([]byte(tc.in), &l); err != nil {
			t.Fatalf("%s: unmarshal: %v", tc.in, err)
		}
		out, err := json.Marshal(l)
		if err != nil {
			t.Fatalf("%s: marshal: %v", tc.in, err)
		}
		if string(out) != tc.want {
			t.Errorf("%s round-tripped to %s, want %s", tc.in, out, tc.want)
		}
	}
}

func TestLazyDescriptionsRejectsGarbage(t *testing.T) {
	var l LazyDescriptions
	if err := json.Unmarshal([]byte(`"yes"`), &l); err == nil {
		t.Fatal("a string should not decode as descriptions.lazy")
	}
}

// The knobs mean different things at zero, and the difference is load-bearing: "no cap" is a
// sensible thing to ask for, "batches of zero" is not.
func TestLazyDescriptionsZeroValues(t *testing.T) {
	var l LazyDescriptions
	if err := json.Unmarshal([]byte(`{"max_nodes":0,"timeout_seconds":0,"batch_size":0,"parallel":0}`), &l); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got := l.Resolve()
	if got.MaxNodes != 0 || got.TimeoutSeconds != 0 {
		t.Errorf("max_nodes/timeout of 0 should stay 0 (no limit), got %d/%d", got.MaxNodes, got.TimeoutSeconds)
	}
	if got.BatchSize != DefaultLazyBatchSize || got.Parallel != DefaultLazyParallel {
		t.Errorf("batch_size/parallel of 0 should fall back to defaults, got %d/%d", got.BatchSize, got.Parallel)
	}
}

// The model is the description executor's, and only the executor's -- out of the box haiku on
// claude_code, and whatever the project renames it to after that. There is no descriptions.model
// to override it with, which is the point: one answer, in the place the sweep already runs.
func TestLazyDescriptionsTakesTheExecutorModel(t *testing.T) {
	cfg := DefaultConfig()
	if got := cfg.EffectiveLazyDescriptions("claude_code").Model; got != "haiku" {
		t.Fatalf("model = %q, want the executor's haiku", got)
	}
	agents := cfg.LLM.ClaudeCode.Agents
	agents[DescriptionsExecutorAgent] = AgentConfig{Model: "gpt-5.4-mini"}
	if got := cfg.EffectiveLazyDescriptions("claude_code").Model; got != "gpt-5.4-mini" {
		t.Fatalf("model = %q, want the executor's new model", got)
	}
}

// An empty harness resolves against claude_code, where the executor's model is pinned. The
// CLI surfaces have no harness of their own and would otherwise silently lose the setting.
func TestLazyDescriptionsEmptyHarnessUsesDefault(t *testing.T) {
	cfg := DefaultConfig()
	if got := cfg.EffectiveLazyDescriptions("").Model; got != "haiku" {
		t.Fatalf("model = %q, want haiku via the default harness", got)
	}
}

func TestLazyDescriptionsValidatesProvider(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Descriptions.Lazy.Provider = "aunthropic"
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "descriptions.lazy.provider") {
		t.Fatalf("Validate() = %v, want a provider error", err)
	}
	for _, ok := range []string{"", "anthropic", "openai", "deepseek", "  Anthropic "} {
		cfg.Descriptions.Lazy.Provider = ok
		if err := cfg.Validate(); err != nil {
			t.Errorf("provider %q: %v", ok, err)
		}
	}
}

// A hand-written `{"descriptions":{"lazy":false}}` is a legitimate new-schema config. Judged
// legacy, EnsureConfig would overwrite it with defaults and silently turn the feature back on
// -- the exact failure the features/mode entries in validConfig already guard against.
func TestLazyOnlyConfigIsNotMistakenForLegacy(t *testing.T) {
	var cfg Config
	if err := json.Unmarshal([]byte(`{"descriptions":{"lazy":false}}`), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !validConfig(&cfg) {
		t.Fatal("a lazy-only config should be recognised as the new schema")
	}
}

// A nil Config is the "no config on disk" case every surface can hit. It must read as off,
// not panic, and not decide it is on by default.
func TestLazyDescriptionsNilConfig(t *testing.T) {
	var cfg *Config
	if cfg.LazyDescriptionsEnabled() {
		t.Fatal("a nil config should not enable lazy descriptions")
	}
	if cfg.EffectiveLazyDescriptions("").Enabled {
		t.Fatal("a nil config should resolve to disabled")
	}
}

// Every accepted provider must validate in both spellings, or `arac init` rejects the whole
// config and writes nothing -- which is how a benchmark run silently lost its .mcp.json and
// its regenerated contract while still reporting a mode it was not running.
func TestDescriptionProviderNamesValidate(t *testing.T) {
	for _, p := range []string{"anthropic", "OPENAI", "deepseek", ""} {
		if err := ValidateDescriptionProvider(DescriptionsSection{Lazy: LazyDescriptions{Provider: p}}); err != nil {
			t.Errorf("provider %q should validate, got %v", p, err)
		}
		if err := ValidateDescriptionProvider(DescriptionsSection{Provider: p}); err != nil {
			t.Errorf("section provider %q should validate, got %v", p, err)
		}
	}
	if err := ValidateDescriptionProvider(DescriptionsSection{Provider: "nope"}); err == nil {
		t.Error("an unknown provider must still be rejected")
	}
	if err := ValidateDescriptionProvider(DescriptionsSection{Lazy: LazyDescriptions{Provider: "nope"}}); err == nil {
		t.Error("an unknown provider in the old spelling must still be rejected")
	}
	for _, section := range []DescriptionsSection{
		{Provider: "cli", CLIProviderCommand: "claude -p"},
		{Lazy: LazyDescriptions{Provider: "CLI"}, CLIProviderCommand: "claude -p"},
	} {
		if err := ValidateDescriptionProvider(section); err != nil {
			t.Errorf("%+v should validate, got %v", section, err)
		}
	}
}

// The retired name is rejected with the command that replaces it, in whichever key it was
// written. A project that wrote it was told to; "unknown provider" would be a dead end.
func TestRetiredClaudeCLIProviderIsRejectedWithTheFix(t *testing.T) {
	for _, section := range []DescriptionsSection{
		{Provider: "claude_cli"},
		{Lazy: LazyDescriptions{Provider: "CLAUDE_CLI"}},
	} {
		err := ValidateDescriptionProvider(section)
		if err == nil {
			t.Fatalf("%+v must be rejected", section)
		}
		for _, want := range []string{ProviderNameCLI, ClaudeCLIReplacementCommand} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("the rejection should name %q, got %v", want, err)
			}
		}
	}
}

// `provider: "cli"` with nothing to run is the one misconfiguration that looks like a working
// one, so it is rejected at init rather than at the first fill.
func TestCLIProviderNeedsACommand(t *testing.T) {
	if err := ValidateDescriptionProvider(DescriptionsSection{Provider: "cli"}); err == nil {
		t.Fatal(`provider "cli" with no cli_provider_command must be rejected`)
	}
	if err := ValidateDescriptionProvider(DescriptionsSection{
		Provider: "cli", CLIProviderCommand: "claude -p",
	}); err != nil {
		t.Fatalf(`provider "cli" with a command should validate, got %v`, err)
	}
	if err := ValidateDescriptionProvider(DescriptionsSection{
		Provider: "cli", CLIProviderCommand: `claude -p "unbalanced`,
	}); err == nil {
		t.Fatal("an unparseable command must be rejected")
	}
}

// The section keys are the current spelling and the lazy ones are where they used to live:
// both must reach the same resolved settings, with the section winning a disagreement.
func TestDescriptionProviderMovedUpFromLazy(t *testing.T) {
	var legacy Config
	if err := json.Unmarshal([]byte(`{"descriptions":{"lazy":{"provider":"openai",`+
		`"base_url":"https://gw.example"}}}`), &legacy); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got := legacy.EffectiveLazyDescriptions("claude_code")
	if got.Provider != "openai" || got.BaseURL != "https://gw.example" {
		t.Fatalf("the old spelling must keep working, got %+v", got)
	}

	var both Config
	if err := json.Unmarshal([]byte(`{"descriptions":{"provider":"cli",`+
		`"cli_provider_command":"claude -p --model haiku",`+
		`"lazy":{"provider":"openai"}}}`), &both); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got = both.EffectiveLazyDescriptions("claude_code")
	if got.Provider != "cli" {
		t.Errorf("the section key must win, got provider %q", got.Provider)
	}
	if want := []string{"claude", "-p", "--model", "haiku"}; !reflect.DeepEqual(got.CLICommand, want) {
		t.Errorf("CLICommand = %q, want %q", got.CLICommand, want)
	}
}

// A config whose only hand edit is the provider must not be mistaken for a legacy file and
// overwritten with defaults -- the same trap `{"descriptions":{"lazy":false}}` was in.
func TestProviderOnlyConfigIsNotLegacy(t *testing.T) {
	var cfg Config
	if err := json.Unmarshal([]byte(`{"descriptions":{"provider":"cli",`+
		`"cli_provider_command":"claude -p"}}`), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !validConfig(&cfg) {
		t.Fatal("a provider-only config is a legitimate new-schema file")
	}
}

func TestSplitCommand(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want []string
	}{
		{"claude -p", []string{"claude", "-p"}},
		{"  claude   -p  ", []string{"claude", "-p"}},
		{`claude -p --append "a b"`, []string{"claude", "-p", "--append", "a b"}},
		{`claude --flag 'a b'`, []string{"claude", "--flag", "a b"}},
		{`sh -c ""`, []string{"sh", "-c", ""}},
		{"", nil},
	} {
		got, err := SplitCommand(tc.in)
		if err != nil {
			t.Errorf("SplitCommand(%q): %v", tc.in, err)
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("SplitCommand(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if _, err := SplitCommand(`claude "oops`); err == nil {
		t.Error("an unbalanced quote must be an error, not a truncated argv")
	}
}
