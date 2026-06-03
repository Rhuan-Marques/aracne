package cli

import (
	"flag"
	"fmt"
	"os"

	"ltp/internal/helper"
	"ltp/internal/llm/agent"
	"ltp/internal/llm/providers"
)

func RunGenerateDescriptions(args []string) {
	fs := flag.NewFlagSet("generate-descriptions", flag.ExitOnError)
	targetsFlag := fs.String("targets", "", "Comma-separated resource kinds to describe (overrides config describe_targets)")
	fs.Parse(args)

	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "Error: DEEPSEEK_API_KEY environment variable is not set")
		os.Exit(1)
	}

	manager, reg := InitRegistry(".ltp/topology.db")
	provider := providers.NewDeepSeek()
	cfg := helper.EnsureConfig(helper.ConfigPath(".ltp/topology.db"))
	if *targetsFlag != "" {
		targets, err := helper.ParseDescribeTargets(*targetsFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		cfg.DescribeTargets = targets
	}
	toolReg := BuildToolRegistry(manager, reg, cfg, ToolProfileDescriptor)

	lang := GetLanguage(manager)
	topo, _ := manager.ReadAll()
	if topo != nil {
		lang = topo.Language
	}

	targetSet := helper.DescribeTargetSet(cfg.DescribeTargets)
	count := 0
	if topo != nil {
		for _, res := range topo.Resources {
			if res.Description == "" && targetSet[res.Kind] {
				count++
			}
		}
	}
	fmt.Printf("Generating descriptions for %d resources (targets: %s)...\n", count, helper.FormatDescribeTargets(cfg.DescribeTargets))

	a := agent.New(provider, toolReg, lang)
	a.SetMaxIterations(200)

	err := a.Run(fmt.Sprintf("Generate descriptions for undocumented resources matching these target kinds only: %s. Use list_undocumented_resources first, then process every listed resource with read_resource_and_cut and update_description. Process ALL listed resources.", helper.FormatDescribeTargets(cfg.DescribeTargets)))
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
