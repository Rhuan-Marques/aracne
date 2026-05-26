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
	"llm-topology/internal/llm/providers"
	"llm-topology/internal/llm/tools"
	"llm-topology/internal/mcp"
	"llm-topology/internal/mermaid"
	"llm-topology/internal/topology"
	"llm-topology/internal/topology/domain"
	"llm-topology/viz"
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
	case "viz":
		runViz()
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
  ltp viz                           Launch interactive topology visualizer in browser

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

Flags for "generate-descriptions":
  --concurrency N  Max concurrent LLM calls (default 5)

  Examples:
    ltp scan -root ./myproject -output myproject.db
    ltp mermaid -input myproject.db -output diagram.mermaid --filter "Function, Struct"
    ltp agent "list all structs"
    ltp install               # add MCP config to opencode.json
    ltp serve                 # start MCP server (used by OpenCode)
    ltp generate-descriptions # generate descriptions for all resources
    ltp viz                   # open interactive topology visualization in browser`)
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
	reg.Register(tools.NewGenerateDescriptions(manager))
	reg.Register(tools.NewReadResourceAndCut(manager))
	reg.Register(tools.NewUpdateDescriptionTool(manager))

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

func runGenerateDescriptions(args []string) {
	fs := flag.NewFlagSet("generate-descriptions", flag.ExitOnError)
	concurrency := fs.Int("concurrency", 5, "Max concurrent LLM calls")
	fs.Parse(args)

	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "Error: DEEPSEEK_API_KEY environment variable is not set")
		os.Exit(1)
	}

	manager := initManager(".ltp/topology.db")
	provider := providers.NewDeepSeek()

	topo, err := manager.ReadAll(topology.WithHasDescription(false))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading topology: %v\n", err)
		os.Exit(1)
	}

	type descTask struct {
		id           string
		name         string
		resourceName domain.ResourceName
		cut          *domain.CodeEntry
	}

	var tasks []descTask

	for id, fn := range topo.Functions {
		cut, err := manager.Cut(fn.Loc)
		if err != nil {
			continue
		}
		tasks = append(tasks, descTask{string(id), fn.Name, domain.FUNCTION_RESOURCE, cut})
	}
	for id, s := range topo.Struct {
		cut, err := manager.Cut(s.Loc)
		if err != nil {
			continue
		}
		tasks = append(tasks, descTask{string(id), s.Name, domain.STRUCT_RESOURCE, cut})
	}
	for id, iface := range topo.Interfaces {
		cut, err := manager.Cut(iface.Loc)
		if err != nil {
			continue
		}
		tasks = append(tasks, descTask{string(id), iface.Name, domain.INTERFACE_RESOURCE, cut})
	}
	for id, v := range topo.ExternalVars {
		cut, err := manager.Cut(v.Location)
		if err != nil {
			continue
		}
		tasks = append(tasks, descTask{string(id), v.Name, domain.EXTERNAL_VAR_RESOURCE, cut})
	}

	for id, f := range topo.Files {
		tasks = append(tasks, descTask{string(id), f.Name, domain.FILE_RESOURCE, nil})
	}
	for id := range topo.Packages {
		tasks = append(tasks, descTask{string(id), string(id), domain.PACKAGE_RESOURCE, nil})
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

			prompt := buildDescriptionPrompt(t.resourceName, cutText)
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

			if err := manager.UpdateDescription(t.id, t.resourceName, desc); err != nil {
				mu.Lock()
				failed++
				mu.Unlock()
				return
			}

			mu.Lock()
			generated++
			fmt.Printf("  [%d/%d] %s %s\n", generated+failed, len(tasks), t.resourceName, t.name)
			mu.Unlock()
		}(t)
	}

	wg.Wait()

	fmt.Printf("\nDone: %d descriptions generated, %d failed\n", generated, failed)
}

func buildDescriptionPrompt(rn domain.ResourceName, cut string) string {
	var instructions string
	switch rn {
	case domain.FUNCTION_RESOURCE:
		instructions = "Generate a concise description (1-3 lines) for this Go function. Include its main purpose, what parameters it takes, what it returns, and any notable behavior or side effects."
	case domain.STRUCT_RESOURCE:
		instructions = "Generate a concise description (1-3 lines) for this Go struct. Include what it represents, its key fields and their purpose, and how it is typically used."
	case domain.INTERFACE_RESOURCE:
		instructions = "Generate a concise description (1-3 lines) for this Go interface. Include what contract it defines, what behavior it abstracts, and the key methods it requires."
	case domain.EXTERNAL_VAR_RESOURCE:
		instructions = "Generate a concise description (1 line) for this Go external variable. Include what it stores and its purpose in the codebase."
	case domain.FILE_RESOURCE:
		instructions = "Generate a concise description (1 line) for this Go source file. What does it contain and what is its role within its package? The file name is all the context available."
	case domain.PACKAGE_RESOURCE:
		instructions = "Generate a concise description (1-2 lines) for this Go package. Include its overall purpose and what functionality it provides."
	default:
		instructions = "Generate a concise description for this resource."
	}

	if cut != "" {
		return fmt.Sprintf("%s\n\n```go\n%s\n```\n\nWrite ONLY the description text, nothing else.", instructions, cut)
	}
	return fmt.Sprintf("%s\n\nWrite ONLY the description text, nothing else.", instructions)
}

func runViz() {
	manager := initManager(".ltp/topology.db")

	fmt.Fprintf(os.Stderr, "Starting topology visualizer...\n")
	if err := viz.Serve(manager, frontendDist); err != nil {
		fmt.Fprintf(os.Stderr, "Error starting visualizer: %v\n", err)
		os.Exit(1)
	}
}
