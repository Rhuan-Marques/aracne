package cli

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
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
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner"
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
	cliFlag := &cliCommandFlag{}
	fs.Var(cliFlag, "cli", "Describe through a command instead of an API key: `--cli \"codex exec\"`, or bare --cli for "+defaultDescribeCLICommand+" (asks first)")
	autoYes := fs.Bool("y", false, "Auto-confirm the bare --cli prompt")
	fs.Parse(expandCLIFlagValue(args))

	manager, reg := InitRegistry(".aracne/topology.db")
	cfg := helper.EnsureConfig(helper.ConfigPath(".aracne/topology.db"))

	lang := GetLanguage(manager)
	if topo, _ := manager.ReadAll(); topo != nil && topo.Language != "" {
		lang = topo.Language
	}

	// Resolved from the one `descriptions` provider block the lazy fill also reads. This
	// used to be providers.NewDeepSeek() outright, so the sweep needed DEEPSEEK_API_KEY
	// while the lazy path over the same descriptions already accepted Anthropic, OpenAI or
	// DeepSeek -- one feature with two different answers to "which key do I need",
	// depending on which entry point you found. Naming the describer once, on the section
	// both paths read, is the end of that split: the sweep now also reaches the CLI
	// transports, which it had no way to express at all.
	descCfg := cfg.EffectiveLazyDescriptions(helper.DefaultLazyHarness)
	if cliFlag.set {
		if err := applyCLIOverride(&descCfg, cliFlag.command, *autoYes); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	}
	runner, describedWith, err := newDescriptionRunner(manager, reg, cfg, descCfg, lang)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "Describing with %s\n", describedWith)
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

	if err := runDescriptionGeneration(manager, runner, cfg.Descriptions.Kinds, *batchSize, *parallel, *maxRetries, cfg.Descriptions.StyleExemplars, filter, includeNotVisible, *regenOversized); err != nil {
		fmt.Fprintf(os.Stderr, "\nError: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("done")
}

// defaultDescribeCLICommand is what a bare `--cli` runs.
//
// `claude -p` and not the fuller `claude --print --model haiku --max-turns 1`: a default is a
// suggestion, and the moment it starts pinning a model it is making a spending decision on the
// user's behalf that they did not type. A project that wants the tuned invocation writes it,
// in the flag or in cli_provider_command, where it can read what it is paying for.
const defaultDescribeCLICommand = "claude -p"

// cliCommandFlag is `--cli`, which takes an OPTIONAL command.
//
// The flag package has no such thing, so this is the two halves of one: IsBoolFlag lets a bare
// `--cli` parse (the package would otherwise fail with "flag needs an argument"), and
// expandCLIFlagValue rewrites `--cli <command>` into `--cli=<command>` before parsing, because
// a bool-shaped flag never consumes the next token. Without that second half
// `--cli "codex exec"` would silently run the default and leave the command as a stray
// argument -- the worst possible outcome for a flag whose whole subject is what gets run.
type cliCommandFlag struct {
	// set records that the flag appeared at all, which is what switches the transport. It is
	// separate from command because "appeared with no command" is a distinct state: it is
	// the one that has to ask before it spends anything.
	set bool
	// command is what the user typed, or "" for the default.
	command string
}

func (f *cliCommandFlag) String() string {
	if f == nil {
		return ""
	}
	return f.command
}

func (f *cliCommandFlag) IsBoolFlag() bool { return true }

func (f *cliCommandFlag) Set(v string) error {
	f.set = true
	// "true" is what the package hands a bare `--cli`. A command literally named `true`
	// describes nothing, so reading it as "no command given" costs nothing real.
	if v == "true" {
		f.command = ""
		return nil
	}
	if v == "false" {
		f.set = false
		f.command = ""
		return nil
	}
	f.command = v
	return nil
}

// expandCLIFlagValue rewrites `--cli <command>` into `--cli=<command>` so an optional-value
// flag can still be written the ordinary way. See cliCommandFlag.
//
// A following token that starts with "-" is left alone: it is the next flag, and `--cli` on
// its own is a legitimate way to write this one. Everything after a bare `--` is copied
// untouched, because that is where flag parsing stops.
func expandCLIFlagValue(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			return append(out, args[i:]...)
		}
		if arg == "--cli" || arg == "-cli" {
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				out = append(out, arg+"="+args[i+1])
				i++
				continue
			}
		}
		out = append(out, arg)
	}
	return out
}

// applyCLIOverride points the run at a command, and asks first when the command is one the
// user did not type.
//
// The flag beats the config outright rather than merging with it: `--cli` is a person at a
// terminal saying "this run, describe with this", and a config's provider is what the project
// does the rest of the time.
func applyCLIOverride(cfg *helper.ResolvedLazyDescriptions, command string, autoYes bool) error {
	if strings.TrimSpace(command) == "" {
		command = defaultDescribeCLICommand
		if err := confirmDefaultCLI(command, autoYes); err != nil {
			return err
		}
	}
	argv, err := helper.SplitCommand(command)
	if err != nil {
		return fmt.Errorf("--cli: %w", err)
	}
	if len(argv) == 0 {
		return fmt.Errorf("--cli: no command given")
	}
	cfg.Provider = helper.ProviderNameCLI
	cfg.CLICommand = argv
	return nil
}

// confirmDefaultCLI states what a bare --cli is about to spend, and waits for a yes.
//
// It asks about the DEFAULT only. `--cli "claude -p"` is the same command with the user's name
// on it, and re-asking a question already answered is how a prompt teaches people to type y
// without reading it.
//
// The cost is the reason for the question. A CLI run bills the subscription behind that CLI,
// not the API key aracne would otherwise use -- a different pool, on plans that meter them
// separately, and one the user may be paying for per-seat rather than per-token. A sweep can
// be thousands of resources. Only the provider can say what that costs, so the prompt says to
// go and ask them rather than guessing on their behalf.
func confirmDefaultCLI(command string, autoYes bool) error {
	if autoYes {
		return nil
	}
	fmt.Fprintf(os.Stderr, `
WARNING: --cli with no command will describe this repository by running:

    %s

  * This spends the subscription behind that CLI, NOT an API key. On some plans that
    is a SEPARATE pool of credits from the one your API key bills, and it shares the
    interactive quota you type into. Check with your provider what a run like this
    costs you before answering.
  * A sweep can be thousands of resources, one process launch per batch.
  * Nothing here is undone by stopping halfway: descriptions already written stay
    written, and re-running only describes what is still missing.

To skip this question, name the command yourself: --cli "%s"

`, command, command)

	// No one there to answer is not the same as a no. Assuming a yes would spend money on a
	// guess; a bare "cancelled" would fail an unattended run for a reason nothing printed. So
	// it says so, and names the two spellings that need no confirmation.
	unattended := fmt.Errorf("--cli needs a confirmation and stdin is not a terminal; "+
		"re-run with --cli %q, or with -y", command)
	if !stdinIsTerminal() {
		return unattended
	}
	fmt.Fprintf(os.Stderr, "Describe this repository with `%s`? [y/N] ", command)
	answer, err := bufio.NewReader(promptReader).ReadString('\n')
	if err != nil && strings.TrimSpace(answer) == "" {
		// stdinIsTerminal only asks whether stdin is a character device, and `< /dev/null`
		// is one. Reading nothing at all is the other half of that check.
		return unattended
	}
	switch strings.TrimSpace(strings.ToLower(answer)) {
	case "y", "yes":
		return nil
	}
	return fmt.Errorf("cancelled")
}

// The two ends of a confirmation prompt, as variables so a test can stand at both of them.
// Production code never assigns either.
var (
	// stdinIsTerminal reports whether there is someone there to answer.
	stdinIsTerminal = func() bool {
		fi, err := os.Stdin.Stat()
		return err == nil && (fi.Mode()&os.ModeCharDevice) != 0
	}
	// promptReader is where the answer is read from.
	promptReader io.Reader = os.Stdin
)

// descriptionRunner describes one batch and reports what it did.
//
// Two implementations, because the transport decides the SHAPE of a run and not just its
// destination. An API provider gets an agent loop -- the executor reads what it is missing and
// calls update_description per resource, round trips worth paying for when a whole repository
// is being described unattended. A CLI provider gets one completion whose reply aracne parses
// and writes itself, because there is no tool protocol to run over a command that answers once
// on stdout.
//
// Everything around the call is shared: the same pending set, the same batching, the same
// re-list-and-retry loop, the same budget check on the write. Only the call is different.
type descriptionRunner interface {
	Run(batch []descriptionResource, topo *domain.Topology, exemplarLimit int) (string, error)
}

// newDescriptionRunner builds the runner the configured provider implies, and the label the
// run announces itself with.
//
// An unbuildable provider is fatal here, unlike on the lazy path where it is silence: `arac
// descriptions generate` was asked for by a person who is waiting for descriptions, and
// exiting with "claude: not found" beats printing "done" over an empty sweep.
func newDescriptionRunner(manager *topology.TopologyManager, reg *scanner.Registry, cfg *helper.Config, descCfg helper.ResolvedLazyDescriptions, lang string) (descriptionRunner, string, error) {
	// Say what is wrong with the provider name before saying nothing is configured. A
	// retired or misspelled name resolves to no provider, and "set an API key" is the wrong
	// advice for a project that named one and got the spelling wrong.
	if cfg != nil {
		if err := helper.ValidateDescriptionProvider(cfg.Descriptions); err != nil {
			return nil, "", err
		}
	}
	if lazydesc.IsCLIProvider(descCfg) {
		gen, err := lazydesc.NewCLIGenerator(descCfg)
		if err != nil {
			return nil, "", fmt.Errorf("descriptions provider %q: %w", descCfg.Provider, err)
		}
		label := descCfg.Provider
		if len(descCfg.CLICommand) > 0 {
			label = fmt.Sprintf("%s (%s)", descCfg.Provider, strings.Join(descCfg.CLICommand, " "))
		}
		return &cliDescriptionRunner{gen: gen, manager: manager}, label, nil
	}
	provider, model, ok := lazydesc.ResolveDescriptionProvider(descCfg)
	if !ok {
		return nil, "", fmt.Errorf("no LLM provider configured for description generation.\n"+
			"Set one of %s, or name a provider in .aracne/config.json:\n"+
			"  \"descriptions\": {\"provider\": \"cli\", \"cli_provider_command\": \"claude -p\"}",
			strings.Join(lazydesc.ProviderKeyEnvNames(), ", "))
	}
	toolReg := BuildToolRegistry(manager, reg, cfg, "claude_code", helper.DescriptionsExecutorAgent)
	return &agentDescriptionRunner{
		provider: provider,
		manager:  manager,
		toolMap:  registryToolMap(toolReg),
		lang:     lang,
	}, model, nil
}

// agentDescriptionRunner is the API-provider runner: a sub-agent per batch, writing through
// the update_description tool.
type agentDescriptionRunner struct {
	provider llm.Provider
	manager  *topology.TopologyManager
	toolMap  map[string]tools.Tool
	lang     string
}

func (r *agentDescriptionRunner) Run(batch []descriptionResource, topo *domain.Topology, exemplarLimit int) (string, error) {
	a := agent.New(r.provider, tools.NewRegistry(), r.lang)
	a.SetMaxIterations(len(batch)*4 + 10)
	input := descriptionExecutorInput(batch, makeResourceReader(r.manager), batchExemplars(batch, topo, exemplarLimit))
	return a.RunSubAgent(prompts.DescriptionsGenerationExecutorPrompt(), input, r.toolMap)
}

// cliDescriptionRunner is the CLI-provider runner: one completion per batch, parsed and
// written here.
//
// It reuses lazydesc's generator wholesale -- the same prompt, the same reply format, the same
// parser -- so a project that describes through `claude -p` gets the same descriptions from
// the sweep as it gets from a read, and the two cannot drift.
//
// Under --regen_oversized it writes a FRESH description rather than being shown the old one to
// shrink. The batch still carries the kind's character budget, and TopologyManager
// UpdateDescription rejects an over-budget write, so an overrun leaves the old description
// standing and the resource comes back in the next wave -- which is exactly how the agent
// runner's failures converge too.
type cliDescriptionRunner struct {
	gen     lazydesc.Generator
	manager *topology.TopologyManager
}

func (r *cliDescriptionRunner) Run(batch []descriptionResource, topo *domain.Topology, exemplarLimit int) (string, error) {
	read := makeResourceReader(r.manager)
	requests := make([]lazydesc.Request, 0, len(batch))
	for _, res := range batch {
		requests = append(requests, lazydesc.Request{
			ID: res.ID, Name: res.Name, Kind: res.Kind, Source: read(res.ID),
		})
	}
	descriptions, err := r.gen.Describe(context.Background(), lazydesc.Batch{
		Resources: requests,
		Exemplars: batchExemplars(batch, topo, exemplarLimit),
	})
	if err != nil {
		return "", err
	}

	// Partial success is success: a batch of five where the model answered for four is four
	// descriptions the project did not have a moment ago, and the fifth is re-listed by the
	// loop above. Same reading as lazydesc.Generator's contract.
	var written []string
	for _, res := range batch {
		desc := strings.TrimSpace(descriptions[res.ID])
		if desc == "" {
			continue
		}
		if err := r.manager.UpdateDescription(res.ID, res.Kind, desc); err != nil {
			continue
		}
		written = append(written, fmt.Sprintf("%s :: %s", res.ID, desc))
	}
	if len(written) == 0 {
		return "", fmt.Errorf("no usable descriptions in the reply")
	}
	return strings.Join(written, "\n"), nil
}

// batchExemplars picks the house-style anchors for one batch, or none when the run turned
// them off.
func batchExemplars(batch []descriptionResource, topo *domain.Topology, exemplarLimit int) []prompts.DescriptionExemplar {
	if topo == nil || exemplarLimit <= 0 {
		return nil
	}
	ids := make([]string, 0, len(batch))
	for _, res := range batch {
		ids = append(ids, res.ID)
	}
	return prompts.BuildDescriptionExemplars(topo, ids, exemplarLimit)
}

// Orchestrates batch-wise LLM description generation with retry logic, splitting undocumented
// resources -- or, under regenOversized, resources whose stored description overruns its
// kind's budget -- into parallel executor waves.
//
// Both modes converge the same way: the database is re-read after every wave and whatever
// still qualifies is re-batched. That works for a rewrite because update_description REJECTS
// an over-budget write, so a failed shrink leaves the old description standing and the
// resource simply comes back in the next round instead of leaving a hole.
func runDescriptionGeneration(manager *topology.TopologyManager, runner descriptionRunner, targets []domain.ResourceKind, batchSize, parallel, maxRetries, exemplarLimit int, filter domain.ContextFilter, includeNotVisible, regenOversized bool) error {
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
		results := runDescriptionBatchWave(runner, batches, parallel, topo, exemplarLimit)
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

// Runs description generation for resource batches in parallel through the run's runner and collects results.
func runDescriptionBatchWave(runner descriptionRunner, batches [][]descriptionResource, parallel int, topo *domain.Topology, exemplarLimit int) []descriptionBatchResult {
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
				text, err := runner.Run(batch, topo, exemplarLimit)
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
