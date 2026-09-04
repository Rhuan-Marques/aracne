package cli

import (
	"flag"
	"fmt"
	"os"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/mcp"
)

// Starts an MCP server exposing topology tools for a specified agent profile.
func RunServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	profileName := fs.String("tool-profile", "main", "Agent whose tools to serve: main, all, or a configured agent name (bug-hunter, bug-judge, bug-solver, descriptions-generation-executor)")
	harness := fs.String("harness", "claude_code", "Harness whose per-agent overrides apply: claude_code or opencode")
	fs.Parse(args)

	manager, reg := InitRegistry(".aracne/topology.db")
	cfg := helper.EnsureConfig(helper.ConfigPath(".aracne/topology.db"))
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "Invalid .aracne/config.json: %v\n", err)
		os.Exit(1)
	}
	if !ValidAgentProfile(cfg, *profileName) {
		fmt.Fprintf(os.Stderr, "invalid --tool-profile %q\n", *profileName)
		os.Exit(1)
	}
	// Only ModeMCP has MCP tools. Serving an empty registry instead of saying so is the
	// failure that looks like a working server: the harness connects, lists nothing, and the
	// model is told about tools it will never be offered.
	if !cfg.MCPEnabled() {
		fmt.Fprintf(os.Stderr, "aracne: mode %q serves no MCP tools. Set \"mode\": \"mcp\" in "+
			".aracne/config.json (or run `arac init --mcp`) to wire the server, or remove the "+
			"aracne entry from this harness's MCP config.\n", cfg.EffectiveMode())
		os.Exit(1)
	}
	registry := BuildToolRegistry(manager, reg, cfg, *harness, *profileName)

	server := mcp.NewServer(registry)
	if err := server.Serve(); err != nil {
		fmt.Fprintf(os.Stderr, "MCP server error: %v\n", err)
		os.Exit(1)
	}
}
