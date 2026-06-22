package cli

import (
	"fmt"
	"os"

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
	bug, err := manager.CreateBug(nodeID, description)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reporting bug: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Bug reported: %s (state: %s)\n", bug.ID, bug.State)
}

// Lists topology bugs filtered by node ID and/or state.
func RunBugList(args []string) {
	dbPath := ".aracne/topology.db"
	nodeID := ""
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
		}
	}

	manager, _ := InitRegistry(dbPath)
	bugs, err := manager.ListBugs(nodeID, state)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing bugs: %v\n", err)
		os.Exit(1)
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

	manager, _ := InitRegistry(dbPath)
	if err := manager.AcknowledgeBug(bugID); err != nil {
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

	manager, _ := InitRegistry(dbPath)
	if err := manager.DismissBug(bugID); err != nil {
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

	manager, _ := InitRegistry(dbPath)
	if err := manager.DeleteBug(bugID); err != nil {
		fmt.Fprintf(os.Stderr, "Error deleting bug: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Bug %s deleted.\n", bugID)
}
