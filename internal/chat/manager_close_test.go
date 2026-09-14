package chat

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestCloseStopsAndWaitsForBackgroundRuns pins the contract the chat manager did not have: after
// Close returns, nothing it started is still running.
//
// WHY THIS IS WRITTEN AGAINST A BLOCKED REQUEST AND NOT AGAINST A RACE. The bug this replaces
// was found as "TempDir RemoveAll cleanup: directory not empty" -- a workflow goroutine still
// writing into the test's directory while the harness removed it -- and a test that reproduces
// THAT is a test that has to lose a race on purpose. It failed perhaps one run in twenty, on
// whichever machine was busiest, and passed the commit that introduced it.
//
// So the run here is pinned open instead: the provider handler reports that it has been entered
// and then blocks, which makes "a background run is in flight" a fact the test has established
// rather than a window it is hoping to hit. Close is then called against that known state and
// both halves of its promise are checked. That it WAITS is the active-run count. That it
// CANCELS is the nil error: this provider request cannot be answered while the test is running,
// so a Close that merely waited could only have returned once its grace expired, with an error
// naming what was still going. Every timeout below is a bounded guard that only a real
// regression reaches -- the cancelling path returns in milliseconds.
//
// What this does NOT claim is that a goroutine started outside goBackground is waited for.
// Nothing can promise that from the outside: goBackground IS the contract, and a run that does
// not go through it is not a run the manager knows it has.
func TestCloseStopsAndWaitsForBackgroundRuns(t *testing.T) {
	entered := make(chan struct{}, 16)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case entered <- struct{}{}:
		default:
		}
		// This request is never answered while the test is running. `release` is the test's
		// own way out at the end; r.Context() is the connection going away.
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)

	manager := setupWorkflowManager(t, server.URL, true)
	session, err := manager.CreateSession("default", "close test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.StartForcedWorkflow(WorkflowRequest{
		SessionID: session.ID,
		Type:      "descriptions",
		BatchSize: 2,
		Parallel:  2,
	}); err != nil {
		t.Fatalf("StartForcedWorkflow: %v", err)
	}

	// The fixture, established rather than assumed: a run is in the provider call right now.
	select {
	case <-entered:
	case <-time.After(30 * time.Second):
		t.Fatal("fixture is wrong: the workflow never reached the provider, so this proves nothing")
	}
	if n := manager.bgActive.Load(); n == 0 {
		t.Fatal("fixture is wrong: no background run is being tracked, so this proves nothing")
	}

	closed := make(chan error, 1)
	go func() { closed <- manager.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			// The grace expired: something Close cancelled did not stop.
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(closeGrace + 30*time.Second):
		t.Fatal("Close never returned, though its own grace should have ended the wait")
	}
	if n := manager.bgActive.Load(); n != 0 {
		t.Errorf("Close returned with %d background run(s) still active", n)
	}

	// Idempotent: a second Close is a no-op that still reports a quiet manager. A server
	// shutdown path and a test cleanup both call it, and they overlap.
	if err := manager.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

// TestCloseRefusesNewBackgroundRuns pins the other half of the shutdown: once Close has been
// called, a run that arrives afterwards does not start writing again behind it -- and the
// session it was asked for is not left marked as running, which would make it unstartable for
// the rest of the process and report an active run that does not exist.
func TestCloseRefusesNewBackgroundRuns(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"done\"}}]}\n\ndata: [DONE]")
	}))
	defer server.Close()

	manager := setupWorkflowManager(t, server.URL, true)
	session, err := manager.CreateSession("default", "closed manager")
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	manager.startRun(session.ID)
	if n := manager.bgActive.Load(); n != 0 {
		t.Errorf("a run started after Close: %d active", n)
	}
	manager.mu.Lock()
	running := manager.running[session.ID]
	manager.mu.Unlock()
	if running {
		t.Error("a refused run left the session marked running, which nothing will ever clear")
	}
}

// newTestManager is NewManager with the shutdown a test needs.
//
// Every caller takes its workspace from t.TempDir() first, and cleanups run in reverse order of
// registration, so the manager is quiet before the directory it writes into is removed. Without
// that, a test that starts a run races the harness's RemoveAll and fails as "TempDir RemoveAll
// cleanup: directory not empty" -- on whichever machine happens to be slow that day, which is
// how this was found.
func newTestManager(t *testing.T, dbPath, workspace string, emit func(Event)) (*Manager, error) {
	t.Helper()
	m, err := NewManager(dbPath, workspace, emit)
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() {
		if err := m.Close(); err != nil {
			t.Errorf("the manager was still running at the end of the test: %v", err)
		}
	})
	return m, nil
}
