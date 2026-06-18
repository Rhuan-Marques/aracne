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
			crossNode := dismissedPatternDigest(allBugs, bug.NodeID, maxCrossNodeDismissedPatterns)
			prompt := m.bugJudgeWorkflowPrompt(bug, byNode[bug.NodeID], crossNode)
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

func (m *Manager) bugJudgeWorkflowPrompt(assigned domain.KnownBug, nodeBugs []domain.KnownBug, crossNodeDismissed []domain.KnownBug) string {
	var dismissedSame, dupCandidates []domain.KnownBug
	for _, bug := range nodeBugs {
		if bug.ID == assigned.ID {
			continue
		}
		if bug.State == domain.BugDismissed {
			dismissedSame = append(dismissedSame, bug)
		} else {
			dupCandidates = append(dupCandidates, bug)
		}
	}

	var b strings.Builder
	b.WriteString("You are validating exactly one assigned pending bug. Do not triage unrelated bugs.\n\n")
	b.WriteString("Assigned bug:\n")
	b.WriteString(fmt.Sprintf("- ID: %s\n  Node: %s\n  State: %s\n  Description: %s\n\n", assigned.ID, assigned.NodeID, assigned.State, assigned.Description))

	b.WriteString("Known false-positive patterns (previously dismissed) — if the assigned bug is the same kind of issue, DELETE it (Rule 1):\n")
	wrotePattern := false
	for _, bug := range dismissedSame {
		wrotePattern = true
		b.WriteString(fmt.Sprintf("- [same node] %s\n", bug.Description))
	}
	for _, bug := range crossNodeDismissed {
		wrotePattern = true
		b.WriteString(fmt.Sprintf("- [%s] %s\n", bug.NodeID, bug.Description))
	}
	if !wrotePattern {
		b.WriteString("- None\n")
	}

	b.WriteString("\nDuplicate candidates (other live bugs on this node) — if the assigned bug duplicates one, DELETE the weaker description (Rule 2):\n")
	if len(dupCandidates) == 0 {
		b.WriteString("- None\n")
	} else {
		for _, bug := range dupCandidates {
			b.WriteString(fmt.Sprintf("- ID: %s\n  State: %s\n  Description: %s\n", bug.ID, bug.State, bug.Description))
		}
	}

	b.WriteString("\nAssigned resource read:\n\n```text\n")
	b.WriteString(m.readResourceForPrompt(assigned.NodeID))
	b.WriteString("\n```\n\n")
	b.WriteString("Orders, in order:\n")
	b.WriteString("\n1. Rule 1 — Dismissed pattern: if the assigned bug matches any known false-positive pattern above, delete it with bug_delete.")
	b.WriteString("\n2. Rule 2 — Duplicate: if it duplicates a live duplicate candidate above, delete the weaker bug with bug_delete and keep the clearest description.")
	b.WriteString("\n3. Rule 3 — False positive: read the resource and the context around it. If the code is correct, the bug is a misunderstanding, it describes intended behavior, or a complete fallback already handles it, dismiss it with bug_dismiss.")
	b.WriteString("\n4. Rule 4 — Genuine: if it is really present and could cause incorrect behavior, a crash, or a security problem, acknowledge it with bug_acknowledge.")
	b.WriteString("\n5. If genuinely unsure, take no action and report the bug as undecided.\n")
	return b.String()
}

// maxCrossNodeDismissedPatterns caps how many dismissed bugs from other nodes
// are surfaced to a judge task as general false-positive patterns.
const maxCrossNodeDismissedPatterns = 12

// dismissedPatternDigest returns a deterministic, capped sample of dismissed
// bugs from nodes other than excludeNode. Dismissed bugs are kept as a library
// of false-positive patterns, and those patterns generalize across nodes, so a
// judge validating one bug benefits from seeing them.
func dismissedPatternDigest(allBugs []domain.KnownBug, excludeNode string, limit int) []domain.KnownBug {
	var dismissed []domain.KnownBug
	for _, bug := range allBugs {
		if bug.State == domain.BugDismissed && bug.NodeID != excludeNode {
			dismissed = append(dismissed, bug)
		}
	}
	sortKnownBugs(dismissed)
	if limit > 0 && len(dismissed) > limit {
		dismissed = dismissed[:limit]
	}
	return dismissed
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
