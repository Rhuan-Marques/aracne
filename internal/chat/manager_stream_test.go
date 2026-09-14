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

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
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

	manager, err := newTestManager(t, dbPath, dir, collect)
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

	manager, err := newTestManager(t, dbPath, dir, collect)
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

	manager, err := newTestManager(t, dbPath, dir, collect)
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

	manager, err := newTestManager(t, dbPath, dir, collect)
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

	manager, err := newTestManager(t, dbPath, dir, collect)
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
		// RESET EACH PASS. This re-reads the WHOLE events slice every time round, so a builder
		// that survives the loop appends what it has already seen: a poll that caught only the
		// first delta, followed by one that caught both, produced "HelloHello world" and failed
		// a test that was working perfectly. It needed the deltas to arrive split across two
		// polls, so it surfaced only under load -- on CI, never here.
		sb.Reset()
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

// TestWaitForDeltasDoesNotDoubleCountAcrossPolls pins the helper above, which six tests read
// their answer from.
//
// It re-reads the whole events slice on every poll, so it only reports the right thing if it
// starts each pass empty. It did not, and a run whose two deltas landed in different polls came
// back as "HelloHello world" -- a failure with no bug behind it, in a test that had passed the
// commit before. The deltas are fed here in two batches with a gap wider than the poll
// interval, so the split is arranged rather than waited for.
func TestWaitForDeltasDoesNotDoubleCountAcrossPolls(t *testing.T) {
	var (
		mu     sync.Mutex
		events []Event
	)
	add := func(e Event) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	}
	go func() {
		add(Event{Type: "delta", Payload: map[string]any{"content": "Hello"}})
		time.Sleep(120 * time.Millisecond) // wider than the 50ms poll, so the halves cannot share one
		add(Event{Type: "delta", Payload: map[string]any{"content": " world"}})
		add(Event{Type: "thinking", Payload: map[string]any{"active": false}})
	}()

	if got := waitForDeltas(t, &mu, &events, 10*time.Second); got != "Hello world" {
		t.Errorf("deltas split across polls read back as %q, want %q", got, "Hello world")
	}
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

func TestSend_EmitsRunStartedAndCompleted(t *testing.T) {
	sse := `data: {"choices":[{"index":0,"delta":{"content":"done"}}]}

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

	manager, err := newTestManager(t, dbPath, dir, collect)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	manager.SetProvider(ProviderSettings{
		Provider: ProviderOpenAI,
		Model:    "gpt-4o",
		BaseURL:  server.URL,
		APIKey:   "test-key",
	})

	session, err := manager.CreateSession("default", "run events test")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	_, err = manager.Send(session.ID, SendRequest{Content: "go"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		var hasStart, hasFinish bool
		for _, e := range events {
			if e.Type == "run_started" {
				hasStart = true
			}
			if e.Type == "run_completed" {
				hasFinish = true
			}
		}
		mu.Unlock()
		if hasStart && hasFinish {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()

	var startIdx, finishIdx int = -1, -1
	for i, e := range events {
		if e.Type == "run_started" {
			startIdx = i
		}
		if e.Type == "run_completed" {
			finishIdx = i
		}
	}

	if startIdx == -1 {
		t.Fatal("expected run_started event")
	}
	if finishIdx == -1 {
		t.Fatal("expected run_completed event")
	}
	if startIdx > finishIdx {
		t.Fatal("run_started must appear before run_completed")
	}

	payload := events[startIdx].Payload
	if active, ok := payload["active"]; !ok || active != true {
		t.Fatalf("run_started payload.active should be true, got %v", payload)
	}
	payload = events[finishIdx].Payload
	if active, ok := payload["active"]; !ok || active != false {
		t.Fatalf("run_completed payload.active should be false, got %v", payload)
	}
}

func TestGetSession_ReturnsRunningDuringAndAfterRun(t *testing.T) {
	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"content":"Hello"}}]}

`)
		w.(http.Flusher).Flush()
		<-done
		fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"content":" world"}}]}

data: [DONE]`)
		w.(http.Flusher).Flush()
	}))
	defer server.Close()

	dir := t.TempDir()
	setupTopologyDB(t, dir)
	storeDir := filepath.Join(dir, "chat")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "topology.db")

	manager, err := newTestManager(t, dbPath, dir, func(e Event) {})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	manager.SetProvider(ProviderSettings{
		Provider: ProviderOpenAI,
		Model:    "gpt-4o",
		BaseURL:  server.URL,
		APIKey:   "test-key",
	})

	session, err := manager.CreateSession("default", "running test")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	_, err = manager.Send(session.ID, SendRequest{Content: "go"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	session, err = manager.GetSession(session.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if !session.Running {
		t.Fatal("expected Running=true while LLM stream is active")
	}

	close(done)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		manager.mu.Lock()
		_, stillRunning := manager.running[session.ID]
		manager.mu.Unlock()
		if !stillRunning {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	session, err = manager.GetSession(session.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if session.Running {
		t.Fatal("expected Running=false after LLM stream completed")
	}
}

func TestSend_RejectsUserMessageWhenAlreadyRunning(t *testing.T) {
	blocked := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-blocked
		fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"content":"finally"}}]}

data: [DONE]`)
		w.(http.Flusher).Flush()
	}))
	defer server.Close()

	dir := t.TempDir()
	setupTopologyDB(t, dir)
	storeDir := filepath.Join(dir, "chat")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "topology.db")

	manager, err := newTestManager(t, dbPath, dir, func(e Event) {})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	manager.SetProvider(ProviderSettings{
		Provider: ProviderOpenAI,
		Model:    "gpt-4o",
		BaseURL:  server.URL,
		APIKey:   "test-key",
	})

	session, err := manager.CreateSession("default", "running reject test")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	_, err = manager.Send(session.ID, SendRequest{Content: "first"})
	if err != nil {
		t.Fatalf("first Send: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		manager.mu.Lock()
		_, running := manager.running[session.ID]
		manager.mu.Unlock()
		if running {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	before, err := manager.GetSession(session.ID)
	if err != nil {
		t.Fatalf("GetSession before rejected send: %v", err)
	}
	beforeMessages := len(before.Messages)
	beforeLLMMessages := len(before.LLMMessages)

	_, err = manager.Send(session.ID, SendRequest{Content: "second"})
	if err == nil {
		t.Fatal("expected second Send to fail while session is running")
	}
	if !strings.Contains(err.Error(), "currently running") {
		t.Fatalf("expected running error, got %v", err)
	}

	during, err := manager.GetSession(session.ID)
	if err != nil {
		t.Fatalf("GetSession after rejected send: %v", err)
	}
	if len(during.Messages) != beforeMessages {
		t.Fatalf("expected visible messages to remain at %d, got %d", beforeMessages, len(during.Messages))
	}
	if len(during.LLMMessages) != beforeLLMMessages {
		t.Fatalf("expected LLM messages to remain at %d, got %d", beforeLLMMessages, len(during.LLMMessages))
	}
	for _, msg := range during.Messages {
		if msg.Role == "user" && msg.Content == "second" {
			t.Fatal("rejected user message was appended to visible messages")
		}
	}
	for _, msg := range during.LLMMessages {
		if msg.Role == "user" && msg.Content == "second" {
			t.Fatal("rejected user message was appended to LLM messages")
		}
	}

	close(blocked)

	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		manager.mu.Lock()
		_, stillRunning := manager.running[session.ID]
		manager.mu.Unlock()
		if !stillRunning {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestGetSession_ReturnsRunningFalseWhenNotActive(t *testing.T) {
	dir := t.TempDir()
	setupTopologyDB(t, dir)
	storeDir := filepath.Join(dir, "chat")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, "topology.db")

	manager, err := newTestManager(t, dbPath, dir, func(e Event) {})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	session, err := manager.CreateSession("default", "idle test")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	got, err := manager.GetSession(session.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.Running {
		t.Fatal("expected Running=false for an idle session with no sends")
	}
}
