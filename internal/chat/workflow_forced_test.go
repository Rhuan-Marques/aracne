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

	"aracne/internal/helper"
	"aracne/internal/topology/domain"
)

func setupWorkflowManager(t *testing.T, serverURL string, populateTopo bool) *Manager {
	t.Helper()
	dir := t.TempDir()
	if populateTopo {
		setupWorkflowTopologyDB(t, dir)
	} else {
		setupTopologyDB(t, dir)
	}
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
	// Ensure NeedDescription includes function and type so undocumentedResources picks them up
	if populateTopo {
		manager.mu.Lock()
		if manager.config.NeedDescription == nil {
			manager.config.NeedDescription = []domain.ResourceKind{domain.ResourceFunction, domain.ResourceType}
		}
		manager.mu.Unlock()
	}
	return manager
}

func setupWorkflowTopologyDB(t *testing.T, dir string) {
	t.Helper()
	topo := &domain.Topology{
		Root:     dir,
		Language: "go",
		Resources: map[string]domain.Resource{
			"func_main": {ID: "func_main", Name: "main", Kind: domain.ResourceFunction, Location: domain.Location{Path: "main.go"}, Description: "the main function"},
			"func_foo":  {ID: "func_foo", Name: "foo", Kind: domain.ResourceFunction, Location: domain.Location{Path: "foo.go"}, Description: ""},
			"func_bar":  {ID: "func_bar", Name: "bar", Kind: domain.ResourceFunction, Location: domain.Location{Path: "bar.go"}, Description: ""},
			"type_baz":  {ID: "type_baz", Name: "Baz", Kind: domain.ResourceType, Location: domain.Location{Path: "baz.go"}, Description: ""},
		},
		Warnings: make(map[string]domain.TopologyWarning),
		Errors:   make(map[string]string),
	}
	dbPath := filepath.Join(dir, "topology.db")
	if err := helper.WriteDb(topo, dbPath); err != nil {
		t.Fatalf("WriteDb: %v", err)
	}
}

func TestStartForcedWorkflow_Descriptions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"content":"done"}}]}

data: [DONE]`)
	}))
	defer server.Close()

	manager := setupWorkflowManager(t, server.URL, true)
	session, err := manager.CreateSession("default", "workflow test")
	if err != nil {
		t.Fatal(err)
	}

	req := WorkflowRequest{
		SessionID: session.ID,
		Type:      "descriptions",
		BatchSize: 2,
		Parallel:  2,
	}

	groupID, err := manager.StartForcedWorkflow(req)
	if err != nil {
		t.Fatalf("StartForcedWorkflow: %v", err)
	}
	if groupID == "" {
		t.Fatal("expected non-empty group ID")
	}

	// Verify the task group was created
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
	if g.Status != "pending" && g.Status != "running" && g.Status != "completed" {
		t.Errorf("unexpected status: %q", g.Status)
	}

	// Verify synthetic messages were injected
	foundUser := false
	foundTool := false
	for _, msg := range sess.Messages {
		if msg.Role == "user" && strings.Contains(msg.Content, "Start descriptions task workflow") {
			foundUser = true
		}
		if msg.ToolName == "CreateTasks" {
			foundTool = true
		}
	}
	if !foundUser {
		t.Error("expected synthetic user message about starting workflow")
	}
	if !foundTool {
		t.Error("expected synthetic CreateTasks tool message")
	}

	// Wait for the async goroutine to finish so temp dir cleanup doesn't race
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		g, _ := manager.GetSession(session.ID)
		if len(g.TaskGroups) > 0 && g.TaskGroups[0].Status == "completed" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestStartForcedWorkflow_NoTasks(t *testing.T) {
	manager := setupWorkflowManager(t, "", false)
	session, err := manager.CreateSession("default", "wf empty")
	if err != nil {
		t.Fatal(err)
	}

	req := WorkflowRequest{
		SessionID: session.ID,
		Type:      "descriptions",
	}

	_, err = manager.StartForcedWorkflow(req)
	if err == nil || !strings.Contains(err.Error(), "no tasks to run") {
		t.Errorf("expected 'no tasks to run' error, got: %v", err)
	}
}

func TestStartForcedWorkflow_InvalidType(t *testing.T) {
	manager := setupWorkflowManager(t, "", false)
	session, err := manager.CreateSession("default", "wf invalid")
	if err != nil {
		t.Fatal(err)
	}

	req := WorkflowRequest{
		SessionID: session.ID,
		Type:      "nonexistent",
	}

	_, err = manager.StartForcedWorkflow(req)
	if err == nil || !strings.Contains(err.Error(), "unknown workflow") {
		t.Errorf("expected 'unknown workflow' error, got: %v", err)
	}
}

func TestStartForcedWorkflow_MissingSession(t *testing.T) {
	manager := setupWorkflowManager(t, "", false)

	req := WorkflowRequest{
		SessionID: "",
		Type:      "descriptions",
	}

	_, err := manager.StartForcedWorkflow(req)
	if err == nil || !strings.Contains(err.Error(), "session_id is required") {
		t.Errorf("expected 'session_id is required' error, got: %v", err)
	}
}

func TestStartForcedWorkflow_MissingType(t *testing.T) {
	manager := setupWorkflowManager(t, "", false)
	session, err := manager.CreateSession("default", "wf missing type")
	if err != nil {
		t.Fatal(err)
	}

	req := WorkflowRequest{
		SessionID: session.ID,
		Type:      "",
	}

	_, err = manager.StartForcedWorkflow(req)
	if err == nil || !strings.Contains(err.Error(), "workflow type is required") {
		t.Errorf("expected 'workflow type is required' error, got: %v", err)
	}
}
