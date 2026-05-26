package helper

import (
	"encoding/json"
	"os"

	"llm-topology/internal/topology/domain"
)

func WriteJson(topo *domain.Topology, path string) error {
	data, err := json.MarshalIndent(topo, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func ReadJson(path string) (*domain.Topology, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var topo domain.Topology
	if err := json.Unmarshal(data, &topo); err != nil {
		return nil, err
	}
	return &topo, nil
}
