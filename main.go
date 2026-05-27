package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"llm-topology/internal/helper"
	"llm-topology/internal/llm"
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
	case "generate-descriptions":
		runGenerateDescriptions(os.Args[2:])
	case "update-file":
		runUpdateFile(os.Args[2:])
	case "read_function":
		runReadFunction()
	case "read_struct":
		runReadStruct()
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
  ltp generate-descriptions [flags]  Generate descriptions for all undocumented resources
  ltp read_function <name>  Show a function's source code and its interconnected context
  ltp read_struct <name>    Show a struct's source code and its interconnected context
  ltp update-file <path>    Re-parse a file and update the topology database

Flags for "scan":
  -root <path>    Root folder of the Go project (default ".")
  -output <file>  Output SQLite database path (default ".ltp/topology.db")

Flags for "install":
  --global        Install globally (~/.config/opencode/opencode.json)

Flags for "generate-descriptions":
  --concurrency N  Max concurrent LLM calls (default 5)

  Examples:
    ltp scan -root ./myproject -output myproject.db
    ltp agent "list all structs"
    ltp install               # add MCP config to opencode.json
    ltp serve                 # start MCP server (used by OpenCode)
    ltp generate-descriptions # generate descriptions for all resources
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
	fs.Parse(args)

	manager := topology.New()
	manager.Load(*output)
	os.MkdirAll(filepath.Dir(*output), 0755)

	fmt.Printf("Analyzing Go project at: %s\n", *root)

	start := time.Now()
	reg := newScannerRegistry()
	if err := manager.FullScan(*root, reg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
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
		if err := mgr.FullScan(".", reg); err != nil {
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

	mcpMap, _ := config["mcp"].(map[string]interface{})
	if mcpMap == nil {
		mcpMap = make(map[string]interface{})
	}

	if _, exists := mcpMap["llm-topology"]; !exists {
		mcpMap["llm-topology"] = map[string]interface{}{
			"type":    "local",
			"command": "ltp",
			"args":    []string{"serve"},
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

	fmt.Println("Restart OpenCode to activate the topology tools.")
}

func runGenerateDescriptions(args []string) {
	fs := flag.NewFlagSet("generate-descriptions", flag.ExitOnError)
	concurrency := fs.Int("concurrency", 5, "Max concurrent LLM calls")
	fs.Parse(args)

	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "Error: DEEPSEEK_API_KEY environment variable is not set")
		os.Exit(1)
	}

	manager, _ := initRegistry(".ltp/topology.db")
	provider := providers.NewDeepSeek()

	topo, err := manager.ReadAll()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading topology: %v\n", err)
		os.Exit(1)
	}

	type descTask struct {
		id   string
		name string
		kind domain.ResourceKind
		cut  *domain.CodeEntry
	}

	var tasks []descTask

	for id, res := range topo.Resources {
		if res.Description != "" {
			continue
		}
		task := descTask{id: id, name: res.Name, kind: res.Kind}
		if res.Location.Path != "" {
			cut, err := manager.Cut(res.Location)
			if err == nil {
				task.cut = cut
			}
		}
		tasks = append(tasks, task)
	}

	fmt.Printf("Generating descriptions for %d resources (concurrency: %d)...\n", len(tasks), *concurrency)

	sem := make(chan struct{}, *concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	generated := 0
	failed := 0
	for _, t := range tasks {
		wg.Add(1)
		sem <- struct{}{}
		go func(t descTask) {
			defer wg.Done()
			defer func() { <-sem }()

			cutText := ""
			if t.cut != nil {
				cutText = t.cut.Cut
			}

			prompt := buildDescriptionPrompt(t.kind, cutText)
			messages := []llm.Message{
				{Role: "system", Content: "You are a code description generator. Generate ONLY the description text, nothing else."},
				{Role: "user", Content: prompt},
			}
			resp, err := provider.Chat(messages, nil)
			if err != nil {
				mu.Lock()
				failed++
				mu.Unlock()
				return
			}

			desc := strings.TrimSpace(resp.Content)
			if desc == "" {
				mu.Lock()
				failed++
				mu.Unlock()
				return
			}

			if err := manager.UpdateDescription(t.id, t.kind, desc); err != nil {
				mu.Lock()
				failed++
				mu.Unlock()
				return
			}

			mu.Lock()
			generated++
			fmt.Printf("  [%d/%d] %s %s\n", generated+failed, len(tasks), t.kind, t.name)
			mu.Unlock()
		}(t)
	}

	wg.Wait()

	fmt.Printf("\nDone: %d descriptions generated, %d failed\n", generated, failed)
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

func buildDescriptionPrompt(kind domain.ResourceKind, cut string) string {
	var instructions string
	switch kind {
	case domain.ResourceFunction, domain.ResourceMethod:
		instructions = "Generate a concise description (1-3 lines) for this Go function. Include its main purpose, what parameters it takes, what it returns, and any notable behavior or side effects."
	case domain.ResourceType:
		instructions = "Generate a concise description (1-3 lines) for this Go struct. Include what it represents, its key fields and their purpose, and how it is typically used."
	case domain.ResourceInterface:
		instructions = "Generate a concise description (1-3 lines) for this Go interface. Include what contract it defines, what behavior it abstracts, and the key methods it requires."
	case domain.ResourceVariable:
		instructions = "Generate a concise description (1 line) for this Go variable. Include what it stores and its purpose in the codebase."
	case domain.ResourceFile:
		instructions = "Generate a concise description (1 line) for this Go source file. The file name is all the context available."
	case domain.ResourcePackage:
		instructions = "Generate a concise description (1-2 lines) for this Go package. Include its overall purpose and what functionality it provides."
	default:
		instructions = "Generate a concise description for this resource."
	}

	if cut != "" {
		return fmt.Sprintf("%s\n\n```go\n%s\n```\n\nWrite ONLY the description text, nothing else.", instructions, cut)
	}
	return fmt.Sprintf("%s\n\nWrite ONLY the description text, nothing else.", instructions)
}
