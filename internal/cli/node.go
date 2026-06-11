package cli

import (
	"fmt"
	"os"

	"aracne/internal/helper"
)

func RunNodeList() {
	manager, _ := InitRegistry(".aracne/topology.db")

	topo, err := manager.ReadAll()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(topo.Resources) == 0 {
		fmt.Println("No nodes found.")
		return
	}

	fmt.Printf("Found %d nodes.\n", len(topo.Resources))
	for id := range topo.Resources {
		fmt.Println(id)
	}
}

func RunNodeListNoDescription() {
	manager, _ := InitRegistry(".aracne/topology.db")
	cfg := helper.EnsureConfig(helper.ConfigPath(".aracne/topology.db"))
	targetSet := helper.DescribeTargetSet(cfg.DescribeTargets)

	topo, err := manager.ReadAll()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	var ids []string
	for id, res := range topo.Resources {
		if res.Description == "" && targetSet[res.Kind] {
			ids = append(ids, id)
		}
	}

	if len(ids) == 0 {
		fmt.Println("All targeted resources already have descriptions.")
		return
	}

	fmt.Printf("Found %d undocumented nodes.\n", len(ids))
	for _, id := range ids {
		fmt.Println(id)
	}
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
	targetSet := helper.DescribeTargetSet(cfg.DescribeTargets)

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
