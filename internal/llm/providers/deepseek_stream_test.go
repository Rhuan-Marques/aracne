package providers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"aracne/internal/llm"
)

func TestDeepSeekStreamChat_Content(t *testing.T) {
	sse := `data: {"choices":[{"index":0,"delta":{"content":"Hello"}}]}

data: {"choices":[{"index":0,"delta":{"content":" world"}}]}

data: [DONE]`

	var deltas []llm.StreamEvent
	server := newDeepSeekTestServer(t, sse)
	defer server.Close()

	provider := NewDeepSeekWithConfig("test-key", "deepseek-chat", server.URL)
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
		t.Fatalf("unexpected deltas: %+v", deltas)
	}
}

func TestDeepSeekStreamChat_Reasoning(t *testing.T) {
	sse := `data: {"choices":[{"index":0,"delta":{"reasoning_content":"thinking..."}}]}

data: {"choices":[{"index":0,"delta":{"content":" answer"}}]}

data: [DONE]`

	var deltas []llm.StreamEvent
	server := newDeepSeekTestServer(t, sse)
	defer server.Close()

	provider := NewDeepSeekWithConfig("test-key", "deepseek-chat", server.URL)
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
	if resp.Reasoning != "thinking..." {
		t.Fatalf("expected 'thinking...', got %q", resp.Reasoning)
	}
	if len(deltas) != 2 {
		t.Fatalf("expected 2 deltas, got %d", len(deltas))
	}
}

func TestDeepSeekStreamChat_ToolCalls(t *testing.T) {
	sse := `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_ds","type":"function","function":{"name":"search","arguments":""}}]}}]}

data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"query\":\"test\"}"}}]}}]}

data: [DONE]`

	server := newDeepSeekTestServer(t, sse)
	defer server.Close()

	provider := NewDeepSeekWithConfig("test-key", "deepseek-chat", server.URL)
	resp, err := provider.StreamChat(
		[]llm.Message{{Role: "user", Content: "search"}},
		[]llm.ToolDefinition{{Name: "search", Description: "search tool", Parameters: llm.Parameters{Type: "object"}}},
		nil,
	)
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].ID != "call_ds" {
		t.Fatalf("expected ID 'call_ds', got %q", resp.ToolCalls[0].ID)
	}
	if resp.ToolCalls[0].Function.Name != "search" {
		t.Fatalf("expected function 'search', got %q", resp.ToolCalls[0].Function.Name)
	}
	if resp.ToolCalls[0].Function.Arguments != `{"query":"test"}` {
		t.Fatalf("unexpected arguments: %q", resp.ToolCalls[0].Function.Arguments)
	}
}

func TestDeepSeekStreamChat_EmptyResponse(t *testing.T) {
	sse := `data: {"choices":[]}

data: [DONE]`

	server := newDeepSeekTestServer(t, sse)
	defer server.Close()

	provider := NewDeepSeekWithConfig("test-key", "deepseek-chat", server.URL)
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

// TestDeepSeekStreamChat_NonContiguousToolCallIndices guards against dropping
// tool calls whose stream indices are not a contiguous 0..n run (gap at index
// 1). The collection must sort by index instead of stopping at the first gap.
func TestDeepSeekStreamChat_NonContiguousToolCallIndices(t *testing.T) {
	sse := `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"tool_a","arguments":"{}"}}]}}]}

data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":2,"id":"call_c","type":"function","function":{"name":"tool_c","arguments":"{}"}}]}}]}

data: [DONE]`

	server := newDeepSeekTestServer(t, sse)
	defer server.Close()

	provider := NewDeepSeekWithConfig("test-key", "deepseek-chat", server.URL)
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

func TestDeepSeekStreamChat_MultipleToolCalls(t *testing.T) {
	sse := `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"tool_a","arguments":""}}]}}]}

data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_2","type":"function","function":{"name":"tool_b","arguments":"{}"}}]}}]}

data: [DONE]`

	server := newDeepSeekTestServer(t, sse)
	defer server.Close()

	provider := NewDeepSeekWithConfig("test-key", "deepseek-chat", server.URL)
	resp, err := provider.StreamChat(
		[]llm.Message{{Role: "user", Content: "do tools"}},
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	if len(resp.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Function.Name != "tool_a" || resp.ToolCalls[1].Function.Name != "tool_b" {
		t.Fatalf("unexpected tool call order: %+v", resp.ToolCalls)
	}
}

func TestDeepSeekStreamChat_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprintf(w, `{"error":"rate limited"}`)
	}))
	defer server.Close()

	provider := NewDeepSeekWithConfig("test-key", "deepseek-chat", server.URL)
	_, err := provider.StreamChat(
		[]llm.Message{{Role: "user", Content: "hi"}},
		nil,
		nil,
	)
	if err == nil {
		t.Fatal("expected error for 429")
	}
}

func TestDeepSeekChat_DelegatesToStreamChat(t *testing.T) {
	sse := `data: {"choices":[{"index":0,"delta":{"content":"from stream"}}]}

data: [DONE]`

	server := newDeepSeekTestServer(t, sse)
	defer server.Close()

	provider := NewDeepSeekWithConfig("test-key", "deepseek-chat", server.URL)
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

func newDeepSeekTestServer(t *testing.T, sse string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}

		var req deepSeekRequest
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
