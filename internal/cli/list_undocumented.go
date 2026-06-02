package cli

import (
	"fmt"
	"os"
	"strings"

	"llm-topology/internal/topology/domain"
)

func RunListUndocumented() {
	manager, _ := InitRegistry(".ltp/topology.db")

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
		if res.Description == "" {
			entries = append(entries, struct {
				ID   string
				Name string
				Kind domain.ResourceKind
			}{ID: id, Name: res.Name, Kind: res.Kind})
		}
	}

	if len(entries) == 0 {
		fmt.Println("All resources already have descriptions.")
		return
	}

	fmt.Printf("Found %d undocumented resources.\n\n", len(entries))
	for _, e := range entries {
		fmt.Printf("  ID: %s\n    Name: %s\n    Kind: %s\n\n", e.ID, e.Name, strings.ToUpper(string(e.Kind)))
	}
}
