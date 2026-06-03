package cli

import (
	"flag"
	"fmt"
	"os"

	"ltp/internal/helper"
	"ltp/internal/mcp"
)

func RunServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	profileName := fs.String("tool-profile", "default", "Tool profile: default, descriptor, bug-hunter, bug-judge, bug-solver, or all")
	fs.Parse(args)

	profile, err := ParseToolProfile(*profileName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	manager, reg := InitRegistry(".ltp/topology.db")
	cfg := helper.EnsureConfig(helper.ConfigPath(".ltp/topology.db"))
	registry := BuildToolRegistry(manager, reg, cfg, profile)

	server := mcp.NewServer(registry)
	if err := server.Serve(); err != nil {
		fmt.Fprintf(os.Stderr, "MCP server error: %v\n", err)
		os.Exit(1)
	}
}
