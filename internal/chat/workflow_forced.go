package chat

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"aracne/internal/llm"
	"aracne/internal/llm/tools"
	"aracne/internal/prompts"
	"aracne/internal/topology/domain"
)

func (m *Manager) StartForcedWorkflow(req WorkflowRequest) (string, error) {
	if req.SessionID == "" {
		return "", fmt.Errorf("session_id is required")
	}
	if req.Type == "" {
		return "", fmt.Errorf("workflow type is required")
	}
	if req.BatchSize <= 0 {
		req.BatchSize = configDescriptionBatchSize(m.config)
	}
	if req.Parallel <= 0 {
		req.Parallel = 2
	}
	tasks, err := m.buildWorkflowTaskSpecs(req)
	if err != nil {
		return "", err
	}
	if len(tasks) == 0 {
		return "", fmt.Errorf("workflow %q has no tasks to run", req.Type)
	}
	args, err := json.Marshal(createTasksRequest{WorkerCount: req.Parallel, Tasks: tasks})
	if err != nil {
		return "", err
	}
	tc := llm.ToolCall{ID: newID("call"), Type: "function", Function: llm.ToolCallFunction{Name: "CreateTasks", Arguments: string(args)}}
	if err := m.addForcedCreateTasksCall(req.SessionID, req.Type, tc); err != nil {
		return "", err
	}
	groupID, err := m.createWorkflowTaskGroupFromToolCall(req.SessionID, tc)
	if err != nil {
		m.appendToolResult(req.SessionID, tc, "Error: "+err.Error(), "error")
		return "", err
	}
	m.recordEvent(req.SessionID, "workflow_started", map[string]any{"job_id": groupID, "type": req.Type})
	go func() {
		result, status := m.runTaskGroup(req.SessionID, groupID)
		if status == "interrupted" {
			return
		}
		m.appendToolResult(req.SessionID, tc, result, status)
		m.startRun(req.SessionID)
	}()
	return groupID, nil
}

func (m *Manager) addForcedCreateTasksCall(sessionID, workflowType string, tc llm.ToolCall) error {
	now := time.Now().UTC()
	label := strings.ReplaceAll(workflowType, "_", " ")
	content := "Start " + label + " task workflow."
	userMsg := SessionMessage{ID: newID("msg"), Role: "user", Content: content, CreatedAt: now, Status: taskStatusCompleted}
	m.mu.Lock()
	session, err := m.getSessionLocked(sessionID)
	if err == nil {
		session.Messages = append(session.Messages, userMsg)
		session.LLMMessages = replaceSystemPrompt(session.LLMMessages, m.systemPrompt(session.Mode, session.Agent))
		session.LLMMessages = append(session.LLMMessages, llm.Message{Role: "user", Content: content})
		session.LLMMessages = append(session.LLMMessages, llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{tc}})
		session.UpdatedAt = now
		err = m.saveLocked(session)
	}
	m.mu.Unlock()
	if err != nil {
		return err
	}
	m.recordEvent(sessionID, "message", map[string]any{"message": userMsg})
	m.addToolMessage(sessionID, tc)
	return nil
}

func (m *Manager) buildWorkflowTaskSpecs(req WorkflowRequest) ([]createTaskSpec, error) {
	switch req.Type {
	case "descriptions":
		resources, err := m.undocumentedResources()
		if err != nil {
			return nil, err
		}
		batches := chunkResources(resources, req.BatchSize)
		result := make([]createTaskSpec, 0, len(batches))
		for _, batch := range batches {
			result = append(result, createTaskSpec{AgentKind: "descriptions-generation-executor", Prompt: m.descriptionWorkflowPrompt(batch), NeedResult: false})
		}
		return result, nil
	case "bug_hunter", "bug-hunter":
		prompt := "Explore the project topology for confirmed correctness, reliability, and security bugs. Read relevant resources, follow context as needed, and report each confirmed bug with bug_report. Do not report speculation or style issues. Return a concise summary of bugs reported."
		return []createTaskSpec{{AgentKind: "bug-hunter", Prompt: prompt, NeedResult: false}}, nil
	case "bug_judge", "bug-judge":
		pending, err := m.manager.ListBugs("", domain.BugPending)
		if err != nil {
			return nil, err
		}
		allBugs, err := m.manager.ListBugs("", "")
		if err != nil {
			return nil, err
		}
		sortKnownBugs(pending)
		byNode := make(map[string][]domain.KnownBug)
		for _, bug := range allBugs {
			byNode[bug.NodeID] = append(byNode[bug.NodeID], bug)
		}
		for nodeID := range byNode {
			sortKnownBugs(byNode[nodeID])
		}
		result := make([]createTaskSpec, 0, len(pending))
		for _, bug := range pending {
			prompt := m.bugJudgeWorkflowPrompt(bug, byNode[bug.NodeID])
			result = append(result, createTaskSpec{AgentKind: "bug-judge", Prompt: prompt, NeedResult: false})
		}
		return result, nil
	case "bug_solver", "bug-solver":
		bugs, err := m.manager.ListBugs("", domain.BugAcknowledged)
		if err != nil {
			return nil, err
		}
		sortKnownBugs(bugs)
		result := make([]createTaskSpec, 0, len(bugs))
		for _, bug := range bugs {
			prompt := m.bugSolverWorkflowPrompt(bug)
			result = append(result, createTaskSpec{AgentKind: "bug-solver", Prompt: prompt, NeedResult: false})
		}
		return result, nil
	default:
		return nil, fmt.Errorf("unknown workflow: %s", req.Type)
	}
}

func (m *Manager) descriptionWorkflowPrompt(batch []workflowResource) string {
	resources := make([]prompts.DescriptionResource, 0, len(batch))
	for _, res := range batch {
		desc := prompts.DescriptionResource{ID: res.ID, Name: res.Name, Kind: res.Kind}
		if len(batch) == 1 {
			desc.ReadOutput = m.readResourceForPrompt(res.ID)
		}
		resources = append(resources, desc)
	}
	return prompts.DescriptionsGenerationExecutorInput(resources)
}

func (m *Manager) bugJudgeWorkflowPrompt(assigned domain.KnownBug, nodeBugs []domain.KnownBug) string {
	var b strings.Builder
	b.WriteString("You are validating exactly one assigned pending bug. Do not triage unrelated bugs.\n\n")
	b.WriteString("Assigned bug:\n")
	b.WriteString(fmt.Sprintf("- ID: %s\n  Node: %s\n  State: %s\n  Description: %s\n\n", assigned.ID, assigned.NodeID, assigned.State, assigned.Description))
	b.WriteString("Other bugs on the same node, for duplicate checking only:\n")
	wroteOther := false
	for _, bug := range nodeBugs {
		if bug.ID == assigned.ID {
			continue
		}
		wroteOther = true
		b.WriteString(fmt.Sprintf("- ID: %s\n  State: %s\n  Description: %s\n", bug.ID, bug.State, bug.Description))
	}
	if !wroteOther {
		b.WriteString("- None\n")
	}
	b.WriteString("\nAssigned resource read:\n\n```text\n")
	b.WriteString(m.readResourceForPrompt(assigned.NodeID))
	b.WriteString("\n```\n\n")
	b.WriteString("Orders, in order:\n")
	b.WriteString("\n1. Check duplicates: if the assigned bug duplicates another bug listed above, delete the assigned bug with bug_delete.")
	b.WriteString("\n2. Investigate the bug: read relevant resources and explore around it. If the bug is not actually present right now, dismiss it with bug_dismiss.")
	b.WriteString("\n3. Question the bug: decide whether it is an intended feature. If it is intended and correctly implemented, dismiss it.")
	b.WriteString("\n4. Look for fallbacks: search for fallback handling elsewhere. If fallbacks cover the entire problem, dismiss it.")
	b.WriteString("\n5. If it is not a duplicate, actually exists, is not an intended correct feature, and has no complete fallback, acknowledge it with bug_acknowledge.\n")
	return b.String()
}

func (m *Manager) bugSolverWorkflowPrompt(bug domain.KnownBug) string {
	var b strings.Builder
	b.WriteString("Fix exactly this acknowledged bug and nothing else. Explore enough surrounding context to understand the full scope of the fix, then make the minimal correct change. Delete the bug report only after the fix is complete.\n\n")
	b.WriteString("Assigned bug:\n")
	b.WriteString(fmt.Sprintf("- ID: %s\n  Node: %s\n  State: %s\n  Description: %s\n\n", bug.ID, bug.NodeID, bug.State, bug.Description))
	b.WriteString("Assigned resource read:\n\n```text\n")
	b.WriteString(m.readResourceForPrompt(bug.NodeID))
	b.WriteString("\n```\n")
	return b.String()
}

func (m *Manager) readResourceForPrompt(resourceID string) string {
	payload, _ := json.Marshal(map[string]string{"resource_id": resourceID})
	text, err := tools.NewRead(m.manager).Run(payload)
	if err != nil {
		return "Error reading resource " + resourceID + ": " + err.Error()
	}
	return strings.TrimSpace(text)
}

func sortKnownBugs(bugs []domain.KnownBug) {
	sort.Slice(bugs, func(i, j int) bool {
		if bugs[i].NodeID != bugs[j].NodeID {
			return bugs[i].NodeID < bugs[j].NodeID
		}
		return bugs[i].ID < bugs[j].ID
	})
}
