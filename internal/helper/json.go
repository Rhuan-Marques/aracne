// Package serializer handles JSON persistence for Topology objects, providing
// read and write operations that bridge the in-memory topology graph with its
// on-disk JSON representation.
package helper

import (
	"encoding/json"
	"os"

	"llm-topology/internal/topology/domain"
)

// Write marshals a Topology to indented JSON and writes it to the given path.
func WriteJson(topo *domain.Topology, path string) error {
	data, err := json.MarshalIndent(topo, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// Read loads a JSON file and unmarshals it into a Topology pointer.
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
