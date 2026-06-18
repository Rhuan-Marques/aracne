package chat

import (
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
