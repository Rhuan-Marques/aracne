package helper

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// TestBugManagementDefaultsOff pins the v1 default. An existing project's config has no
// `features` key at all, and an absent bool must decode to false.
func TestBugManagementDefaultsOff(t *testing.T) {
	if DefaultConfig().BugManagementEnabled() {
		t.Fatal("features.bug_management must default to false")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	// A realistic pre-existing config: valid new schema, no features key.
	if err := os.WriteFile(path, []byte(`{"read":{"max_file_size":1024}}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if LoadConfig(path).BugManagementEnabled() {
		t.Fatal("a config with no features key must decode with the feature off")
	}
}

// TestFeaturesOnlyConfigSurvivesEnsureConfig is the sharpest migration edge.
//
// validConfig decides whether a decoded file is the new schema or a legacy one to be
// clean-break overwritten. Turning a feature on is the single edit a user makes by hand, and
// a file whose only non-zero field is `features` used to look exactly like a legacy config --
// so EnsureConfig replaced it with defaults and the feature silently turned itself back off.
func TestFeaturesOnlyConfigSurvivesEnsureConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"features":{"bug_management":true}}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if !LoadConfig(path).BugManagementEnabled() {
		t.Fatal("a features-only config must load with the feature on")
	}
	if got := EnsureConfig(path); !got.BugManagementEnabled() {
		t.Fatal("EnsureConfig overwrote a hand-enabled feature with defaults")
	}
	if !LoadConfig(path).BugManagementEnabled() {
		t.Fatal("the on-disk file lost the feature after EnsureConfig")
	}
}

// TestBugManagementRoundTrips checks the field survives save/load.
func TestBugManagementRoundTrips(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg := DefaultConfig()
	cfg.Features.BugManagement = true
	if err := SaveConfig(cfg, path); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	if !LoadConfig(path).BugManagementEnabled() {
		t.Fatal("features.bug_management did not round-trip")
	}
}

// TestReadOnlyBugAgentsCannotEditOrWrite pins the privilege split: the hunter and the judge
// only read and record a verdict, so they must not carry the native edit/write tools. The
// solver does change code and keeps them.
//
// Deliberately NOT blocking "read": that would rename the aracne read tool
// (toolspec.ResolveReadToolName), which is a separate decision from privilege.
func TestReadOnlyBugAgentsCannotEditOrWrite(t *testing.T) {
	cfg := DefaultConfig()
	has := func(xs []string, want string) bool {
		for _, x := range xs {
			if x == want {
				return true
			}
		}
		return false
	}
	for _, harness := range []string{"claude_code", "opencode"} {
		for _, agent := range []string{"bug-hunter", "bug-judge"} {
			blocked := cfg.EffectiveAgent(harness, agent).BlockedTools
			for _, tool := range []string{"edit", "write"} {
				if !has(blocked, tool) {
					t.Errorf("%s/%s must block %q, blocked=%v", harness, agent, tool, blocked)
				}
			}
			if has(blocked, "read") {
				t.Errorf("%s/%s must NOT block read (it renames the MCP tool), blocked=%v", harness, agent, blocked)
			}
		}
		solver := cfg.EffectiveAgent(harness, "bug-solver").BlockedTools
		for _, tool := range []string{"edit", "write"} {
			if has(solver, tool) {
				t.Errorf("%s/bug-solver must keep %q -- fixing bugs means changing code, blocked=%v", harness, tool, solver)
			}
		}
	}
}

// TestDeleteBugRejectsMissingID pins that a delete matching no row is an error, matching
// UpdateBugState. Reporting success for a bug that was not there made the bug-judge
// fan-out's mutual-delete race invisible.
func TestDeleteBugRejectsMissingID(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "topology.db")
	topo := &domain.Topology{Root: dir, Resources: map[string]domain.Resource{}}
	if err := WriteDb(topo, dbPath); err != nil {
		t.Fatalf("WriteDb: %v", err)
	}
	if err := CreateBug(dbPath, domain.KnownBug{ID: "b1", NodeID: "n", Description: "d", State: domain.BugPending}); err != nil {
		t.Fatalf("CreateBug: %v", err)
	}

	if err := DeleteBug(dbPath, "b1"); err != nil {
		t.Fatalf("deleting an existing bug should succeed: %v", err)
	}
	if err := DeleteBug(dbPath, "b1"); err == nil {
		t.Fatal("deleting the same bug twice must error, not report success")
	}
	if err := DeleteBug(dbPath, "never-existed"); err == nil {
		t.Fatal("deleting an unknown bug id must error")
	}
}

// TestChatAndAgentDefaultOff pins the 1.0 defaults for the two surfaces that are in the
// tree but not in the product.
func TestChatAndAgentDefaultOff(t *testing.T) {
	if DefaultConfig().ChatEnabled() {
		t.Error("features.chat must default to false")
	}
	if DefaultConfig().AgentEnabled() {
		t.Error("features.agent must default to false")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"read":{"max_file_size":1024}}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg := LoadConfig(path)
	if cfg.ChatEnabled() || cfg.AgentEnabled() {
		t.Fatal("a config with no features key must decode with chat and agent off")
	}
}

// TestFeaturesOnlyChatAndAgentSurviveEnsureConfig runs the migration edge above for the two
// new flags. validConfig tests each feature bit by name, so a flag added without being added
// there loads correctly and is then silently reset by the next EnsureConfig -- the failure is
// invisible until someone's hand-enabled feature turns itself back off.
func TestFeaturesOnlyChatAndAgentSurviveEnsureConfig(t *testing.T) {
	for _, tc := range []struct {
		name string
		json string
		got  func(*Config) bool
	}{
		{"chat", `{"features":{"chat":true}}`, (*Config).ChatEnabled},
		{"agent", `{"features":{"agent":true}}`, (*Config).AgentEnabled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(tc.json), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			if !tc.got(LoadConfig(path)) {
				t.Fatal("a features-only config must load with the feature on")
			}
			if !tc.got(EnsureConfig(path)) {
				t.Fatal("EnsureConfig overwrote a hand-enabled feature with defaults")
			}
			if !tc.got(LoadConfig(path)) {
				t.Fatal("the on-disk file lost the feature after EnsureConfig")
			}
		})
	}
}

// TestFeaturesOnlyWarningReadsSurvivesEnsureConfig runs the migration edge for the new flag:
// turning a feature on by hand is the single edit a user makes to this file, and a config whose
// only non-zero field is `features` must not be judged legacy and replaced with defaults.
func TestFeaturesOnlyWarningReadsSurvivesEnsureConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"features":{"warning_reads":true}}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !LoadConfig(path).WarningReadsEnabled() {
		t.Fatal("a features-only config must load with the feature on")
	}
	if !EnsureConfig(path).WarningReadsEnabled() {
		t.Fatal("EnsureConfig overwrote a hand-enabled feature with defaults")
	}
	if !LoadConfig(path).WarningReadsEnabled() {
		t.Fatal("the on-disk file lost the feature after EnsureConfig")
	}
}

// TestEffectiveWarningReadMaxBytes pins the three states. ABSENT IS NOT ZERO: an untouched config
// means DefaultWarningReadMaxBytes, and only a value a project actually wrote -- 0 or lower --
// means no limit at all.
func TestEffectiveWarningReadMaxBytes(t *testing.T) {
	if got := DefaultConfig().EffectiveWarningReadMaxBytes(); got != DefaultWarningReadMaxBytes {
		t.Errorf("an absent warning_read_max_bytes = %d, want %d", got, DefaultWarningReadMaxBytes)
	}
	for _, tc := range []struct {
		name string
		json string
		want int
	}{
		{"absent", `{"features":{"warning_reads":true}}`, DefaultWarningReadMaxBytes},
		{"explicit", `{"features":{"warning_reads":true,"warning_read_max_bytes":3}}`, 3},
		{"zero is unlimited", `{"features":{"warning_reads":true,"warning_read_max_bytes":0}}`, 0},
		{"negative is unlimited", `{"features":{"warning_reads":true,"warning_read_max_bytes":-1}}`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(tc.json), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			if got := LoadConfig(path).EffectiveWarningReadMaxBytes(); got != tc.want {
				t.Errorf("EffectiveWarningReadMaxBytes() = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestWarningReadMaxBytesRoundTrips checks the pointer survives save/load -- an explicit 0 must
// come back as an explicit 0 and not as the default.
func TestWarningReadMaxBytesRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := DefaultConfig()
	on := true
	cfg.Features.WarningReads = &on
	zero := 0
	cfg.Features.WarningReadMaxBytes = &zero
	if err := SaveConfig(cfg, path); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	loaded := LoadConfig(path)
	if !loaded.WarningReadsEnabled() {
		t.Fatal("features.warning_reads did not round-trip")
	}
	if got := loaded.EffectiveWarningReadMaxBytes(); got != 0 {
		t.Fatalf("an explicit 0 came back as %d -- the default was substituted for it", got)
	}
}

// TestValidateRejectsAnUnknownFeatureKey is the "silent failure" guard for the one section the
// documentation asks users to hand-edit.
//
// Every feature is a bool defaulting to off, so a misspelled key is indistinguishable from a
// key that is working and set to false: encoding/json drops it without a word and the feature
// simply never turns on. Measured on a real session -- `"warnings_reads": true`, plural, was
// written and the feature's absence was then reported as a bug in the feature.
func TestValidateRejectsAnUnknownFeatureKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"features":{"warnings_reads":true}}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	err := LoadConfig(path).Validate()
	if err == nil {
		t.Fatal("a misspelled feature key validated clean")
	}
	// By name, and with the alternatives: the whole point is that the user can see the typo.
	if !strings.Contains(err.Error(), `"warnings_reads"`) {
		t.Errorf("the error does not name the offending key: %v", err)
	}
	if !strings.Contains(err.Error(), "warning_reads") {
		t.Errorf("the error does not name the key that was meant: %v", err)
	}
}

// The check must not fire on the keys the section really has, on an absent features block, or
// on a config built in code -- Validate runs on the way into setup, init, serve and chat.
func TestValidateAcceptsTheRealFeatureKeys(t *testing.T) {
	for _, body := range []string{
		`{"features":{"warning_reads":true,"warning_read_max_bytes":3,"bug_management":true,"chat":true,"agent":true}}`,
		`{"features":{}}`,
		`{"read":{"max_file_size":1024}}`,
	} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		if err := LoadConfig(path).Validate(); err != nil {
			t.Errorf("%s: %v", body, err)
		}
	}
	if err := DefaultConfig().Validate(); err != nil {
		t.Errorf("a config built in code must validate: %v", err)
	}
}

// TestWarningReadsDefaultOn: an untouched config has the reads on, and only an explicit false
// turns them off -- through a save and load as much as in memory.
func TestWarningReadsDefaultOn(t *testing.T) {
	if !DefaultConfig().WarningReadsEnabled() {
		t.Fatal("DefaultConfig() has features.warning_reads off")
	}
	dir := t.TempDir()
	for name, tc := range map[string]struct {
		body string
		want bool
	}{
		"no features key":        {`{}`, true},
		"features without reads": {`{"features":{}}`, true},
		"explicit true":          {`{"features":{"warning_reads":true}}`, true},
		"explicit false":         {`{"features":{"warning_reads":false}}`, false},
	} {
		path := filepath.Join(dir, strings.ReplaceAll(name, " ", "_")+".json")
		if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := LoadConfig(path).WarningReadsEnabled(); got != tc.want {
			t.Errorf("%s: WarningReadsEnabled() = %v, want %v", name, got, tc.want)
		}
	}
	off := false
	cfg := DefaultConfig()
	cfg.Features.WarningReads = &off
	path := filepath.Join(dir, "roundtrip.json")
	if err := SaveConfig(cfg, path); err != nil {
		t.Fatal(err)
	}
	if LoadConfig(path).WarningReadsEnabled() {
		t.Error("an explicit false did not survive save and load")
	}
}
