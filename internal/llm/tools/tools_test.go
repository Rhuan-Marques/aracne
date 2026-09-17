package tools

import (
	"encoding/json"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/llm/toolapi"
)

func TestNewRegistry(t *testing.T) {
	r := toolapi.NewRegistry()
	if r == nil {
		t.Fatal("toolapi.NewRegistry returned nil")
	}
	if len(r.List()) != 0 {
		t.Errorf("expected empty registry")
	}
}

func TestRegisterAndGet(t *testing.T) {
	r := toolapi.NewRegistry()
	tool := &Ls{}
	r.Register(tool)

	got, ok := r.Get("ls")
	if !ok {
		t.Fatal("expected tool to be found")
	}
	if got.Name() != "ls" {
		t.Errorf("expected name 'ls', got %q", got.Name())
	}
}

func TestGetNonexistent(t *testing.T) {
	r := toolapi.NewRegistry()
	_, ok := r.Get("nonexistent")
	if ok {
		t.Error("expected false for nonexistent tool")
	}
}

func TestListTools(t *testing.T) {
	r := toolapi.NewRegistry()
	r.Register(&Ls{})
	r.Register(&WarningsList{})

	list := r.List()
	if len(list) != 2 {
		t.Errorf("expected 2 tools, got %d", len(list))
	}
}

func TestToolInterface(t *testing.T) {
	var tool toolapi.Tool = &Ls{}
	if tool.Name() != "ls" {
		t.Errorf("unexpected name: %q", tool.Name())
	}
	if tool.Description() == "" {
		t.Error("expected non-empty description")
	}
}

func TestLsToolInterface(t *testing.T) {
	var tool toolapi.Tool = &Ls{}
	if tool.Name() != "ls" {
		t.Errorf("unexpected name: %q", tool.Name())
	}
	params := tool.Parameters()
	if len(params) != 2 {
		t.Errorf("expected 2 parameters, got %d", len(params))
	}
	foundPath := false
	foundRecursive := false
	for _, p := range params {
		if p.Name == "path" {
			foundPath = true
			if p.Required {
				t.Error("expected path to be optional")
			}
		}
		if p.Name == "recursive" {
			foundRecursive = true
			if p.Type != "boolean" {
				t.Errorf("expected boolean type, got %q", p.Type)
			}
		}
	}
	if !foundPath || !foundRecursive {
		t.Error("expected path and recursive parameters")
	}
}

func TestLsRunDefaultPath(t *testing.T) {
	tool := &Ls{}

	args, _ := json.Marshal(map[string]interface{}{})
	result, err := tool.Run(args)
	if err != nil {
		t.Fatalf("Ls Run: %v", err)
	}
	if result == "" {
		t.Error("expected non-empty result")
	}
}

func TestLsRunRecursive(t *testing.T) {
	tool := &Ls{}

	args, _ := json.Marshal(map[string]interface{}{
		"recursive": true,
	})
	result, err := tool.Run(args)
	if err != nil {
		t.Fatalf("Ls recursive: %v", err)
	}
	if result == "" {
		t.Error("expected non-empty result")
	}
}

func TestLsRunWithPath(t *testing.T) {
	tool := &Ls{}

	args, _ := json.Marshal(map[string]interface{}{
		"path": ".",
	})
	result, err := tool.Run(args)
	if err != nil {
		t.Fatalf("Ls with path: %v", err)
	}
	if result == "" {
		t.Error("expected non-empty result")
	}
}

func TestLsRunNonexistentPath(t *testing.T) {
	tool := &Ls{}

	args, _ := json.Marshal(map[string]interface{}{
		"path": "nonexistent_dir_xyz123",
	})
	_, err := tool.Run(args)
	if err == nil {
		t.Error("expected error for nonexistent directory")
	}
}

func TestParameter(t *testing.T) {
	p := toolapi.Parameter{
		Name:        "test",
		Type:        "string",
		Description: "a test parameter",
		Required:    true,
	}
	if p.Name != "test" || p.Type != "string" || !p.Required {
		t.Errorf("unexpected Parameter: %+v", p)
	}
}
