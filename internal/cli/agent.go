package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"ltp/internal/helper"
	"ltp/internal/llm/agent"
	"ltp/internal/llm/providers"
)

func RunAgent(args []string) {
	manager, reg := InitRegistry(".ltp/topology.db")

	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "Error: DEEPSEEK_API_KEY environment variable is not set")
		os.Exit(1)
	}

	provider := providers.NewDeepSeek()
	cfg := helper.EnsureConfig(helper.ConfigPath(".ltp/topology.db"))
	toolReg := BuildToolRegistry(manager, reg, cfg, ToolProfileDefault)

	lang := GetLanguage(manager)
	topo, _ := manager.ReadAll()
	if topo != nil {
		lang = topo.Language
	}

	a := agent.New(provider, toolReg, lang)

	input := strings.Join(args, " ")
	if input != "" {
		if err := a.Run(input); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	scanner := bufio.NewScanner(os.Stdin)
	fmt.Print("> ")
	for scanner.Scan() {
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			fmt.Print("> ")
			continue
		}
		if input == "exit" || input == "quit" {
			break
		}
		if err := a.Run(input); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		fmt.Print("> ")
	}
}
