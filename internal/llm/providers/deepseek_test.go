package providers

import (
	"encoding/json"
	"testing"

	"aracne/internal/llm"
)

func TestDeepSeekRequestUsesOpenAIToolEnvelope(t *testing.T) {
	req := deepSeekRequest{
		Model:    "deepseek-chat",
		Messages: []llm.Message{{Role: "user", Content: "hello"}},
		Tools: toOpenAITools([]llm.ToolDefinition{{
			Name:        "read_file",
			Description: "Read a file",
			Parameters: llm.Parameters{
				Type:       "object",
				Properties: map[string]llm.Property{"path": {Type: "string"}},
				Required:   []string{"path"},
			},
		}}),
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	tools, ok := decoded["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("expected one tool, got %#v", decoded["tools"])
	}
	tool, ok := tools[0].(map[string]any)
	if !ok {
		t.Fatalf("expected object tool, got %#v", tools[0])
	}
	if tool["type"] != "function" {
		t.Fatalf("expected function tool type, got %#v", tool["type"])
	}
	fn, ok := tool["function"].(map[string]any)
	if !ok {
		t.Fatalf("expected function object, got %#v", tool["function"])
	}
	if fn["name"] != "read_file" {
		t.Fatalf("expected function name, got %#v", fn["name"])
	}
}
