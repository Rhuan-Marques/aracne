package mcp

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/llm/toolapi"
)

func TestRequestJSON(t *testing.T) {
	req := Request{
		JSONRPC: "2.0",
		ID:      rawID(1),
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
	if string(decoded.ID) != "1" {
		t.Errorf("expected id 1, got %q", string(decoded.ID))
	}
	if decoded.Method != "tools/list" {
		t.Errorf("expected method 'tools/list', got %q", decoded.Method)
	}
}

func TestResponseJSON(t *testing.T) {
	resp := Response{
		JSONRPC: "2.0",
		ID:      rawID(1),
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
		ID:      rawID(1),
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
			Name:    "aracne",
			Version: "1.0.0",
		},
	}
	if result.ProtocolVersion != "2024-11-05" {
		t.Errorf("unexpected protocol version: %q", result.ProtocolVersion)
	}
	if result.ServerInfo.Name != "aracne" {
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

// rawID renders a numeric JSON-RPC id the way a client sends it. The id is raw JSON on the
// wire and echoed back verbatim, so a STRING id is equally legal -- see TestStringIDsSurvive.
func rawID(n int) json.RawMessage {
	return json.RawMessage(strconv.Itoa(n))
}

// A JSON-RPC id may be a string, a number or null. It used to be decoded into a *int, so a
// client sending `"id": "1"` -- legal, and what several hosts send -- failed to unmarshal and
// got a parse error carrying no id at all, which it could not correlate with anything.
func TestStringIDsSurviveAndAreEchoedBack(t *testing.T) {
	srv := NewServer(toolapi.NewRegistry())
	for _, id := range []string{`"call-1"`, `7`, `"0"`} {
		var req Request
		line := `{"jsonrpc":"2.0","id":` + id + `,"method":"tools/list"}`
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			t.Fatalf("id %s: unmarshal: %v", id, err)
		}
		resp := srv.dispatch(&req)
		if resp == nil {
			t.Fatalf("id %s: no response", id)
		}
		if string(resp.ID) != id {
			t.Errorf("id %s echoed back as %s", id, string(resp.ID))
		}
	}
}

// `ping` is part of the base protocol and is how a client checks the server is alive.
// Answering "method not found" to a liveness probe is a fine way to be judged dead.
func TestPingIsAnswered(t *testing.T) {
	srv := NewServer(toolapi.NewRegistry())
	var req Request
	if err := json.Unmarshal([]byte(`{"jsonrpc":"2.0","id":3,"method":"ping"}`), &req); err != nil {
		t.Fatal(err)
	}
	resp := srv.dispatch(&req)
	if resp == nil || resp.Error != nil {
		t.Fatalf("ping was not answered cleanly: %+v", resp)
	}
}

// An error raised before the id could be read carries `"id": null`, which is what the spec
// asks for. Omitting the member is a different message.
func TestParseErrorCarriesANullID(t *testing.T) {
	srv := NewServer(toolapi.NewRegistry())
	resp := srv.errorResponse(nil, -32700, "Parse error")
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"id":null`) {
		t.Errorf("error response omits the null id: %s", data)
	}
}

// optionalArgsTool is a tool whose parameters are all optional -- the shape warnings_list,
// bug_list and node_list_no_description have -- and whose Run decodes its arguments the way
// every tool in this repository does.
type optionalArgsTool struct{ ran bool }

func (o *optionalArgsTool) Name() string        { return "warnings_list" }
func (o *optionalArgsTool) Description() string { return "list warnings" }
func (o *optionalArgsTool) Parameters() []toolapi.Parameter {
	return []toolapi.Parameter{{Name: "kind", Type: "string", Required: false}}
}
func (o *optionalArgsTool) Run(args json.RawMessage) (string, error) {
	var p struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", err
	}
	o.ran = true
	return "no outstanding warnings.", nil
}

// A tool with no required parameters is legally called with no `arguments` member at all.
// That left Arguments nil, and every Run begins with json.Unmarshal(args, &params), which
// fails on nil with "unexpected end of JSON input" -- so the call came back as a tool error
// carrying a JSON parser message. Claude Code sends `"arguments": {}` and never hit it, which
// is exactly why it survived: the same class as the `*int` request id above.
func TestCallToolAcceptsOmittedArguments(t *testing.T) {
	for _, params := range []string{
		`{"name":"warnings_list"}`,
		`{"name":"warnings_list","arguments":null}`,
		`{"name":"warnings_list","arguments":{}}`,
	} {
		reg := toolapi.NewRegistry()
		tool := &optionalArgsTool{}
		reg.Register(tool)
		srv := NewServer(reg)

		var req Request
		line := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":` + params + `}`
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			t.Fatalf("%s: unmarshal: %v", params, err)
		}
		resp := srv.dispatch(&req)
		if resp == nil {
			t.Fatalf("%s: no response", params)
		}
		result, ok := resp.Result.(CallToolResult)
		if !ok {
			t.Fatalf("%s: result is %T, want CallToolResult (%+v)", params, resp.Result, resp)
		}
		if result.IsError {
			t.Fatalf("%s: call failed: %s", params, result.Content[0].Text)
		}
		if !tool.ran {
			t.Fatalf("%s: the tool was never reached", params)
		}
	}
}

// A tools/call with no params at all, or with no tool name, is a client error and must be
// reported as one -- not as whatever json.Unmarshal says about nil input.
func TestCallToolRejectsAMissingName(t *testing.T) {
	srv := NewServer(toolapi.NewRegistry())
	for _, line := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call"}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}`,
	} {
		var req Request
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		resp := srv.dispatch(&req)
		if resp == nil || resp.Error == nil || resp.Error.Code != -32602 {
			t.Fatalf("%s: got %+v, want an Invalid params error", line, resp)
		}
	}
}
