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

func TestChatAgentProviderOverride_BugJudgeThinking(t *testing.T) {
	manager := setupWorkflowManager(t, "", false)
	base := ProviderSettings{Provider: ProviderAnthropic, Model: "claude-sonnet-4"}

	judge := manager.chatAgentProviderOverride("bug-judge", base)
	if judge.ThinkingBudget != helper.DefaultBugJudgeThinkingBudget {
		t.Fatalf("bug-judge thinking budget = %d, want %d", judge.ThinkingBudget, helper.DefaultBugJudgeThinkingBudget)
	}
	if explorer := manager.chatAgentProviderOverride("explorer", base); explorer.ThinkingBudget != 0 {
		t.Fatalf("explorer thinking budget = %d, want 0", explorer.ThinkingBudget)
	}
}

func TestChatAgentProviderOverride_BugHunterThinking(t *testing.T) {
	manager := setupWorkflowManager(t, "", false)
	base := ProviderSettings{Provider: ProviderAnthropic, Model: "claude-sonnet-4"}

	hunter := manager.chatAgentProviderOverride("bug-hunter", base)
	if hunter.ThinkingBudget != helper.DefaultBugHunterThinkingBudget {
		t.Fatalf("bug-hunter thinking budget = %d, want %d", hunter.ThinkingBudget, helper.DefaultBugHunterThinkingBudget)
	}
}

func TestChatAgentProviderOverride_BugSolverThinking(t *testing.T) {
	manager := setupWorkflowManager(t, "", false)
	base := ProviderSettings{Provider: ProviderAnthropic, Model: "claude-sonnet-4"}

	solver := manager.chatAgentProviderOverride("bug-solver", base)
	if solver.ThinkingBudget != helper.DefaultBugSolverThinkingBudget {
		t.Fatalf("bug-solver thinking budget = %d, want %d", solver.ThinkingBudget, helper.DefaultBugSolverThinkingBudget)
	}
}

func TestOpenAIReasoningHelpers(t *testing.T) {
	if isOpenAIReasoningModel("gpt-4.1") {
		t.Fatal("gpt-4.1 is not a reasoning model")
	}
	for _, m := range []string{"o1-preview", "o3", "o4-mini", "gpt-5"} {
		if !isOpenAIReasoningModel(m) {
			t.Fatalf("%s should be a reasoning model", m)
		}
	}
	if got := openAIReasoningEffort(0); got != "" {
		t.Fatalf("budget 0 -> %q, want empty", got)
	}
	if got := openAIReasoningEffort(4096); got != "medium" {
		t.Fatalf("budget 4096 -> %q, want medium", got)
	}
	if got := openAIReasoningEffort(8000); got != "high" {
		t.Fatalf("budget 8000 -> %q, want high", got)
	}
}

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
		if manager.config.Descriptions.Kinds == nil {
			manager.config.Descriptions.Kinds = []domain.ResourceKind{domain.ResourceFunction, domain.ResourceStruct}
		}
		manager.mu.Unlock()
	}
	return manager
}

func setupWorkflowTopologyDB(t *testing.T, dir string) {
	t.Helper()
	files := map[string]string{
		"main.go": "package main\n\nfunc main() {}\n",
		"foo.go":  "package main\n\nfunc foo() int {\n\treturn 1\n}\n",
		"bar.go":  "package main\n\nfunc bar() int {\n\treturn 2\n}\n",
		"baz.go":  "package main\n\ntype Baz struct {\n\tValue int\n}\n",
	}
	paths := make(map[string]string, len(files))
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
		paths[name] = path
	}

	topo := &domain.Topology{
		Root:     dir,
		Language: "go",
		Resources: map[string]domain.Resource{
			"func_main": {ID: "func_main", Name: "main", Kind: domain.ResourceFunction, Location: domain.Location{Path: paths["main.go"], StartsAt: 3, EndsAt: 3}, Description: "the main function"},
			"func_foo":  {ID: "func_foo", Name: "foo", Kind: domain.ResourceFunction, Location: domain.Location{Path: paths["foo.go"], StartsAt: 3, EndsAt: 5}, Description: ""},
			"func_bar":  {ID: "func_bar", Name: "bar", Kind: domain.ResourceFunction, Location: domain.Location{Path: paths["bar.go"], StartsAt: 3, EndsAt: 5}, Description: ""},
			"type_baz":  {ID: "type_baz", Name: "Baz", Kind: domain.ResourceStruct, Location: domain.Location{Path: paths["baz.go"], StartsAt: 3, EndsAt: 5}, Description: ""},
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

func TestBuildWorkflowTaskSpecs_DescriptionPrompts(t *testing.T) {
	manager := setupWorkflowManager(t, "", true)

	single, err := manager.buildWorkflowTaskSpecs(WorkflowRequest{Type: "descriptions", BatchSize: 1})
	if err != nil {
		t.Fatalf("buildWorkflowTaskSpecs single: %v", err)
	}
	if len(single) != 3 {
		t.Fatalf("single task count = %d, want 3", len(single))
	}
	if !strings.Contains(single[0].Prompt, "Source (already read for you)") {
		t.Fatalf("single-resource prompt should include pre-read content:\n%s", single[0].Prompt)
	}
	if strings.Contains(single[0].Prompt, "## Per-kind limits") {
		t.Fatalf("single-resource prompt should not use the multi per-kind list:\n%s", single[0].Prompt)
	}

	multi, err := manager.buildWorkflowTaskSpecs(WorkflowRequest{Type: "descriptions", BatchSize: 2})
	if err != nil {
		t.Fatalf("buildWorkflowTaskSpecs multi: %v", err)
	}
	if len(multi) != 2 {
		t.Fatalf("multi task count = %d, want 2", len(multi))
	}
	if !strings.Contains(multi[0].Prompt, "Assigned resources") || !strings.Contains(multi[0].Prompt, "## Per-kind limits") {
		t.Fatalf("multi-resource prompt missing assigned list or per-kind limits:\n%s", multi[0].Prompt)
	}
	if !strings.Contains(multi[0].Prompt, "Source (already read)") {
		t.Fatalf("multi-resource prompt should pre-read assigned resources:\n%s", multi[0].Prompt)
	}
}

func TestBuildWorkflowTaskSpecs_BugWorkflows(t *testing.T) {
	manager := setupWorkflowManager(t, "", true)
	pendingOne, err := manager.manager.CreateBug("func_foo", "first pending bug")
	if err != nil {
		t.Fatalf("CreateBug pendingOne: %v", err)
	}
	pendingTwo, err := manager.manager.CreateBug("func_foo", "duplicate candidate")
	if err != nil {
		t.Fatalf("CreateBug pendingTwo: %v", err)
	}
	pendingOther, err := manager.manager.CreateBug("type_baz", "other node bug")
	if err != nil {
		t.Fatalf("CreateBug pendingOther: %v", err)
	}
	ack, err := manager.manager.CreateBug("func_bar", "acknowledged bug")
	if err != nil {
		t.Fatalf("CreateBug ack: %v", err)
	}
	if err := manager.manager.AcknowledgeBug(ack.ID); err != nil {
		t.Fatalf("AcknowledgeBug: %v", err)
	}

	hunter, err := manager.buildWorkflowTaskSpecs(WorkflowRequest{Type: "bug_hunter", BatchSize: 1})
	if err != nil {
		t.Fatalf("build hunter: %v", err)
	}
	if len(hunter) != 4 {
		t.Fatalf("hunter task count = %d, want 4 (one per inspectable resource)", len(hunter))
	}
	var fooHunter string
	for _, task := range hunter {
		if task.AgentKind != "bug-hunter" {
			t.Fatalf("hunter task kind = %q, want bug-hunter", task.AgentKind)
		}
		if strings.Contains(task.Prompt, "func_foo") {
			fooHunter = task.Prompt
		}
	}
	if fooHunter == "" {
		t.Fatal("missing hunter task for func_foo")
	}
	if !strings.Contains(fooHunter, "first pending bug") {
		t.Fatalf("func_foo hunter prompt should list already-reported bugs:\n%s", fooHunter)
	}

	judge, err := manager.buildWorkflowTaskSpecs(WorkflowRequest{Type: "bug_judge", BatchSize: 1})
	if err != nil {
		t.Fatalf("build judge: %v", err)
	}
	if len(judge) != 3 {
		t.Fatalf("judge task count = %d, want 3", len(judge))
	}
	var firstPrompt string
	for _, task := range judge {
		if strings.Contains(task.Prompt, pendingOne.ID) {
			firstPrompt = task.Prompt
		}
	}
	if firstPrompt == "" {
		t.Fatal("missing judge task for first pending bug")
	}
	if !strings.Contains(firstPrompt, pendingTwo.ID) {
		t.Fatalf("judge prompt should include same-node bug for duplicate checking:\n%s", firstPrompt)
	}
	if strings.Contains(firstPrompt, pendingOther.ID) {
		t.Fatalf("judge prompt should not include other-node bugs:\n%s", firstPrompt)
	}

	solver, err := manager.buildWorkflowTaskSpecs(WorkflowRequest{Type: "bug_solver", BatchSize: 1})
	if err != nil {
		t.Fatalf("build solver: %v", err)
	}
	if len(solver) != 1 || solver[0].AgentKind != "bug-solver" || !strings.Contains(solver[0].Prompt, ack.ID) {
		t.Fatalf("solver tasks = %+v, want one assigned ack bug", solver)
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
