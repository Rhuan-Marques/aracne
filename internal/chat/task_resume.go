package chat

import (
	"fmt"

	"aracne/internal/llm"
)

// Resumes processing a paused task group by running it asynchronously and appending results to the session message thread.
func (m *Manager) ResumeTaskGroup(sessionID, groupID string) (*Session, error) {
	m.mu.Lock()
	session, err := m.getSessionLocked(sessionID)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	group, _, err := findTaskGroup(session, groupID)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	if m.runningTaskGroups[taskGroupRunKey(sessionID, groupID)] {
		m.mu.Unlock()
		return nil, fmt.Errorf("task group is already processing")
	}
	if group.Status == taskStatusCompleted {
		if taskGroupToolResultPresent(session, group.ToolCallID) {
			m.mu.Unlock()
			return nil, fmt.Errorf("task group is already completed")
		}
		groupCopy := *group
		toolCallID := group.ToolCallID
		m.mu.Unlock()
		result, status := formatTaskGroupResult(groupCopy)
		tc := llm.ToolCall{ID: toolCallID, Type: "function", Function: llm.ToolCallFunction{Name: "CreateTasks"}}
		m.appendToolResult(sessionID, tc, result, status)
		m.startRun(sessionID)
		return m.GetSession(sessionID)
	}
	if group.ToolCallID == "" {
		m.mu.Unlock()
		return nil, fmt.Errorf("task group has no tool call to resume")
	}
	toolCallID := group.ToolCallID
	m.mu.Unlock()

	go func() {
		result, status := m.runTaskGroup(sessionID, groupID)
		if status == "interrupted" {
			return
		}
		tc := llm.ToolCall{ID: toolCallID, Type: "function", Function: llm.ToolCallFunction{Name: "CreateTasks"}}
		m.appendToolResult(sessionID, tc, result, status)
		m.startRun(sessionID)
	}()
	return m.GetSession(sessionID)
}

// Checks if a tool call has a completed result in the session messages.
func taskGroupToolResultPresent(session *Session, toolCallID string) bool {
	for _, msg := range session.Messages {
		if msg.ToolCallID == toolCallID && msg.Status != "" && msg.Status != taskStatusPending && msg.ToolOutput != "" {
			return true
		}
	}
	return false
}
