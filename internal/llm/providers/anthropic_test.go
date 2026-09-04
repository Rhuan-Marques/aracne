package providers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/llm"
)

func TestAnthropicStreamChat_Content(t *testing.T) {
	sse := `data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":"Hello"}}

data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}

data: {"type":"content_block_stop","index":0}`

	var deltas []llm.StreamEvent
	server := newAnthropicTestServer(t, sse)
	defer server.Close()

	provider := NewAnthropic("test-key", "claude-opus-4", server.URL)
	resp, err := provider.StreamChat(
		[]llm.Message{{Role: "user", Content: "hi"}},
		nil,
		func(event llm.StreamEvent) { deltas = append(deltas, event) },
	)
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	if resp.Content != "Hello world" {
		t.Fatalf("expected 'Hello world', got %q", resp.Content)
	}
	if len(deltas) != 2 {
		t.Fatalf("expected 2 deltas, got %d", len(deltas))
	}
	if deltas[0].Content != "Hello" {
		t.Fatalf("expected delta 'Hello', got %q", deltas[0].Content)
	}
	if deltas[1].Content != " world" {
		t.Fatalf("expected delta ' world', got %q", deltas[1].Content)
	}
}

func TestAnthropicStreamChat_Thinking(t *testing.T) {
	sse := `data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"I need to analyze..."}}

data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Answer: 42"}}

data: {"type":"content_block_stop","index":0}`

	var deltas []llm.StreamEvent
	server := newAnthropicTestServer(t, sse)
	defer server.Close()

	provider := NewAnthropic("test-key", "claude-opus-4", server.URL)
	resp, err := provider.StreamChat(
		[]llm.Message{{Role: "user", Content: "think"}},
		nil,
		func(event llm.StreamEvent) { deltas = append(deltas, event) },
	)
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	if resp.Content != "Answer: 42" {
		t.Fatalf("expected 'Answer: 42', got %q", resp.Content)
	}
	if resp.Reasoning != "I need to analyze..." {
		t.Fatalf("expected reasoning, got %q", resp.Reasoning)
	}
	if len(deltas) != 2 {
		t.Fatalf("expected 2 deltas, got %d", len(deltas))
	}
}

func TestAnthropicStreamChat_ToolUse(t *testing.T) {
	sse := `data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"tu_123","name":"get_weather","input":{}}}

data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"location\": \"NYC\""}}

data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"}"}}

data: {"type":"content_block_stop","index":0}`

	server := newAnthropicTestServer(t, sse)
	defer server.Close()

	provider := NewAnthropic("test-key", "claude-opus-4", server.URL)
	resp, err := provider.StreamChat(
		[]llm.Message{{Role: "user", Content: "weather"}},
		[]llm.ToolDefinition{{Name: "get_weather", Description: "get weather", Parameters: llm.Parameters{Type: "object"}}},
		nil,
	)
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].ID != "tu_123" {
		t.Fatalf("expected ID 'tu_123', got %q", resp.ToolCalls[0].ID)
	}
	if resp.ToolCalls[0].Function.Name != "get_weather" {
		t.Fatalf("expected function 'get_weather', got %q", resp.ToolCalls[0].Function.Name)
	}
	if resp.ToolCalls[0].Function.Arguments != `{"location": "NYC"}` {
		t.Fatalf("unexpected arguments: %q", resp.ToolCalls[0].Function.Arguments)
	}
	if resp.Content != "" {
		t.Fatalf("expected empty content, got %q", resp.Content)
	}
}

func TestAnthropicStreamChat_MultipleBlocks(t *testing.T) {
	sse := `data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":"Let me search"}}

data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" for you"}}

data: {"type":"content_block_stop","index":0}

data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"tu_456","name":"search","input":{}}}

data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"q\":\"test\"}"}}

data: {"type":"content_block_stop","index":1}`

	server := newAnthropicTestServer(t, sse)
	defer server.Close()

	provider := NewAnthropic("test-key", "claude-opus-4", server.URL)
	resp, err := provider.StreamChat(
		[]llm.Message{{Role: "user", Content: "search"}},
		[]llm.ToolDefinition{{Name: "search", Description: "search", Parameters: llm.Parameters{Type: "object"}}},
		nil,
	)
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	if resp.Content != "Let me search for you" {
		t.Fatalf("expected 'Let me search for you', got %q", resp.Content)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Function.Name != "search" {
		t.Fatalf("expected function 'search', got %q", resp.ToolCalls[0].Function.Name)
	}
}

func TestAnthropicStreamChat_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `{"error":{"message":"Invalid request"}}`)
	}))
	defer server.Close()

	provider := NewAnthropic("test-key", "claude-opus-4", server.URL)
	_, err := provider.StreamChat(
		[]llm.Message{{Role: "user", Content: "hi"}},
		nil,
		nil,
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestAnthropicStreamChat_EmptyAPIKey(t *testing.T) {
	provider := NewAnthropic("", "claude-opus-4", "http://example.com")
	_, err := provider.StreamChat(
		[]llm.Message{{Role: "user", Content: "hi"}},
		nil,
		nil,
	)
	if err == nil {
		t.Fatal("expected error for empty API key")
	}
}

func TestAnthropicStreamChat_NoDeltasForTextBlock(t *testing.T) {
	sse := `data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":"Hello"}}

data: {"type":"content_block_stop","index":0}`

	server := newAnthropicTestServer(t, sse)
	defer server.Close()

	provider := NewAnthropic("test-key", "claude-opus-4", server.URL)
	resp, err := provider.StreamChat(
		[]llm.Message{{Role: "user", Content: "hi"}},
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	if resp.Content != "Hello" {
		t.Fatalf("expected 'Hello', got %q", resp.Content)
	}
}

func TestAnthropicChat_DelegatesToStreamChat(t *testing.T) {
	sse := `data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":"from stream"}}

data: {"type":"content_block_stop","index":0}`

	server := newAnthropicTestServer(t, sse)
	defer server.Close()

	provider := NewAnthropic("test-key", "claude-opus-4", server.URL)
	resp, err := provider.Chat(
		[]llm.Message{{Role: "user", Content: "hi"}},
		nil,
	)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "from stream" {
		t.Fatalf("expected 'from stream', got %q", resp.Content)
	}
}

func TestAnthropicStreamChat_ErrorEvent(t *testing.T) {
	sse := `data: {"type":"error","error":{"message":"Overloaded"}}`

	server := newAnthropicTestServer(t, sse)
	defer server.Close()

	provider := NewAnthropic("test-key", "claude-opus-4", server.URL)
	_, err := provider.StreamChat(
		[]llm.Message{{Role: "user", Content: "hi"}},
		nil,
		nil,
	)
	if err == nil {
		t.Fatal("expected error from error event")
	}
}

func newAnthropicTestServer(t *testing.T, sse string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}

		var req anthropicRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if req.Stream != true {
			t.Errorf("expected stream=true, got %v", req.Stream)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sse)
	}))
}
