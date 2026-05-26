package scanner

import "llm-topology/internal/topology/domain"

type LanguageScanner interface {
	Name() string
	Extensions() []string
	Detect(root string) bool
	Scan(root string) (*domain.Topology, error)
	UpdateFile(topo *domain.Topology, path string) []domain.TopologyWarning
}
