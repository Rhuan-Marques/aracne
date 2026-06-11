package chat

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestPermissionPolicyAllowsInWorkspaceReads(t *testing.T) {
	root := t.TempDir()
	policy := NewPermissionPolicy(root)

	decision := policy.Decide("grep", `{"path":"internal"}`, ModeBuild, ApprovalManual)
	if decision.Action != permissionAllow {
		t.Fatalf("expected allow, got %s: %s", decision.Action, decision.Reason)
	}
}

func TestPermissionPolicyAsksForOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	policy := NewPermissionPolicy(root)

	decision := policy.Decide("read_file", fmt.Sprintf(`{"path":%q}`, outside), ModeBuild, ApprovalAlways)
	if decision.Action != permissionAsk {
		t.Fatalf("expected ask, got %s: %s", decision.Action, decision.Reason)
	}
}

func TestPermissionPolicyBlocksMutationsInPlanMode(t *testing.T) {
	root := t.TempDir()
	policy := NewPermissionPolicy(root)

	decision := policy.Decide("write", `{"file_path":"internal/new.go"}`, ModePlan, ApprovalAlways)
	if decision.Action != permissionDeny {
		t.Fatalf("expected deny, got %s: %s", decision.Action, decision.Reason)
	}
}

func TestPermissionPolicyApprovalModes(t *testing.T) {
	root := t.TempDir()
	policy := NewPermissionPolicy(root)

	manual := policy.Decide("edit", `{"file_path":"internal/a.go"}`, ModeBuild, ApprovalManual)
	if manual.Action != permissionAsk {
		t.Fatalf("expected manual ask, got %s", manual.Action)
	}

	always := policy.Decide("edit", `{"file_path":"internal/a.go"}`, ModeBuild, ApprovalAlways)
	if always.Action != permissionAllow {
		t.Fatalf("expected always allow, got %s", always.Action)
	}
}

func TestStoreSaveLoadAll(t *testing.T) {
	store := NewStore(t.TempDir())
	now := time.Now().UTC()
	session := &Session{
		ID:           "chat_test",
		Title:        "test",
		CreatedAt:    now,
		UpdatedAt:    now,
		Mode:         ModeBuild,
		ApprovalMode: ApprovalManual,
		Provider:     ProviderOpenAI,
	}
	if err := store.Save(session); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.Load(session.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.ID != session.ID || loaded.Title != session.Title {
		t.Fatalf("loaded session mismatch: %+v", loaded)
	}

	all, err := store.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(all) != 1 || all[0].ID != session.ID {
		t.Fatalf("unexpected sessions: %+v", all)
	}
}
