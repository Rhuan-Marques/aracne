package cli

import (
	"flag"
	"fmt"
	"os"

	"aracne/internal/topogrep"
)

func RunGrep(args []string) {
	fs := flag.NewFlagSet("grep", flag.ExitOnError)
	dbPath := fs.String("db", ".aracne/topology.db", "Topology database path")
	fs.Parse(args)
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: arac grep [--db <path>] <pattern> [path]")
		os.Exit(1)
	}

	pattern := fs.Arg(0)
	path := "."
	if fs.NArg() > 1 {
		path = fs.Arg(1)
	}

	manager, _ := InitRegistry(*dbPath)
	topo, err := manager.ReadAll()
	if err != nil {
		topo = nil
	}
	matches, err := topogrep.Search(pattern, path, topo)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	output := topogrep.Format(matches)
	if output != "" {
		fmt.Println(output)
	}
}
