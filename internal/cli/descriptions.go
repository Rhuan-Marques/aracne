package cli

import (
	"flag"
	"fmt"
	"os"

	"llm-topology/internal/helper"
	"llm-topology/internal/llm/agent"
	"llm-topology/internal/llm/languages/gotools"
	"llm-topology/internal/llm/languages/pythontools"
	"llm-topology/internal/llm/providers"
	"llm-topology/internal/llm/tools"
	"llm-topology/internal/topology/golang"
	"llm-topology/internal/topology/python"
)

func RunGenerateDescriptions(args []string) {
	fs := flag.NewFlagSet("generate-descriptions", flag.ExitOnError)
	fs.Parse(args)

	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "Error: DEEPSEEK_API_KEY environment variable is not set")
		os.Exit(1)
	}

	manager, reg := InitRegistry(".ltp/topology.db")
	provider := providers.NewDeepSeek()

	toolReg := tools.NewRegistry()
	toolReg.Register(&tools.Ls{})
	toolReg.Register(&tools.ReadFile{})
	toolReg.Register(tools.NewEdit(manager, reg))

	lang := GetLanguage(manager)
	if lang == "python" {
		pythonManager := python.NewPythonManager(manager)
		pythontools.RegisterPythonTools(toolReg, pythonManager)
	} else {
		goManager := golang.NewGoManager(manager)
		gotools.RegisterGoTools(toolReg, goManager)
	}

	topo, _ := manager.ReadAll()
	if topo != nil {
		lang = topo.Language
	}

	count := 0
	if topo != nil {
		for _, res := range topo.Resources {
			if res.Description == "" {
				count++
			}
		}
	}
	fmt.Printf("Generating descriptions for %d resources...\n", count)

	a := agent.New(provider, toolReg, lang)
	a.SetMaxIterations(200)

	err := a.Run("Generate descriptions for all undocumented resources. Use list_undocumented_resources first, then dispatch descriptor sub-agents for each resource. Process ALL of them.")
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nError: %v\n", err)
		os.Exit(1)
	}
}

func RunDescriptionApply(args []string) {
	manager, _ := InitRegistry(".ltp/topology.db")

	topo, err := manager.ReadAll()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	count := 0
	for _, res := range topo.Resources {
		if res.Description != "" {
			count++
		}
	}
	fmt.Printf("Applying %d descriptions to source files...\n", count)

	if err := helper.ApplyDescriptions(topo); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("done")
}