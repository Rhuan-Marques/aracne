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

// Launches a workflow (descriptions/bug_hunter/bug_judge/bug_solver) by building tasks, creating a tool call, and running task groups with optional follow-up rounds.
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
	hunterPrevPending := 0
	if isHunterWorkflow(req.Type) {
		hunterPrevPending = m.pendingBugCount()
	}
	descPrevRemaining := 0
	if isDescriptionsWorkflow(req.Type) {
		descPrevRemaining = m.undocumentedCount()
	}
	tc, groupID, err := m.startForcedRound(req, tasks)
	if err != nil {
		return "", err
	}
	m.recordEvent(req.SessionID, "workflow_started", map[string]any{"job_id": groupID, "type": req.Type})
	go func() {
		result, status := m.runTaskGroup(req.SessionID, groupID)
		if status == "interrupted" {
			return
		}
		m.appendToolResult(req.SessionID, tc, result, status)
		if isHunterWorkflow(req.Type) {
			m.runHunterFollowupRounds(req, hunterPrevPending)
		}
		if isDescriptionsWorkflow(req.Type) {
			m.runDescriptionsFollowupRounds(req, descPrevRemaining)
		}
		m.startRun(req.SessionID)
	}()
	return groupID, nil
}

// startForcedRound builds one CreateTasks tool call + task group for the given
// specs and returns them ready to run. Shared by the initial workflow launch
// and the bug-hunter follow-up rounds.
func (m *Manager) startForcedRound(req WorkflowRequest, tasks []createTaskSpec) (llm.ToolCall, string, error) {
	args, err := json.Marshal(createTasksRequest{WorkerCount: req.Parallel, Tasks: tasks})
	if err != nil {
		return llm.ToolCall{}, "", err
	}
	tc := llm.ToolCall{ID: newID("call"), Type: "function", Function: llm.ToolCallFunction{Name: "CreateTasks", Arguments: string(args)}}
	if err := m.addForcedCreateTasksCall(req.SessionID, req.Type, tc); err != nil {
		return llm.ToolCall{}, "", err
	}
	groupID, err := m.createWorkflowTaskGroupFromToolCall(req.SessionID, tc)
	if err != nil {
		m.appendToolResult(req.SessionID, tc, "Error: "+err.Error(), "error")
		return llm.ToolCall{}, "", err
	}
	return tc, groupID, nil
}

// maxHunterRounds caps how many full bug-hunter fan-out passes a single workflow
// runs before stopping, regardless of whether bugs are still being found.
const maxHunterRounds = 3

// Returns true if the workflow type is "bug_hunter" or "bug-hunter".
func isHunterWorkflow(typ string) bool {
	return typ == "bug_hunter" || typ == "bug-hunter"
}

// Returns the count of bugs currently in pending status.
func (m *Manager) pendingBugCount() int {
	bugs, err := m.manager.ListBugs("", domain.BugPending)
	if err != nil {
		return 0
	}
	return len(bugs)
}

// runHunterFollowupRounds re-runs the hunter fan-out (loop-until-dry) until a
// round adds no new pending bugs, capped at maxHunterRounds total rounds. Round
// 1 has already run; prevPending is the pending-bug count taken before it. Each
// round re-reads the current pending bugs, so its hunters skip what earlier
// rounds already reported.
func (m *Manager) runHunterFollowupRounds(req WorkflowRequest, prevPending int) {
	for round := 2; round <= maxHunterRounds; round++ {
		cur := m.pendingBugCount()
		if cur <= prevPending {
			return // the previous round found nothing new
		}
		prevPending = cur
		tasks, err := m.buildWorkflowTaskSpecs(req)
		if err != nil || len(tasks) == 0 {
			return
		}
		tc, groupID, err := m.startForcedRound(req, tasks)
		if err != nil {
			return
		}
		result, status := m.runTaskGroup(req.SessionID, groupID)
		if status == "interrupted" {
			return
		}
		m.appendToolResult(req.SessionID, tc, result, status)
	}
}

// maxDescriptionRounds caps how many full description fan-out passes a single
// workflow runs before stopping.
const maxDescriptionRounds = 3

// Returns true if the workflow type is "descriptions".
func isDescriptionsWorkflow(typ string) bool {
	return typ == "descriptions"
}

// Returns the count of resources lacking descriptions in the configured target kinds.
func (m *Manager) undocumentedCount() int {
	res, err := m.undocumentedResources()
	if err != nil {
		return 0
	}
	return len(res)
}

// runDescriptionsFollowupRounds re-runs the executor fan-out (loop-until-dry)
// for any still-undocumented resources, capped at maxDescriptionRounds total
// rounds and stopping as soon as a round makes no progress (some resources may
// be undescribable). Round 1 has already run; prevRemaining is the undocumented
// count taken before it. Each round re-reads the current undocumented set, so
// its executors only cover what is still missing.
func (m *Manager) runDescriptionsFollowupRounds(req WorkflowRequest, prevRemaining int) {
	for round := 2; round <= maxDescriptionRounds; round++ {
		cur := m.undocumentedCount()
		if cur == 0 || cur >= prevRemaining {
			return // nothing left, or the previous round made no progress
		}
		prevRemaining = cur
		tasks, err := m.buildWorkflowTaskSpecs(req)
		if err != nil || len(tasks) == 0 {
			return
		}
		tc, groupID, err := m.startForcedRound(req, tasks)
		if err != nil {
			return
		}
		result, status := m.runTaskGroup(req.SessionID, groupID)
		if status == "interrupted" {
			return
		}
		m.appendToolResult(req.SessionID, tc, result, status)
	}
}

// Appends a synthetic user message and assistant tool call to a session's transcript for workflow task creation.
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

// Builds task specs for workflow agents (descriptions, bug-hunter, bug-judge, or bug-solver) based on request type.
func (m *Manager) buildWorkflowTaskSpecs(req WorkflowRequest) ([]createTaskSpec, error) {
	switch req.Type {
	case "descriptions":
		resources, err := m.undocumentedResources()
		if err != nil {
			return nil, err
		}
		topo, _ := m.manager.ReadAll()
		batches := chunkResources(resources, req.BatchSize)
		result := make([]createTaskSpec, 0, len(batches))
		for _, batch := range batches {
			result = append(result, createTaskSpec{AgentKind: "descriptions-generation-executor", Prompt: m.descriptionWorkflowPrompt(batch, topo), NeedResult: false})
		}
		return result, nil
	case "bug_hunter", "bug-hunter":
		resources, err := m.inspectableResources()
		if err != nil {
			return nil, err
		}
		pending, err := m.manager.ListBugs("", domain.BugPending)
		if err != nil {
			return nil, err
		}
		reportedByNode := make(map[string][]domain.KnownBug)
		for _, bug := range pending {
			reportedByNode[bug.NodeID] = append(reportedByNode[bug.NodeID], bug)
		}
		batches := chunkResources(resources, req.BatchSize)
		result := make([]createTaskSpec, 0, len(batches))
		for _, batch := range batches {
			result = append(result, createTaskSpec{AgentKind: "bug-hunter", Prompt: m.bugHunterWorkflowPrompt(batch, reportedByNode), NeedResult: false})
		}
		return result, nil
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

// Builds a prompt for the description generation executor with resources, style exemplars, and read output.
func (m *Manager) descriptionWorkflowPrompt(batch []workflowResource, topo *domain.Topology) string {
	resources := make([]prompts.DescriptionResource, 0, len(batch))
	ids := make([]string, 0, len(batch))
	for _, res := range batch {
		resources = append(resources, prompts.DescriptionResource{
			ID:         res.ID,
			Name:       res.Name,
			Kind:       res.Kind,
			ReadOutput: m.readResourceForPrompt(res.ID),
		})
		ids = append(ids, res.ID)
	}
	var exemplars []prompts.DescriptionExemplar
	if m.config != nil && m.config.Descriptions.StyleExemplars > 0 {
		exemplars = prompts.BuildDescriptionExemplars(topo, ids, m.config.Descriptions.StyleExemplars)
	}
	return prompts.DescriptionsGenerationExecutorInput(resources, exemplars)
}

// Generates prompt for bug-hunting workflow, assigning resources and tracking already-reported bugs.
func (m *Manager) bugHunterWorkflowPrompt(batch []workflowResource, reportedByNode map[string][]domain.KnownBug) string {
	var b strings.Builder
	b.WriteString("Inspect only these assigned resources for confirmed correctness, reliability, and security bugs. Read each one and the context it touches, and report every confirmed bug with bug_report on the node at the root of the issue — the resource whose code must change to fix it, not a node that merely exhibits the symptom (precise node_id + concrete scenario). Do not report style issues or speculation.\n\n")
	b.WriteString("Assigned resources:\n")
	for _, res := range batch {
		b.WriteString(fmt.Sprintf("- ID: %s\n  Name: %s\n  Kind: %s\n", res.ID, res.Name, res.Kind))
	}
	var already []string
	for _, res := range batch {
		for _, bug := range reportedByNode[res.ID] {
			already = append(already, fmt.Sprintf("- [%s] %s", bug.NodeID, bug.Description))
		}
	}
	if len(already) > 0 {
		b.WriteString("\nAlready reported on these resources — do NOT report these again, only new distinct bugs:\n")
		b.WriteString(strings.Join(already, "\n"))
		b.WriteString("\n")
	}
	b.WriteString("\nMake more than one pass; stop when a pass finds nothing new. Return a concise summary of the bugs you reported.\n")
	return b.String()
}

// Generates prompt for bug validation workflow with rules for dismissing false positives, duplicates, and confirming genuine bugs.
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

// Generates a workflow prompt for the bug-solver agent to fix an acknowledged bug with minimal, correct changes.
func (m *Manager) bugSolverWorkflowPrompt(bug domain.KnownBug) string {
	var b strings.Builder
	b.WriteString("Fix exactly this acknowledged bug and nothing else: find the root cause, make the minimal correct change, verify it, then delete the bug report.\n\n")
	b.WriteString("Assigned bug:\n")
	b.WriteString(fmt.Sprintf("- ID: %s\n  Node: %s\n  State: %s\n  Description: %s\n\n", bug.ID, bug.NodeID, bug.State, bug.Description))
	b.WriteString("Assigned resource read:\n\n```text\n")
	b.WriteString(m.readResourceForPrompt(bug.NodeID))
	b.WriteString("\n```\n\n")
	b.WriteString("Orders, in order:\n")
	b.WriteString("\n1. Investigate: explore enough surrounding context (callers, implementations, related types) to understand the full scope of the fix.")
	b.WriteString("\n2. Fix: apply the minimal correct change at every site it is needed; preserve the existing style.")
	b.WriteString("\n3. Verify: re-read what you changed and heed the topology warnings that edit/write return — a new use_missing_node means you broke a reference; you may also call warnings_list.")
	b.WriteString("\n4. If an edit fails because another agent changed the file while your edit was queued, re-read the resource and retry against the current text.")
	b.WriteString("\n5. Delete the bug report with bug_delete once the fix is complete. If it cannot be fixed, leave it in place and explain why.\n")
	return b.String()
}

// Fetches resource content via the Read tool and returns it as a string for LLM prompt injection.
func (m *Manager) readResourceForPrompt(resourceID string) string {
	payload, _ := json.Marshal(map[string]string{"resource_id": resourceID})
	text, err := tools.NewRead(m.manager).Run(payload)
	if err != nil {
		return "Error reading resource " + resourceID + ": " + err.Error()
	}
	return strings.TrimSpace(text)
}

// Sorts known bugs by NodeID then by ID.
func sortKnownBugs(bugs []domain.KnownBug) {
	sort.Slice(bugs, func(i, j int) bool {
		if bugs[i].NodeID != bugs[j].NodeID {
			return bugs[i].NodeID < bugs[j].NodeID
		}
		return bugs[i].ID < bugs[j].ID
	})
}
