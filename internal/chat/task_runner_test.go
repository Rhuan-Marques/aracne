package chat

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"aracne/internal/llm"
)

// setupTaskManager creates a Manager with a mock LLM provider at serverURL (may be "").
func setupTaskManager(t *testing.T, serverURL string) (*Manager, *Session, *sync.Mutex, *[]Event) {
	t.Helper()
	dir := t.TempDir()
	setupTopologyDB(t, dir)
	storeDir := filepath.Join(dir, "chat")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "topology.db")

	var mu sync.Mutex
	var events []Event
	collect := func(e Event) { mu.Lock(); events = append(events, e); mu.Unlock() }

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
	session, err := manager.CreateSession("default", "task test")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return manager, session, &mu, &events
}

func sseResponse(content string) string {
	return fmt.Sprintf(`data: {"choices":[{"index":0,"delta":{"content":%q}}]}

data: [DONE]`, content)
}

func TestCreateTaskGroupFromToolCall_Valid(t *testing.T) {
	manager, session, _, _ := setupTaskManager(t, "")
	tc := llm.ToolCall{
		ID:   "call_1",
		Type: "function",
		Function: llm.ToolCallFunction{
			Name:      "CreateTasks",
			Arguments: `{"worker_count":3,"tasks":[{"agent_kind":"explorer","prompt":"Find bugs","need_result":true}]}`,
		},
	}

	groupID, err := manager.createTaskGroupFromToolCall(session.ID, tc)
	if err != nil {
		t.Fatalf("createTaskGroupFromToolCall: %v", err)
	}
	if groupID == "" {
		t.Fatal("expected non-empty group ID")
	}

	sess, err := manager.GetSession(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sess.TaskGroups) != 1 {
		t.Fatalf("expected 1 task group, got %d", len(sess.TaskGroups))
	}
	g := sess.TaskGroups[0]
	if g.ID != groupID {
		t.Errorf("group ID mismatch")
	}
	if g.ToolCallID != "call_1" {
		t.Errorf("ToolCallID = %q, want %q", g.ToolCallID, "call_1")
	}
	if g.Status != "pending" {
		t.Errorf("Status = %q, want %q", g.Status, "pending")
	}
	if g.WorkerCount != 3 {
		t.Errorf("WorkerCount = %d, want 3", g.WorkerCount)
	}
	if len(g.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(g.Tasks))
	}
	task := g.Tasks[0]
	if task.AgentKind != "explorer" {
		t.Errorf("AgentKind = %q", task.AgentKind)
	}
	if task.Prompt != "Find bugs" {
		t.Errorf("Prompt = %q", task.Prompt)
	}
	if !task.NeedResult {
		t.Error("NeedResult should be true")
	}
	if task.Status != "pending" {
		t.Errorf("Status = %q", task.Status)
	}
}

func TestCreateTaskGroupFromToolCall_Errors(t *testing.T) {
	manager, session, _, _ := setupTaskManager(t, "")

	t.Run("no tasks", func(t *testing.T) {
		tc := llm.ToolCall{
			ID: "call_e1", Type: "function",
			Function: llm.ToolCallFunction{Name: "CreateTasks", Arguments: `{"tasks":[]}`},
		}
		_, err := manager.createTaskGroupFromToolCall(session.ID, tc)
		if err == nil || !strings.Contains(err.Error(), "at least one task") {
			t.Errorf("expected 'at least one task' error, got: %v", err)
		}
	})

	t.Run("bad JSON", func(t *testing.T) {
		tc := llm.ToolCall{
			ID: "call_e2", Type: "function",
			Function: llm.ToolCallFunction{Name: "CreateTasks", Arguments: `not json`},
		}
		_, err := manager.createTaskGroupFromToolCall(session.ID, tc)
		if err == nil {
			t.Error("expected JSON error")
		}
	})

	t.Run("empty agent_kind", func(t *testing.T) {
		tc := llm.ToolCall{
			ID: "call_e3", Type: "function",
			Function: llm.ToolCallFunction{Name: "CreateTasks", Arguments: `{"tasks":[{"agent_kind":"","prompt":"hi","need_result":false}]}`},
		}
		_, err := manager.createTaskGroupFromToolCall(session.ID, tc)
		if err == nil || !strings.Contains(err.Error(), "agent_kind is required") {
			t.Errorf("expected 'agent_kind is required', got: %v", err)
		}
	})

	t.Run("empty prompt", func(t *testing.T) {
		tc := llm.ToolCall{
			ID: "call_e4", Type: "function",
			Function: llm.ToolCallFunction{Name: "CreateTasks", Arguments: `{"tasks":[{"agent_kind":"explorer","prompt":"","need_result":false}]}`},
		}
		_, err := manager.createTaskGroupFromToolCall(session.ID, tc)
		if err == nil || !strings.Contains(err.Error(), "prompt is required") {
			t.Errorf("expected 'prompt is required', got: %v", err)
		}
	})

	t.Run("unknown agent kind", func(t *testing.T) {
		tc := llm.ToolCall{
			ID: "call_e5", Type: "function",
			Function: llm.ToolCallFunction{Name: "CreateTasks", Arguments: `{"tasks":[{"agent_kind":"nonexistent","prompt":"hi","need_result":false}]}`},
		}
		_, err := manager.createTaskGroupFromToolCall(session.ID, tc)
		if err == nil || !strings.Contains(err.Error(), "unknown agent kind") {
			t.Errorf("expected 'unknown agent kind', got: %v", err)
		}
	})
}

func TestExecuteCreateTasksTool_SingleTaskCompletes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseResponse("found 3 bugs in main.go"))
	}))
	defer server.Close()

	manager, session, mu, events := setupTaskManager(t, server.URL)

	tc := llm.ToolCall{
		ID:   "call_run1",
		Type: "function",
		Function: llm.ToolCallFunction{
			Name:      "CreateTasks",
			Arguments: `{"worker_count":2,"tasks":[{"agent_kind":"explorer","prompt":"Find bugs","need_result":true}]}`,
		},
	}

	// Mimic the real flow: addToolMessage is called before executeToolCall
	manager.addToolMessage(session.ID, tc)

	err := manager.executeCreateTasksTool(session.ID, tc)
	if err != nil {
		t.Fatalf("executeCreateTasksTool: %v", err)
	}

	sess, err := manager.GetSession(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sess.TaskGroups) != 1 {
		t.Fatalf("expected 1 task group, got %d", len(sess.TaskGroups))
	}
	g := sess.TaskGroups[0]
	if g.Status != "completed" {
		t.Errorf("group status = %q, want %q", g.Status, "completed")
	}
	if len(g.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(g.Tasks))
	}
	task := g.Tasks[0]
	if task.Status != "completed" {
		t.Errorf("task status = %q, want %q", task.Status, "completed")
	}
	if task.Result != "found 3 bugs in main.go" {
		t.Errorf("task result = %q", task.Result)
	}
	if task.Error != "" {
		t.Errorf("task error = %q", task.Error)
	}

	var toolResultMsg *SessionMessage
	for i := range sess.Messages {
		if sess.Messages[i].ToolCallID == "call_run1" {
			toolResultMsg = &sess.Messages[i]
			break
		}
	}
	if toolResultMsg == nil {
		t.Fatal("expected tool result message in session")
	}
	if !strings.Contains(toolResultMsg.ToolOutput, "tasks_successful: 1") {
		t.Errorf("tool output missing success count:\n%s", toolResultMsg.ToolOutput)
	}
	if !strings.Contains(toolResultMsg.ToolOutput, "found 3 bugs in main.go") {
		t.Errorf("tool output missing task result:\n%s", toolResultMsg.ToolOutput)
	}

	mu.Lock()
	var foundGroupStatus, foundTaskStatus bool
	for _, e := range *events {
		if e.Type == "task_group_status" {
			foundGroupStatus = true
		}
		if e.Type == "task_status" {
			foundTaskStatus = true
		}
	}
	mu.Unlock()
	if !foundGroupStatus {
		t.Error("expected task_group_status event")
	}
	if !foundTaskStatus {
		t.Error("expected task_status event")
	}
}

func TestExecuteCreateTasksTool_MultipleTasks(t *testing.T) {
	var mu sync.Mutex
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		callCount++
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseResponse("done"))
	}))
	defer server.Close()

	manager, session, _, _ := setupTaskManager(t, server.URL)

	tc := llm.ToolCall{
		ID:   "call_run2",
		Type: "function",
		Function: llm.ToolCallFunction{
			Name:      "CreateTasks",
			Arguments: `{"worker_count":4,"tasks":[{"agent_kind":"explorer","prompt":"Find bugs 1","need_result":false},{"agent_kind":"explorer","prompt":"Find bugs 2","need_result":false},{"agent_kind":"explorer","prompt":"Find bugs 3","need_result":false}]}`,
		},
	}

	if err := manager.executeCreateTasksTool(session.ID, tc); err != nil {
		t.Fatalf("executeCreateTasksTool: %v", err)
	}

	if callCount != 3 {
		t.Errorf("expected 3 LLM calls (one per task), got %d", callCount)
	}

	sess, err := manager.GetSession(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	g := sess.TaskGroups[0]
	if g.Status != "completed" {
		t.Errorf("group status = %q", g.Status)
	}
	if len(g.Tasks) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(g.Tasks))
	}
	for i, task := range g.Tasks {
		if task.Status != "completed" {
			t.Errorf("task[%d] status = %q", i, task.Status)
		}
	}
}

func TestExecuteCreateTasksTool_TaskFails_EmptyResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseResponse(""))
	}))
	defer server.Close()

	manager, session, _, _ := setupTaskManager(t, server.URL)

	tc := llm.ToolCall{
		ID:   "call_e6",
		Type: "function",
		Function: llm.ToolCallFunction{
			Name:      "CreateTasks",
			Arguments: `{"tasks":[{"agent_kind":"explorer","prompt":"Do something","need_result":false}]}`,
		},
	}

	if err := manager.executeCreateTasksTool(session.ID, tc); err != nil {
		t.Fatalf("executeCreateTasksTool: %v", err)
	}

	sess, err := manager.GetSession(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	g := sess.TaskGroups[0]
	if g.Status != "completed" {
		t.Errorf("group status = %q, want %q", g.Status, "completed")
	}
	if len(g.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(g.Tasks))
	}
	task := g.Tasks[0]
	if task.Status != "failed" {
		t.Errorf("task status = %q, want %q", task.Status, "failed")
	}
	if !strings.Contains(task.Error, "empty final answer") {
		t.Errorf("task error = %q, should contain 'empty final answer'", task.Error)
	}
}

func TestExecuteCreateTasksTool_TaskFails_ProviderError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":"test error"}`)
	}))
	defer server.Close()

	manager, session, _, _ := setupTaskManager(t, server.URL)

	tc := llm.ToolCall{
		ID:   "call_e7",
		Type: "function",
		Function: llm.ToolCallFunction{
			Name:      "CreateTasks",
			Arguments: `{"tasks":[{"agent_kind":"explorer","prompt":"Do something","need_result":false}]}`,
		},
	}

	if err := manager.executeCreateTasksTool(session.ID, tc); err != nil {
		t.Fatalf("executeCreateTasksTool: %v", err)
	}

	sess, err := manager.GetSession(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	g := sess.TaskGroups[0]
	task := g.Tasks[0]
	if task.Status != "failed" {
		t.Errorf("task status = %q, want %q", task.Status, "failed")
	}
	if !strings.Contains(task.Error, "LLM call failed") {
		t.Errorf("task error = %q, should contain 'LLM call failed'", task.Error)
	}
}

func TestExecuteCreateTasksTool_ClampsWorkerCount(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseResponse("done"))
	}))
	defer server.Close()

	manager, _, _, _ := setupTaskManager(t, server.URL)

	t.Run("zero worker count defaults to 2", func(t *testing.T) {
		s, err := manager.CreateSession("default", "clamp test")
		if err != nil {
			t.Fatal(err)
		}
		tc := llm.ToolCall{
			ID: "call_clamp1", Type: "function",
			Function: llm.ToolCallFunction{Name: "CreateTasks", Arguments: `{"worker_count":0,"tasks":[{"agent_kind":"explorer","prompt":"t","need_result":false},{"agent_kind":"explorer","prompt":"t","need_result":false}]}`},
		}
		if err := manager.executeCreateTasksTool(s.ID, tc); err != nil {
			t.Fatal(err)
		}
		sess2, _ := manager.GetSession(s.ID)
		if sess2.TaskGroups[0].WorkerCount != 2 {
			t.Errorf("WorkerCount = %d, want 2", sess2.TaskGroups[0].WorkerCount)
		}
	})

	t.Run("worker count above 8 clamped to 8", func(t *testing.T) {
		s3, err := manager.CreateSession("default", "clamp test 2")
		if err != nil {
			t.Fatal(err)
		}
		tc := llm.ToolCall{
			ID: "call_clamp2", Type: "function",
			Function: llm.ToolCallFunction{Name: "CreateTasks", Arguments: `{"worker_count":20,"tasks":[{"agent_kind":"explorer","prompt":"t","need_result":false},{"agent_kind":"explorer","prompt":"t","need_result":false}]}`},
		}
		if err := manager.executeCreateTasksTool(s3.ID, tc); err != nil {
			t.Fatal(err)
		}
		sess4, _ := manager.GetSession(s3.ID)
		if sess4.TaskGroups[0].WorkerCount != 8 {
			t.Errorf("WorkerCount = %d, want 8", sess4.TaskGroups[0].WorkerCount)
		}
	})
}
