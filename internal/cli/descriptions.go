package cli

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/lazydesc"
	"github.com/Rhuan-Marques/aracne/internal/llm"
	"github.com/Rhuan-Marques/aracne/internal/llm/agent"
	"github.com/Rhuan-Marques/aracne/internal/llm/tools"
	"github.com/Rhuan-Marques/aracne/internal/prompts"
	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
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
	// Current is the resource's stored description, set only in a --regen_oversized run,
	// where the executor is replacing an over-budget description rather than writing a
	// first one. Empty in a normal generation run.
	Current string
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
	includeNotVisibleFlag := fs.Bool("include-not-visible", false, "Include resources the read context filter would not render as a normal line (small functions / external vars set full or hidden)")
	regenOversized := fs.Bool("regen_oversized", false, "Rewrite existing descriptions that overrun their kind's character budget instead of describing undocumented resources")
	fs.Parse(args)

	manager, reg := InitRegistry(".aracne/topology.db")
	cfg := helper.EnsureConfig(helper.ConfigPath(".aracne/topology.db"))

	// Resolved the same way the lazy fill resolves it, from descriptions.lazy plus the
	// descriptions-generation-executor's model. This used to be providers.NewDeepSeek()
	// outright, so the sweep needed DEEPSEEK_API_KEY while the lazy path over the same
	// descriptions already accepted Anthropic, OpenAI or DeepSeek -- one feature with two
	// different answers to "which key do I need", depending on which entry point you found.
	provider, model, ok := lazydesc.ResolveDescriptionProvider(
		cfg.EffectiveLazyDescriptions(helper.DefaultLazyHarness))
	if !ok {
		fmt.Fprintf(os.Stderr, "Error: no LLM provider configured for description generation.\n"+
			"Set one of %s, or name a provider under \"descriptions\": {\"lazy\": {\"provider\": ...}}\n"+
			"in .aracne/config.json.\n", strings.Join(lazydesc.ProviderKeyEnvNames(), ", "))
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "Describing with %s\n", model)
	batchSizeProvided := false
	includeNotVisibleProvided := false
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "batch-size":
			batchSizeProvided = true
		case "include-not-visible":
			includeNotVisibleProvided = true
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

	filter := cfg.EffectiveContextFilter()
	includeNotVisible := cfg.Descriptions.IncludeNotVisible
	if includeNotVisibleProvided {
		includeNotVisible = *includeNotVisibleFlag
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

	pending, err := pendingDescriptionResources(manager, cfg.Descriptions.Kinds, filter, includeNotVisible, *regenOversized)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	action := fmt.Sprintf("Generating descriptions for %d resources", len(pending))
	if *regenOversized {
		action = fmt.Sprintf("Regenerating %d over-budget description(s)", len(pending))
	}
	fmt.Printf("%s (targets: %s, batch size: %d, parallel: %d, max retries: %d)...\n", action, helper.FormatDescribeTargets(cfg.Descriptions.Kinds), *batchSize, *parallel, *maxRetries)
	if len(pending) == 0 {
		fmt.Println("done")
		return
	}

	toolReg := BuildToolRegistry(manager, reg, cfg, "claude_code", "descriptions-generation-executor")
	toolMap := registryToolMap(toolReg)

	if err := runDescriptionGeneration(manager, provider, toolMap, lang, cfg.Descriptions.Kinds, *batchSize, *parallel, *maxRetries, cfg.Descriptions.StyleExemplars, filter, includeNotVisible, *regenOversized); err != nil {
		fmt.Fprintf(os.Stderr, "\nError: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("done")
}

// Orchestrates batch-wise LLM description generation with retry logic, splitting undocumented
// resources -- or, under regenOversized, resources whose stored description overruns its
// kind's budget -- into parallel executor waves.
//
// Both modes converge the same way: the database is re-read after every wave and whatever
// still qualifies is re-batched. That works for a rewrite because update_description REJECTS
// an over-budget write, so a failed shrink leaves the old description standing and the
// resource simply comes back in the next round instead of leaving a hole.
func runDescriptionGeneration(manager *topology.TopologyManager, provider llm.Provider, toolMap map[string]tools.Tool, lang string, targets []domain.ResourceKind, batchSize, parallel, maxRetries, exemplarLimit int, filter domain.ContextFilter, includeNotVisible, regenOversized bool) error {
	attempts := make(map[string]int)
	failed := make(map[string]string)

	for {
		pending, err := pendingDescriptionResources(manager, targets, filter, includeNotVisible, regenOversized)
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
			if regenOversized {
				return fmt.Errorf("failed to shrink descriptions for %d resources: %s", len(failed), formatDescriptionFailures(failed))
			}
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
		results := runDescriptionBatchWave(provider, manager, toolMap, lang, batches, parallel, topo, exemplarLimit)
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
func runDescriptionBatchWave(provider llm.Provider, manager *topology.TopologyManager, toolMap map[string]tools.Tool, lang string, batches [][]descriptionResource, parallel int, topo *domain.Topology, exemplarLimit int) []descriptionBatchResult {
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
				text, err := runDescriptionExecutorBatch(provider, manager, toolMap, lang, batch, topo, exemplarLimit)
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
func runDescriptionExecutorBatch(provider llm.Provider, manager *topology.TopologyManager, toolMap map[string]tools.Tool, lang string, batch []descriptionResource, topo *domain.Topology, exemplarLimit int) (string, error) {
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
	input := descriptionExecutorInput(batch, makeResourceReader(manager), exemplars)
	return a.RunSubAgent(prompts.DescriptionsGenerationExecutorPrompt(), input, toolMap)
}

// Returns the resources a generation run has left to do, sorted by kind, name, and ID:
// targeted resources that lack a description, or -- under regenOversized -- targeted
// resources whose stored description overruns its kind's budget. Both selections apply the
// same kind targeting and read-context visibility gate.
func pendingDescriptionResources(manager *topology.TopologyManager, targets []domain.ResourceKind, filter domain.ContextFilter, includeNotVisible, regenOversized bool) ([]descriptionResource, error) {
	topo, err := manager.ReadAll()
	if err != nil {
		return nil, err
	}
	targetSet := helper.DescribeTargetSet(targets)
	resources := make([]descriptionResource, 0)
	for id, res := range topo.Resources {
		if regenOversized {
			if !helper.ShouldRegenerateDescription(res, targetSet, filter, includeNotVisible) {
				continue
			}
			resources = append(resources, descriptionResource{ID: id, Name: res.Name, Kind: res.Kind, Current: strings.TrimSpace(res.Description)})
			continue
		}
		if !helper.ShouldDescribe(res, targetSet, filter, includeNotVisible) {
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
		dr := prompts.DescriptionResource{ID: res.ID, Name: res.Name, Kind: res.Kind, CurrentDescription: res.Current}
		if readResource != nil {
			dr.ReadOutput = readResource(res.ID)
		}
		resources = append(resources, dr)
	}
	return prompts.DescriptionsGenerationExecutorInput(resources, exemplars)
}

// makeResourceReader returns a closure that pre-reads a resource's source, so executors don't
// have to read each one themselves.
//
// It takes the RAW cut rather than going through the read tool. The executor's job is to
// describe this one resource; a "# CONTEXT:" tree of its neighbours would both bloat the batch
// prompt and pull the description toward what the resource touches instead of what it does.
func makeResourceReader(mgr *topology.TopologyManager) func(string) string {
	return func(id string) string {
		text, err := tools.ResourceSource(mgr, id)
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
	oversized := fs.Bool("oversized", false,
		"Clear ONLY descriptions that overrun their kind's character budget, leaving the rest "+
			"untouched. domain.DescriptionForStorage keeps new ones out, but rows written before "+
			"it existed are grandfathered; this is the explicit sweep for those.")
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

	if *oversized {
		count, err := helper.ClearOversizedDescriptions(".aracne/topology.db", targets)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		scope := "all kinds"
		if len(targets) > 0 {
			scope = helper.FormatDescribeTargets(targets)
		}
		fmt.Printf("Cleared %d over-budget description(s) (%s). "+
			"Run `arac descriptions generate` to write real ones for them.\n", count, scope)
		return
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
