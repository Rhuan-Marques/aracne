package chat

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"aracne/internal/helper"
	"aracne/internal/topology/domain"
)

func TestManagerSend_EmitsStreamingDeltas(t *testing.T) {
	sse := `data: {"choices":[{"index":0,"delta":{"content":"Hello"}}]}

data: {"choices":[{"index":0,"delta":{"content":" world"}}]}

data: [DONE]`

	var (
		mu     sync.Mutex
		events []Event
	)
	collect := func(e Event) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	}

	dir := t.TempDir()
	setupTopologyDB(t, dir)

	storeDir := filepath.Join(dir, "chat")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(dir, "topology.db")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sse)
	}))
	defer server.Close()

	manager, err := NewManager(dbPath, dir, collect)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	manager.SetProvider(ProviderSettings{
		Provider: ProviderOpenAI,
		Model:    "gpt-4o",
		BaseURL:  server.URL,
		APIKey:   "test-key",
	})

	session, err := manager.CreateSession("default", "stream test")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	_, err = manager.Send(session.ID, SendRequest{
		Content: "hi",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	deltaContent := waitForDeltas(t, &mu, &events, 5*time.Second)
	if deltaContent != "Hello world" {
		t.Fatalf("expected delta content 'Hello world', got %q", deltaContent)
	}

	session, err = manager.GetSession(session.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	var assistantMsg *SessionMessage
	for i := range session.Messages {
		if session.Messages[i].Role == "assistant" {
			assistantMsg = &session.Messages[i]
			break
		}
	}
	if assistantMsg == nil {
		t.Fatal("expected assistant message in session")
	}
	if assistantMsg.Content != "Hello world" {
		t.Fatalf("expected assistant content 'Hello world', got %q", assistantMsg.Content)
	}
	if assistantMsg.Status != "completed" {
		t.Fatalf("expected status 'completed', got %q", assistantMsg.Status)
	}
}

func TestManagerSend_EmitsReasoningDeltas(t *testing.T) {
	sse := `data: {"choices":[{"index":0,"delta":{"reasoning_content":"step 1..."}}]}

data: {"choices":[{"index":0,"delta":{"content":" answer"}}]}

data: [DONE]`

	var (
		mu     sync.Mutex
		events []Event
	)
	collect := func(e Event) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	}

	dir := t.TempDir()
	setupTopologyDB(t, dir)

	storeDir := filepath.Join(dir, "chat")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(dir, "topology.db")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sse)
	}))
	defer server.Close()

	manager, err := NewManager(dbPath, dir, collect)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	manager.SetProvider(ProviderSettings{
		Provider: ProviderOpenAI,
		Model:    "gpt-4o",
		BaseURL:  server.URL,
		APIKey:   "test-key",
	})

	session, err := manager.CreateSession("default", "reasoning test")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	_, err = manager.Send(session.ID, SendRequest{
		Content: "think",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	var reasoningSeen, contentSeen bool
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		for _, e := range events {
			if e.Type == "delta" {
				payload := e.Payload
				if payload == nil {
					continue
				}
				if r, ok := payload["reasoning"]; ok && r.(string) != "" {
					reasoningSeen = true
				}
				if c, ok := payload["content"]; ok && c.(string) != "" {
					contentSeen = true
				}
			}
		}
		done := reasoningSeen && contentSeen
		mu.Unlock()
		if done {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !reasoningSeen {
		t.Fatal("expected reasoning delta event")
	}
	if !contentSeen {
		t.Fatal("expected content delta event")
	}

	session, err = manager.GetSession(session.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	var assistantMsg *SessionMessage
	for i := range session.Messages {
		if session.Messages[i].Role == "assistant" {
			assistantMsg = &session.Messages[i]
			break
		}
	}
	if assistantMsg == nil {
		t.Fatal("expected assistant message after thinking")
	}
	if assistantMsg.Reasoning != "step 1..." {
		t.Fatalf("expected reasoning 'step 1...', got %q", assistantMsg.Reasoning)
	}
	if assistantMsg.Content != " answer" {
		t.Fatalf("expected content ' answer', got %q", assistantMsg.Content)
	}
}

func TestManagerSend_NoContentDeltasForToolOnlyCall(t *testing.T) {
	sse := `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_t1","type":"function","function":{"name":"mock_tool","arguments":"{}"}}]}}]}

data: [DONE]`

	var (
		mu     sync.Mutex
		events []Event
	)
	collect := func(e Event) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	}

	dir := t.TempDir()
	setupTopologyDB(t, dir)

	storeDir := filepath.Join(dir, "chat")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(dir, "topology.db")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sse)
	}))
	defer server.Close()

	manager, err := NewManager(dbPath, dir, collect)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	manager.SetProvider(ProviderSettings{
		Provider: ProviderOpenAI,
		Model:    "gpt-4o",
		BaseURL:  server.URL,
		APIKey:   "test-key",
	})

	session, err := manager.CreateSession("default", "tool test")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	_, err = manager.Send(session.ID, SendRequest{
		Content: "run tool",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	deltaCount := 0
	for _, e := range events {
		if e.Type == "delta" {
			deltaCount++
		}
	}
	mu.Unlock()
	if deltaCount != 0 {
		t.Fatalf("expected 0 delta events for tool-only call, got %d", deltaCount)
	}
}

func TestManagerSend_ProviderErrorEmitsThinkingFalse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, `{"error":"internal error"}`)
	}))
	defer server.Close()

	var (
		mu     sync.Mutex
		events []Event
	)
	collect := func(e Event) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	}

	dir := t.TempDir()
	setupTopologyDB(t, dir)

	storeDir := filepath.Join(dir, "chat")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(dir, "topology.db")

	manager, err := NewManager(dbPath, dir, collect)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	manager.SetProvider(ProviderSettings{
		Provider: ProviderOpenAI,
		Model:    "gpt-4o",
		BaseURL:  server.URL,
		APIKey:   "test-key",
	})

	session, err := manager.CreateSession("default", "error test")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	_, err = manager.Send(session.ID, SendRequest{
		Content: "hi",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	var thinkingSilenced bool
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		for _, e := range events {
			if e.Type == "thinking" {
				payload := e.Payload
				if payload == nil {
					continue
				}
				if active, ok := payload["active"]; ok && active == false {
					thinkingSilenced = true
				}
			}
		}
		mu.Unlock()
		if thinkingSilenced {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !thinkingSilenced {
		t.Fatal("expected thinking=false event after error")
	}
}

func TestManagerSend_DeepSeekProviderStreams(t *testing.T) {
	sse := `data: {"choices":[{"index":0,"delta":{"reasoning_content":"thinking..."}}]}

data: {"choices":[{"index":0,"delta":{"content":"Hello world"}}]}

data: [DONE]`

	var (
		mu     sync.Mutex
		events []Event
	)
	collect := func(e Event) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	}

	dir := t.TempDir()
	setupTopologyDB(t, dir)

	storeDir := filepath.Join(dir, "chat")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(dir, "topology.db")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sse)
	}))
	defer server.Close()

	manager, err := NewManager(dbPath, dir, collect)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	manager.SetProvider(ProviderSettings{
		Provider: ProviderDeepSeek,
		Model:    "deepseek-chat",
		BaseURL:  server.URL,
		APIKey:   "test-key",
	})

	session, err := manager.CreateSession("default", "deepseek test")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	_, err = manager.Send(session.ID, SendRequest{
		Content: "hello",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	content := waitForDeltas(t, &mu, &events, 5*time.Second)
	if !strings.Contains(content, "Hello world") {
		t.Fatalf("expected 'Hello world' in deltas, got %q", content)
	}
}

func waitForDeltas(t *testing.T, mu *sync.Mutex, events *[]Event, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var sb strings.Builder
	for time.Now().Before(deadline) {
		mu.Lock()
		for _, e := range *events {
			if e.Type == "thinking" {
				payload := e.Payload
				if payload == nil {
					continue
				}
				if active, ok := payload["active"]; ok && active == false {
					mu.Unlock()
					return sb.String()
				}
			}
			if e.Type == "delta" {
				payload := e.Payload
				if payload == nil {
					continue
				}
				if c, ok := payload["content"]; ok {
					sb.WriteString(c.(string))
				}
			}
		}
		mu.Unlock()
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for streaming deltas; accumulated: %q", sb.String())
	return ""
}

func setupTopologyDB(t *testing.T, dir string) {
	t.Helper()
	topo := &domain.Topology{
		Root:      dir,
		Language:  "go",
		Resources: make(map[string]domain.Resource),
		Warnings:  make(map[string]domain.TopologyWarning),
		Errors:    make(map[string]string),
	}
	dbPath := filepath.Join(dir, "topology.db")
	if err := helper.WriteDb(topo, dbPath); err != nil {
		t.Fatalf("WriteDb: %v", err)
	}
}
