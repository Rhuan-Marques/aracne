package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"llm-topology/internal/helper"
	"llm-topology/internal/llm/agent"
	"llm-topology/internal/llm/providers"
	"llm-topology/internal/llm/tools"
	"llm-topology/internal/mcp"
	"llm-topology/internal/mermaid"
	"llm-topology/internal/topology"
	"llm-topology/internal/topology/domain"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		return
	}

	switch os.Args[1] {
	case "scan":
		runScan(os.Args[2:])
	case "mermaid":
		runMermaid(os.Args[2:])
	case "agent":
		runAgent()
	case "serve":
		runServe()
	case "install":
		runInstall(os.Args[2:])
	default:
		printUsage()
	}
}

func printUsage() {
	fmt.Println(`ltp - Go project topology analyzer

Usage:
  ltp scan    [flags]    Scan a Go project and build topology database
  ltp mermaid [flags]    Generate a Mermaid diagram from topology database
  ltp agent   [prompt]   Run the AI coding agent
  ltp serve              Start MCP server (for OpenCode plugin integration)
  ltp install [flags]    Configure OpenCode to use llm-topology as a plugin

Flags for "scan":
  -root <path>    Root folder of the Go project (default ".")
  -output <file>  Output SQLite database path (default ".ltp/topology.db")

Flags for "mermaid":
  -input <file>   Input topology database (default ".ltp/topology.db")
  -output <file>  Output .mermaid file path (default ".ltp/topology.mermaid")
  --filter <csv>  Comma-separated resource types to include:
                  Package, File, Function, Struct, Interface, ExternalVar, Dependency

Flags for "install":
  --global        Install globally (~/.config/opencode/opencode.json)

  Examples:
    ltp scan -root ./myproject -output myproject.db
    ltp mermaid -input myproject.db -output diagram.mermaid --filter "Function, Struct"
    ltp agent "list all structs"
    ltp install               # add MCP config to opencode.json
    ltp serve                 # start MCP server (used by OpenCode)`)
}

func runScan(args []string) {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	root := fs.String("root", ".", "Root folder of the Go project to analyze")
	output := fs.String("output", ".ltp/topology.db", "Output SQLite database path")
	fs.Parse(args)

	manager := topology.New()
	manager.Load(*output)
	os.MkdirAll(filepath.Dir(*output), 0755)

	fmt.Printf("Analyzing Go project at: %s\n", *root)

	start := time.Now()
	if err := manager.FullScan(*root); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	elapsed := time.Since(start)

	topo, err := helper.ReadDb(*output)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading output: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Topology written to: %s\n", *output)
	fmt.Printf("Analyzed in %s\n", elapsed.Round(time.Millisecond))
	fmt.Printf("-%d packages\n-%d files\n-%d structs\n-%d interfaces\n-%d functions\n-%d external vars\n-%d dependencies\n-%d errors\n",
		len(topo.Packages), len(topo.Files), len(topo.Struct), len(topo.Interfaces),
		len(topo.Functions), len(topo.ExternalVars), len(topo.Dependancies), len(topo.Errors))
}

var resourceNames = map[string]domain.ResourceName{
	"Package":     domain.PACKAGE_RESOURCE,
	"File":        domain.FILE_RESOURCE,
	"Function":    domain.FUNCTION_RESOURCE,
	"Struct":      domain.STRUCT_RESOURCE,
	"Interface":   domain.INTERFACE_RESOURCE,
	"ExternalVar": domain.EXTERNAL_VAR_RESOURCE,
	"Dependency":  domain.DEPENDENCY_RESOURCE,
}

func parseResourceFilter(csv string) []domain.ResourceName {
	if csv == "" {
		return nil
	}
	var result []domain.ResourceName
	for _, s := range strings.Split(csv, ",") {
		s = strings.TrimSpace(s)
		if r, ok := resourceNames[s]; ok {
			result = append(result, r)
		}
	}
	return result
}

func runMermaid(args []string) {
	fs := flag.NewFlagSet("mermaid", flag.ExitOnError)
	input := fs.String("input", ".ltp/topology.db", "Input topology database path")
	output := fs.String("output", ".ltp/topology.mermaid", "Output .mermaid file path")
	filter := fs.String("filter", "", "Comma-separated resource types to include")
	fs.Parse(args)

	topo, err := helper.ReadDb(*input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading %s: %v\n", *input, err)
		os.Exit(1)
	}

	resourceFilter := parseResourceFilter(*filter)
	os.MkdirAll(filepath.Dir(*output), 0755)
	if err := mermaid.GenerateToFile(*output, topo, resourceFilter...); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating mermaid: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Mermaid diagram written to %s\n", *output)
	if *filter != "" {
		fmt.Printf("Filter: %s\n", *filter)
	}
}

func runAgent() {
	manager := topology.New()
	dbPath := ".ltp/topology.db"
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		manager.Load(dbPath)
		fmt.Printf("No topology found. Scanning project...\n")
		start := time.Now()
		if err := manager.FullScan("."); err != nil {
			fmt.Fprintf(os.Stderr, "Error scanning project: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Topology built in %s\n", time.Since(start).Round(time.Millisecond))
	} else {
		manager.Load(dbPath)
	}

	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "Error: DEEPSEEK_API_KEY environment variable is not set")
		os.Exit(1)
	}

	provider := providers.NewDeepSeek()

	reg := tools.NewRegistry()
	reg.Register(&tools.Ls{})
	reg.Register(&tools.Read{})
	reg.Register(tools.NewReadFunction(manager))
	reg.Register(tools.NewReadStruct(manager))
	reg.Register(tools.NewEdit(manager))

	a := agent.New(provider, reg)

	args := os.Args[2:]
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

func initManager(dbPath string) *topology.TopologyManager {
	mgr := topology.New()
	os.MkdirAll(filepath.Dir(dbPath), 0755)
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		mgr.Load(dbPath)
		fmt.Fprintf(os.Stderr, "No topology found. Scanning project...\n")
		start := time.Now()
		if err := mgr.FullScan("."); err != nil {
			fmt.Fprintf(os.Stderr, "Error scanning project: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Topology built in %s\n", time.Since(start).Round(time.Millisecond))
	} else {
		mgr.Load(dbPath)
	}
	return mgr
}

func runServe() {
	manager := initManager(".ltp/topology.db")

	server := mcp.NewServer(manager)
	if err := server.Serve(); err != nil {
		fmt.Fprintf(os.Stderr, "MCP server error: %v\n", err)
		os.Exit(1)
	}
}

func runInstall(args []string) {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	global := fs.Bool("global", false, "Install globally (~/.config/opencode/opencode.json)")
	fs.Parse(args)

	var configPath string
	if *global {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error finding home dir: %v\n", err)
			os.Exit(1)
		}
		configPath = filepath.Join(home, ".config", "opencode", "opencode.json")
		os.MkdirAll(filepath.Dir(configPath), 0755)
	} else {
		configPath = "opencode.json"
	}

	var config map[string]interface{}
	data, err := os.ReadFile(configPath)
	if err == nil && len(data) > 0 {
		if err := json.Unmarshal(data, &config); err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing %s: %v\n  Please fix or remove the file and try again.\n", configPath, err)
			os.Exit(1)
		}
	}
	if config == nil {
		config = make(map[string]interface{})
	}

	mcp, _ := config["mcp"].(map[string]interface{})
	if mcp == nil {
		mcp = make(map[string]interface{})
	}

	if _, exists := mcp["llm-topology"]; exists {
		fmt.Printf("llm-topology MCP config already present in %s\n", configPath)
		return
	}

	mcp["llm-topology"] = map[string]interface{}{
		"type":    "local",
		"command": "ltp",
		"args":    []string{"serve"},
	}
	config["mcp"] = mcp

	out, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error encoding config: %v\n", err)
		os.Exit(1)
	}
	out = append(out, '\n')

	if err := os.WriteFile(configPath, out, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", configPath, err)
		os.Exit(1)
	}

	fmt.Printf("llm-topology MCP server configured in %s\n", configPath)
	fmt.Println("Restart OpenCode to activate the topology tools.")
}
