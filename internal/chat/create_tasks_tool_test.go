package chat

import (
	"encoding/json"
	"testing"
)

func TestCreateTasksTool_Name(t *testing.T) {
	tool := &CreateTasksTool{}
	if got := tool.Name(); got != "CreateTasks" {
		t.Errorf("Name() = %q, want %q", got, "CreateTasks")
	}
}

func TestCreateTasksTool_Description(t *testing.T) {
	tool := &CreateTasksTool{}
	if got := tool.Description(); got == "" {
		t.Error("Description() returned empty string")
	}
}

func TestCreateTasksTool_Parameters(t *testing.T) {
	tool := &CreateTasksTool{}
	params := tool.Parameters()
	if len(params) != 2 {
		t.Fatalf("got %d params, want 2", len(params))
	}
	if params[0].Name != "worker_count" || params[0].Type != "number" || params[0].Required {
		t.Errorf("worker_count param mismatch: %+v", params[0])
	}
	if params[1].Name != "tasks" || params[1].Type != "array" || !params[1].Required {
		t.Errorf("tasks param mismatch: %+v", params[1])
	}
}

func TestCreateTasksTool_Run(t *testing.T) {
	tool := &CreateTasksTool{}
	result, err := tool.Run(json.RawMessage(`{}`))
	if err == nil {
		t.Error("expected error from Run()")
	}
	if result != "" {
		t.Errorf("expected empty result, got %q", result)
	}
}
