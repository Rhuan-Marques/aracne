package chat

import (
	"fmt"
	"strings"
	"sync"

	"aracne/internal/helper"
	"aracne/internal/llm/agent"
	"aracne/internal/llm/tools"
	"aracne/internal/prompts"
	"aracne/internal/topology/domain"
)

type workflowResource struct {
	ID   string
	Name string
	Kind domain.ResourceKind
}

func (m *Manager) StartWorkflow(req WorkflowRequest) (string, error) {
	return m.StartForcedWorkflow(req)
}

func (m *Manager) runWorkflow(jobID string, req WorkflowRequest) {
	var err error
	switch req.Type {
	case "descriptions":
		err = m.runDescriptionsWorkflow(jobID, req)
	case "bug_hunter":
		err = m.runBugHunterWorkflow(jobID, req)
	case "bug_judge":
		err = m.runBugJudgeWorkflow(jobID, req)
	case "bug_solver":
		err = m.runBugSolverWorkflow(jobID, req)
	default:
		err = fmt.Errorf("unknown workflow: %s", req.Type)
	}
	if err != nil {
		m.recordEvent(req.SessionID, "workflow_failed", map[string]any{"job_id": jobID, "type": req.Type, "error": err.Error()})
		return
	}
	m.recordEvent(req.SessionID, "workflow_completed", map[string]any{"job_id": jobID, "type": req.Type})
}

func (m *Manager) runDescriptionsWorkflow(jobID string, req WorkflowRequest) error {
	resources, err := m.undocumentedResources()
	if err != nil {
		return err
	}
	m.recordEvent(req.SessionID, "workflow_progress", map[string]any{"job_id": jobID, "type": req.Type, "queued": len(resources)})
	batches := chunkResources(resources, req.BatchSize)
	workflowTools := m.descriptionWorkflowTools()
	return m.runBatches(req.SessionID, jobID, req.Type, batches, req.Parallel, func(batch []workflowResource) (string, error) {
		input := descriptionInput(batch)
		return m.runSubAgent(prompts.DescriptionsGenerationExecutorPrompt(), input, workflowTools, len(batch)*4+10)
	})
}

func (m *Manager) runBugHunterWorkflow(jobID string, req WorkflowRequest) error {
	resources, err := m.inspectableResources()
	if err != nil {
		return err
	}
	m.recordEvent(req.SessionID, "workflow_progress", map[string]any{"job_id": jobID, "type": req.Type, "queued": len(resources)})
	allowed := allowedToolSet("read", "read_function", "read_struct", "read_interface", "read_file", "read_package", "read_dependency", "grep", "bug_report")
	workflowTools := toolMap(m.registry, allowed)
	batches := chunkResources(resources, req.BatchSize)
	return m.runBatches(req.SessionID, jobID, req.Type, batches, req.Parallel, func(batch []workflowResource) (string, error) {
		return m.runSubAgent(prompts.BugHunterPrompt(), assignedResourcesInput("Inspect only these resources for confirmed bugs.", batch), workflowTools, len(batch)*6+10)
	})
}

func (m *Manager) runBugJudgeWorkflow(jobID string, req WorkflowRequest) error {
	bugs, err := m.manager.ListBugs("", domain.BugPending)
	if err != nil {
		return err
	}
	byNode := make(map[string][]domain.KnownBug)
	for _, bug := range bugs {
		byNode[bug.NodeID] = append(byNode[bug.NodeID], bug)
	}
	var batches [][]workflowResource
	for nodeID, nodeBugs := range byNode {
		var text strings.Builder
		for _, bug := range nodeBugs {
			text.WriteString(fmt.Sprintf("- %s: %s\n", bug.ID, bug.Description))
		}
		batches = append(batches, []workflowResource{{ID: nodeID, Name: text.String(), Kind: "bug_batch"}})
	}
	m.recordEvent(req.SessionID, "workflow_progress", map[string]any{"job_id": jobID, "type": req.Type, "queued": len(batches)})
	allowed := allowedToolSet("read", "read_function", "read_struct", "bug_list", "bug_acknowledge", "bug_dismiss", "bug_delete")
	workflowTools := toolMap(m.registry, allowed)
	return m.runBatches(req.SessionID, jobID, req.Type, batches, req.Parallel, func(batch []workflowResource) (string, error) {
		res := batch[0]
		input := fmt.Sprintf("Triage pending bugs for node %s. Pending bugs:\n%s", res.ID, res.Name)
		return m.runSubAgent(prompts.BugJudgePrompt(), input, workflowTools, 20)
	})
}

func (m *Manager) runBugSolverWorkflow(jobID string, req WorkflowRequest) error {
	bugs, err := m.manager.ListBugs("", domain.BugAcknowledged)
	if err != nil {
		return err
	}
	var batches [][]workflowResource
	for _, bug := range bugs {
		batches = append(batches, []workflowResource{{ID: bug.ID, Name: fmt.Sprintf("node: %s\ndescription: %s", bug.NodeID, bug.Description), Kind: "bug"}})
	}
	m.recordEvent(req.SessionID, "workflow_progress", map[string]any{"job_id": jobID, "type": req.Type, "queued": len(batches)})
	allowed := allowedToolSet("read", "read_function", "read_struct", "read_file", "grep", "edit", "write", "bug_delete")
	workflowTools := toolMap(m.registry, allowed)
	return m.runBatches(req.SessionID, jobID, req.Type, batches, req.Parallel, func(batch []workflowResource) (string, error) {
		res := batch[0]
		input := fmt.Sprintf("Fix acknowledged bug %s.\n%s", res.ID, res.Name)
		return m.runSubAgent(prompts.BugSolverPrompt(), input, workflowTools, 30)
	})
}

func (m *Manager) runBatches(sessionID, jobID, typ string, batches [][]workflowResource, parallel int, run func([]workflowResource) (string, error)) error {
	if len(batches) == 0 {
		return nil
	}
	jobs := make(chan []workflowResource)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	completed := 0
	for i := 0; i < parallel; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for batch := range jobs {
				text, err := run(batch)
				mu.Lock()
				completed++
				if err != nil && firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
				payload := map[string]any{"job_id": jobID, "type": typ, "completed": completed, "total": len(batches), "batch_size": len(batch)}
				if err != nil {
					payload["error"] = err.Error()
				}
				if strings.TrimSpace(text) != "" {
					payload["text"] = strings.TrimSpace(text)
				}
				m.recordEvent(sessionID, "workflow_progress", payload)
			}
		}()
	}
	for _, batch := range batches {
		jobs <- batch
	}
	close(jobs)
	wg.Wait()
	return firstErr
}

func (m *Manager) runSubAgent(systemPrompt, input string, toolMap map[string]tools.Tool, maxIterations int) (string, error) {
	m.mu.Lock()
	settings := m.provider
	m.mu.Unlock()
	provider, err := newProvider(settings)
	if err != nil {
		return "", err
	}
	a := agent.New(provider, tools.NewRegistry(), getLanguage(m.manager))
	a.SetMaxIterations(maxIterations)
	return a.RunSubAgent(systemPrompt, input, toolMap)
}

func (m *Manager) descriptionWorkflowTools() map[string]tools.Tool {
	reg := tools.NewRegistry()
	reg.Register(tools.WrapWithReadScan(tools.NewRead(m.manager), m.manager, m.scanners, m.config.EffectiveReadScan()))
	registerLanguageMaintenanceTools(reg, nil, m.manager, getLanguage(m.manager), m.config.Descriptions.Kinds, configDescriptionBatchSize(m.config))
	return toolMap(reg, allowedToolSet("read", "update_description"))
}

func (m *Manager) undocumentedResources() ([]workflowResource, error) {
	topo, err := m.manager.ReadAll()
	if err != nil {
		return nil, err
	}
	targets := helper.DescribeTargetSet(m.config.Descriptions.Kinds)
	var result []workflowResource
	for _, res := range topo.Resources {
		if strings.TrimSpace(res.Description) != "" {
			continue
		}
		if !targets[res.Kind] {
			continue
		}
		result = append(result, workflowResource{ID: res.ID, Name: res.Name, Kind: res.Kind})
	}
	return result, nil
}

func (m *Manager) inspectableResources() ([]workflowResource, error) {
	topo, err := m.manager.ReadAll()
	if err != nil {
		return nil, err
	}
	var result []workflowResource
	for _, res := range topo.Resources {
		switch res.Kind {
		case domain.ResourceFunction, domain.ResourceMethod, domain.ResourceType, domain.ResourceInterface:
			result = append(result, workflowResource{ID: res.ID, Name: res.Name, Kind: res.Kind})
		}
	}
	return result, nil
}

func chunkResources(resources []workflowResource, size int) [][]workflowResource {
	if size <= 0 {
		size = 5
	}
	var batches [][]workflowResource
	for len(resources) > 0 {
		end := size
		if end > len(resources) {
			end = len(resources)
		}
		batches = append(batches, resources[:end])
		resources = resources[end:]
	}
	return batches
}

func descriptionInput(batch []workflowResource) string {
	resources := make([]prompts.DescriptionResource, 0, len(batch))
	for _, res := range batch {
		resources = append(resources, prompts.DescriptionResource{ID: res.ID, Name: res.Name, Kind: res.Kind})
	}
	return prompts.DescriptionsGenerationExecutorInput(resources)
}

func assignedResourcesInput(prefix string, batch []workflowResource) string {
	var b strings.Builder
	b.WriteString(prefix)
	b.WriteString("\n\nAssigned resources:\n")
	for _, res := range batch {
		b.WriteString(fmt.Sprintf("- ID: %s\n  Name: %s\n  Kind: %s\n", res.ID, res.Name, res.Kind))
	}
	return b.String()
}

func allowedToolSet(names ...string) map[string]bool {
	allowed := make(map[string]bool, len(names))
	for _, name := range names {
		allowed[name] = true
	}
	return allowed
}
