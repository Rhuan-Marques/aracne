package mcp

import (
	"encoding/json"
	"testing"
)

func TestRequestJSON(t *testing.T) {
	req := Request{
		JSONRPC: "2.0",
		ID:      intPtr(1),
		Method:  "tools/list",
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded Request
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.JSONRPC != "2.0" {
		t.Errorf("expected jsonrpc 2.0, got %q", decoded.JSONRPC)
	}
	if decoded.ID == nil || *decoded.ID != 1 {
		t.Errorf("expected id 1")
	}
	if decoded.Method != "tools/list" {
		t.Errorf("expected method 'tools/list', got %q", decoded.Method)
	}
}

func TestResponseJSON(t *testing.T) {
	resp := Response{
		JSONRPC: "2.0",
		ID:      intPtr(1),
		Result: ListToolsResult{
			Tools: []Tool{
				{
					Name:        "test_tool",
					Description: "A test tool",
					InputSchema: InputSchema{
						Type: "object",
						Properties: map[string]Property{
							"arg1": {Type: "string", Description: "first arg"},
						},
						Required: []string{"arg1"},
					},
				},
			},
		},
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded Response
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.JSONRPC != "2.0" {
		t.Errorf("expected jsonrpc 2.0")
	}
}

func TestResponseError(t *testing.T) {
	resp := Response{
		JSONRPC: "2.0",
		ID:      intPtr(1),
		Error: &RPCError{
			Code:    -32601,
			Message: "Method not found",
		},
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded Response
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.Error == nil {
		t.Fatal("expected error")
	}
	if decoded.Error.Code != -32601 {
		t.Errorf("expected code -32601, got %d", decoded.Error.Code)
	}
}

func TestCallToolResult(t *testing.T) {
	result := CallToolResult{
		Content: []ContentBlock{
			{Type: "text", Text: "Hello World"},
		},
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded CallToolResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(decoded.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(decoded.Content))
	}
	if decoded.Content[0].Text != "Hello World" {
		t.Errorf("expected 'Hello World', got %q", decoded.Content[0].Text)
	}
}

func TestCallToolResultError(t *testing.T) {
	result := CallToolResult{
		Content: []ContentBlock{
			{Type: "text", Text: "error occurred"},
		},
		IsError: true,
	}
	if !result.IsError {
		t.Error("expected IsError=true")
	}
}

func TestInputSchema(t *testing.T) {
	schema := InputSchema{
		Type: "object",
		Properties: map[string]Property{
			"name": {Type: "string", Description: "The name"},
		},
		Required: []string{"name"},
	}
	if schema.Type != "object" {
		t.Errorf("expected type object, got %q", schema.Type)
	}
	if len(schema.Required) != 1 || schema.Required[0] != "name" {
		t.Errorf("unexpected required: %v", schema.Required)
	}
}

func TestInitializeResult(t *testing.T) {
	result := InitializeResult{
		ProtocolVersion: "2024-11-05",
		Capabilities:    Capabilities{Tools: &struct{}{}},
		ServerInfo: ServerInfo{
			Name:    "llm-topology",
			Version: "1.0.0",
		},
	}
	if result.ProtocolVersion != "2024-11-05" {
		t.Errorf("unexpected protocol version: %q", result.ProtocolVersion)
	}
	if result.ServerInfo.Name != "llm-topology" {
		t.Errorf("unexpected server name: %q", result.ServerInfo.Name)
	}
}

func TestCallToolParams(t *testing.T) {
	args, _ := json.Marshal(map[string]string{"name": "test"})
	params := CallToolParams{
		Name:      "read_function",
		Arguments: args,
	}
	if params.Name != "read_function" {
		t.Errorf("unexpected name: %q", params.Name)
	}
	if params.Arguments == nil {
		t.Error("expected non-nil arguments")
	}
}

func TestRPCError(t *testing.T) {
	err := RPCError{
		Code:    -32700,
		Message: "Parse error",
	}
	data, _ := json.Marshal(err)
	var decoded RPCError
	json.Unmarshal(data, &decoded)
	if decoded.Code != -32700 || decoded.Message != "Parse error" {
		t.Errorf("unexpected RPCError: %+v", decoded)
	}
}

func intPtr(n int) *int {
	return &n
}
