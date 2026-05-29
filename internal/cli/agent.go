package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"llm-topology/internal/llm/agent"
	"llm-topology/internal/llm/languages/gotools"
	"llm-topology/internal/llm/languages/pythontools"
	"llm-topology/internal/llm/providers"
	"llm-topology/internal/llm/tools"
	"llm-topology/internal/topology/golang"
	"llm-topology/internal/topology/python"
)

func RunAgent(args []string) {
	manager, reg := InitRegistry(".ltp/topology.db")

	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "Error: DEEPSEEK_API_KEY environment variable is not set")
		os.Exit(1)
	}

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