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

// Plan Mode is a hard refusal and the workspace scope is a prompt, so the refusal has to be
// decided first. The order used to be the other way round, which made `rm -rf /etc/nginx` in
// Plan Mode return `ask` while `rm -rf build` returned `deny` -- the safety gradient inverted,
// in the one mode a user selects to be certain nothing runs.
func TestPlanModeDeniesEvenOutsideTheWorkspace(t *testing.T) {
	policy := NewPermissionPolicy("/tmp/ws")
	for _, tc := range []struct {
		name string
		tool string
		args string
	}{
		{"bash inside", "bash", `{"command":"rm -rf build"}`},
		{"bash outside", "bash", `{"command":"rm -rf /etc/nginx"}`},
		{"write outside", "write", `{"file_path":"/etc/hosts","content":"x"}`},
		{"edit outside", "edit", `{"file_path":"/etc/hosts","old_string":"a","new_string":"b"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := policy.Decide(tc.tool, tc.args, ModePlan, ApprovalAlways)
			if d.Action != permissionDeny {
				t.Fatalf("Decide(%s) = %s (%s), want deny", tc.tool, d.Action, d.Reason)
			}
		})
	}
}

// The read tool's parameter is `ids`, and nothing used to look at it: isReadOnlyTool then
// returned allow with the reason "read-only in workspace", a claim nothing had checked. An
// absolute path among the ids falls through to rawFileUnit, which reads it off disk.
func TestReadIDsOutsideWorkspaceAreNotAutoAllowed(t *testing.T) {
	policy := NewPermissionPolicy("/tmp/ws")
	for _, tool := range []string{"read", "read_resource"} {
		d := policy.Decide(tool, `{"ids":["/etc/passwd"]}`, ModeBuild, ApprovalManual)
		if d.Action == permissionAllow {
			t.Fatalf("Decide(%s, ids=/etc/passwd) = allow (%s), want ask", tool, d.Reason)
		}
	}
}

// The same check must not turn resource ids into scope questions: an id is not a path, and a
// relative path resolves inside the workspace by construction. Reads of both stay allowed.
func TestReadIDsInsideWorkspaceStayAllowed(t *testing.T) {
	policy := NewPermissionPolicy("/tmp/ws")
	for _, args := range []string{
		`{"ids":["internal/cli.RunGuard"]}`,
		`{"ids":["github.com/x/y/internal/helper.LoadConfig","pkg.Thing"]}`,
		`{"ids":["internal/cli/guard.go"]}`,
		`{"ids":["/tmp/ws/internal/cli/guard.go"]}`,
	} {
		d := policy.Decide("read_resource", args, ModeBuild, ApprovalManual)
		if d.Action != permissionAllow {
			t.Fatalf("Decide(read_resource, %s) = %s (%s), want allow", args, d.Action, d.Reason)
		}
	}
}
