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
	"llm-topology/internal/mermaid"
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
	case "mermaid":
		runMermaid(os.Args[2:])
	case "agent":
		runAgent()
	case "serve":
		runServe()
	case "install":
		runInstall(os.Args[2:])
	case "generate-descriptions":
		runGenerateDescriptions(os.Args[2:])
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
  ltp generate-descriptions [flags]  Generate descriptions for all undocumented resources

Flags for "scan":
  -root <path>    Root folder of the Go project (default ".")
  -output <file>  Output SQLite database path (default ".ltp/topology.db")

Flags for "mermaid":
  -input <file>   Input topology database (default ".ltp/topology.db")
  -output <file>  Output .mermaid file path (default ".ltp/topology.mermaid")
  --filter <csv>  Comma-separated resource types to include:
                  Function, Type, Interface, Variable, Package, File, Dependency

Flags for "install":
  --global        Install globally (~/.config/opencode/opencode.json)

Flags for "generate-descriptions":
  --concurrency N  Max concurrent LLM calls (default 5)

  Examples:
    ltp scan -root ./myproject -output myproject.db
    ltp mermaid -input myproject.db -output diagram.mermaid --filter "Function, Type"
    ltp agent "list all structs"
    ltp install               # add MCP config to opencode.json
    ltp serve                 # start MCP server (used by OpenCode)
    ltp generate-descriptions # generate descriptions for all resources`)
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

var filterNames = map[string]domain.ResourceKind{
	"Package":    domain.ResourcePackage,
	"File":       domain.ResourceFile,
	"Function":   domain.ResourceFunction,
	"Type":       domain.ResourceType,
	"Struct":     domain.ResourceType,
	"Interface":  domain.ResourceInterface,
	"Variable":   domain.ResourceVariable,
	"Dependency": domain.ResourceDependency,
	"Var":        domain.ResourceVariable,
}

func parseFilterCSV(csv string) []domain.ResourceKind {
	if csv == "" {
		return nil
	}
	var result []domain.ResourceKind
	for _, s := range strings.Split(csv, ",") {
		s = strings.TrimSpace(s)
		if r, ok := filterNames[s]; ok {
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

	filterKinds := parseFilterCSV(*filter)
	os.MkdirAll(filepath.Dir(*output), 0755)
	if err := mermaid.GenerateToFile(*output, topo, filterKinds...); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating mermaid: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Mermaid diagram written to %s\n", *output)
	if *filter != "" {
		fmt.Printf("Filter: %s\n", *filter)
	}
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

	if _, exists := mcpMap["llm-topology"]; exists {
		fmt.Printf("llm-topology MCP config already present in %s\n", configPath)
		return
	}

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
