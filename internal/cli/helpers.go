package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner/goscanner"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner/javascanner"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner/jsscanner"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner/pyscanner"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner/rustscanner"
)

// Creates a scanner registry with registered Go, Python, JavaScript, TypeScript, Rust, and Java scanners.
func NewScannerRegistry() *scanner.Registry {
	reg := scanner.NewRegistry()
	reg.Register(goscanner.NewGoScanner())
	reg.Register(pyscanner.NewPythonScanner())
	reg.Register(jsscanner.NewJavaScriptScanner())
	reg.Register(jsscanner.NewTypeScriptScanner())
	reg.Register(rustscanner.NewRustScanner())
	reg.Register(javascanner.NewJavaScanner())
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

// TopologyLanguages returns the languages the topology was scanned in, most significant first,
// for the contract that gets written about it.
//
// It never scans and never creates a database. `arac setup` is the caller that matters and it
// legitimately runs before the first scan -- on a fresh checkout there is nothing to report and
// an empty slice is the honest answer, which the high-verbosity contract renders as its
// language-free form. Reporting "go" there (the way GetLanguage does, because a tool
// constructor has to pick something) would write Go's ID vocabulary into a Python project's
// CLAUDE.md.
func TopologyLanguages(dbPath string) []string {
	if _, err := os.Stat(dbPath); err != nil {
		return nil
	}
	topo, err := helper.ReadDb(dbPath)
	if err != nil || topo == nil {
		return nil
	}
	if len(topo.Languages) > 0 {
		return topo.Languages
	}
	if topo.Language != "" {
		return []string{topo.Language}
	}
	return nil
}

// TopologyLanguagesFor is TopologyLanguages for a caller that already has the manager open.
func TopologyLanguagesFor(manager *topology.TopologyManager) []string {
	topo, err := manager.ReadAll()
	if err != nil || topo == nil {
		return nil
	}
	if len(topo.Languages) > 0 {
		return topo.Languages
	}
	if topo.Language != "" {
		return []string{topo.Language}
	}
	return nil
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
	case "struct":
		return domain.ResourceStruct
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
