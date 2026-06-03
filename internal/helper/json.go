package helper

import (
	"encoding/json"
	"os"

	"ltp/internal/topology/domain"
)

// Serializes a Topology to indented JSON and writes it to a file at the given path. Takes a *Topology and output path string. Returns an error if marshaling or writing fails.
func WriteJson(topo *domain.Topology, path string) error {
	data, err := json.MarshalIndent(topo, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// Reads a JSON file from the given path, unmarshals it into a domain.Topology struct, and returns the result. Takes path string, returns *domain.Topology and error.
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
