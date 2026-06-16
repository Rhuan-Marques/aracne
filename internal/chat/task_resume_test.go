package chat

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aracne/internal/llm"
)

func setupResumeTest(t *testing.T, serverURL string) (*Manager, *Session, func()) {
	t.Helper()
	dir := t.TempDir()
	setupTopologyDB(t, dir)
	storeDir := filepath.Join(dir, "chat")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "topology.db")

	collect := func(e Event) {}
	manager, err := NewManager(dbPath, dir, collect)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if serverURL != "" {
		manager.SetProvider(ProviderSettings{
			Provider: ProviderOpenAI,
			Model:    "gpt-4o",
			BaseURL:  serverURL,
			APIKey:   "test-key",
		})
	}
	session, err := manager.CreateSession("default", "resume test")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return manager, session, func() {}
}

// injectCompletedGroup adds a completed task group directly into a session for resume testing.
func injectCompletedGroup(t *testing.T, manager *Manager, sessionID, toolCallID string) string {
	t.Helper()
	groupID := "tg_resume_" + toolCallID
	group := TaskGroup{
		ID:         groupID,
		ToolCallID: toolCallID,
		Status:     "completed",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
		Tasks: []AgentTask{
			{
				ID:         "task_1",
				AgentKind:  "explorer",
				Prompt:     "find bugs",
				Status:     "completed",
				Result:     "found 2 bugs",
				NeedResult: true,
			},
		},
	}
	manager.mu.Lock()
	session, err := manager.getSessionLocked(sessionID)
	if err == nil {
		session.TaskGroups = append(session.TaskGroups, group)
		_ = manager.saveLocked(session)
	}
	manager.mu.Unlock()
	if err != nil {
		t.Fatalf("getSessionLocked: %v", err)
	}
	return groupID
}

func TestResumeTaskGroup_CompletedNotDelivered(t *testing.T) {
	manager, session, cleanup := setupResumeTest(t, "")
	defer cleanup()

	toolCallID := "call_resume_deliver"
	groupID := injectCompletedGroup(t, manager, session.ID, toolCallID)

	sess, err := manager.ResumeTaskGroup(session.ID, groupID)
	if err != nil {
		t.Fatalf("ResumeTaskGroup: %v", err)
	}
	_ = sess

	// Verify the tool result was injected into the LLM messages
	manager.mu.Lock()
	freshSess, _ := manager.getSessionLocked(session.ID)
	var found bool
	for _, msg := range freshSess.LLMMessages {
		if msg.Role == "tool" && msg.ToolCallID == toolCallID && strings.Contains(msg.Content, "tasks_successful: 1") {
			found = true
			break
		}
	}
	manager.mu.Unlock()
	if !found {
		t.Error("expected tool result in LLM messages for completed-but-not-delivered group")
	}
}

func TestResumeTaskGroup_CompletedAlreadyDelivered(t *testing.T) {
	manager, session, cleanup := setupResumeTest(t, "")
	defer cleanup()

	toolCallID := "call_resume_already"
	groupID := injectCompletedGroup(t, manager, session.ID, toolCallID)

	// Simulate that the tool result was already delivered
	manager.mu.Lock()
	sess, _ := manager.getSessionLocked(session.ID)
	sess.Messages = append(sess.Messages, SessionMessage{
		ToolCallID: toolCallID,
		Status:     "completed",
		ToolOutput: "results already here",
		CreatedAt:  time.Now(),
	})
	_ = manager.saveLocked(sess)
	manager.mu.Unlock()

	_, err := manager.ResumeTaskGroup(session.ID, groupID)
	if err == nil || !strings.Contains(err.Error(), "already completed") {
		t.Errorf("expected 'already completed' error, got: %v", err)
	}
}

func TestResumeTaskGroup_NotCompleted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `data: {"choices":[{"index":0,"delta":{"content":"resume result"}}]}

data: [DONE]`)
	}))
	defer server.Close()

	manager, session, cleanup := setupResumeTest(t, server.URL)
	defer cleanup()

	// First, create a pending task group via the normal path
	tc := llm.ToolCall{
		ID:   "call_resume_pending",
		Type: "function",
		Function: llm.ToolCallFunction{
			Name:      "CreateTasks",
			Arguments: `{"tasks":[{"agent_kind":"explorer","prompt":"resume me","need_result":false}]}`,
		},
	}
	groupID, err := manager.createTaskGroupFromToolCall(session.ID, tc)
	if err != nil {
		t.Fatalf("createTaskGroupFromToolCall: %v", err)
	}

	// Now resume it
	sess, err := manager.ResumeTaskGroup(session.ID, groupID)
	if err != nil {
		t.Fatalf("ResumeTaskGroup: %v", err)
	}
	_ = sess

	// Wait for the goroutine to finish
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		manager.mu.Lock()
		freshSess, _ := manager.getSessionLocked(session.ID)
		var g *TaskGroup
		for i := range freshSess.TaskGroups {
			if freshSess.TaskGroups[i].ID == groupID {
				g = &freshSess.TaskGroups[i]
				break
			}
		}
		done := g != nil && g.Status == "completed"
		manager.mu.Unlock()
		if done {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	manager.mu.Lock()
	freshSess, _ := manager.getSessionLocked(session.ID)
	group, _, _ := findTaskGroup(freshSess, groupID)
	manager.mu.Unlock()
	if group == nil {
		t.Fatal("task group not found")
	}
	if group.Status != "completed" {
		t.Errorf("group status = %q, want %q", group.Status, "completed")
	}
	if len(group.Tasks) != 1 || group.Tasks[0].Status != "completed" {
		t.Errorf("task status = %q", group.Tasks[0].Status)
	}
}

func TestResumeTaskGroup_AlreadyRunning(t *testing.T) {
	manager, session, cleanup := setupResumeTest(t, "")
	defer cleanup()

	groupID := injectCompletedGroup(t, manager, session.ID, "call_running")
	// Mark it as running in the runningTaskGroups map
	key := taskGroupRunKey(session.ID, groupID)
	manager.mu.Lock()
	manager.runningTaskGroups[key] = true
	manager.mu.Unlock()

	_, err := manager.ResumeTaskGroup(session.ID, groupID)
	if err == nil || !strings.Contains(err.Error(), "already processing") {
		t.Errorf("expected 'already processing' error, got: %v", err)
	}
}

func TestResumeTaskGroup_NoToolCallID(t *testing.T) {
	manager, session, cleanup := setupResumeTest(t, "")
	defer cleanup()

	group := TaskGroup{
		ID:        "tg_no_tcid",
		Status:    "pending",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Tasks:     []AgentTask{{ID: "t1", Status: "pending"}},
	}
	manager.mu.Lock()
	sess, _ := manager.getSessionLocked(session.ID)
	sess.TaskGroups = append(sess.TaskGroups, group)
	_ = manager.saveLocked(sess)
	manager.mu.Unlock()

	_, err := manager.ResumeTaskGroup(session.ID, "tg_no_tcid")
	if err == nil || !strings.Contains(err.Error(), "no tool call to resume") {
		t.Errorf("expected 'no tool call to resume' error, got: %v", err)
	}
}
