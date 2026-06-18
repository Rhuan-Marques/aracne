package main

import (
	"fmt"
	"os"

	"aracne/internal/cli"
)

// main dispatches Aracne subcommands
func main() {
	if len(os.Args) < 2 {
		cli.PrintUsage()
		return
	}

	switch os.Args[1] {
	case "scan":
		cli.RunScan(os.Args[2:])
	case "agent":
		cli.RunAgent(os.Args[2:])
	case "serve":
		cli.RunServe(os.Args[2:])
	case "viz":
		cli.RunViz(os.Args[2:])
	case "init":
		cli.RunInit(os.Args[2:])
	case "disable":
		cli.RunDisable(os.Args[2:])
	case "descriptions":
		if len(os.Args) < 3 {
			cli.PrintUsage()
			return
		}
		switch os.Args[2] {
		case "generate":
			cli.RunGenerateDescriptions(os.Args[3:])
		case "apply":
			cli.RunDescriptionApply(os.Args[3:])
		case "clear":
			cli.RunClearDescriptions(os.Args[3:])
		default:
			cli.PrintUsage()
		}
	case "update-file":
		cli.RunUpdateFile(os.Args[2:])
	case "guard":
		cli.RunGuard(os.Args[2:])
	case "read":
		cli.RunRead()
	case "grep":
		cli.RunGrep(os.Args[2:])
	case "update-description":
		cli.RunUpdateDescription(os.Args[2:])
	case "edit":
		cli.RunEdit()
	case "write":
		cli.RunWrite()
	case "resource":
		if len(os.Args) < 3 || os.Args[2] != "list" {
			fmt.Fprintln(os.Stderr, "Usage: arac resource list [query] [--kind <kind>]... [--no-description]")
			os.Exit(1)
		}
		cli.RunResourceList(os.Args[3:])
	case "node":
		if len(os.Args) < 3 || os.Args[2] != "count" {
			fmt.Fprintln(os.Stderr, "Usage: arac node count [--no-description]")
			os.Exit(1)
		}
		noDesc := len(os.Args) > 3 && os.Args[3] == "--no-description"
		switch os.Args[2] {
		case "count":
			if noDesc {
				cli.RunNodeCountNoDescription()
			} else {
				cli.RunNodeCount()
			}
		}
	case "warnings":
		if len(os.Args) < 3 || os.Args[2] != "list" {
			fmt.Fprintln(os.Stderr, "Usage: arac warnings list [--source <id>] [--target <id>] [--kind <kind>]")
			os.Exit(1)
		}
		cli.RunWarningsList(os.Args[3:])
	case "bug":
		cli.RunBug(os.Args[2:])
	case "scanner":
		cli.RunScanner(os.Args[2:])
	case "check-updates":
		cli.RunCheckUpdates(os.Args[2:])
	case "analyze":
		cli.RunAnalyze(os.Args[2:])
	default:
		cli.PrintUsage()
	}
}
