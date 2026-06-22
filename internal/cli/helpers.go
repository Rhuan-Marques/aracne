package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"aracne/internal/helper"
	"aracne/internal/topology"
	"aracne/internal/topology/domain"
	"aracne/internal/topology/scanner"
	"aracne/internal/topology/scanner/goscanner"
	"aracne/internal/topology/scanner/jsscanner"
	"aracne/internal/topology/scanner/pyscanner"
)

// Creates a scanner registry with registered Go, Python, JavaScript, and TypeScript scanners.
func NewScannerRegistry() *scanner.Registry {
	reg := scanner.NewRegistry()
	reg.Register(goscanner.NewGoScanner())
	reg.Register(pyscanner.NewPythonScanner())
	reg.Register(jsscanner.NewJavaScriptScanner())
	reg.Register(jsscanner.NewTypeScriptScanner())
	return reg
}

// Retrieves the project language from topology manager, defaulting to Go.
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

// runReadScan runs the read.scan pre-scan for the CLI read/grep commands. It is
// a no-op when read.scan is "none" (the default), preserving existing behavior.
// A scan failure is reported as a warning but does not abort the command.
func runReadScan(manager *topology.TopologyManager, reg *scanner.Registry) {
	cfg := helper.EnsureConfig(helper.ConfigPath(manager.DbPath()))
	mode := cfg.EffectiveReadScan()
	if mode == helper.ReadScanNone {
		return
	}
	if err := manager.RunReadScan(reg, mode); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: read.scan (%s) failed: %v\n", mode, err)
	}
}

// Initializes topology manager and scanner registry, performing initial scan if needed.
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

// Normalizes and maps a string name to the corresponding domain ResourceKind enum value.
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

// Compares two warning maps and returns newly added and removed warnings.
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
