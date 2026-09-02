package helper

import (
	"encoding/json"
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
		`"batch_size":2,"parallel":1,"model":"haiku","provider":"anthropic"}}}`
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got := cfg.EffectiveLazyDescriptions("claude_code")
	want := ResolvedLazyDescriptions{
		Enabled: true, MaxNodes: 3, TimeoutSeconds: 9, BatchSize: 2, Parallel: 1,
		Model: "haiku", Provider: "anthropic",
	}
	if got != want {
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
		{`{"enabled":true,"model":"haiku"}`, `{"enabled":true,"model":"haiku"}`},
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

// The lazy model falls back to whatever the project already told the description executor to
// use, which out of the box is haiku on claude_code.
func TestLazyDescriptionsInheritsExecutorModel(t *testing.T) {
	cfg := DefaultConfig()
	if got := cfg.EffectiveLazyDescriptions("claude_code").Model; got != "haiku" {
		t.Fatalf("model = %q, want the executor's haiku", got)
	}
	// And an explicit lazy model wins over it.
	cfg.Descriptions.Lazy.Model = "gpt-5.4-mini"
	if got := cfg.EffectiveLazyDescriptions("claude_code").Model; got != "gpt-5.4-mini" {
		t.Fatalf("model = %q, want the explicit override", got)
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
