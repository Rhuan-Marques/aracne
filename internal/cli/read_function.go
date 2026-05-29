package cli

import (
	"fmt"
	"os"

	"llm-topology/internal/llm/languages/gotools"
	"llm-topology/internal/llm/languages/pythontools"
	"llm-topology/internal/topology/golang"
	"llm-topology/internal/topology/python"
)

func RunReadFunction() {
	args := os.Args[2:]
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: ltp read_function <name>")
		fmt.Fprintln(os.Stderr, "Example: ltp read_function ReadFunction")
		os.Exit(1)
	}
	name := args[0]

	manager, _ := InitRegistry(".ltp/topology.db")
	lang := GetLanguage(manager)

	if lang == "python" {
		pythonManager := python.NewPythonManager(manager)
		ctx, err := pythonManager.ReadFunction(name)
		if err == nil {
			fmt.Print(pythontools.FormatPythonFunctionContext(ctx))
			return
		}
		ids, err := pythonManager.FindFunctionsByName(name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if len(ids) == 0 {
			fmt.Fprintf(os.Stderr, "Function %q not found in topology\n", name)
			os.Exit(1)
		}
		if len(ids) > 1 {
			fmt.Printf("Multiple functions named %q found:\n", name)
			for _, id := range ids {
				fmt.Printf("  - %s\n", id)
			}
			return
		}
		ctx, err = pythonManager.ReadFunction(string(ids[0]))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Print(pythontools.FormatPythonFunctionContext(ctx))
		return
	}

	goManager := golang.NewGoManager(manager)

	ctx, err := goManager.ReadFunction(name)
	if err == nil {
		fmt.Print(gotools.FormatGoFunctionContext(ctx))
		return
	}

	ids, err := goManager.FindFunctionsByName(name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if len(ids) == 0 {
		fmt.Fprintf(os.Stderr, "Function %q not found in topology\n", name)
		os.Exit(1)
	}
	if len(ids) > 1 {
		fmt.Printf("Multiple functions named %q found:\n", name)
		for _, id := range ids {
			fmt.Printf("  - %s\n", id)
		}
		return
	}

	ctx, err = goManager.ReadFunction(string(ids[0]))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(gotools.FormatGoFunctionContext(ctx))
}