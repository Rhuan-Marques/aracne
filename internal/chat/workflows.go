package chat

import (
	"sort"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// Represents a resource within a workflow with its ID, name, and resource kind.
type workflowResource struct {
	ID   string
	Name string
	Kind domain.ResourceKind
}

// Delegates to StartForcedWorkflow to launch a workflow.
func (m *Manager) StartWorkflow(req WorkflowRequest) (string, error) {
	return m.StartForcedWorkflow(req)
}

// Returns the list of resources without descriptions matching the configured target kinds.
func (m *Manager) undocumentedResources() ([]workflowResource, error) {
	topo, err := m.manager.ReadAll()
	if err != nil {
		return nil, err
	}
	targets := helper.DescribeTargetSet(m.config.Descriptions.Kinds)
	filter := m.config.EffectiveContextFilter()
	includeNotVisible := m.config.Descriptions.IncludeNotVisible
	var result []workflowResource
	for _, res := range topo.Resources {
		if !helper.ShouldDescribe(res, targets, filter, includeNotVisible) {
			continue
		}
		result = append(result, workflowResource{ID: res.ID, Name: res.Name, Kind: res.Kind})
	}
	return result, nil
}

// inspectableResources returns the functions, methods, types, and interfaces
// the bug-hunter fan-out partitions across hunter tasks, sorted by ID so the
// partition is deterministic across runs.
func (m *Manager) inspectableResources() ([]workflowResource, error) {
	topo, err := m.manager.ReadAll()
	if err != nil {
		return nil, err
	}
	var result []workflowResource
	for _, res := range topo.Resources {
		switch res.Kind {
		case domain.ResourceFunction, domain.ResourceMethod, domain.ResourceStruct, domain.ResourceInterface:
			result = append(result, workflowResource{ID: res.ID, Name: res.Name, Kind: res.Kind})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

// Splits resources into batches of specified size (defaults to 5 if size is invalid).
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
