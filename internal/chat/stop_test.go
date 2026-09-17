package chat

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/llm"
	"github.com/Rhuan-Marques/aracne/internal/llm/toolapi"
)

type blockingTool struct {
	started chan struct{}
	release chan struct{}
}

func (t *blockingTool) Name() string                    { return "blocking_tool" }
func (t *blockingTool) Description() string             { return "blocks until released" }
func (t *blockingTool) Parameters() []toolapi.Parameter { return nil }
func (t *blockingTool) Run(args json.RawMessage) (string, error) {
	close(t.started)
	<-t.release
	return "real tool result", nil
}

func TestStopSession_InterruptsRunningTool(t *testing.T) {
	manager, session, _, _ := setupTaskManager(t, "")
	tool := &blockingTool{started: make(chan struct{}), release: make(chan struct{})}
	manager.registry.Register(tool)
	tc := llm.ToolCall{ID: "call_blocking", Type: "function", Function: llm.ToolCallFunction{Name: tool.Name(), Arguments: `{}`}}
	manager.addToolMessage(session.ID, tc)

	done := make(chan error, 1)
	go func() { done <- manager.executeToolCall(session.ID, tc) }()
	select {
	case <-tool.started:
	case <-time.After(2 * time.Second):
		t.Fatal("tool did not start")
	}

	if _, err := manager.StopSession(session.ID); err != nil {
		t.Fatalf("StopSession: %v", err)
	}
	close(tool.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("executeToolCall: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("tool did not return after release")
	}

	sess, err := manager.GetSession(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	var found *SessionMessage
	for i := range sess.Messages {
		if sess.Messages[i].ToolCallID == tc.ID {
			found = &sess.Messages[i]
		}
	}
	if found == nil {
		t.Fatal("tool message not found")
	}
	if found.Status != "interrupted" {
		t.Fatalf("tool status = %q, want interrupted", found.Status)
	}
	if strings.TrimSpace(found.ToolOutput) != "Tool interrupted." {
		t.Fatalf("tool output = %q", found.ToolOutput)
	}
	if strings.Contains(found.ToolOutput, "real tool result") {
		t.Fatal("late tool result overwrote interrupted output")
	}
}

func TestStopSession_LeavesTaskGroupResumable(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-started:
		default:
			close(started)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	defer server.Close()

	manager, session, _, _ := setupTaskManager(t, server.URL)
	tc := llm.ToolCall{
		ID:   "call_tasks_stop",
		Type: "function",
		Function: llm.ToolCallFunction{
			Name:      "CreateTasks",
			Arguments: `{"worker_count":1,"tasks":[{"agent_kind":"explorer","prompt":"wait","need_result":false}]}`,
		},
	}
	manager.addToolMessage(session.ID, tc)

	done := make(chan error, 1)
	go func() { done <- manager.executeCreateTasksTool(session.ID, tc) }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("task LLM call did not start")
	}
	if _, err := manager.StopSession(session.ID); err != nil {
		t.Fatalf("StopSession: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("executeCreateTasksTool: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("task group did not stop")
	}

	sess, err := manager.GetSession(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sess.TaskGroups) != 1 {
		t.Fatalf("expected 1 task group, got %d", len(sess.TaskGroups))
	}
	group := sess.TaskGroups[0]
	if group.Status == taskStatusCompleted {
		t.Fatal("stopped task group should not be marked completed")
	}
	if group.Processing {
		t.Fatal("stopped task group should not be processing")
	}
	if taskGroupToolResultPresent(sess, tc.ID) {
		t.Fatal("CreateTasks tool result should not be delivered after stop")
	}
	if _, _, err := findTaskGroup(sess, group.ID); err != nil {
		t.Fatalf("task group should remain resumable: %v", err)
	}
}
