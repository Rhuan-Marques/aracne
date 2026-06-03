package cli

import (
	"fmt"
	"os"
	"strings"

	"ltp/internal/helper"
	"ltp/internal/topology/domain"
)

func RunListUndocumented() {
	manager, _ := InitRegistry(".ltp/topology.db")
	cfg := helper.EnsureConfig(helper.ConfigPath(".ltp/topology.db"))
	targetSet := helper.DescribeTargetSet(cfg.DescribeTargets)

	topo, err := manager.ReadAll()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	var entries []struct {
		ID   string
		Name string
		Kind domain.ResourceKind
	}
	for id, res := range topo.Resources {
		if res.Description == "" && targetSet[res.Kind] {
			entries = append(entries, struct {
				ID   string
				Name string
				Kind domain.ResourceKind
			}{ID: id, Name: res.Name, Kind: res.Kind})
		}
	}

	if len(entries) == 0 {
		fmt.Println("All targeted resources already have descriptions.")
		return
	}

	fmt.Printf("Found %d undocumented resources for targets: %s.\n\n", len(entries), helper.FormatDescribeTargets(cfg.DescribeTargets))
	for _, e := range entries {
		fmt.Printf("  ID: %s\n    Name: %s\n    Kind: %s\n\n", e.ID, e.Name, strings.ToUpper(string(e.Kind)))
	}
}
