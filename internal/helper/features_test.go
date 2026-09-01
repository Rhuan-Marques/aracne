package helper

import (
	"os"
	"path/filepath"
	"testing"

	"aracne/internal/topology/domain"
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
	if err := os.WriteFile(path, []byte(`{"scan":{"mode":"default"},"read":{"max_file_size":1024}}`), 0o644); err != nil {
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
