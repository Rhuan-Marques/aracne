package chat

import (
	"sort"
	"strings"

	"aracne/internal/helper"
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
		case domain.ResourceFunction, domain.ResourceMethod, domain.ResourceType, domain.ResourceInterface:
			result = append(result, workflowResource{ID: res.ID, Name: res.Name, Kind: res.Kind})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
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
