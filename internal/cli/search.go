package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

func RunSearch(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: ltp search <string>")
		os.Exit(1)
	}

	query := strings.ToLower(args[0])
	manager, _ := InitRegistry(".ltp/topology.db")

	topo, err := manager.ReadAll()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	var matches []string
	for id, res := range topo.Resources {
		if strings.Contains(strings.ToLower(id), query) ||
			strings.Contains(strings.ToLower(res.ID), query) ||
			strings.Contains(strings.ToLower(res.Name), query) {
			matches = append(matches, id)
		}
	}

	sort.Strings(matches)
	for _, id := range matches {
		fmt.Println(id)
	}
}
