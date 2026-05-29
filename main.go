package main

import (
	"fmt"
	"os"

	"llm-topology/internal/cli"
)

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
		cli.RunServe()
	case "init":
		cli.RunInit(os.Args[2:])
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
		default:
			cli.PrintUsage()
		}
	case "update-file":
		cli.RunUpdateFile(os.Args[2:])
	case "read_function":
		cli.RunReadFunction()
	case "read_struct":
		cli.RunReadStruct()
	case "read-resource-and-cut":
		cli.RunReadResourceAndCut(os.Args[2:])
	case "update-description":
		cli.RunUpdateDescription(os.Args[2:])
	case "edit":
		cli.RunEdit()
	case "list-undocumented":
		cli.RunListUndocumented()
	case "warnings":
		if len(os.Args) < 3 || os.Args[2] != "list" {
			fmt.Fprintln(os.Stderr, "Usage: ltp warnings list [--source <id>] [--target <id>] [--kind <kind>]")
			os.Exit(1)
		}
		cli.RunWarningsList(os.Args[3:])
	case "read_file":
		cli.RunReadFile()
	default:
		cli.PrintUsage()
	}
}
