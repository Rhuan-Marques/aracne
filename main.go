package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"path/filepath"
	"strings"
	"time"

	"llm-topology/internal/helper"
	"llm-topology/internal/llm/agent"
	"llm-topology/internal/llm/languages/gotools"
	"llm-topology/internal/llm/languages/pythontools"
	"llm-topology/internal/llm/providers"
	"llm-topology/internal/llm/tools"
	"llm-topology/internal/mcp"
	"llm-topology/internal/topology"
	"llm-topology/internal/topology/domain"
	"llm-topology/internal/topology/golang"
	"llm-topology/internal/topology/python"
	"llm-topology/internal/topology/scanner"
	"llm-topology/internal/topology/scanner/goscanner"
	"llm-topology/internal/topology/scanner/pyscanner"
)

// Entry point of the ltp CLI. Parses os.Args to dispatch to subcommands: scan, agent, serve, init, descriptions (generate/apply), update-file, read_function, read_struct, or printUsage. Takes no parameters and returns nothing.
func main() {
	if len(os.Args) < 2 {
		printUsage()
		return
	}

	switch os.Args[1] {
	case "scan":
		runScan(os.Args[2:])
	case "agent":
		runAgent()
	case "serve":
		runServe()
	case "init":
		runInit(os.Args[2:], "")
	case "descriptions":
		if len(os.Args) < 3 {
			printUsage()
			return
		}
		switch os.Args[2] {
		case "generate":
			runGenerateDescriptions(os.Args[3:])
		case "apply":
			runDescriptionApply(os.Args[3:])
		default:
			printUsage()
		}
	case "update-file":
		runUpdateFile(os.Args[2:])
	case "read_function":
		runReadFunction()
	case "read_struct":
		runReadStruct()
		// Prints the ltp CLI usage information to stdout, documenting all subcommands (scan, agent, serve, init, descriptions, read_function, read_struct, etc.) and their flags. No parameters, no return value.
	case "read-resource-and-cut":
		runReadResourceAndCut(os.Args[2:])
	case "update-description":
		runUpdateDescription(os.Args[2:])
	case "edit":
		runEdit(os.Args[2:])
	case "list-undocumented":
		runListUndocumented()
	case "warnings":
		if len(os.Args) < 3 || os.Args[2] != "list" {
			fmt.Fprintln(os.Stderr, "Usage: ltp warnings list [--source <id>] [--target <id>] [--kind <kind>]")
			os.Exit(1)
		}
		runWarningsList(os.Args[3:])
	case "read_file":
		runReadFile()
	default:
		printUsage()
	}
}

func printUsage() {
	fmt.Println(`ltp - Go project topology analyzer

Usage:
  ltp scan    [flags]    Scan a Go project and build topology database
  ltp agent   [prompt]   Run the AI coding agent
  ltp serve              Start MCP server (for OpenCode / Claude Code integration)
  ltp init   [flags]    Initialize topology integration (--claude, --opencode, --global)
  ltp descriptions generate [flags]  Generate descriptions for all undocumented resources
  ltp descriptions apply            Write topology descriptions back into source as doc comments
  ltp read_function <name>  Show a function's source code and its interconnected context
  ltp read_struct <name>    Show a struct's source code and its interconnected context
  ltp update-file <path>    Re-parse a file and update the topology database
  ltp read-resource-and-cut <id> <kind>  Get a resource's source code cut (kind: Function, Struct, Interface, ExternalVar, File, Package)
  ltp update-description <id> <kind> <desc>  Update a resource's description in the topology DB
  ltp list-undocumented     List all resources without descriptions
  ltp warnings list [flags] List outstanding topology warnings
  ltp edit                 Edit a file (reads JSON from stdin: {"file_path", "old_string", "new_string"})
  ltp read_file <path>     Read a file by path, returning filename and full content

Flags for "scan":
  -root <path>    Root folder of the Go project (default ".")
  -output <file>  Output SQLite database path (default ".ltp/topology.db")
  --hard          Force full rebuild instead of incremental update
  --debug         Compare warnings before and after scan, print differences

Flags for "warnings list":
  --db <path>     Topology database path (default ".ltp/topology.db")
Flags for "warnings list":
  --target <id>   Filter by target resource ID
  --kind <kind>    Filter by warning kind (use_missing_node, node_removed, signature_changed)

Flags for "init":
  --claude        Initialize Claude Code integration (.mcp.json, CLAUDE.md, .claude/commands/)
  --opencode      Initialize OpenCode integration (.opencode/opencode.json, custom tools, commands)
  --global        Install to user-level (applies across all projects)

  Without flags, initializes both Claude Code and OpenCode.

  Examples:
    ltp scan -root ./myproject -output myproject.db
    ltp agent "list all structs"
// Parses CLI flags for the "scan" command (root, output, hard), creates a scanner registry, and runs either a FullScan or IncrementalScan on the project. Reports timing statistics and resource counts after completion.
    ltp init                   # init for both Claude Code and OpenCode
    ltp init --claude          # init for Claude Code only
    ltp init --opencode        # init for OpenCode only
    ltp init --global          # global config (both platforms)
    ltp serve                 # start MCP server (used by OpenCode)
    ltp descriptions generate # generate descriptions for all resources
    ltp descriptions apply    # write descriptions into source as doc comments
    ltp read_function ReadFunction
    ltp read_struct TopologyManager`)
}

func newScannerRegistry() *scanner.Registry {
	reg := scanner.NewRegistry()
	reg.Register(goscanner.NewGoScanner())
	reg.Register(pyscanner.NewPythonScanner())
	return reg
}

func getLanguage(manager *topology.TopologyManager) string {
	topo, err := manager.ReadAll()
	if err != nil || topo == nil {
		return "go"
	}
	if topo.Language != "" {
		return topo.Language
	}
	return "go"
}

func runScan(args []string) {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	root := fs.String("root", ".", "Root folder of the Go project to analyze")
	output := fs.String("output", ".ltp/topology.db", "Output SQLite database path")
	hard := fs.Bool("hard", false, "Force full rebuild (clears all existing descriptions)")
	debug := fs.Bool("debug", false, "Compare warnings before and after scan, print differences")
	fs.Parse(args)

	manager := topology.New()
	manager.Load(*output)
	os.MkdirAll(filepath.Dir(*output), 0755)

	var beforeWarnings map[string]domain.TopologyWarning
	if *debug {
		if _, statErr := os.Stat(*output); statErr == nil {
			if beforeTopo, readErr := helper.ReadDb(*output); readErr == nil {
				beforeWarnings = beforeTopo.Warnings
			}
		}
	}

	fmt.Printf("Analyzing Go project at: %s\n", *root)

	start := time.Now()
	reg := newScannerRegistry()
	if *hard {
		fmt.Println("Hard scan: rebuilding topology from scratch")
		if err := manager.FullScan(*root, reg); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	} else {
		fmt.Println("Incremental scan: preserving existing descriptions")
		if err := manager.IncrementalScan(*root, reg); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	}
	elapsed := time.Since(start)

	topo, err := helper.ReadDb(*output)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading output: %v\n", err)
		os.Exit(1)
	}

	if *debug && beforeWarnings != nil {
		added, removed := diffWarnings(beforeWarnings, topo.Warnings)
		if len(added) > 0 || len(removed) > 0 {
			fmt.Printf("\n=== Warning Differences ===\n\n")
			if len(added) > 0 {
				fmt.Printf("Added (%d):\n", len(added))
				for _, w := range added {
					fmt.Printf("  + [%s] %s\n    source: %s", w.Kind, w.Message, w.SourceID)
					if w.TargetID != "" {
						fmt.Printf("\n    target: %s", w.TargetID)
					}
					fmt.Print("\n\n")
				}
			}
			if len(removed) > 0 {
				fmt.Printf("Resolved (%d):\n", len(removed))
				for _, w := range removed {
					fmt.Printf("  - [%s] %s\n    source: %s", w.Kind, w.Message, w.SourceID)
					if w.TargetID != "" {
						fmt.Printf("\n    target: %s", w.TargetID)
					}
					fmt.Print("\n\n")
				}
			}
		} else {
			fmt.Println("\nNo warning differences detected.")
		}
	}

	pkgCount := 0
	fileCount := 0
	funcCount := 0
	typeCount := 0
	ifaceCount := 0
	varCount := 0
	depCount := 0

	for _, res := range topo.Resources {
		// Initializes the scanner registry and performs a full project topology scan, categorizing resources by kind (package, file, function, type, interface, variable, dependency) and displaying summary statistics.
		switch res.Kind {
		case domain.ResourcePackage:
			pkgCount++
		case domain.ResourceFile:
			fileCount++
		case domain.ResourceFunction, domain.ResourceMethod:
			funcCount++
		case domain.ResourceType, domain.ResourceNamedType:
			typeCount++
		case domain.ResourceInterface:
			ifaceCount++
		case domain.ResourceVariable:
			varCount++
		case domain.ResourceDependency:
			depCount++
		}
	}

	fmt.Printf("Topology written to: %s\n", *output)
	// CLI handler for the "agent" command. Initializes the topology database and tool registry, creates the DeepSeek provider and agent, then runs either a single-prompt session (from CLI args) or a REPL loop reading stdin prompts.
	fmt.Printf("Analyzed in %s\n", elapsed.Round(time.Millisecond))
	fmt.Printf("-%d packages\n-%d files\n-%d functions\n-%d types\n-%d interfaces\n-%d variables\n-%d dependencies\n-%d errors\n",
		pkgCount, fileCount, funcCount, typeCount, ifaceCount, varCount, depCount, len(topo.Errors))
}

func initRegistry(dbPath string) (*topology.TopologyManager, *scanner.Registry) {
	reg := newScannerRegistry()
	mgr := topology.New()
	os.MkdirAll(filepath.Dir(dbPath), 0755)
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		mgr.Load(dbPath)
		fmt.Fprintf(os.Stderr, "No topology found. Scanning project...\n")
		start := time.Now()
		if err := mgr.IncrementalScan(".", reg); err != nil {
			fmt.Fprintf(os.Stderr, "Error scanning project: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Topology built in %s\n", time.Since(start).Round(time.Millisecond))
	} else {
		mgr.Load(dbPath)
	}
	return mgr, reg
}

func runAgent() {
	manager, reg := initRegistry(".ltp/topology.db")

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

	lang := getLanguage(manager)
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

	args := os.Args[2:]
	input := strings.Join(args, " ")
	if input != "" {
		if err := a.Run(input); err != nil {
			// Initializes the topology database and scanner registry, then starts the MCP server on stdio to handle JSON-RPC tool requests from OpenCode or other MCP clients.
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
		// Configures the MCP server entry in opencode.json and installs the custom edit.ts tool. Supports --global flag for user-wide installation.
		if err := a.Run(input); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		fmt.Print("> ")
	}
}

func runServe() {
	manager, reg := initRegistry(".ltp/topology.db")

	registry := tools.NewRegistry()
	registry.Register(&tools.Ls{})
	registry.Register(&tools.ReadFile{})
	registry.Register(tools.NewEdit(manager, reg))

	lang := getLanguage(manager)
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

func runInit(args []string, defaultPlatform string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	claude := fs.Bool("claude", false, "Initialize Claude Code integration")
	opencode := fs.Bool("opencode", false, "Initialize OpenCode integration")
	global := fs.Bool("global", false, "Install globally")
	fs.Parse(args)

	if defaultPlatform != "" && !*claude && !*opencode {
		switch defaultPlatform {
		case "opencode":
			*opencode = true
		case "claude":
			*claude = true
		}
	}

	if !*claude && !*opencode {
		*claude = true
		*opencode = true
	}

	if *opencode {
		initOpenCode(*global)
	}
	if *claude {
		initClaudeCode(*global)
	}
}

func initOpenCode(global bool) {
	var configPath string
	if global {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error finding home dir: %v\n", err)
			os.Exit(1)
		}
		configPath = filepath.Join(home, ".config", "opencode", "opencode.json")
		os.MkdirAll(filepath.Dir(configPath), 0755)
	} else {
		configPath = ".opencode/opencode.json"
		os.MkdirAll(".opencode", 0755)
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

	mcpMap, _ := config["mcp"].(map[string]interface{})
	if mcpMap == nil {
		mcpMap = make(map[string]interface{})
	}

	if _, exists := mcpMap["llm-topology"]; !exists {
		mcpMap["llm-topology"] = map[string]interface{}{
			"type":    "local",
			"command": []string{"ltp", "serve"},
			"enabled": true,
		}
		config["mcp"] = mcpMap
		permissionMap, _ := config["permission"].(map[string]interface{})
		if permissionMap == nil {
			permissionMap = make(map[string]interface{})
		}
		for _, toolName := range []string{"read", "edit"} {
			if _, exists := permissionMap[toolName]; !exists {
				permissionMap[toolName] = "deny"
			}
		}
		config["permission"] = permissionMap

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
		fmt.Printf("[OpenCode] llm-topology MCP server configured in %s\n", configPath)
	} else {
		fmt.Printf("[OpenCode] llm-topology MCP config already present in %s\n", configPath)
	}

	var commandsDir string
	if global {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error finding home dir: %v\n", err)
			os.Exit(1)
		}
		commandsDir = filepath.Join(home, ".config", "opencode", "commands")
	} else {
		commandsDir = ".opencode/commands"
	}
	os.MkdirAll(commandsDir, 0755)

	writeOpenCodeCommand := func(name, description, template string) {
		cmdPath := filepath.Join(commandsDir, name+".md")
		if _, err := os.Stat(cmdPath); err == nil {
			fmt.Printf("[OpenCode] Command %s already present at %s\n", name, cmdPath)
			return
		}
		content := fmt.Sprintf("---\ndescription: %s\n---\n\n%s\n", description, template)
		if err := os.WriteFile(cmdPath, []byte(content), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing command %s: %v\n", name, err)
			os.Exit(1)
		}
		fmt.Printf("[OpenCode] Command %s written to %s\n", name, cmdPath)
	}

	writeOpenCodeCommand(
		"descriptions-generate",
		"Generate descriptions for undocumented resources in the topology",
		`The following resources are missing descriptions and need them:

!`+"`ltp list-undocumented`"+`

For each resource listed above, call **read_resource_and_cut** with its `+"`id`"+` and `+"`resource_name`"+`, then call **update_description** to write a concise description. Follow these guidelines:

- Functions: 1-3 lines covering purpose, parameters, return values, side effects
- Structs: 1-3 lines covering what it represents, key fields, usage
- Interfaces: 1-3 lines covering the contract and key methods
- Variables: 1 line covering what it stores and purpose
- Files: 1 line covering the file's role in its package
- Packages: 1-2 lines covering overall purpose

Process ALL resources listed above. Report how many descriptions were generated.`,
	)

	writeOpenCodeCommand(
		"descriptions-apply",
		"Write topology descriptions back into source files as doc comments",
		`Run `+"`ltp descriptions apply`"+` to write all topology descriptions back into the source files as Go doc comments. Report any files that were modified.`,
	)

	fmt.Println("[OpenCode] Restart OpenCode to activate the topology tools.")
}

func initClaudeCode(global bool) {
	var mcpConfigPath string
	var commandsDir string
	var agentsDir string
	var claudeMdPath string

	if global {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error finding home dir: %v\n", err)
			os.Exit(1)
		}
		mcpConfigPath = filepath.Join(home, ".claude.json")
		commandsDir = filepath.Join(home, ".claude", "commands")
		agentsDir = filepath.Join(home, ".claude", "agents")
		claudeMdPath = filepath.Join(home, ".claude", "CLAUDE.md")
	} else {
		mcpConfigPath = ".mcp.json"
		commandsDir = ".claude/commands"
		agentsDir = ".claude/agents"
		claudeMdPath = "CLAUDE.md"
	}

	var claudeConfig map[string]interface{}
	data, err := os.ReadFile(mcpConfigPath)
	if err == nil && len(data) > 0 {
		if err := json.Unmarshal(data, &claudeConfig); err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing %s: %v\n  Please fix or remove the file and try again.\n", mcpConfigPath, err)
			os.Exit(1)
		}
	}
	if claudeConfig == nil {
		claudeConfig = make(map[string]interface{})
	}

	if global {
		mcpServers, _ := claudeConfig["mcpServers"].(map[string]interface{})
		if mcpServers == nil {
			mcpServers = make(map[string]interface{})
		}
		if _, exists := mcpServers["llm-topology"]; !exists {
			mcpServers["llm-topology"] = map[string]interface{}{
				"command": "ltp",
				"args":    []string{"serve"},
			}
			claudeConfig["mcpServers"] = mcpServers

			out, err := json.MarshalIndent(claudeConfig, "", "  ")
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error encoding config: %v\n", err)
				os.Exit(1)
			}
			out = append(out, '\n')
			if err := os.WriteFile(mcpConfigPath, out, 0644); err != nil {
				fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", mcpConfigPath, err)
				os.Exit(1)
			}
			fmt.Printf("[Claude Code] Global MCP server configured in %s\n", mcpConfigPath)
		} else {
			fmt.Printf("[Claude Code] Global MCP config already present in %s\n", mcpConfigPath)
		}
	} else {
		mcpServers, _ := claudeConfig["mcpServers"].(map[string]interface{})
		if mcpServers == nil {
			mcpServers = make(map[string]interface{})
		}
		if _, exists := mcpServers["llm-topology"]; !exists {
			mcpServers["llm-topology"] = map[string]interface{}{
				"command": "ltp",
				"args":    []string{"serve"},
			}
			claudeConfig["mcpServers"] = mcpServers

			out, err := json.MarshalIndent(claudeConfig, "", "  ")
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error encoding config: %v\n", err)
				os.Exit(1)
			}
			out = append(out, '\n')
			if err := os.WriteFile(mcpConfigPath, out, 0644); err != nil {
				fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", mcpConfigPath, err)
				os.Exit(1)
			}
			fmt.Printf("[Claude Code] Project MCP server configured in %s\n", mcpConfigPath)
		} else {
			fmt.Printf("[Claude Code] MCP config already present in %s\n", mcpConfigPath)
		}
	}

	os.MkdirAll(commandsDir, 0755)
	os.MkdirAll(agentsDir, 0755)

	writeClaudeCommand := func(name, description, template string) {
		cmdPath := filepath.Join(commandsDir, name+".md")
		if _, err := os.Stat(cmdPath); err == nil {
			fmt.Printf("[Claude Code] Command %s already present at %s\n", name, cmdPath)
			return
		}
		content := fmt.Sprintf("---\ndescription: %s\n---\n\n%s\n", description, template)
		if err := os.WriteFile(cmdPath, []byte(content), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing command %s: %v\n", name, err)
			os.Exit(1)
		}
		fmt.Printf("[Claude Code] Command %s written to %s\n", name, cmdPath)
	}

	writeClaudeCommand(
		"descriptions-generate",
		"Generate descriptions for undocumented resources in the topology",
		`The following resources are missing descriptions and need them:

!`+"`ltp list-undocumented`"+`

For each resource listed above, call **read_resource_and_cut** with its `+"`id`"+` and `+"`resource_name`"+`, then call **update_description** to write a concise description. Follow these guidelines:

- Functions: 1-3 lines covering purpose, parameters, return values, side effects
- Structs: 1-3 lines covering what it represents, key fields, usage
- Interfaces: 1-3 lines covering the contract and key methods
- Variables: 1 line covering what it stores and purpose
- Files: 1 line covering the file's role in its package
- Packages: 1-2 lines covering overall purpose

Process ALL resources listed above. Report how many descriptions were generated.`,
	)

	writeClaudeCommand(
		"descriptions-apply",
		"Write topology descriptions back into source files as doc comments",
		`Run `+"`ltp descriptions apply`"+` to write all topology descriptions back into the source files as Go doc comments. Report any files that were modified.`,
	)

	agentPath := filepath.Join(agentsDir, "describe.md")
	if _, err := os.Stat(agentPath); err == nil {
		fmt.Printf("[Claude Code] Agent describe already present at %s\n", agentPath)
	} else {
		agentContent := `---
description: Generates descriptions for undocumented resources in the project topology
mode: subagent
---

You are a description generator for the project topology database.

Your goal is to generate concise descriptions for ALL undocumented resources.

## Workflow

1. Call **list_undocumented_resources** to get the full list of resources needing descriptions
2. For each resource in the list:
   a. Call **read_resource_and_cut** with its ` + "`id`" + ` and ` + "`resource_name`" + `
   b. Read the returned source code and type-specific instructions
   c. Call **update_description** with ` + "`id`" + `, ` + "`resource_name`" + `, and your generated description
3. Continue until all resources have been processed
4. Report how many descriptions were generated

## Guidelines

- Functions: 1-3 lines covering purpose, parameters, return values, side effects
- Structs: 1-3 lines covering what it represents, key fields, usage
- Interfaces: 1-3 lines covering the contract and key methods
- Variables: 1 line covering what it stores and purpose
- Files: 1 line covering the file's role in its package
- Packages: 1-2 lines covering overall purpose
- Be concise and accurate
- Do not skip any resource
`
		if err := os.WriteFile(agentPath, []byte(agentContent), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing agent: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("[Claude Code] Agent describe written to %s\n", agentPath)
	}

	if _, err := os.Stat(claudeMdPath); err == nil {
		fmt.Printf("[Claude Code] CLAUDE.md already present at %s\n", claudeMdPath)
	} else {
		claudeMdContent := fmt.Sprintf(`# CLAUDE.md

This project uses **llm-topology** for codebase navigation. The topology database provides a pre-analyzed graph of all functions, structs, interfaces, variables, and their relationships.

## Navigation Tools

Prefer these topology-aware tools over standard file reading:

| Tool | Purpose |
|------|---------|
| %[1]sread_function%[1]s | Function source + connected context (called functions, structs, interfaces) |
| %[1]sread_struct%[1]s | Struct source + methods, interfaces, constructor |
| %[1]sread_resource_and_cut%[1]s | Get source code + description instructions for any resource |
| %[1]supdate_description%[1]s | Persist a description into the topology database |
| %[1]slist_undocumented_resources%[1]s | List all resources missing descriptions |
| %[1]sls%[1]s | List files and directories |
| %[1]sread%[1]s | Read raw file contents (use only when topology tools aren't sufficient) |

## How to Use

1. Start with %[1]sls%[1]s to explore the project structure
2. Use %[1]sread_function%[1]s or %[1]sread_struct%[1]s to investigate code — these return both source code AND a # CONTEXT: section showing all connected resources
3. After editing a file, run %[1]sltp update-file <path>%[1]s to keep the topology in sync

## Description Generation

To document the project:
1. Call %[1]slist_undocumented_resources%[1]s
2. For each resource, call %[1]sread_resource_and_cut%[1]s followed by %[1]supdate_description%[1]s

## Guidelines

- Prefer topology tools over raw file reads — they provide richer context
- The # CONTEXT: section in tool output often answers follow-up questions without extra calls
- Keep descriptions concise (1-3 lines for functions/structs/interfaces)
`, "`")

		if err := os.WriteFile(claudeMdPath, []byte(claudeMdContent), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing CLAUDE.md: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("[Claude Code] CLAUDE.md written to %s\n", claudeMdPath)
	}

	fmt.Println("[Claude Code] Restart Claude Code to activate the topology tools.")
}

func runGenerateDescriptions(args []string) {
	fs := flag.NewFlagSet("generate-descriptions", flag.ExitOnError)
	fs.Parse(args)

	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "Error: DEEPSEEK_API_KEY environment variable is not set")
		os.Exit(1)
	}

	manager, reg := initRegistry(".ltp/topology.db")
	provider := providers.NewDeepSeek()

	toolReg := tools.NewRegistry()
	toolReg.Register(&tools.Ls{})
	toolReg.Register(&tools.ReadFile{})
	toolReg.Register(tools.NewEdit(manager, reg))

	lang := getLanguage(manager)
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

func runDescriptionApply(args []string) {
	manager, _ := initRegistry(".ltp/topology.db")

	topo, err := manager.ReadAll()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	count := 0
	// CLI handler for the update-file subcommand. Re-parses a single Go file, updates the topology database in-place via TopologyManager.UpdateFile, and prints any topology warnings.
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

// CLI handler for the read_resource_and_cut command: initializes the topology database, creates a GoManager, and looks up a resource by ID and kind, displaying its source code cut and generating description instructions.
func runReadFunction() {
	args := os.Args[2:]
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: ltp read_function <name>")
		fmt.Fprintln(os.Stderr, "Example: ltp read_function ReadFunction")
		os.Exit(1)
	}
	name := args[0]

	manager, _ := initRegistry(".ltp/topology.db")
	lang := getLanguage(manager)

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

func runReadStruct() {
	args := os.Args[2:]
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: ltp read_struct <name>")
		fmt.Fprintln(os.Stderr, "Example: ltp read_struct TopologyManager")
		os.Exit(1)
	}
	name := args[0]

	manager, _ := initRegistry(".ltp/topology.db")
	lang := getLanguage(manager)

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

func runUpdateFile(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: ltp update-file <path>")
		os.Exit(1)
	}
	path := args[0]
	manager, reg := initRegistry(".ltp/topology.db")
	warnings, err := manager.UpdateFile(path, reg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error updating file: %v\n", err)
		os.Exit(1)
	}
	if len(warnings) > 0 {
		for _, w := range warnings {
			fmt.Printf("Warning: [%s] %s (source: %s, target: %s)\n", w.Kind, w.Message, w.SourceID, w.TargetID)
		}
	}
}

func runEdit(args []string) {
	var input struct {
		FilePath  string `json:"file_path"`
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading stdin: %v\n", err)
		os.Exit(1)
	}
	if err := json.Unmarshal(data, &input); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing JSON: %v\n", err)
		os.Exit(1)
	}
	if input.FilePath == "" || input.OldString == "" || input.NewString == "" {
		fmt.Fprintln(os.Stderr, "Usage: echo '{\"file_path\":\"...\",\"old_string\":\"...\",\"new_string\":\"...\"}' | ltp edit")
		os.Exit(1)
	}

	content, err := os.ReadFile(input.FilePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading %s: %v\n", input.FilePath, err)
		os.Exit(1)
	}

	s := string(content)
	if !strings.Contains(s, input.OldString) {
		fmt.Fprintf(os.Stderr, "old_string not found in %s\n", input.FilePath)
		os.Exit(1)
	}

	newContent := strings.Replace(s, input.OldString, input.NewString, 1)
	if err := os.WriteFile(input.FilePath, []byte(newContent), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", input.FilePath, err)
		os.Exit(1)
	}

	manager, reg := initRegistry(".ltp/topology.db")
	warnings, err := manager.UpdateFile(input.FilePath, reg)
	if err == nil && len(warnings) > 0 {
		for _, w := range warnings {
			fmt.Printf("Warning: [%s] %s (source: %s, target: %s)\n", w.Kind, w.Message, w.SourceID, w.TargetID)
		}
	}
	fmt.Println("edit succeeded")
}

func runReadResourceAndCut(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: ltp read-resource-and-cut <id> <kind>")
		fmt.Fprintln(os.Stderr, "Kind: Function, Struct, Interface, ExternalVar, File, Package")
		os.Exit(1)
	}
	id, resourceName := args[0], args[1]

	manager, _ := initRegistry(".ltp/topology.db")
	lang := getLanguage(manager)

	kind := mapResourceKind(resourceName)
	if kind == "" {
		fmt.Fprintf(os.Stderr, "Error: unknown resource kind %q\n", resourceName)
		os.Exit(1)
	}

	if kind == domain.ResourceFile {
		fmt.Printf("File: %s\n", id)
		return
	}
	if kind == domain.ResourcePackage {
		fmt.Printf("Package: %s\n", id)
		return
	}

	var entry *domain.CodeEntry
	var err error

	if lang == "python" {
		pythonManager := python.NewPythonManager(manager)
		entry, err = pythonManager.ReadResourceAndCut(id, kind)
	} else {
		goManager := golang.NewGoManager(manager)
		entry, err = goManager.ReadResourceAndCut(id, kind)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Print(entry.Cut)
}

func runUpdateDescription(args []string) {
	if len(args) < 3 {
		fmt.Fprintln(os.Stderr, "Usage: ltp update-description <id> <kind> <description>")
		os.Exit(1)
	}
	id, resourceName := args[0], args[1]
	description := strings.Join(args[2:], " ")

	manager, _ := initRegistry(".ltp/topology.db")

	kind := mapResourceKind(resourceName)
	if kind == "" {
		fmt.Fprintf(os.Stderr, "Error: unknown resource kind %q\n", resourceName)
		os.Exit(1)
	}

	if err := manager.UpdateDescription(id, kind, description); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("description updated")
}

func runWarningsList(args []string) {
	dbPath := ".ltp/topology.db"
	sourceID := ""
	targetID := ""
	var kind domain.WarningKind
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--db":
			if i+1 < len(args) {
				dbPath = args[i+1]
				i++
			}
		case "--source":
			if i+1 < len(args) {
				sourceID = args[i+1]
				i++
			}
		case "--target":
			if i+1 < len(args) {
				targetID = args[i+1]
				i++
			}
		case "--kind":
			if i+1 < len(args) {
				kind = domain.WarningKind(args[i+1])
				i++
			}
		}
	}

	manager, _ := initRegistry(dbPath)
	warnings, err := manager.ListWarnings(sourceID, targetID, kind)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(warnings) == 0 {
		fmt.Println("No warnings found.")
		return
	}

	sort.SliceStable(warnings, func(i, j int) bool {
		if warnings[i].Kind != warnings[j].Kind {
			return warnings[i].Kind < warnings[j].Kind
		}
		return warnings[i].SourceID < warnings[j].SourceID
	})

	counts := make(map[domain.WarningKind]int)
	for _, w := range warnings {
		counts[w.Kind]++
	}

	fmt.Printf("Found %d warning(s):\n\n", len(warnings))
	for _, k := range []domain.WarningKind{domain.WarnUseMissingNode, domain.WarnNodeRemoved, domain.WarnSignatureChanged} {
		if c := counts[k]; c > 0 {
			fmt.Printf("  %s: %d\n", k, c)
		}
	}
	fmt.Println()

	for _, w := range warnings {
		fmt.Printf("  [%s] %s\n", w.Kind, w.Message)
		fmt.Printf("    source: %s\n", w.SourceID)
		if w.TargetID != "" {
			fmt.Printf("    target: %s\n", w.TargetID)
		}
		fmt.Println()
	}
}

func runListUndocumented() {
	manager, _ := initRegistry(".ltp/topology.db")

	topo, err := manager.ReadAll()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	var entries []struct {
		ID   string
		Name string
		Kind domain.ResourceKind
	}
	for id, res := range topo.Resources {
		if res.Description == "" {
			entries = append(entries, struct {
				ID   string
				Name string
				Kind domain.ResourceKind
			}{ID: id, Name: res.Name, Kind: res.Kind})
		}
	}

	if len(entries) == 0 {
		fmt.Println("All resources already have descriptions.")
		return
	}

	fmt.Printf("Found %d undocumented resources.\n\n", len(entries))
	for _, e := range entries {
		fmt.Printf("  ID: %s\n    Name: %s\n    Kind: %s\n\n", e.ID, e.Name, strings.ToUpper(string(e.Kind)))
	}
}

func mapResourceKind(name string) domain.ResourceKind {
	switch name {
	case "Function":
		return domain.ResourceFunction
	case "Struct", "Type":
		return domain.ResourceType
	case "Interface":
		return domain.ResourceInterface
	case "ExternalVar", "Variable":
		return domain.ResourceVariable
	case "File":
		return domain.ResourceFile
	case "Package":
		return domain.ResourcePackage
	}
	return ""
}

func runReadFile() {
	args := os.Args[2:]
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: ltp read_file <path>")
		os.Exit(1)
	}
	path := args[0]

	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	name := filepath.Base(path)
	fmt.Printf("%s\n%s", name, string(data))
}

func diffWarnings(before, after map[string]domain.TopologyWarning) (added, removed []domain.TopologyWarning) {
	for id, w := range after {
		if _, exists := before[id]; !exists {
			added = append(added, w)
		}
	}
	for id, w := range before {
		if _, exists := after[id]; !exists {
			removed = append(removed, w)
		}
	}
	return
}
