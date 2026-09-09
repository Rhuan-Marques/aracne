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

	dbPath := ProjectDBPath(DefaultDBRelative)
	manager, reg := InitRegistry(dbPath)
	cfg := helper.EnsureConfig(helper.ConfigPath(dbPath))
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "Invalid .aracne/config.json: %v\n", err)
		os.Exit(1)
	}
	if !ValidAgentProfile(cfg, *profileName) {
		fmt.Fprintf(os.Stderr, "invalid --tool-profile %q\n", *profileName)
		os.Exit(1)
	}
	registry := BuildToolRegistry(manager, reg, cfg, *harness, *profileName)
	// Refused on an EMPTY REGISTRY, not on the mode.
	//
	// Serving nothing instead of saying so is the failure that looks like a working server:
	// the harness connects, lists nothing, and the model is told about tools it will never be
	// offered. But the test used to be `!MCPEnabled()`, which also refused every SUB-AGENT
	// profile -- and those declare their own scoped server inline in exactly the three modes
	// that test rejected, so `arac serve --tool-profile descriptions-generation-executor`
	// exited 1 and the descriptions pipeline had no way to write anything.
	if len(registry.List()) == 0 {
		if helper.IsMainAgentName(*profileName) {
			fmt.Fprintf(os.Stderr, "aracne: mode %q serves no MCP tools to the main agent. Set "+
				"\"mode\": \"mcp\" in .aracne/config.json (or run `arac init`) to wire the server, "+
				"or remove the aracne entry from this harness's MCP config.\n", cfg.EffectiveMode())
		} else {
			fmt.Fprintf(os.Stderr, "aracne: --tool-profile %q has no mcp_tools to serve. Give it "+
				"some under llm.<harness>.agents.%s.mcp_tools in .aracne/config.json.\n",
				*profileName, *profileName)
		}
		os.Exit(1)
	}

	server := mcp.NewServer(registry)
	if err := server.Serve(); err != nil {
		fmt.Fprintf(os.Stderr, "MCP server error: %v\n", err)
		os.Exit(1)
	}
}
