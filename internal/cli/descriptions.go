package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"aracne/internal/helper"
	"aracne/internal/llm"
	"aracne/internal/llm/agent"
	"aracne/internal/llm/providers"
	"aracne/internal/llm/tools"
	"aracne/internal/prompts"
	"aracne/internal/topology"
	"aracne/internal/topology/domain"
)

const (
	defaultDescriptionBatchSize  = helper.DefaultDescriptionBatchSize
	defaultDescriptionParallel   = 4
	defaultDescriptionMaxRetries = 3
)

// Represents a topology resource with its ID, name, and kind for description operations.
type descriptionResource struct {
	ID   string
	Name string
	Kind domain.ResourceKind
}

// Holds a batch of resources with their generated descriptions text and any error from the LLM
type descriptionBatchResult struct {
	Batch []descriptionResource
	Text  string
	Err   error
}

// Generates descriptions for undocumented topology resources using an LLM provider, with configurable batch size, parallelism, and retry limits.
func RunGenerateDescriptions(args []string) {
	fs := flag.NewFlagSet("generate-descriptions", flag.ExitOnError)
	targetsFlag := fs.String("targets", "", "Comma-separated resource kinds to describe (overrides config need_description)")
	batchSize := fs.Int("batch-size", defaultDescriptionBatchSize, "Maximum resources assigned to each description executor")
	parallel := fs.Int("parallel", defaultDescriptionParallel, "Maximum description executors to run concurrently")
	maxRetries := fs.Int("max-retries", defaultDescriptionMaxRetries, "Maximum executor attempts per resource")
	fs.Parse(args)

	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "Error: DEEPSEEK_API_KEY environment variable is not set")
		os.Exit(1)
	}

	manager, reg := InitRegistry(".aracne/topology.db")
	provider := providers.NewDeepSeek()
	cfg := helper.EnsureConfig(helper.ConfigPath(".aracne/topology.db"))
	batchSizeProvided := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "batch-size" {
			batchSizeProvided = true
		}
	})
	if cfgBatch := cfg.AgentParam("claude_code", "descriptions-generation-executor", "max-batch-size", 0); !batchSizeProvided && cfgBatch > 0 {
		*batchSize = cfgBatch
	}
	if *targetsFlag != "" {
		targets, err := helper.ParseDescribeTargets(*targetsFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		cfg.Descriptions.Kinds = targets
	}

	lang := GetLanguage(manager)
	if topo, _ := manager.ReadAll(); topo != nil && topo.Language != "" {
		lang = topo.Language
	}

	if *batchSize <= 0 {
		*batchSize = defaultDescriptionBatchSize
	}
	if *parallel <= 0 {
		*parallel = 1
	}
	if *maxRetries <= 0 {
		*maxRetries = 1
	}

	pending, err := undocumentedDescriptionResources(manager, cfg.Descriptions.Kinds)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Generating descriptions for %d resources (targets: %s, batch size: %d, parallel: %d, max retries: %d)...\n", len(pending), helper.FormatDescribeTargets(cfg.Descriptions.Kinds), *batchSize, *parallel, *maxRetries)
	if len(pending) == 0 {
		fmt.Println("done")
		return
	}

	toolReg := BuildToolRegistry(manager, reg, cfg, "claude_code", "descriptions-generation-executor")
	toolMap := registryToolMap(toolReg)

	if err := runDescriptionGeneration(manager, provider, toolMap, lang, cfg.Descriptions.Kinds, *batchSize, *parallel, *maxRetries, cfg.Descriptions.StyleExemplars); err != nil {
		fmt.Fprintf(os.Stderr, "\nError: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("done")
}

// Orchestrates batch-wise LLM description generation with retry logic, splitting undocumented resources into parallel executor waves.
func runDescriptionGeneration(manager *topology.TopologyManager, provider llm.Provider, toolMap map[string]tools.Tool, lang string, targets []domain.ResourceKind, batchSize, parallel, maxRetries, exemplarLimit int) error {
	attempts := make(map[string]int)
	failed := make(map[string]string)

	for {
		pending, err := undocumentedDescriptionResources(manager, targets)
		if err != nil {
			return err
		}
		if len(pending) == 0 {
			return nil
		}

		retryable := make([]descriptionResource, 0, len(pending))
		for _, res := range pending {
			if attempts[res.ID] >= maxRetries {
				failed[res.ID] = "exceeded max retries"
				continue
			}
			retryable = append(retryable, res)
		}
		if len(retryable) == 0 {
			return fmt.Errorf("failed to generate descriptions for %d resources: %s", len(failed), formatDescriptionFailures(failed))
		}

		batches := chunkDescriptionResources(retryable, batchSize)
		for _, batch := range batches {
			for _, res := range batch {
				attempts[res.ID]++
			}
		}

		fmt.Printf("Starting %d executor batch(es) for %d remaining resource(s)...\n", len(batches), len(retryable))
		var topo *domain.Topology
		if exemplarLimit > 0 {
			topo, _ = manager.ReadAll()
		}
		results := runDescriptionBatchWave(provider, toolMap, lang, batches, parallel, topo, exemplarLimit)
		for _, result := range results {
			if result.Err != nil {
				fmt.Fprintf(os.Stderr, "Executor batch failed (%d resources): %v\n", len(result.Batch), result.Err)
				continue
			}
			if strings.TrimSpace(result.Text) != "" {
				fmt.Printf("Executor batch completed (%d resources):\n%s\n", len(result.Batch), strings.TrimSpace(result.Text))
			}
		}
	}
}

// Runs description generation for resource batches in parallel using an LLM provider and collects results.
func runDescriptionBatchWave(provider llm.Provider, toolMap map[string]tools.Tool, lang string, batches [][]descriptionResource, parallel int, topo *domain.Topology, exemplarLimit int) []descriptionBatchResult {
	if parallel > len(batches) {
		parallel = len(batches)
	}
	jobs := make(chan []descriptionResource)
	results := make(chan descriptionBatchResult, len(batches))
	var wg sync.WaitGroup

	for i := 0; i < parallel; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for batch := range jobs {
				text, err := runDescriptionExecutorBatch(provider, toolMap, lang, batch, topo, exemplarLimit)
				results <- descriptionBatchResult{Batch: batch, Text: text, Err: err}
			}
		}()
	}

	for _, batch := range batches {
		jobs <- batch
	}
	close(jobs)
	wg.Wait()
	close(results)

	collected := make([]descriptionBatchResult, 0, len(batches))
	for result := range results {
		collected = append(collected, result)
	}
	return collected
}

// Executes an LLM agent to generate descriptions for a batch of resources using exemplars from the topology for consistency.
func runDescriptionExecutorBatch(provider llm.Provider, toolMap map[string]tools.Tool, lang string, batch []descriptionResource, topo *domain.Topology, exemplarLimit int) (string, error) {
	a := agent.New(provider, tools.NewRegistry(), lang)
	a.SetMaxIterations(len(batch)*4 + 10)
	var exemplars []prompts.DescriptionExemplar
	if topo != nil && exemplarLimit > 0 {
		ids := make([]string, 0, len(batch))
		for _, res := range batch {
			ids = append(ids, res.ID)
		}
		exemplars = prompts.BuildDescriptionExemplars(topo, ids, exemplarLimit)
	}
	input := descriptionExecutorInput(batch, makeResourceReader(toolMap), exemplars)
	return a.RunSubAgent(prompts.DescriptionsGenerationExecutorPrompt(), input, toolMap)
}

// Returns all resources of specified kinds that lack descriptions, sorted by kind, name, and ID.
func undocumentedDescriptionResources(manager *topology.TopologyManager, targets []domain.ResourceKind) ([]descriptionResource, error) {
	topo, err := manager.ReadAll()
	if err != nil {
		return nil, err
	}
	targetSet := helper.DescribeTargetSet(targets)
	resources := make([]descriptionResource, 0)
	for id, res := range topo.Resources {
		if strings.TrimSpace(res.Description) != "" || !targetSet[res.Kind] {
			continue
		}
		resources = append(resources, descriptionResource{ID: id, Name: res.Name, Kind: res.Kind})
	}
	sortDescriptionResources(resources)
	return resources, nil
}

// Sorts description resources in-place by kind, name, then ID.
func sortDescriptionResources(resources []descriptionResource) {
	sort.Slice(resources, func(i, j int) bool {
		if resources[i].Kind != resources[j].Kind {
			return resources[i].Kind < resources[j].Kind
		}
		if resources[i].Name != resources[j].Name {
			return resources[i].Name < resources[j].Name
		}
		return resources[i].ID < resources[j].ID
	})
}

// Divides description resources into batches of specified size.
func chunkDescriptionResources(resources []descriptionResource, batchSize int) [][]descriptionResource {
	if batchSize <= 0 {
		batchSize = defaultDescriptionBatchSize
	}
	var batches [][]descriptionResource
	for start := 0; start < len(resources); start += batchSize {
		end := start + batchSize
		if end > len(resources) {
			end = len(resources)
		}
		batch := append([]descriptionResource(nil), resources[start:end]...)
		batches = append(batches, batch)
	}
	return batches
}

// Formats a batch of resources for description generation by constructing prompt input with resource metadata and read outputs
func descriptionExecutorInput(batch []descriptionResource, readResource func(string) string, exemplars []prompts.DescriptionExemplar) string {
	resources := make([]prompts.DescriptionResource, 0, len(batch))
	for _, res := range batch {
		dr := prompts.DescriptionResource{ID: res.ID, Name: res.Name, Kind: res.Kind}
		if readResource != nil {
			dr.ReadOutput = readResource(res.ID)
		}
		resources = append(resources, dr)
	}
	return prompts.DescriptionsGenerationExecutorInput(resources, exemplars)
}

// makeResourceReader returns a closure that pre-reads a resource's source via
// the read tool, so executors don't have to read each one themselves. Returns
// nil when the read tool is unavailable.
func makeResourceReader(toolMap map[string]tools.Tool) func(string) string {
	readTool, ok := toolMap["read"]
	if !ok {
		return nil
	}
	return func(id string) string {
		payload, err := json.Marshal(map[string]string{"resource_id": id})
		if err != nil {
			return ""
		}
		text, err := readTool.Run(payload)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(text)
	}
}

// Converts tool registry list into a name-indexed map for lookup.
func registryToolMap(registry *tools.Registry) map[string]tools.Tool {
	result := make(map[string]tools.Tool)
	for _, tool := range registry.List() {
		result[tool.Name()] = tool
	}
	return result
}

// Formats a map of failed resource IDs and their error reasons into a comma-separated string.
func formatDescriptionFailures(failed map[string]string) string {
	ids := make([]string, 0, len(failed))
	for id := range failed {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, fmt.Sprintf("%s (%s)", id, failed[id]))
	}
	return strings.Join(parts, ", ")
}

// Writes topology descriptions back to source code files as inline documentation.
func RunDescriptionApply(args []string) {
	manager, _ := InitRegistry(".aracne/topology.db")

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

// Clears descriptions for specified or all resource kinds from the topology database.
func RunClearDescriptions(args []string) {
	fs := flag.NewFlagSet("descriptions-clear", flag.ExitOnError)
	targetFlag := fs.String("target", "", "Comma-separated resource kinds to clear; clears all kinds when omitted")
	fs.Parse(args)

	targetValue := strings.TrimSpace(*targetFlag)
	if fs.NArg() > 0 {
		if targetValue == "" {
			fmt.Fprintln(os.Stderr, "Usage: arac descriptions clear [--target <kinds>]")
			os.Exit(1)
		}
		targetValue = strings.TrimSpace(targetValue + " " + strings.Join(fs.Args(), " "))
	}

	targets, err := parseClearDescriptionTargets(targetValue)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	manager, _ := InitRegistry(".aracne/topology.db")
	count, err := manager.ClearDescriptions(targets)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(targets) > 0 {
		fmt.Printf("Cleared %d description(s) for targets: %s\n", count, helper.FormatDescribeTargets(targets))
		return
	}
	fmt.Printf("Cleared %d description(s)\n", count)
}

// Parses a comma-separated list of resource kinds to clear descriptions for.
func parseClearDescriptionTargets(value string) ([]domain.ResourceKind, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	value = strings.TrimPrefix(value, "[")
	value = strings.TrimSuffix(value, "]")
	return helper.ParseDescribeTargets(value)
}
