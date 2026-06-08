package cli

import (
	"flag"
	"fmt"
	"os"

	"ltp/internal/helper"
)

type repeatedStringFlag []string

func (f *repeatedStringFlag) String() string {
	return fmt.Sprint([]string(*f))
}

func (f *repeatedStringFlag) Set(value string) error {
	*f = append(*f, value)
	return nil
}

func RunAgentRoute(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: ltp agent-route record [--db <path>] --session <id> --platform <name> --kind <full_cut|description> --resource <id>")
		os.Exit(1)
	}
	switch args[0] {
	case "record":
		runAgentRouteRecord(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown agent-route command: %s\n", args[0])
		os.Exit(1)
	}
}

func runAgentRouteRecord(args []string) {
	fs := flag.NewFlagSet("agent-route record", flag.ExitOnError)
	dbPath := fs.String("db", ".ltp/topology.db", "Topology database path")
	sessionID := fs.String("session", "", "Agent/session ID")
	platform := fs.String("platform", "", "Agent platform: claude or opencode")
	label := fs.String("label", "", "Optional display label")
	accessKind := fs.String("kind", "", "Access kind: full_cut or description")
	var resources repeatedStringFlag
	fs.Var(&resources, "resource", "Resource ID; repeat for multiple resources")
	fs.Parse(args)

	cfg := helper.EnsureConfig(helper.ConfigPath(*dbPath))
	if err := helper.RecordAgentRouteAccess(*dbPath, *sessionID, *platform, *label, *accessKind, resources, cfg.EffectiveAgentRouteTTL()); err != nil {
		fmt.Fprintf(os.Stderr, "agent-route record failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("agent route recorded")
}
