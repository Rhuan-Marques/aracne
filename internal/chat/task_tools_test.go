package chat

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"aracne/internal/llm"
	"aracne/internal/llm/tools"
)

type testTool struct {
	name string
}

func (t *testTool) Name() string                          { return t.name }
func (t *testTool) Description() string                   { return "test tool " + t.name }
func (t *testTool) Parameters() []tools.Parameter         { return nil }
func (t *testTool) Run(_ json.RawMessage) (string, error) { return "result:" + t.name, nil }

type errTool struct {
	name string
}

func (t *errTool) Name() string                          { return t.name }
func (t *errTool) Description() string                   { return "error tool" }
func (t *errTool) Parameters() []tools.Parameter         { return nil }
func (t *errTool) Run(_ json.RawMessage) (string, error) { return "", errRun }

var errRun = &testToolError{"tool failed"}

type testToolError struct{ msg string }

func (e *testToolError) Error() string { return e.msg }

func TestClampWorkerCount(t *testing.T) {
	cases := []struct {
		input, want int
	}{
		{-1, 2}, {0, 2}, {1, 1}, {2, 2}, {3, 3}, {5, 5}, {8, 8}, {9, 8}, {100, 8},
	}
	for _, c := range cases {
		if got := clampWorkerCount(c.input); got != c.want {
			t.Errorf("clampWorkerCount(%d) = %d, want %d", c.input, got, c.want)
		}
	}
}

func TestTaskGroupCounts(t *testing.T) {
	now := time.Now()
	_ = now
	cases := []struct {
		name         string
		group        TaskGroup
		wantC, wantF int
	}{
		{"mixed", TaskGroup{Tasks: []AgentTask{{Status: "completed"}, {Status: "failed"}, {Status: "pending"}}}, 1, 1},
		{"all completed", TaskGroup{Tasks: []AgentTask{{Status: "completed"}, {Status: "completed"}}}, 2, 0},
		{"all failed", TaskGroup{Tasks: []AgentTask{{Status: "failed"}, {Status: "failed"}}}, 0, 2},
		{"none finished", TaskGroup{Tasks: []AgentTask{{Status: "pending"}, {Status: "running"}}}, 0, 0},
		{"empty", TaskGroup{}, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotC, gotF := taskGroupCounts(c.group)
			if gotC != c.wantC || gotF != c.wantF {
				t.Errorf("got (%d,%d), want (%d,%d)", gotC, gotF, c.wantC, c.wantF)
			}
		})
	}
}

func TestFormatTaskGroupResult(t *testing.T) {
	cases := []struct {
		name       string
		group      TaskGroup
		check      []string
		wantStatus string
	}{
		{
			"success with need_result",
			TaskGroup{ID: "tg_1", Tasks: []AgentTask{
				{ID: "t_1", AgentKind: "bug-hunter", NeedResult: true, Status: "completed", Result: "found bug"},
			}},
			[]string{"task_group_id: tg_1", "tasks_successful: 1", "tasks_failed: 0", "results:", "t_1", "bug-hunter", "found bug"},
			"completed",
		},
		{
			"partial failure",
			TaskGroup{Tasks: []AgentTask{
				{ID: "t_1", NeedResult: true, Status: "completed", Result: "ok"},
				{ID: "t_2", Status: "failed", Error: "LLM error"},
			}},
			[]string{"tasks_successful: 1", "tasks_failed: 1", "failures:", "t_2", "LLM error"},
			"completed",
		},
		{
			"all failed",
			TaskGroup{Tasks: []AgentTask{
				{ID: "t_1", Status: "failed", Error: "err1"},
			}},
			[]string{"tasks_successful: 0", "tasks_failed: 1", "failures:", "err1"},
			"error",
		},
		{
			"no need_result",
			TaskGroup{Tasks: []AgentTask{
				{ID: "t_1", NeedResult: false, Status: "completed", Result: "should not appear"},
			}},
			[]string{"tasks_successful: 1", "tasks_failed: 0"},
			"completed",
		},
		{
			"empty tasks",
			TaskGroup{Tasks: []AgentTask{}},
			[]string{"tasks_successful: 0", "tasks_failed: 0"},
			"completed",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			result, status := formatTaskGroupResult(c.group)
			if status != c.wantStatus {
				t.Errorf("status = %q, want %q", status, c.wantStatus)
			}
			for _, substr := range c.check {
				if !strings.Contains(result, substr) {
					t.Errorf("result missing %q:\n%s", substr, result)
				}
			}
		})
	}
}

func TestPendingTaskIDs(t *testing.T) {
	cases := []struct {
		name  string
		group TaskGroup
		want  []string
	}{
		{"mixed", TaskGroup{Tasks: []AgentTask{
			{ID: "t1", Status: "pending"},
			{ID: "t2", Status: "running"},
			{ID: "t3", Status: "completed"},
			{ID: "t4", Status: "failed"},
		}}, []string{"t1", "t2"}},
		{"none", TaskGroup{Tasks: []AgentTask{{ID: "t1", Status: "completed"}}}, nil},
		{"all pending", TaskGroup{Tasks: []AgentTask{{ID: "t1", Status: "pending"}, {ID: "t2", Status: "pending"}}}, []string{"t1", "t2"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := pendingTaskIDs(c.group)
			if len(got) != len(c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("ids[%d] = %q, want %q", i, got[i], c.want[i])
				}
			}
		})
	}
}

func TestTaskGroupRunKey(t *testing.T) {
	if got := taskGroupRunKey("sid", "gid"); got != "sid:gid" {
		t.Errorf("got %q, want %q", got, "sid:gid")
	}
}

func TestFindTaskGroup(t *testing.T) {
	group := TaskGroup{ID: "tg_1"}
	session := &Session{TaskGroups: []TaskGroup{group}}

	found, idx, err := findTaskGroup(session, "tg_1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found.ID != "tg_1" || idx != 0 {
		t.Errorf("found.ID = %q, idx = %d", found.ID, idx)
	}

	_, _, err = findTaskGroup(session, "missing")
	if err == nil {
		t.Error("expected error for missing group")
	}
}

func TestTaskGroupToolResultPresent(t *testing.T) {
	cases := []struct {
		name       string
		session    *Session
		toolCallID string
		want       bool
	}{
		{"present completed", &Session{Messages: []SessionMessage{{ToolCallID: "c1", Status: "completed", ToolOutput: "results"}}}, "c1", true},
		{"pending status", &Session{Messages: []SessionMessage{{ToolCallID: "c1", Status: "pending", ToolOutput: ""}}}, "c1", false},
		{"not found", &Session{}, "c1", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := taskGroupToolResultPresent(c.session, c.toolCallID); got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestSortedTools(t *testing.T) {
	toolMap := map[string]tools.Tool{
		"z_tool": &testTool{"z_tool"},
		"a_tool": &testTool{"a_tool"},
		"m_tool": &testTool{"m_tool"},
	}
	sorted := sortedTools(toolMap)
	if len(sorted) != 3 {
		t.Fatalf("got %d tools, want 3", len(sorted))
	}
	names := make([]string, len(sorted))
	for i, tool := range sorted {
		names[i] = tool.Name()
	}
	expected := []string{"a_tool", "m_tool", "z_tool"}
	for i := range names {
		if names[i] != expected[i] {
			t.Errorf("sorted[%d] = %q, want %q", i, names[i], expected[i])
		}
	}
}

func TestRunAllowedTaskTool(t *testing.T) {
	toolMap := map[string]tools.Tool{
		"good": &testTool{"good"},
		"bad":  &errTool{"bad"},
	}

	t.Run("known tool", func(t *testing.T) {
		result, status := runAllowedTaskTool(toolMap, llm.ToolCall{
			ID: "c1", Type: "function",
			Function: llm.ToolCallFunction{Name: "good", Arguments: "{}"},
		})
		if status != "completed" || result != "result:good" {
			t.Errorf("got (%q, %q), want (\"result:good\", \"completed\")", result, status)
		}
	})

	t.Run("unknown tool", func(t *testing.T) {
		result, status := runAllowedTaskTool(toolMap, llm.ToolCall{
			ID: "c2", Type: "function",
			Function: llm.ToolCallFunction{Name: "unknown", Arguments: "{}"},
		})
		if status != "error" || !strings.Contains(result, "unknown tool") {
			t.Errorf("got (%q, %q), want error", result, status)
		}
	})

	t.Run("tool run error", func(t *testing.T) {
		result, status := runAllowedTaskTool(toolMap, llm.ToolCall{
			ID: "c3", Type: "function",
			Function: llm.ToolCallFunction{Name: "bad", Arguments: "{}"},
		})
		if status != "error" || !strings.Contains(result, "tool failed") {
			t.Errorf("got (%q, %q), want error containing 'tool failed'", result, status)
		}
	})
}

func TestToolMap(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(&testTool{"a"})
	reg.Register(&testTool{"b"})

	t.Run("filtered", func(t *testing.T) {
		m := toolMap(reg, map[string]bool{"a": true})
		if len(m) != 1 || m["a"] == nil {
			t.Errorf("got %d tools, want 1", len(m))
		}
	})

	t.Run("nil allowed", func(t *testing.T) {
		m := toolMap(reg, nil)
		if len(m) != 2 {
			t.Errorf("got %d tools, want 2", len(m))
		}
	})
}
