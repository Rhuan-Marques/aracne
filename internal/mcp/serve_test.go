package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/llm/tools"
)

type namedTool struct{ name string }

func (n namedTool) Name() string                        { return n.name }
func (n namedTool) Description() string                 { return "tool " + n.name }
func (n namedTool) Parameters() []tools.Parameter       { return nil }
func (n namedTool) Run(json.RawMessage) (string, error) { return "ran " + n.name, nil }

// runLines feeds newline-separated messages through the stdio loop and returns the replies,
// one decoded JSON value per output line.
func runLines(t *testing.T, srv *Server, lines ...string) []json.RawMessage {
	t.Helper()
	var out bytes.Buffer
	if err := srv.serve(strings.NewReader(strings.Join(lines, "\n")+"\n"), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}
	var replies []json.RawMessage
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if l != "" {
			replies = append(replies, json.RawMessage(l))
		}
	}
	return replies
}

func errorCode(t *testing.T, raw json.RawMessage) (string, int) {
	t.Helper()
	var r struct {
		ID    json.RawMessage `json:"id"`
		Error *RPCError       `json:"error"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("reply is not a response object: %s", raw)
	}
	if r.Error == nil {
		return string(r.ID), 0
	}
	return string(r.ID), r.Error.Code
}

// RD-12: a bufio.Scanner treats a line over its buffer as fatal, so one oversized request ended
// the loop and the server exited under the harness. The line is now answered and skipped, and
// the next request is served.
func TestOversizedLineDoesNotKillTheServer(t *testing.T) {
	srv := NewServer(tools.NewRegistry())
	huge := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"x","arguments":{"s":"` +
		strings.Repeat("x", maxMessageBytes) + `"}}}`
	replies := runLines(t, srv, huge, `{"jsonrpc":"2.0","id":2,"method":"ping"}`)
	if len(replies) != 2 {
		t.Fatalf("want a reply to each line, got %d: %s", len(replies), replies)
	}
	if id, code := errorCode(t, replies[0]); code != codeInvalidRequest || id != "null" {
		t.Fatalf("oversized line: id %s code %d, want null / %d", id, code, codeInvalidRequest)
	}
	if id, code := errorCode(t, replies[1]); code != 0 || id != "2" {
		t.Fatalf("the request after it must still be served: %s", replies[1])
	}
}

// The codes JSON-RPC 2.0 and MCP assign, one per failure. A request that is JSON but not a
// Request object is -32600, not a parse error; an unknown tool is a bad PARAMETER to a method
// that exists (-32602, as the MCP spec gives it), not a missing method.
func TestErrorCodesFollowTheSpec(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(namedTool{"known"})
	srv := NewServer(reg)
	cases := []struct {
		line   string
		wantID string
		code   int
	}{
		{`{not json`, "null", codeParseError},
		{`42`, "null", codeInvalidRequest},
		{`{"jsonrpc":"2.0","id":3}`, "3", codeInvalidRequest},
		{`{"jsonrpc":"1.0","id":4,"method":"ping"}`, "4", codeInvalidRequest},
		{`{"id":5,"method":"ping"}`, "5", codeInvalidRequest},
		{`{"jsonrpc":"2.0","id":6,"method":7}`, "null", codeInvalidRequest},
		{`{"jsonrpc":"2.0","id":{"a":1},"method":"ping"}`, "null", codeInvalidRequest},
		{`[]`, "null", codeInvalidRequest},
		{`{"jsonrpc":"2.0","id":8,"method":"nope"}`, "8", codeMethodNotFound},
		{`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"missing"}}`, "9", codeInvalidParams},
	}
	for _, tc := range cases {
		replies := runLines(t, srv, tc.line)
		if len(replies) != 1 {
			t.Fatalf("%s: want one reply, got %d", tc.line, len(replies))
		}
		if id, code := errorCode(t, replies[0]); code != tc.code || id != tc.wantID {
			t.Errorf("%s: id %s code %d, want id %s code %d", tc.line, id, code, tc.wantID, tc.code)
		}
	}
}

// What must keep working exactly as it did for the two harnesses: notifications get no reply,
// requests are answered with their own id, and a known tool runs.
func TestHarnessTrafficIsUnchanged(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(namedTool{"known"})
	srv := NewServer(reg)
	replies := runLines(t, srv,
		`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"c","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":0}}`,
		`{"jsonrpc":"2.0","id":"s-1","method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"known","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":3,"result":{}}`, // a client response: nothing to answer
	)
	if len(replies) != 3 {
		t.Fatalf("want 3 replies (initialize, tools/list, tools/call), got %d: %s", len(replies), replies)
	}
	for i, id := range []string{"0", `"s-1"`, "2"} {
		if got, code := errorCode(t, replies[i]); got != id || code != 0 {
			t.Errorf("reply %d: id %s code %d, want id %s and a result", i, got, code, id)
		}
	}
	if !strings.Contains(string(replies[2]), "ran known") {
		t.Errorf("the tool did not run: %s", replies[2])
	}
}

// A batch is a JSON array of requests; it used to come back as a single parse error. Each
// request gets its reply, notifications none, all in one array.
func TestBatchIsAnsweredAsABatch(t *testing.T) {
	srv := NewServer(tools.NewRegistry())
	replies := runLines(t, srv,
		`[{"jsonrpc":"2.0","id":1,"method":"ping"},{"jsonrpc":"2.0","method":"notifications/x"},{"jsonrpc":"2.0","id":2,"method":"nope"}]`)
	if len(replies) != 1 {
		t.Fatalf("want one reply line, got %d", len(replies))
	}
	var batch []json.RawMessage
	if err := json.Unmarshal(replies[0], &batch); err != nil || len(batch) != 2 {
		t.Fatalf("want an array of 2 responses, got %s", replies[0])
	}
	if id, code := errorCode(t, batch[0]); id != "1" || code != 0 {
		t.Errorf("batch[0]: %s", batch[0])
	}
	if id, code := errorCode(t, batch[1]); id != "2" || code != codeMethodNotFound {
		t.Errorf("batch[1]: %s", batch[1])
	}
	// A batch of notifications owes nothing.
	if replies := runLines(t, srv, `[{"jsonrpc":"2.0","method":"notifications/x"}]`); len(replies) != 0 {
		t.Errorf("a batch of notifications must get no reply, got %s", replies)
	}
}

// The registry is a map, so tools/list came back in a different order from call to call -- and
// a client sends that list at the head of every request, so the order is a cache key.
func TestToolsListIsSortedAndStable(t *testing.T) {
	reg := tools.NewRegistry()
	for _, n := range []string{"warnings_list", "read", "bug_list", "update_description", "bug_report"} {
		reg.Register(namedTool{n})
	}
	srv := NewServer(reg)
	var first string
	for i := 0; i < 20; i++ {
		resp := srv.handleListTools(json.RawMessage(fmt.Sprint(i)))
		var names []string
		for _, tl := range resp.Result.(ListToolsResult).Tools {
			names = append(names, tl.Name)
		}
		got := strings.Join(names, ",")
		if got != "bug_list,bug_report,read,update_description,warnings_list" {
			t.Fatalf("tools/list is not sorted by name: %s", got)
		}
		if first == "" {
			first = got
		} else if got != first {
			t.Fatalf("tools/list order changed between calls: %s vs %s", first, got)
		}
	}
}
