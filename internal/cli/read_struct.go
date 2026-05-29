package cli

import (
	"fmt"
	"os"

	"llm-topology/internal/llm/languages/gotools"
	"llm-topology/internal/llm/languages/pythontools"
	"llm-topology/internal/topology/golang"
	"llm-topology/internal/topology/python"
)

func RunReadStruct() {
	args := os.Args[2:]
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: ltp read_struct <name>")
		fmt.Fprintln(os.Stderr, "Example: ltp read_struct TopologyManager")
		os.Exit(1)
	}
	name := args[0]

	manager, _ := InitRegistry(".ltp/topology.db")
	lang := GetLanguage(manager)

	if lang == "python" {
		pythonManager := python.NewPythonManager(manager)
		ctx, err := pythonManager.ReadClass(name)
		if err == nil {
			fmt.Print(pythontools.FormatPythonClassContext(ctx))
			return
		}
		ids, err := pythonManager.FindClassesByName(name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if len(ids) == 0 {
			fmt.Fprintf(os.Stderr, "Class %q not found in topology\n", name)
			os.Exit(1)
		}
		if len(ids) > 1 {
			fmt.Printf("Multiple classes named %q found:\n", name)
			for _, id := range ids {
				fmt.Printf("  - %s\n", id)
			}
			return
		}
		ctx, err = pythonManager.ReadClass(string(ids[0]))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Print(pythontools.FormatPythonClassContext(ctx))
		return
	}

	goManager := golang.NewGoManager(manager)

	ctx, err := goManager.ReadStruct(name)
	if err == nil {
		fmt.Print(gotools.FormatGoStructContext(ctx))
		return
	}

	ids, err := goManager.FindStructsByName(name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if len(ids) == 0 {
		fmt.Fprintf(os.Stderr, "Struct %q not found in topology\n", name)
		os.Exit(1)
	}
	if len(ids) > 1 {
		fmt.Printf("Multiple structs named %q found:\n", name)
		for _, id := range ids {
			fmt.Printf("  - %s\n", id)
		}
		return
	}

	ctx, err = goManager.ReadStruct(string(ids[0]))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(gotools.FormatGoStructContext(ctx))
}