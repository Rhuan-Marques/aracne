package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

type kindFilters []string

// Returns a comma-separated string representation of resource kind filters.
func (k *kindFilters) String() string {
	return strings.Join(*k, ",")
}

// Parses a comma-separated string into kindFilters, trimming spaces and converting to lowercase.
func (k *kindFilters) Set(value string) error {
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(strings.ToLower(part))
		if part != "" {
			*k = append(*k, part)
		}
	}
	return nil
}

// Parses command-line arguments for the resource list command, extracting search query, kind filters, and description visibility flag.
func parseResourceListArgs(args []string) (string, kindFilters, bool) {
	var query string
	var kinds kindFilters
	var noDesc bool

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--no-description":
			noDesc = true
		case arg == "--kind" || arg == "-kind" || arg == "-k":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "Usage: arac resource list [query] [--kind <kind>]... [--no-description]")
				os.Exit(1)
			}
			i++
			kinds.Set(args[i])
		case strings.HasPrefix(arg, "--kind="):
			kinds.Set(strings.TrimPrefix(arg, "--kind="))
		case strings.HasPrefix(arg, "-kind="):
			kinds.Set(strings.TrimPrefix(arg, "-kind="))
		case strings.HasPrefix(arg, "-k="):
			kinds.Set(strings.TrimPrefix(arg, "-k="))
		case strings.HasPrefix(arg, "-"):
			fmt.Fprintf(os.Stderr, "Unknown flag: %s\n", arg)
			fmt.Fprintln(os.Stderr, "Usage: arac resource list [query] [--kind <kind>]... [--no-description]")
			os.Exit(1)
		case query == "":
			query = strings.ToLower(arg)
		default:
			fmt.Fprintln(os.Stderr, "Usage: arac resource list [query] [--kind <kind>]... [--no-description]")
			os.Exit(1)
		}
	}

	return query, kinds, noDesc
}

// Lists topology resources filtered by kind, query string, and description status.
func RunResourceList(args []string) {
	query, kinds, noDesc := parseResourceListArgs(args)

	manager, _ := InitRegistry(".aracne/topology.db")
	topo, err := manager.ReadAll()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	kindSet := make(map[string]bool)
	for _, kind := range kinds {
		kindSet[kind] = true
	}

	var targetSet map[domain.ResourceKind]bool
	var filter domain.ContextFilter
	var includeNotVisible bool
	if noDesc {
		cfg := helper.EnsureConfig(helper.ConfigPath(".aracne/topology.db"))
		targetSet = helper.DescribeTargetSet(cfg.Descriptions.Kinds)
		filter = cfg.EffectiveContextFilter()
		includeNotVisible = cfg.Descriptions.IncludeNotVisible
	}

	var ids []string
	for id, res := range topo.Resources {
		kind := strings.ToLower(string(res.Kind))
		if len(kindSet) > 0 && !kindSet[kind] {
			continue
		}
		if noDesc && !helper.ShouldDescribe(res, targetSet, filter, includeNotVisible) {
			continue
		}
		if query != "" && !resourceMatchesQuery(id, res.ID, res.Name, query) {
			continue
		}
		ids = append(ids, id)
	}

	if len(ids) == 0 {
		fmt.Println("No resources found.")
		return
	}

	sort.Strings(ids)
	fmt.Printf("Found %d resources.\n", len(ids))
	for _, id := range ids {
		fmt.Println(id)
	}
}

// Checks if a resource matches a query by case-insensitive substring search on key, ID, or name.
func resourceMatchesQuery(key, id, name, query string) bool {
	return strings.Contains(strings.ToLower(key), query) ||
		strings.Contains(strings.ToLower(id), query) ||
		strings.Contains(strings.ToLower(name), query)
}

// Reads the topology database and prints the total count of resources.
func RunNodeCount() {
	manager, _ := InitRegistry(".aracne/topology.db")

	topo, err := manager.ReadAll()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(len(topo.Resources))
}

// Counts resources without descriptions in the topology database matching configured target kinds.
func RunNodeCountNoDescription() {
	manager, _ := InitRegistry(".aracne/topology.db")
	cfg := helper.EnsureConfig(helper.ConfigPath(".aracne/topology.db"))
	targetSet := helper.DescribeTargetSet(cfg.Descriptions.Kinds)
	filter := cfg.EffectiveContextFilter()
	includeNotVisible := cfg.Descriptions.IncludeNotVisible

	topo, err := manager.ReadAll()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	var count int
	for _, res := range topo.Resources {
		if helper.ShouldDescribe(res, targetSet, filter, includeNotVisible) {
			count++
		}
	}

	fmt.Println(count)
}
