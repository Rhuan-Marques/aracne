package cli

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"ltp/internal/helper"
	"ltp/internal/llm"
	"ltp/internal/llm/agent"
	"ltp/internal/llm/providers"
	"ltp/internal/llm/tools"
	"ltp/internal/prompts"
	"ltp/internal/topology"
	"ltp/internal/topology/domain"
)

const (
	defaultDescriptionBatchSize  = 20
	defaultDescriptionParallel   = 4
	defaultDescriptionMaxRetries = 3
)

type descriptionResource struct {
	ID   string
	Name string
	Kind domain.ResourceKind
}

type descriptionBatchResult struct {
	Batch []descriptionResource
	Text  string
	Err   error
}

func RunGenerateDescriptions(args []string) {
	fs := flag.NewFlagSet("generate-descriptions", flag.ExitOnError)
	targetsFlag := fs.String("targets", "", "Comma-separated resource kinds to describe (overrides config describe_targets)")
	batchSize := fs.Int("batch-size", defaultDescriptionBatchSize, "Maximum resources assigned to each description executor")
	parallel := fs.Int("parallel", defaultDescriptionParallel, "Maximum description executors to run concurrently")
	maxRetries := fs.Int("max-retries", defaultDescriptionMaxRetries, "Maximum executor attempts per resource")
	fs.Parse(args)

	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "Error: DEEPSEEK_API_KEY environment variable is not set")
		os.Exit(1)
	}

	manager, reg := InitRegistry(".ltp/topology.db")
	provider := providers.NewDeepSeek()
	cfg := helper.EnsureConfig(helper.ConfigPath(".ltp/topology.db"))
	if *targetsFlag != "" {
		targets, err := helper.ParseDescribeTargets(*targetsFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		cfg.DescribeTargets = targets
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

	pending, err := undocumentedDescriptionResources(manager, cfg.DescribeTargets)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Generating descriptions for %d resources (targets: %s, batch size: %d, parallel: %d, max retries: %d)...\n", len(pending), helper.FormatDescribeTargets(cfg.DescribeTargets), *batchSize, *parallel, *maxRetries)
	if len(pending) == 0 {
		fmt.Println("done")
		return
	}

	agentCfg := *cfg
	agentCfg.ToolModes = helper.ToolModes{Read: helper.ReadModeMCP, Edit: helper.EditModeMCP, Other: helper.OtherModeMCP}
	toolReg := BuildToolRegistry(manager, reg, &agentCfg, ToolProfileDescriptionsExecutor)
	toolMap := registryToolMap(toolReg)

	if err := runDescriptionGeneration(manager, provider, toolMap, lang, cfg.DescribeTargets, *batchSize, *parallel, *maxRetries); err != nil {
		fmt.Fprintf(os.Stderr, "\nError: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("done")
}

func runDescriptionGeneration(manager *topology.TopologyManager, provider llm.Provider, toolMap map[string]tools.Tool, lang string, targets []domain.ResourceKind, batchSize, parallel, maxRetries int) error {
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
		results := runDescriptionBatchWave(provider, toolMap, lang, batches, parallel)
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

func runDescriptionBatchWave(provider llm.Provider, toolMap map[string]tools.Tool, lang string, batches [][]descriptionResource, parallel int) []descriptionBatchResult {
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
				text, err := runDescriptionExecutorBatch(provider, toolMap, lang, batch)
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

func runDescriptionExecutorBatch(provider llm.Provider, toolMap map[string]tools.Tool, lang string, batch []descriptionResource) (string, error) {
	a := agent.New(provider, tools.NewRegistry(), lang)
	a.SetMaxIterations(len(batch)*4 + 10)
	return a.RunSubAgent(prompts.DescriptionsGenerationExecutorPrompt(), descriptionExecutorInput(batch), toolMap)
}

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

func descriptionExecutorInput(batch []descriptionResource) string {
	var b strings.Builder
	b.WriteString("Process only the assigned resources below. Do not discover or update any other resource.\n\n")
	b.WriteString("For each resource, call read_resource_and_cut with the exact ID and Kind shown as resource_name, manually write a concise description, then call update_description immediately.\n\n")
	b.WriteString("Assigned resources:\n\n")
	for _, res := range batch {
		b.WriteString(fmt.Sprintf("- ID: %s\n  Name: %s\n  Kind: %s\n\n", res.ID, res.Name, res.Kind))
	}
	return b.String()
}

func registryToolMap(registry *tools.Registry) map[string]tools.Tool {
	result := make(map[string]tools.Tool)
	for _, tool := range registry.List() {
		result[tool.Name()] = tool
	}
	return result
}

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

func RunDescriptionApply(args []string) {
	manager, _ := InitRegistry(".ltp/topology.db")

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
