package helper

import (
	"encoding/json"
	"strings"
	"testing"
)

// A misspelled key silently does nothing, which looks exactly like a key that was applied.
// `"mode": "not_a_mode"` has always been refused by name; `"read": {"max_file_szie": 99}` was
// accepted, kept across a setup round-trip, and never mentioned.
func TestUnknownConfigKeysFindsATypoInAKnownSection(t *testing.T) {
	raw := []byte(`{
	  "read": {"max_file_size": 1024, "max_file_szie": 99},
	  "scan": {"workers": 0},
	  "totally_unknown": 123,
	  "mode": "cli"
	}`)
	got := UnknownConfigKeys(raw)
	want := []string{"read.max_file_szie", "totally_unknown"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("UnknownConfigKeys = %v, want %v", got, want)
	}
	warning := ConfigKeyWarning(raw)
	for _, fragment := range want {
		if !strings.Contains(warning, fragment) {
			t.Errorf("warning does not name %q: %s", fragment, warning)
		}
	}
}

// The default config is the one every project starts from. If it tripped this check, the
// warning would fire for everyone and be trained away on day one.
func TestDefaultConfigHasNoUnknownKeys(t *testing.T) {
	raw, err := json.Marshal(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if got := UnknownConfigKeys(raw); len(got) != 0 {
		t.Errorf("the default config reports its own keys as unknown: %v", got)
	}
	if w := ConfigKeyWarning(raw); w != "" {
		t.Errorf("the default config warns: %s", w)
	}
}

// A map keyed by names the user chooses -- chat.agents -- must not have every name it holds
// reported as a typo.
func TestUnknownConfigKeysDoesNotDescendIntoUserKeyedMaps(t *testing.T) {
	raw := []byte(`{"llm": {"agents": {"anything_at_all": {"tools": ["read"]}}},
	                "chat": {"agents": {"a_name_only_this_project_uses": {"tools": ["read"]}}}}`)
	for _, key := range UnknownConfigKeys(raw) {
		if strings.Contains(key, "anything_at_all") || strings.Contains(key, "a_name_only_this_project_uses") {
			t.Errorf("an agent name was reported as an unknown key: %s", key)
		}
	}
}
