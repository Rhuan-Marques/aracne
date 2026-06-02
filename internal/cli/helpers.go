package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"llm-topology/internal/topology"
	"llm-topology/internal/topology/domain"
	"llm-topology/internal/topology/scanner"
	"llm-topology/internal/topology/scanner/goscanner"
	"llm-topology/internal/topology/scanner/pyscanner"
)

func NewScannerRegistry() *scanner.Registry {
	reg := scanner.NewRegistry()
	reg.Register(goscanner.NewGoScanner())
	reg.Register(pyscanner.NewPythonScanner())
	return reg
}

func GetLanguage(manager *topology.TopologyManager) string {
	topo, err := manager.ReadAll()
	if err != nil || topo == nil {
		return "go"
	}
	if topo.Language != "" {
		return topo.Language
	}
	return "go"
}

func InitRegistry(dbPath string) (*topology.TopologyManager, *scanner.Registry) {
	reg := NewScannerRegistry()
	mgr := topology.New()
	os.MkdirAll(filepath.Dir(dbPath), 0755)
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		mgr.Load(dbPath)
		fmt.Fprintf(os.Stderr, "No topology found. Scanning project...\n")
		start := time.Now()
		if _, err := mgr.IncrementalScan(".", reg); err != nil {
			fmt.Fprintf(os.Stderr, "Error scanning project: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Topology built in %s\n", time.Since(start).Round(time.Millisecond))
	} else {
		mgr.Load(dbPath)
	}
	return mgr, reg
}

func MapResourceKind(name string) domain.ResourceKind {
	switch name {
	case "Function":
		return domain.ResourceFunction
	case "Struct", "Type":
		return domain.ResourceType
	case "Interface":
		return domain.ResourceInterface
	case "ExternalVar", "Variable":
		return domain.ResourceVariable
	case "File":
		return domain.ResourceFile
	case "Package":
		return domain.ResourcePackage
	}
	return ""
}

func DiffWarnings(before, after map[string]domain.TopologyWarning) (added, removed []domain.TopologyWarning) {
	for id, w := range after {
		if _, exists := before[id]; !exists {
			added = append(added, w)
		}
	}
	for id, w := range before {
		if _, exists := after[id]; !exists {
			removed = append(removed, w)
		}
	}
	return
}

