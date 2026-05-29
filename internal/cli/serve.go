package cli

import (
	"fmt"
	"os"

	"llm-topology/internal/llm/languages/gotools"
	"llm-topology/internal/llm/languages/pythontools"
	"llm-topology/internal/llm/tools"
	"llm-topology/internal/mcp"
	"llm-topology/internal/topology/golang"
	"llm-topology/internal/topology/python"
)

func RunServe() {
	manager, reg := InitRegistry(".ltp/topology.db")

	registry := tools.NewRegistry()
	registry.Register(&tools.Ls{})
	registry.Register(&tools.ReadFile{})
	registry.Register(tools.NewEdit(manager, reg))

	lang := GetLanguage(manager)
	if lang == "python" {
		pythonManager := python.NewPythonManager(manager)
		pythontools.RegisterPythonTools(registry, pythonManager)
	} else {
		goManager := golang.NewGoManager(manager)
		gotools.RegisterGoTools(registry, goManager)
	}

	server := mcp.NewServer(registry)
	if err := server.Serve(); err != nil {
		fmt.Fprintf(os.Stderr, "MCP server error: %v\n", err)
		os.Exit(1)
	}
}