package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"aracne/internal/topology"
	"aracne/internal/topology/domain"
)

// Dispatcher for bug management subcommands (report, list, acknowledge, dismiss, delete).
func RunBug(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: arac bug <report|list|acknowledge|dismiss|delete> [args...]")
		os.Exit(1)
	}
	switch args[0] {
	case "report":
		RunBugReport(args[1:])
	case "list":
		RunBugList(args[1:])
	case "acknowledge":
		RunBugAcknowledge(args[1:])
	case "dismiss":
		RunBugDismiss(args[1:])
	case "delete":
		RunBugDelete(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown bug command: %s\n", args[0])
		fmt.Fprintln(os.Stderr, "Usage: arac bug <report|list|acknowledge|dismiss|delete> [args...]")
		os.Exit(1)
	}
}

// Creates a bug report for a topology resource with a given node ID and description.
func RunBugReport(args []string) {
	dbPath := ".aracne/topology.db"
	nodeID := ""
	description := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--db":
			if i+1 < len(args) {
				dbPath = args[i+1]
				i++
			}
		case "--node":
			if i+1 < len(args) {
				nodeID = args[i+1]
				i++
			}
		case "--description":
			if i+1 < len(args) {
				description = args[i+1]
				i++
			}
		}
	}

	if nodeID == "" || description == "" {
		fmt.Fprintln(os.Stderr, "Usage: arac bug report --node <id> --description <text>")
		os.Exit(1)
	}

	manager, _ := InitRegistry(dbPath)
	// Same resolve-before-insert contract as the bug_report MCP tool: an id the topology
	// does not hold would be silently deleted by the next scan's orphan cleanup.
	resolved, err := manager.ResolveNodeID(nodeID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reporting bug: %v\n", err)
		os.Exit(1)
	}
	bug, err := manager.CreateBug(resolved, description)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reporting bug: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Bug reported: %s (node: %s, state: %s)\n", bug.ID, bug.NodeID, bug.State)
}

// bugManager opens the topology database WITHOUT the scan-if-missing behaviour of
// InitRegistry.
//
// The bug subcommands are the orchestration channel the generated slash commands drive
// through Bash, so they run several times per fan-out round. InitRegistry runs a full
// project scan and os.Exit(1)s when the database is absent, which is far too heavy — and
// far too destructive of the agent's turn — for what is a single SELECT. Load is a pure
// setter; the read paths need nothing else.
func bugManager(dbPath string) *topology.TopologyManager {
	mgr := topology.New()
	mgr.Load(dbPath)
	return mgr
}

// Lists topology bugs filtered by node ID and/or state.
func RunBugList(args []string) {
	dbPath := ".aracne/topology.db"
	nodeID := ""
	asJSON := false
	var state domain.BugState
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--db":
			if i+1 < len(args) {
				dbPath = args[i+1]
				i++
			}
		case "--node":
			if i+1 < len(args) {
				nodeID = args[i+1]
				i++
			}
		case "--state":
			if i+1 < len(args) {
				state = domain.BugState(args[i+1])
				i++
			}
		case "--json":
			asJSON = true
		}
	}

	bugs, err := bugManager(dbPath).ListBugs(nodeID, state)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing bugs: %v\n", err)
		os.Exit(1)
	}

	if asJSON {
		// Always an array, never "No bugs found." — this output is parsed by the agent
		// orchestrating the fan-out, and an empty round must decode, not read as prose.
		if bugs == nil {
			bugs = []domain.KnownBug{}
		}
		out, err := json.MarshalIndent(bugs, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error encoding bugs: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(string(out))
		return
	}

	if len(bugs) == 0 {
		fmt.Println("No bugs found.")
		return
	}

	fmt.Printf("Found %d bug(s):\n\n", len(bugs))
	for _, b := range bugs {
		fmt.Printf("  [%s] %s\n", b.State, b.ID)
		fmt.Printf("    node: %s\n", b.NodeID)
		fmt.Printf("    description: %s\n\n", b.Description)
	}
}

// Marks a bug as acknowledged in the topology database.
func RunBugAcknowledge(args []string) {
	dbPath := ".aracne/topology.db"
	bugID := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--db":
			if i+1 < len(args) {
				dbPath = args[i+1]
				i++
			}
		default:
			if bugID == "" {
				bugID = args[i]
			}
		}
	}

	if bugID == "" {
		fmt.Fprintln(os.Stderr, "Usage: arac bug acknowledge <bugID>")
		os.Exit(1)
	}

	if err := bugManager(dbPath).AcknowledgeBug(bugID); err != nil {
		fmt.Fprintf(os.Stderr, "Error acknowledging bug: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Bug %s acknowledged.\n", bugID)
}

// Marks a bug as dismissed in the topology database.
func RunBugDismiss(args []string) {
	dbPath := ".aracne/topology.db"
	bugID := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--db":
			if i+1 < len(args) {
				dbPath = args[i+1]
				i++
			}
		default:
			if bugID == "" {
				bugID = args[i]
			}
		}
	}

	if bugID == "" {
		fmt.Fprintln(os.Stderr, "Usage: arac bug dismiss <bugID>")
		os.Exit(1)
	}

	if err := bugManager(dbPath).DismissBug(bugID); err != nil {
		fmt.Fprintf(os.Stderr, "Error dismissing bug: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Bug %s dismissed.\n", bugID)
}

// Removes a bug from the topology database.
func RunBugDelete(args []string) {
	dbPath := ".aracne/topology.db"
	bugID := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--db":
			if i+1 < len(args) {
				dbPath = args[i+1]
				i++
			}
		default:
			if bugID == "" {
				bugID = args[i]
			}
		}
	}

	if bugID == "" {
		fmt.Fprintln(os.Stderr, "Usage: arac bug delete <bugID>")
		os.Exit(1)
	}

	if err := bugManager(dbPath).DeleteBug(bugID); err != nil {
		fmt.Fprintf(os.Stderr, "Error deleting bug: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Bug %s deleted.\n", bugID)
}
