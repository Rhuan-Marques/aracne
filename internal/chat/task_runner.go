package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/llm"
	"github.com/Rhuan-Marques/aracne/internal/llm/toolapi"
)

const (
	taskStatusPending   = "pending"
	taskStatusRunning   = "running"
	taskStatusCompleted = "completed"
	taskStatusFailed    = "failed"
)

// Specifies a task to be created with agent kind, prompt, and result requirement.
type createTaskSpec struct {
	AgentKind  string `json:"agent_kind"`
	Prompt     string `json:"prompt"`
	NeedResult bool   `json:"need_result"`
}

// Request payload specifying worker count and list of tasks to execute in parallel.
type createTasksRequest struct {
	WorkerCount int              `json:"worker_count"`
	Tasks       []createTaskSpec `json:"tasks"`
}

// Returns the set of agent kinds allowed for task creation via LLM tool calls.
func createTasksAgentKindSet() map[string]bool {
	return map[string]bool{"explorer": true}
}

// Returns the set of workflow agent kinds supported: descriptions-generation-executor, bug-hunter, bug-judge, and bug-solver.
func workflowAgentKindSet() map[string]bool {
	return map[string]bool{
		"descriptions-generation-executor": true,
		"bug-hunter":                       true,
		"bug-judge":                        true,
		"bug-solver":                       true,
	}
}

// Executes a CreateTasks tool call by creating and running the task group, then appending the result.
func (m *Manager) executeCreateTasksTool(sessionID string, tc llm.ToolCall) error {
	groupID, err := m.createTaskGroupFromToolCall(sessionID, tc)
	if err != nil {
		return err
	}
	result, status := m.runTaskGroup(sessionID, groupID)
	if status == "interrupted" {
		return nil
	}
	m.appendToolResult(sessionID, tc, result, status)
	return nil
}

// Creates a task group from an LLM tool call using the default set of allowed agent kinds.
func (m *Manager) createTaskGroupFromToolCall(sessionID string, tc llm.ToolCall) (string, error) {
	return m.createTaskGroupFromToolCallWithAllowed(sessionID, tc, createTasksAgentKindSet())
}

// Creates a task group from a tool call restricted to workflow-compatible agent kinds.
func (m *Manager) createWorkflowTaskGroupFromToolCall(sessionID string, tc llm.ToolCall) (string, error) {
	return m.createTaskGroupFromToolCallWithAllowed(sessionID, tc, workflowAgentKindSet())
}

// Parses a CreateTasks tool call and creates a task group with validated agent kinds against allowed set.
func (m *Manager) createTaskGroupFromToolCallWithAllowed(sessionID string, tc llm.ToolCall, allowedKinds map[string]bool) (string, error) {
	var req createTasksRequest
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &req); err != nil {
		return "", fmt.Errorf("invalid CreateTasks arguments: %w", err)
	}
	if len(req.Tasks) == 0 {
		return "", fmt.Errorf("CreateTasks requires at least one task")
	}
	workerCount := clampWorkerCount(req.WorkerCount)
	now := time.Now().UTC()
	group := TaskGroup{
		ID:          newID("taskgroup"),
		ToolCallID:  tc.ID,
		Status:      taskStatusPending,
		WorkerCount: workerCount,
		CreatedAt:   now,
		UpdatedAt:   now,
		Tasks:       make([]AgentTask, 0, len(req.Tasks)),
	}
	for _, requested := range req.Tasks {
		agentKind := normalizeAgentKindName(requested.AgentKind)
		if agentKind == "" {
			return "", fmt.Errorf("task agent_kind is required")
		}
		prompt := strings.TrimSpace(requested.Prompt)
		if prompt == "" {
			return "", fmt.Errorf("task prompt is required")
		}
		kind, err := resolveAgentKind(m.agentDir, agentKind)
		if err != nil {
			return "", err
		}
		if len(allowedKinds) > 0 && !allowedKinds[kind.Name] {
			return "", fmt.Errorf("agent kind %q is not available in this task context", kind.Name)
		}
		if _, err := m.toolsForAgentKind(kind); err != nil {
			return "", err
		}
		group.Tasks = append(group.Tasks, AgentTask{
			ID:         newID("task"),
			AgentKind:  kind.Name,
			Prompt:     prompt,
			NeedResult: requested.NeedResult,
			Status:     taskStatusPending,
			Messages: []SessionMessage{{
				ID:        newID("msg"),
				Role:      "user",
				Content:   prompt,
				CreatedAt: now,
				Status:    taskStatusCompleted,
			}},
		})
	}

	m.mu.Lock()
	session, err := m.getSessionLocked(sessionID)
	if err == nil {
		session.TaskGroups = append(session.TaskGroups, group)
		session.UpdatedAt = now
		err = m.saveLocked(session)
	}
	m.mu.Unlock()
	if err != nil {
		return "", err
	}
	m.recordTaskGroupStatus(sessionID, group)
	return group.ID, nil
}

// Executes all pending tasks in a group using a worker pool, returning status and result message on completion.
func (m *Manager) runTaskGroup(sessionID, groupID string) (string, string) {
	key := taskGroupRunKey(sessionID, groupID)
	m.mu.Lock()
	if m.runningTaskGroups[key] {
		m.mu.Unlock()
		return "Task group is already processing.", "error"
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.runningTaskGroups[key] = true
	m.taskGroupCancels[key] = cancel
	delete(m.stoppedTaskGroups, key)
	m.mu.Unlock()
	group, err := m.updateTaskGroupLocked(sessionID, groupID, func(group *TaskGroup) {
		group.Status = taskStatusRunning
		group.UpdatedAt = time.Now().UTC()
	})
	if err != nil {
		m.finishTaskGroupRun(key)
		return "Error: " + err.Error(), "error"
	}
	m.recordTaskGroupStatus(sessionID, group)
	defer m.finishTaskGroupRun(key)

	taskIDs := pendingTaskIDs(group)
	if len(taskIDs) > 0 {
		jobs := make(chan string)
		done := make(chan struct{})
		var wg sync.WaitGroup
		workers := group.WorkerCount
		if workers > len(taskIDs) {
			workers = len(taskIDs)
		}
		// TRACKED BY THE MANAGER AS WELL AS BY wg. The local WaitGroup is what `done` reports,
		// but the select below abandons it on ctx.Done: a cancelled group returns
		// "interrupted" while a worker is still inside runAgentTask, writing. Only
		// Manager.Close waits for that worker, so only Manager.Close can promise the store is
		// quiet.
		for i := 0; i < workers; i++ {
			wg.Add(1)
			if !m.goBackground(func() {
				defer wg.Done()
				for taskID := range jobs {
					if ctx.Err() != nil {
						return
					}
					m.runAgentTask(ctx, sessionID, groupID, taskID)
				}
			}) {
				wg.Done()
			}
		}
		if !m.goBackground(func() {
			defer close(jobs)
			for _, taskID := range taskIDs {
				select {
				case <-ctx.Done():
					return
				case jobs <- taskID:
				}
			}
		}) {
			close(jobs)
		}
		if !m.goBackground(func() {
			wg.Wait()
			close(done)
		}) {
			close(done)
		}
		select {
		case <-ctx.Done():
			m.recordTaskGroupProgress(sessionID, groupID)
			return "Task group interrupted.", "interrupted"
		case <-done:
		}
	}

	if ctx.Err() != nil || m.isTaskGroupStopped(key) {
		m.recordTaskGroupProgress(sessionID, groupID)
		return "Task group interrupted.", "interrupted"
	}
	group, err = m.updateTaskGroupLocked(sessionID, groupID, func(group *TaskGroup) {
		group.Status = taskStatusCompleted
		group.UpdatedAt = time.Now().UTC()
	})
	if err != nil {
		return "Error: " + err.Error(), "error"
	}
	m.recordTaskGroupStatus(sessionID, group)
	result, status := formatTaskGroupResult(group)
	return result, status
}

// Executes a sub-agent task, handling LLM calls, tool invocations, and result collection with a max iteration limit.
func (m *Manager) runAgentTask(ctx context.Context, sessionID, groupID, taskID string) {
	if ctx.Err() != nil {
		return
	}
	startedAt := time.Now().UTC()
	task, err := m.updateTaskLocked(sessionID, groupID, taskID, func(group *TaskGroup, task *AgentTask) {
		task.Status = taskStatusRunning
		task.Error = ""
		task.Result = ""
		task.StartedAt = &startedAt
		group.UpdatedAt = startedAt
	})
	if err != nil {
		m.recordTaskFailure(sessionID, groupID, taskID, err)
		return
	}
	m.recordTaskStatus(sessionID, groupID, task)

	kind, err := resolveAgentKind(m.agentDir, task.AgentKind)
	if err != nil {
		m.recordTaskFailure(sessionID, groupID, taskID, err)
		return
	}
	toolMap, err := m.toolsForAgentKind(kind)
	if err != nil {
		m.recordTaskFailure(sessionID, groupID, taskID, err)
		return
	}

	m.mu.Lock()
	session, sessionErr := m.getSessionLocked(sessionID)
	var settings ProviderSettings
	if sessionErr == nil {
		settings = m.providerForSession(session)
	}
	m.mu.Unlock()
	if sessionErr != nil {
		m.recordTaskFailure(sessionID, groupID, taskID, sessionErr)
		return
	}
	settings = m.chatAgentProviderOverride(kind.Name, settings)
	provider, err := newProvider(settings)
	if err != nil {
		m.recordTaskFailure(sessionID, groupID, taskID, err)
		return
	}

	messages := task.LLMMessages
	if len(messages) == 0 {
		messages = []llm.Message{{Role: "system", Content: kind.SystemPrompt}, {Role: "user", Content: task.Prompt}}
		m.setTaskLLMMessages(sessionID, groupID, taskID, messages)
	}
	toolDefs := toToolDefinitions(sortedTools(toolMap))
	for i := 0; i < 40; i++ {
		if ctx.Err() != nil {
			return
		}
		resp, err := streamChat(ctx, provider, messages, toolDefs, func(event llm.StreamEvent) {
			payload := map[string]any{"group_id": groupID, "task_id": taskID}
			if event.Content != "" {
				payload["content"] = event.Content
			}
			if event.Reasoning != "" {
				payload["reasoning"] = event.Reasoning
			}
			if len(payload) > 2 {
				m.recordEvent(sessionID, "task_delta", payload)
			}
		})
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			m.recordTaskFailure(sessionID, groupID, taskID, fmt.Errorf("LLM call failed: %w", err))
			return
		}
		if ctx.Err() != nil {
			return
		}

		assistantMsg := llm.Message{Role: "assistant", Content: resp.Content,
			ToolCalls: resp.ToolCalls, Thinking: resp.Thinking}
		messages = append(messages, assistantMsg)
		m.appendTaskAssistantMessage(sessionID, groupID, taskID, resp.Content, resp.Reasoning, messages)

		if len(resp.ToolCalls) == 0 {
			result := strings.TrimSpace(resp.Content)
			if result == "" {
				m.recordTaskFailure(sessionID, groupID, taskID, fmt.Errorf("sub-agent returned an empty final answer"))
				return
			}
			completedAt := time.Now().UTC()
			task, err := m.updateTaskLocked(sessionID, groupID, taskID, func(group *TaskGroup, task *AgentTask) {
				task.Status = taskStatusCompleted
				task.Result = result
				task.Error = ""
				task.CompletedAt = &completedAt
				task.LLMMessages = append([]llm.Message(nil), messages...)
				group.UpdatedAt = completedAt
			})
			if err != nil {
				m.recordTaskFailure(sessionID, groupID, taskID, err)
				return
			}
			m.recordTaskStatus(sessionID, groupID, task)
			return
		}

		for _, tc := range resp.ToolCalls {
			if ctx.Err() != nil {
				return
			}
			m.appendTaskToolCall(sessionID, groupID, taskID, tc)
			result, status := runAllowedTaskTool(toolMap, tc)
			if ctx.Err() != nil {
				return
			}
			messages = append(messages, llm.Message{Role: "tool", Content: result, ToolCallID: tc.ID})
			m.appendTaskToolResult(sessionID, groupID, taskID, tc.ID, result, status, messages)
		}
	}
	m.recordTaskFailure(sessionID, groupID, taskID, fmt.Errorf("sub-agent exceeded max iterations"))
}

// Executes a tool from an allowed tool map and returns its result or error status.
func runAllowedTaskTool(toolMap map[string]toolapi.Tool, tc llm.ToolCall) (string, string) {
	tool, ok := toolMap[tc.Function.Name]
	if !ok {
		return fmt.Sprintf("Error: unknown tool %q", tc.Function.Name), "error"
	}
	result, err := tool.Run(json.RawMessage(tc.Function.Arguments))
	if err != nil {
		if strings.TrimSpace(result) != "" {
			result = result + "\n" + err.Error()
		} else {
			result = err.Error()
		}
		return "Error: " + result, "error"
	}
	return result, taskStatusCompleted
}

// chatAgentProviderOverride layers a chat sub-agent's per-agent model and
// thinking budget (from viz.chat.agents.<name>) on top of the session provider,
// so a judgment-heavy agent like bug-judge can run on a stronger model with
// extended reasoning while cheaper agents keep the session default.
func (m *Manager) chatAgentProviderOverride(agentName string, settings ProviderSettings) ProviderSettings {
	if m.config == nil {
		return settings
	}
	ag, ok := m.config.Viz.Chat.Agents.Agents[agentName]
	if !ok {
		return settings
	}
	if model := chatAgentModelID(ag.Model); model != "" {
		settings.Model = model
	}
	if budget := ag.Params["thinking"]; budget > 0 {
		settings.ThinkingBudget = budget
	}
	return settings
}

// chatAgentModelID strips an optional "provider/" prefix from a configured
// model id, matching how the chat-wide model selection is resolved.
func chatAgentModelID(model string) string {
	model = strings.TrimSpace(model)
	if idx := strings.LastIndex(model, "/"); idx >= 0 {
		model = strings.TrimSpace(model[idx+1:])
	}
	return model
}

// Resolves the set of available tools for an agent kind from config or agent markdown, with validation.
func (m *Manager) toolsForAgentKind(kind AgentKind) (map[string]toolapi.Tool, error) {
	// The config's viz.chat.agents.<name>.tools is authoritative; the agent
	// markdown's tools: frontmatter is the fallback. viz.chat never inherits
	// from the llm section.
	toolNames := kind.Tools
	if m.config != nil {
		if ag, ok := m.config.Viz.Chat.Agents.Agents[kind.Name]; ok && len(ag.Tools) > 0 {
			toolNames = ag.Tools
		}
	}
	allowed := make(map[string]bool, len(toolNames))
	for _, name := range toolNames {
		name = strings.TrimSpace(name)
		if name != "" {
			allowed[name] = true
		}
	}
	toolMap := toolMap(m.agentToolRegistry, allowed)
	for name := range allowed {
		if _, ok := toolMap[name]; !ok {
			return nil, fmt.Errorf("agent kind %q references unavailable tool %q", kind.Name, name)
		}
	}
	return toolMap, nil
}

// Returns tools from a map as a sorted slice by name.
func sortedTools(toolMap map[string]toolapi.Tool) []toolapi.Tool {
	names := make([]string, 0, len(toolMap))
	for name := range toolMap {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]toolapi.Tool, 0, len(names))
	for _, name := range names {
		result = append(result, toolMap[name])
	}
	return result
}

// Sets LLM messages on a task and updates the task group's timestamp.
func (m *Manager) setTaskLLMMessages(sessionID, groupID, taskID string, messages []llm.Message) {
	_, _ = m.updateTaskLocked(sessionID, groupID, taskID, func(group *TaskGroup, task *AgentTask) {
		task.LLMMessages = append([]llm.Message(nil), messages...)
		group.UpdatedAt = time.Now().UTC()
	})
}

// Adds an assistant message with content and reasoning to a task's message history and LLM context.
func (m *Manager) appendTaskAssistantMessage(sessionID, groupID, taskID, content, reasoning string, messages []llm.Message) {
	if strings.TrimSpace(content) == "" && strings.TrimSpace(reasoning) == "" {
		m.setTaskLLMMessages(sessionID, groupID, taskID, messages)
		return
	}
	msg := SessionMessage{ID: newID("msg"), Role: "assistant", Content: content, Reasoning: reasoning, CreatedAt: time.Now().UTC(), Status: taskStatusCompleted}
	task, err := m.updateTaskLocked(sessionID, groupID, taskID, func(group *TaskGroup, task *AgentTask) {
		task.Messages = append(task.Messages, msg)
		task.LLMMessages = append([]llm.Message(nil), messages...)
		group.UpdatedAt = msg.CreatedAt
	})
	if err == nil {
		m.recordEvent(sessionID, "task_message", map[string]any{"group_id": groupID, "task_id": taskID, "message": msg, "task": task})
	}
}

// Appends a tool call message to a task's message history with pending status.
func (m *Manager) appendTaskToolCall(sessionID, groupID, taskID string, tc llm.ToolCall) {
	input := jsonRaw{}
	_ = json.Unmarshal([]byte(tc.Function.Arguments), &input)
	msg := SessionMessage{ID: newID("tool"), Role: "tool", CreatedAt: time.Now().UTC(), Status: taskStatusPending, ToolCallID: tc.ID, ToolName: tc.Function.Name, ToolInput: input}
	task, err := m.updateTaskLocked(sessionID, groupID, taskID, func(group *TaskGroup, task *AgentTask) {
		task.Messages = append(task.Messages, msg)
		group.UpdatedAt = msg.CreatedAt
	})
	if err == nil {
		m.recordEvent(sessionID, "task_message", map[string]any{"group_id": groupID, "task_id": taskID, "message": msg, "task": task})
	}
}

// Records a tool execution result in a task's message history and updates the corresponding tool call status.
func (m *Manager) appendTaskToolResult(sessionID, groupID, taskID, toolCallID, result, status string, messages []llm.Message) {
	task, err := m.updateTaskLocked(sessionID, groupID, taskID, func(group *TaskGroup, task *AgentTask) {
		task.LLMMessages = append([]llm.Message(nil), messages...)
		for i := len(task.Messages) - 1; i >= 0; i-- {
			if task.Messages[i].ToolCallID == toolCallID {
				task.Messages[i].ToolOutput = result
				task.Messages[i].Status = status
				break
			}
		}
		group.UpdatedAt = time.Now().UTC()
	})
	if err == nil {
		m.recordEvent(sessionID, "task_status", map[string]any{"group_id": groupID, "task_id": taskID, "task": task})
	}
}

// Marks a task as failed with an error message and records its final status.
func (m *Manager) recordTaskFailure(sessionID, groupID, taskID string, taskErr error) {
	completedAt := time.Now().UTC()
	task, err := m.updateTaskLocked(sessionID, groupID, taskID, func(group *TaskGroup, task *AgentTask) {
		task.Status = taskStatusFailed
		task.Error = taskErr.Error()
		task.CompletedAt = &completedAt
		group.UpdatedAt = completedAt
	})
	if err == nil {
		m.recordTaskStatus(sessionID, groupID, task)
	}
}

// Applies an update function to a task within a task group and persists changes, updating timestamps.
func (m *Manager) updateTaskLocked(sessionID, groupID, taskID string, update func(*TaskGroup, *AgentTask)) (AgentTask, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, err := m.getSessionLocked(sessionID)
	if err != nil {
		return AgentTask{}, err
	}
	group, _, err := findTaskGroup(session, groupID)
	if err != nil {
		return AgentTask{}, err
	}
	for i := range group.Tasks {
		if group.Tasks[i].ID == taskID {
			update(group, &group.Tasks[i])
			group.UpdatedAt = time.Now().UTC()
			session.UpdatedAt = group.UpdatedAt
			if err := m.saveLocked(session); err != nil {
				return AgentTask{}, err
			}
			return group.Tasks[i], nil
		}
	}
	return AgentTask{}, fmt.Errorf("task not found: %s", taskID)
}

// Updates a task group within a session via a callback function and persists the change.
func (m *Manager) updateTaskGroupLocked(sessionID, groupID string, update func(*TaskGroup)) (TaskGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, err := m.getSessionLocked(sessionID)
	if err != nil {
		return TaskGroup{}, err
	}
	group, _, err := findTaskGroup(session, groupID)
	if err != nil {
		return TaskGroup{}, err
	}
	update(group)
	session.UpdatedAt = group.UpdatedAt
	if err := m.saveLocked(session); err != nil {
		return TaskGroup{}, err
	}
	return *group, nil
}

// Locates a task group by ID in a session and returns the group, its index, or an error if not found.
func findTaskGroup(session *Session, groupID string) (*TaskGroup, int, error) {
	for i := range session.TaskGroups {
		if session.TaskGroups[i].ID == groupID {
			return &session.TaskGroups[i], i, nil
		}
	}
	return nil, -1, fmt.Errorf("task group not found: %s", groupID)
}

// Extracts IDs of pending or running tasks from a task group.
func pendingTaskIDs(group TaskGroup) []string {
	ids := make([]string, 0, len(group.Tasks))
	for _, task := range group.Tasks {
		if task.Status == taskStatusPending || task.Status == taskStatusRunning {
			ids = append(ids, task.ID)
		}
	}
	return ids
}

// Removes task group tracking data from running, cancels, and stopped maps.
func (m *Manager) finishTaskGroupRun(key string) {
	m.mu.Lock()
	delete(m.runningTaskGroups, key)
	delete(m.taskGroupCancels, key)
	delete(m.stoppedTaskGroups, key)
	m.mu.Unlock()
}

// Checks whether a task group has been marked as stopped.
func (m *Manager) isTaskGroupStopped(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stoppedTaskGroups[key]
}

// Generates a unique key combining session and group IDs.
func taskGroupRunKey(sessionID, groupID string) string {
	return sessionID + ":" + groupID
}

// Constrains worker count to valid range [2, 8], defaulting to 2 if invalid.
func clampWorkerCount(workerCount int) int {
	if workerCount <= 0 {
		return 2
	}
	if workerCount > 8 {
		return 8
	}
	return workerCount
}

// Records a task status event and updates task group progress tracking.
func (m *Manager) recordTaskStatus(sessionID, groupID string, task AgentTask) {
	m.recordEvent(sessionID, "task_status", map[string]any{"group_id": groupID, "task_id": task.ID, "task": task})
	m.recordTaskGroupProgress(sessionID, groupID)
}

// Records a task group's status event with completion and failure counts.
func (m *Manager) recordTaskGroupStatus(sessionID string, group TaskGroup) {
	completed, failed := taskGroupCounts(group)
	m.recordEvent(sessionID, "task_group_status", map[string]any{
		"group_id":     group.ID,
		"status":       group.Status,
		"worker_count": group.WorkerCount,
		"completed":    completed + failed,
		"failed":       failed,
		"total":        len(group.Tasks),
		"task_group":   group,
	})
}

// Fetches the latest task group state and records its status update.
func (m *Manager) recordTaskGroupProgress(sessionID, groupID string) {
	m.mu.Lock()
	session, err := m.getSessionLocked(sessionID)
	var group TaskGroup
	if err == nil {
		if found, _, findErr := findTaskGroup(session, groupID); findErr == nil {
			group = *found
		} else {
			err = findErr
		}
	}
	m.mu.Unlock()
	if err == nil {
		m.recordTaskGroupStatus(sessionID, group)
	}
}

// Counts completed and failed tasks within a task group.
func taskGroupCounts(group TaskGroup) (completed, failed int) {
	for _, task := range group.Tasks {
		switch task.Status {
		case taskStatusCompleted:
			completed++
		case taskStatusFailed:
			failed++
		}
	}
	return completed, failed
}

// Formats a task group's results into a structured text report with status, successful/failed counts, task results, and failures.
func formatTaskGroupResult(group TaskGroup) (string, string) {
	completed, failed := taskGroupCounts(group)
	status := taskStatusCompleted
	if completed == 0 && failed > 0 {
		status = "error"
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("task_group_id: %s\n", group.ID))
	b.WriteString(fmt.Sprintf("tasks_successful: %d\n", completed))
	b.WriteString(fmt.Sprintf("tasks_failed: %d\n", failed))

	wroteResultsHeader := false
	for _, task := range group.Tasks {
		if !task.NeedResult || task.Status != taskStatusCompleted {
			continue
		}
		if !wroteResultsHeader {
			b.WriteString("\nresults:\n")
			wroteResultsHeader = true
		}
		b.WriteString(fmt.Sprintf("- task_id: %s\n  agent_kind: %s\n  output:\n", task.ID, task.AgentKind))
		for _, line := range strings.Split(strings.TrimSpace(task.Result), "\n") {
			b.WriteString("    ")
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}

	if failed > 0 {
		b.WriteString("\nfailures:\n")
		for _, task := range group.Tasks {
			if task.Status != taskStatusFailed {
				continue
			}
			b.WriteString(fmt.Sprintf("- task_id: %s\n  agent_kind: %s\n  error: %s\n", task.ID, task.AgentKind, task.Error))
		}
	}
	return strings.TrimSpace(b.String()), status
}
