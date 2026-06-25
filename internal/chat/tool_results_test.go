package chat

import (
	"testing"

	"aracne/internal/llm"
)

func assistantWithCalls(ids ...string) llm.Message {
	msg := llm.Message{Role: "assistant", Content: ""}
	for _, id := range ids {
		msg.ToolCalls = append(msg.ToolCalls, llm.ToolCall{ID: id, Type: "function"})
	}
	return msg
}

func toolResult(id string) llm.Message {
	return llm.Message{Role: "tool", Content: "ok", ToolCallID: id}
}

func TestEnsureToolResultsComplete_BackfillsMissing(t *testing.T) {
	session := &Session{LLMMessages: []llm.Message{
		{Role: "user", Content: "hi"},
		assistantWithCalls("call_1", "call_2"),
		toolResult("call_1"),
		// call_2 has no result -> orphaned (e.g. paused sibling / interruption).
	}}

	if !ensureToolResultsComplete(session) {
		t.Fatalf("expected ensureToolResultsComplete to report a change")
	}

	last := session.LLMMessages[len(session.LLMMessages)-1]
	if last.Role != "tool" || last.ToolCallID != "call_2" {
		t.Fatalf("expected a backfilled tool result for call_2, got role=%q id=%q", last.Role, last.ToolCallID)
	}

	// Exactly one synthetic result should have been added (call_1 was untouched).
	results := map[string]int{}
	for _, m := range session.LLMMessages {
		if m.Role == "tool" {
			results[m.ToolCallID]++
		}
	}
	if results["call_1"] != 1 || results["call_2"] != 1 {
		t.Fatalf("expected one result per tool call, got %v", results)
	}
}

func TestEnsureToolResultsComplete_NoopWhenComplete(t *testing.T) {
	session := &Session{LLMMessages: []llm.Message{
		assistantWithCalls("call_1", "call_2"),
		toolResult("call_1"),
		toolResult("call_2"),
	}}
	before := len(session.LLMMessages)

	if ensureToolResultsComplete(session) {
		t.Fatalf("expected no change for a fully answered turn")
	}
	if len(session.LLMMessages) != before {
		t.Fatalf("expected message count to stay %d, got %d", before, len(session.LLMMessages))
	}
}

func TestEnsureToolResultsComplete_NoToolCalls(t *testing.T) {
	session := &Session{LLMMessages: []llm.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
	}}
	if ensureToolResultsComplete(session) {
		t.Fatalf("expected no change when no assistant turn carries tool calls")
	}
}

func TestEnsureToolResultsComplete_IgnoresPastTurns(t *testing.T) {
	// The tool calls here are followed by a newer assistant message, so the turn
	// is already complete/moved-on and must not be backfilled at the tail.
	session := &Session{LLMMessages: []llm.Message{
		assistantWithCalls("call_1"),
		toolResult("call_1"),
		{Role: "assistant", Content: "done"},
	}}
	before := len(session.LLMMessages)
	if ensureToolResultsComplete(session) {
		t.Fatalf("expected no change for a past, completed turn")
	}
	if len(session.LLMMessages) != before {
		t.Fatalf("expected message count to stay %d, got %d", before, len(session.LLMMessages))
	}
}
