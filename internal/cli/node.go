package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"aracne/internal/helper"
	"aracne/internal/topology/domain"
)

type kindFilters []string

func (k *kindFilters) String() string {
	return strings.Join(*k, ",")
}

func (k *kindFilters) Set(value string) error {
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(strings.ToLower(part))
		if part != "" {
			*k = append(*k, part)
		}
	}
	return nil
}

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
	if noDesc {
		cfg := helper.EnsureConfig(helper.ConfigPath(".aracne/topology.db"))
		targetSet = helper.DescribeTargetSet(cfg.Descriptions.Kinds)
	}

	var ids []string
	for id, res := range topo.Resources {
		kind := strings.ToLower(string(res.Kind))
		if len(kindSet) > 0 && !kindSet[kind] {
			continue
		}
		if noDesc && (res.Description != "" || !targetSet[res.Kind]) {
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

func resourceMatchesQuery(key, id, name, query string) bool {
	return strings.Contains(strings.ToLower(key), query) ||
		strings.Contains(strings.ToLower(id), query) ||
		strings.Contains(strings.ToLower(name), query)
}

func RunNodeCount() {
	manager, _ := InitRegistry(".aracne/topology.db")

	topo, err := manager.ReadAll()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(len(topo.Resources))
}

func RunNodeCountNoDescription() {
	manager, _ := InitRegistry(".aracne/topology.db")
	cfg := helper.EnsureConfig(helper.ConfigPath(".aracne/topology.db"))
	targetSet := helper.DescribeTargetSet(cfg.Descriptions.Kinds)

	topo, err := manager.ReadAll()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	var count int
	for _, res := range topo.Resources {
		if res.Description == "" && targetSet[res.Kind] {
			count++
		}
	}

	fmt.Println(count)
}
