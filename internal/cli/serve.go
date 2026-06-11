package cli

import (
	"flag"
	"fmt"
	"os"

	"aracne/internal/helper"
	"aracne/internal/mcp"
)

func RunServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	profileName := fs.String("tool-profile", "default", "Tool profile: default, descriptions-executor, bug-hunter, bug-judge, bug-solver, or all")
	fs.Parse(args)

	profile, err := ParseToolProfile(*profileName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	manager, reg := InitRegistry(".aracne/topology.db")
	cfg := helper.EnsureConfig(helper.ConfigPath(".aracne/topology.db"))
	registry := BuildToolRegistry(manager, reg, cfg, profile)

	server := mcp.NewServerWithAgentRoutes(registry, ".aracne/topology.db", cfg.EffectiveAgentRouteTTL())
	if err := server.Serve(); err != nil {
		fmt.Fprintf(os.Stderr, "MCP server error: %v\n", err)
		os.Exit(1)
	}
}
