package scanner

import "llm-topology/internal/topology/domain"

// Defines the contract for scanning a codebase in a specific language. Requires methods to return the scanner name, supported file extensions, detect whether a root applies, perform a full scan returning a Topology, and incrementally update a single file.
type LanguageScanner interface {
	Name() string
	Extensions() []string
	Detect(root string) bool
	Scan(root string) (*domain.Topology, error)
	UpdateFile(topo *domain.Topology, path string) []domain.TopologyWarning
}
