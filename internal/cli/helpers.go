package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"aracne/internal/topology"
	"aracne/internal/topology/domain"
	"aracne/internal/topology/scanner"
	"aracne/internal/topology/scanner/goscanner"
	"aracne/internal/topology/scanner/pyscanner"
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
	normalized := strings.TrimSpace(name)
	normalized = strings.TrimPrefix(normalized, "domain.")
	normalized = strings.TrimPrefix(normalized, "Resource")
	normalized = strings.TrimPrefix(normalized, "Kind")
	normalized = strings.ToLower(normalized)
	normalized = strings.ReplaceAll(normalized, "-", "_")
	normalized = strings.ReplaceAll(normalized, " ", "_")
	normalized = strings.ReplaceAll(normalized, "external_var", "variable")
	normalized = strings.ReplaceAll(normalized, "externalvar", "variable")
	normalized = strings.ReplaceAll(normalized, "namedtype", "named_type")

	switch normalized {
	case "function":
		return domain.ResourceFunction
	case "method":
		return domain.ResourceMethod
	case "struct", "type":
		return domain.ResourceType
	case "named_type":
		return domain.ResourceNamedType
	case "interface":
		return domain.ResourceInterface
	case "variable", "var":
		return domain.ResourceVariable
	case "file":
		return domain.ResourceFile
	case "package":
		return domain.ResourcePackage
	case "dependency", "dependancy", "dep":
		return domain.ResourceDependency
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
