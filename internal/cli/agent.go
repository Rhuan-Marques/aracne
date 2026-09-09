package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/llm/agent"
	"github.com/Rhuan-Marques/aracne/internal/llm/providers"
)

// Runs the AI coding agent with DeepSeek provider, accepting input from CLI args or interactive stdin.
//
// Gated by features.agent. The harness is not part of 1.0 -- it is unproven next to the
// harnesses that do ship, and it is the one surface needing a provider key of its own -- but
// it stays dispatchable so turning the flag on is the only step required.
func RunAgent(args []string) {
	dbPath := ProjectDBPath(DefaultDBRelative)
	if !helper.LoadConfig(helper.ConfigPath(dbPath)).AgentEnabled() {
		fmt.Fprintln(os.Stderr, "arac agent is not enabled for this project.")
		fmt.Fprintln(os.Stderr, `Set {"features": {"agent": true}} in .aracne/config.json to turn it on.`)
		os.Exit(1)
	}

	manager, reg := InitRegistry(dbPath)

	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "Error: DEEPSEEK_API_KEY environment variable is not set")
		os.Exit(1)
	}

	provider := providers.NewDeepSeek()
	cfg := helper.EnsureConfig(helper.ConfigPath(dbPath))
	toolReg := BuildToolRegistry(manager, reg, cfg, "claude_code", "main")

	a := agent.New(provider, toolReg, cfg, TopologyLanguagesFor(manager))

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
