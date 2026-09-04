package providers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/llm"
)

func TestOpenAIStreamChat_Content(t *testing.T) {
	sse := `data: {"choices":[{"index":0,"delta":{"content":"Hello"}}]}

data: {"choices":[{"index":0,"delta":{"content":" world"}}]}

data: [DONE]`

	var deltas []llm.StreamEvent
	server := newOpenAITestServer(t, sse)
	defer server.Close()

	provider := NewOpenAI("test-key", "gpt-4o", server.URL)
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
	if deltas[0].Content != "Hello" || deltas[1].Content != " world" {
		t.Fatalf("unexpected delta contents: %+v", deltas)
	}
}

func TestOpenAIStreamChat_Reasoning(t *testing.T) {
	sse := `data: {"choices":[{"index":0,"delta":{"reasoning_content":"thinking"}}]}

data: {"choices":[{"index":0,"delta":{"content":" answer"}}]}

data: [DONE]`

	var deltas []llm.StreamEvent
	server := newOpenAITestServer(t, sse)
	defer server.Close()

	provider := NewOpenAI("test-key", "gpt-4o", server.URL)
	resp, err := provider.StreamChat(
		[]llm.Message{{Role: "user", Content: "hi"}},
		nil,
		func(event llm.StreamEvent) { deltas = append(deltas, event) },
	)
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	if resp.Content != " answer" {
		t.Fatalf("expected ' answer', got %q", resp.Content)
	}
	if resp.Reasoning != "thinking" {
		t.Fatalf("expected 'thinking', got %q", resp.Reasoning)
	}
	if len(deltas) != 2 {
		t.Fatalf("expected 2 deltas, got %d", len(deltas))
	}
	if deltas[0].Reasoning != "thinking" {
		t.Fatalf("expected delta reasoning 'thinking', got %q", deltas[0].Reasoning)
	}
	if deltas[1].Content != " answer" {
		t.Fatalf("expected delta content ' answer', got %q", deltas[1].Content)
	}
}

// TestOpenAIStreamChat_NonContiguousToolCallIndices guards against dropping
// tool calls whose stream indices are not a contiguous 0..n run (e.g. a gap at
// index 1). The collection must sort by index instead of stopping at the first
// missing one.
func TestOpenAIStreamChat_NonContiguousToolCallIndices(t *testing.T) {
	sse := `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"tool_a","arguments":"{}"}}]}}]}

data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":2,"id":"call_c","type":"function","function":{"name":"tool_c","arguments":"{}"}}]}}]}

data: [DONE]`

	server := newOpenAITestServer(t, sse)
	defer server.Close()

	provider := NewOpenAI("test-key", "gpt-4o", server.URL)
	resp, err := provider.StreamChat(
		[]llm.Message{{Role: "user", Content: "do tools"}},
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	if len(resp.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls (non-contiguous indices), got %d: %+v", len(resp.ToolCalls), resp.ToolCalls)
	}
	if resp.ToolCalls[0].Function.Name != "tool_a" || resp.ToolCalls[1].Function.Name != "tool_c" {
		t.Fatalf("expected tool_a then tool_c (sorted by index), got: %+v", resp.ToolCalls)
	}
}

func TestOpenAIStreamChat_ToolCalls(t *testing.T) {
	sse := `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read_file","arguments":""}}]}}]}

data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\": \"test.txt\"}"}}]}}]}

data: [DONE]`

	var deltas []llm.StreamEvent
	server := newOpenAITestServer(t, sse)
	defer server.Close()

	provider := NewOpenAI("test-key", "gpt-4o", server.URL)
	resp, err := provider.StreamChat(
		[]llm.Message{{Role: "user", Content: "read a file"}},
		[]llm.ToolDefinition{{Name: "read_file", Description: "read", Parameters: llm.Parameters{Type: "object"}}},
		func(event llm.StreamEvent) { deltas = append(deltas, event) },
	)
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	if resp.Content != "" {
		t.Fatalf("expected empty content, got %q", resp.Content)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].ID != "call_1" {
		t.Fatalf("expected ID 'call_1', got %q", resp.ToolCalls[0].ID)
	}
	if resp.ToolCalls[0].Function.Name != "read_file" {
		t.Fatalf("expected function 'read_file', got %q", resp.ToolCalls[0].Function.Name)
	}
	if resp.ToolCalls[0].Function.Arguments != `{"path": "test.txt"}` {
		t.Fatalf("unexpected arguments: %q", resp.ToolCalls[0].Function.Arguments)
	}
	if len(deltas) != 0 {
		t.Fatalf("expected 0 content deltas for tool-only call, got %d", len(deltas))
	}
}

func TestOpenAIStreamChat_MixedContentAndToolCalls(t *testing.T) {
	sse := `data: {"choices":[{"index":0,"delta":{"content":"I'll help"}}]}

data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"search","arguments":""}}]}}]}

data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"q\": \"test\"}"}}]}}]}

data: [DONE]`

	var deltas []llm.StreamEvent
	server := newOpenAITestServer(t, sse)
	defer server.Close()

	provider := NewOpenAI("test-key", "gpt-4o", server.URL)
	resp, err := provider.StreamChat(
		[]llm.Message{{Role: "user", Content: "search"}},
		nil,
		func(event llm.StreamEvent) { deltas = append(deltas, event) },
	)
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	if resp.Content != "I'll help" {
		t.Fatalf("expected content, got %q", resp.Content)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	if len(deltas) != 1 {
		t.Fatalf("expected 1 content delta, got %d", len(deltas))
	}
	if deltas[0].Content != "I'll help" {
		t.Fatalf("expected delta 'I\\'ll help', got %q", deltas[0].Content)
	}
}

func TestOpenAIStreamChat_NilCallback(t *testing.T) {
	sse := `data: {"choices":[{"index":0,"delta":{"content":"Hello"}}]}

data: [DONE]`

	server := newOpenAITestServer(t, sse)
	defer server.Close()

	provider := NewOpenAI("test-key", "gpt-4o", server.URL)
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

func TestOpenAIStreamChat_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintf(w, `{"error":"invalid API key"}`)
	}))
	defer server.Close()

	provider := NewOpenAI("bad-key", "gpt-4o", server.URL)
	_, err := provider.StreamChat(
		[]llm.Message{{Role: "user", Content: "hi"}},
		nil,
		nil,
	)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected 401 in error, got %v", err)
	}
}

func TestOpenAIStreamChat_EmptyAPIKey(t *testing.T) {
	provider := NewOpenAI("", "gpt-4o", "http://example.com")
	_, err := provider.StreamChat(
		[]llm.Message{{Role: "user", Content: "hi"}},
		nil,
		nil,
	)
	if err == nil {
		t.Fatal("expected error for empty API key")
	}
}

func TestOpenAIStreamChat_MultipleChoices(t *testing.T) {
	sse := `data: {"choices":[{"index":0,"delta":{"content":"First"}},{"index":1,"delta":{"content":"Second"}}]}

data: [DONE]`

	server := newOpenAITestServer(t, sse)
	defer server.Close()

	provider := NewOpenAI("test-key", "gpt-4o", server.URL)
	resp, err := provider.StreamChat(
		[]llm.Message{{Role: "user", Content: "hi"}},
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	if resp.Content != "FirstSecond" {
		t.Fatalf("expected content from first choice, got %q", resp.Content)
	}
}

func TestOpenAIStreamChat_NoChoices(t *testing.T) {
	sse := `data: {"choices":[]}

data: [DONE]`

	server := newOpenAITestServer(t, sse)
	defer server.Close()

	provider := NewOpenAI("test-key", "gpt-4o", server.URL)
	resp, err := provider.StreamChat(
		[]llm.Message{{Role: "user", Content: "hi"}},
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	if resp.Content != "" {
		t.Fatalf("expected empty content, got %q", resp.Content)
	}
}

func TestOpenAIStreamChat_BadJSONChunk(t *testing.T) {
	sse := `data: not-json

data: [DONE]`

	server := newOpenAITestServer(t, sse)
	defer server.Close()

	provider := NewOpenAI("test-key", "gpt-4o", server.URL)
	_, err := provider.StreamChat(
		[]llm.Message{{Role: "user", Content: "hi"}},
		nil,
		nil,
	)
	if err == nil {
		t.Fatal("expected error for bad JSON chunk")
	}
}

func TestOpenAIChat_DelegatesToStreamChat(t *testing.T) {
	sse := `data: {"choices":[{"index":0,"delta":{"content":"from stream"}}]}

data: [DONE]`

	server := newOpenAITestServer(t, sse)
	defer server.Close()

	provider := NewOpenAI("test-key", "gpt-4o", server.URL)
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

func newOpenAITestServer(t *testing.T, sse string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}

		var req openAIRequest
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
