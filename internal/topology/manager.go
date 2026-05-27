package topology

import (
	"fmt"
	"io"
	"os"
	"strings"

	"llm-topology/internal/helper"
	"llm-topology/internal/topology/domain"
	"llm-topology/internal/topology/scanner"
)

// TopologyManager is the core persistence and query engine for the project topology. It manages SQLite read/write operations, scanning (FullScan), file-level updates (UpdateFile), resource lookup (FindResourcesByName), source code cuts (Cut), and description updates. The dbPath field holds the path to the SQLite database file.
type TopologyManager struct {
	dbPath string
}

// Creates and returns a new empty TopologyManager instance with default zero values.
func New() *TopologyManager {
	return &TopologyManager{}
}

// Returns the current SQLite database path used by the TopologyManager.
func (m *TopologyManager) DbPath() string {
	return m.dbPath
}

// Performs a full recursive scan of the given root directory using the provided scanner registry, then writes the resulting topology to the database.
func (m *TopologyManager) FullScan(root string, reg *scanner.Registry) error {
	langScanner := reg.Detect(root)
	if langScanner == nil {
		return fmt.Errorf("no language scanner detected for %s", root)
	}

	topo, err := langScanner.Scan(root)
	if err != nil {
		return err
	}
	return helper.WriteDb(topo, m.dbPath)
}

// Sets the database file path for the TopologyManager to use for all subsequent read/write operations. Returns nil.
func (m *TopologyManager) IncrementalScan(root string, reg *scanner.Registry) error {
	langScanner := reg.Detect(root)
	if langScanner == nil {
		return fmt.Errorf("no language scanner detected for %s", root)
	}
// Copies the current topology database to a specified output path. Preserves existing descriptions from the old database when the new scan produces empty descriptions for the same resources.

	newTopo, err := langScanner.Scan(root)
	if err != nil {
		return err
	}

	oldTopo, readErr := helper.ReadDb(m.dbPath)
	if readErr == nil && oldTopo != nil {
		for id, oldRes := range oldTopo.Resources {
			newRes, exists := newTopo.Resources[id]
			if !exists {
				continue
			}
			if newRes.Description == "" && oldRes.Description != "" {
				newRes.Description = oldRes.Description
				newTopo.Resources[id] = newRes
			}
		}
// Reads all resources from the SQLite database, optionally filtered by resource kind(s) and description status, and returns the populated Topology.
	}

	return helper.WriteDb(newTopo, m.dbPath)
}
// Reads a source file and returns a CodeEntry containing the lines between the given Location's StartsAt and EndsAt. Returns an error if the range is out of bounds.

func (m *TopologyManager) Load(path string) error {
	m.dbPath = path
	return nil
}

func (m *TopologyManager) Write(path string) error {
	if m.dbPath == "" {
		return nil
	}
	src, err := os.Open(m.dbPath)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.Create(path)
	if err != nil {
		return err
	}
// Re-parses a file via the scanner registry and updates the topology database in-place, returning TopologyWarnings for any removed or changed resources.
	defer dst.Close()
	_, err = io.Copy(dst, src)
	return err
}

func (m *TopologyManager) ReadAll() (*domain.Topology, error) {
	return helper.ReadDb(m.dbPath)
}

func (m *TopologyManager) Cut(loc domain.Location) (*domain.CodeEntry, error) {
	data, err := os.ReadFile(loc.Path)
	if err != nil {
		return nil, fmt.Errorf("read file %s: %w", loc.Path, err)
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if loc.StartsAt < 1 || loc.StartsAt > len(lines) {
		return nil, fmt.Errorf("StartsAt %d out of range (1-%d)", loc.StartsAt, len(lines))
	}
	if loc.EndsAt > len(lines) {
		return nil, fmt.Errorf("EndsAt %d out of range (max %d)", loc.EndsAt, len(lines))
	}
	cut := strings.Join(lines[loc.StartsAt-1:loc.EndsAt], "\n")
	return &domain.CodeEntry{Location: loc, Cut: cut}, nil
}

func (m *TopologyManager) UpdateFile(path string, reg *scanner.Registry) []domain.TopologyWarning {
	topo, err := helper.ReadDb(m.dbPath)
	if err != nil {
// Finds resources in the topology by matching their name, optionally filtered by resource kinds. Returns a slice of resource IDs for all matching resources. Delegates to the detected language scanner's UpdateFile before searching.
		return nil
	}

	var langScanner scanner.LanguageScanner
	if topo.Language != "" {
		for _, s := range reg.All() {
			if s.Name() == topo.Language {
				langScanner = s
				break
			}
		}
	}
	if langScanner == nil {
		langScanner = reg.Detect(topo.Root)
	}
	if langScanner == nil {
		return nil
	}

	warnings := langScanner.UpdateFile(topo, path)

	if err := helper.WriteDb(topo, m.dbPath); err != nil {
// Updates the description of a resource in the SQLite topology database by ID and kind.
		return nil
	}

	return warnings
}

func (m *TopologyManager) FindResourcesByName(name string, kinds ...domain.ResourceKind) ([]string, error) {
	topo, err := helper.ReadDb(m.dbPath)
	if err != nil {
		return nil, err
	}
	kindSet := make(map[domain.ResourceKind]bool, len(kinds))
	for _, k := range kinds {
		kindSet[k] = true
	}
	var results []string
	for id, res := range topo.Resources {
		if res.Name != name {
			continue
		}
		if len(kindSet) > 0 && !kindSet[res.Kind] {
			continue
		}
		results = append(results, id)
	}
	return results, nil
}

func (m *TopologyManager) UpdateDescription(id string, kind domain.ResourceKind, description string) error {
	return helper.UpdateDescription(m.dbPath, kind, id, description)
}
