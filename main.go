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
	"llm-topology/internal/llm/languages/gotools"
	"llm-topology/internal/llm/providers"
	"llm-topology/internal/llm/tools"
	"llm-topology/internal/mcp"
	"llm-topology/internal/topology"
	"llm-topology/internal/topology/domain"
	"llm-topology/internal/topology/golang"
	"llm-topology/internal/topology/scanner"
	"llm-topology/internal/topology/scanner/goscanner"
)

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
	case "install":
		runInstall(os.Args[2:])
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
	case "read-resource-and-cut":
		runReadResourceAndCut(os.Args[2:])
	case "update-description":
		runUpdateDescription(os.Args[2:])
	case "list-undocumented":
		runListUndocumented()
	default:
		printUsage()
	}
}

func printUsage() {
	fmt.Println(`ltp - Go project topology analyzer

Usage:
  ltp scan    [flags]    Scan a Go project and build topology database
  ltp agent   [prompt]   Run the AI coding agent
  ltp serve              Start MCP server (for OpenCode plugin integration)
  ltp install [flags]    Configure OpenCode to use llm-topology as a plugin
  ltp descriptions generate [flags]  Generate descriptions for all undocumented resources
  ltp descriptions apply            Write topology descriptions back into source as doc comments
  ltp read_function <name>  Show a function's source code and its interconnected context
  ltp read_struct <name>    Show a struct's source code and its interconnected context
  ltp update-file <path>    Re-parse a file and update the topology database
  ltp read-resource-and-cut <id> <kind>  Get a resource's source code cut (kind: Function, Struct, Interface, ExternalVar, File, Package)
  ltp update-description <id> <kind> <desc>  Update a resource's description in the topology DB
  ltp list-undocumented     List all resources without descriptions

Flags for "scan":
  -root <path>    Root folder of the Go project (default ".")
  -output <file>  Output SQLite database path (default ".ltp/topology.db")
  --hard          Force full rebuild instead of incremental update

Flags for "install":
  --global        Install globally (~/.config/opencode/opencode.json)

Flags for "descriptions generate":
  (no flags required — uses agent loop to process all undocumented resources)

  Examples:
    ltp scan -root ./myproject -output myproject.db
    ltp agent "list all structs"
    ltp install               # add MCP config to opencode.json
    ltp serve                 # start MCP server (used by OpenCode)
    ltp descriptions generate # generate descriptions for all resources
    ltp descriptions apply    # write descriptions into source as doc comments
    ltp read_function ReadFunction
    ltp read_struct TopologyManager`)
}

func newScannerRegistry() *scanner.Registry {
	reg := scanner.NewRegistry()
	reg.Register(goscanner.NewGoScanner())
	return reg
}

func runScan(args []string) {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	root := fs.String("root", ".", "Root folder of the Go project to analyze")
	output := fs.String("output", ".ltp/topology.db", "Output SQLite database path")
	hard := fs.Bool("hard", false, "Force full rebuild (clears all existing descriptions)")
	fs.Parse(args)

	manager := topology.New()
	manager.Load(*output)
	os.MkdirAll(filepath.Dir(*output), 0755)

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

	pkgCount := 0
	fileCount := 0
	funcCount := 0
	typeCount := 0
	ifaceCount := 0
	varCount := 0
	depCount := 0

	for _, res := range topo.Resources {
		switch res.Kind {
		case domain.ResourcePackage:
			pkgCount++
		case domain.ResourceFile:
			fileCount++
		case domain.ResourceFunction, domain.ResourceMethod:
			funcCount++
		case domain.ResourceType:
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
	goManager := golang.NewGoManager(manager)

	toolReg := tools.NewRegistry()
	toolReg.Register(&tools.Ls{})
	toolReg.Register(&tools.Read{})
	toolReg.Register(tools.NewEdit(manager, reg))
	gotools.RegisterGoTools(toolReg, goManager)

	topo, _ := manager.ReadAll()
	lang := "go"
	if topo != nil {
		lang = topo.Language
	}

	a := agent.New(provider, toolReg, lang)

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

func runServe() {
	manager, reg := initRegistry(".ltp/topology.db")
	goManager := golang.NewGoManager(manager)

	registry := tools.NewRegistry()
	registry.Register(&tools.Ls{})
	registry.Register(&tools.Read{})
	registry.Register(tools.NewEdit(manager, reg))
	gotools.RegisterGoTools(registry, goManager)

	server := mcp.NewServer(registry)
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
		configPath = filepath.Join(filepath.Dir(configPath), ".opencode", "opencode.json")
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
	} else {
		fmt.Printf("llm-topology MCP config already present in %s\n", configPath)
	}

	var toolsDir string
	if *global {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error finding home dir: %v\n", err)
			os.Exit(1)
		}
		toolsDir = filepath.Join(home, ".config", "opencode", "tools")
	} else {
		toolsDir = filepath.Join(filepath.Dir(configPath), ".opencode", "tools")
	}
	os.MkdirAll(toolsDir, 0755)

	toolPath := filepath.Join(toolsDir, "edit.ts")
	if _, err := os.Stat(toolPath); err == nil {
		fmt.Printf("Custom edit tool already present at %s\n", toolPath)
	} else {
		toolContent := []byte(`import { tool } from "@opencode-ai/plugin"
import path from "path"

export default tool({
  description: "Edit a file by replacing exact text with new text. The project topology is automatically updated.",
  args: {
    file_path: tool.schema.string().describe("The absolute path to the file to edit"),
    old_string: tool.schema.string().describe("The exact text to search for and replace"),
    new_string: tool.schema.string().describe("The replacement text"),
  },
  async execute(args, context) {
    const filePath = args.file_path
    const oldStr = args.old_string
    const newStr = args.new_string

    const file = Bun.file(filePath)
    const content = await file.text()

    if (!content.includes(oldStr)) {
      return "old_string not found in " + filePath
    }

    const newContent = content.replace(oldStr, newStr)
    await Bun.write(filePath, newContent)

    const ltpPath = path.join(
      context.worktree,
      "ltp" + (process.platform === "win32" ? ".exe" : ""),
    )
    const proc = Bun.spawnSync([ltpPath, "update-file", filePath])
    if (proc.exitCode === 0) {
      const out = proc.stdout.toString().trim()
      if (out) {
        return "edit succeeded\n\nTopology warnings:\n" + out
      }
    }
    // topology DB missing or file not tracked -- edit still succeeded
    return "edit succeeded"
  },
})
`)
		if err := os.WriteFile(toolPath, []byte(toolContent), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing custom edit tool: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Custom edit tool written to %s\n", toolPath)
	}

	var commandsDir string
	if *global {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error finding home dir: %v\n", err)
			os.Exit(1)
		}
		commandsDir = filepath.Join(home, ".config", "opencode", "commands")
	} else {
		commandsDir = filepath.Join(filepath.Dir(configPath), ".opencode", "commands")
	}
	os.MkdirAll(commandsDir, 0755)

	writeCommand := func(name, description, template string) {
		cmdPath := filepath.Join(commandsDir, name+".md")
		if _, err := os.Stat(cmdPath); err == nil {
			fmt.Printf("Command %s already present at %s\n", name, cmdPath)
			return
		}
		content := fmt.Sprintf("---\ndescription: %s\n---\n\n%s\n", description, template)
		if err := os.WriteFile(cmdPath, []byte(content), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing command %s: %v\n", name, err)
			os.Exit(1)
		}
		fmt.Printf("Command %s written to %s\n", name, cmdPath)
	}

	writeCommand(
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

	writeCommand(
		"descriptions-apply",
		"Write topology descriptions back into source files as doc comments",
		`Run `+"`ltp descriptions apply`"+` to write all topology descriptions back into the source files as Go doc comments. Report any files that were modified.`,
	)

	fmt.Println("Restart OpenCode to activate the topology tools.")
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
	goManager := golang.NewGoManager(manager)

	toolReg := tools.NewRegistry()
	toolReg.Register(&tools.Ls{})
	toolReg.Register(&tools.Read{})
	toolReg.Register(tools.NewEdit(manager, reg))
	gotools.RegisterGoTools(toolReg, goManager)

	topo, _ := manager.ReadAll()
	lang := "go"
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

func runReadFunction() {
	args := os.Args[2:]
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: ltp read_function <name>")
		fmt.Fprintln(os.Stderr, "Example: ltp read_function ReadFunction")
		os.Exit(1)
	}
	name := args[0]

	manager, _ := initRegistry(".ltp/topology.db")
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
	warnings := manager.UpdateFile(path, reg)
	if len(warnings) > 0 {
		for _, w := range warnings {
			fmt.Printf("Warning: %s: %s (affects: %s)\n", w.Resource, w.Message, strings.Join(w.AffectedResources, ", "))
		}
	}
}

func runReadResourceAndCut(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: ltp read-resource-and-cut <id> <kind>")
		fmt.Fprintln(os.Stderr, "Kind: Function, Struct, Interface, ExternalVar, File, Package")
		os.Exit(1)
	}
	id, resourceName := args[0], args[1]

	manager, _ := initRegistry(".ltp/topology.db")
	goManager := golang.NewGoManager(manager)

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

	entry, err := goManager.ReadResourceAndCut(id, kind)
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
	goManager := golang.NewGoManager(manager)

	kind := mapResourceKind(resourceName)
	if kind == "" {
		fmt.Fprintf(os.Stderr, "Error: unknown resource kind %q\n", resourceName)
		os.Exit(1)
	}

	if err := goManager.UpdateDescription(id, kind, description); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("description updated")
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
