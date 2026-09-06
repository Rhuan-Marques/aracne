package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/progress"
	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner"
	"github.com/Rhuan-Marques/aracne/internal/tui"
)

// `arac init` -- the starter pack.
//
// WHY THIS IS A WIZARD AND NOT A COMMAND WITH FLAGS. Everything a repository needs from aracne
// is decided by four or five values, and every one of them used to be discovered somewhere
// else: `mode` and `contract_verbosity` by reading docs/modes.md and hand-editing JSON, and
// who writes the descriptions by running `arac descriptions generate` and being asked, in the
// middle of a command someone ran for a different reason. A new project's first experience of
// aracne was a default config that had answered nothing and a sweep that failed naming a
// vendor the user had never chosen.
//
// So the questions are collected in one place, at the one moment where asking them is the
// whole point of the command. Full screen, because two of them need a paragraph and an example
// to answer well, and prose printed as scrollback is prose the reader has to hold in their
// head while the next question prints underneath it. The terminal is handed back before
// anything long-running starts.
//
// WHAT IT IS NOT. It is not the way to re-render your integration files. That is `arac setup`,
// which asks nothing, and which this command calls once the questions are done.

// RunInit asks the setup questions, saves them, scans, writes the harness integration, and
// optionally sweeps descriptions.
func RunInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	global := fs.Bool("global", false, "Install the integration to user level rather than into this project")
	fs.Parse(args)

	// Checked before anything is opened, so a pipe gets an error instead of a hang or a
	// screen full of escape codes in a log. It names the command that does this job without
	// a person present, which is the answer an unattended run actually needs.
	if !tui.Available() {
		fmt.Fprint(os.Stderr, nonInteractiveInitError)
		os.Exit(1)
	}

	// Loaded, never created. EnsureConfig would write a default config here, before a single
	// question had been asked -- and then a cancel at question one would leave a .aracne
	// directory behind and make "nothing was written" a lie. Nothing is written until every
	// question has an answer.
	configPath := helper.ConfigPath(".aracne/topology.db")
	cfg, parsed := helper.LoadConfigStrict(configPath)
	var notes []string
	if !parsed {
		if _, err := os.Stat(configPath); err == nil {
			// The same clean break EnsureConfig makes, said out loud. Held as a note
			// because the alternate screen is about to cover anything printed now.
			notes = append(notes, fmt.Sprintf("Note: %s could not be read as the current "+
				"config schema, so these answers start from defaults. Any other keys it "+
				"set are gone; re-apply them if you need them.", configPath))
		}
	}

	// The file walk is cheap (no parsing) and it is the only size signal available before the
	// scan, which by design runs after the questions.
	reg := NewScannerRegistry()
	answers, err := askInitQuestions(cfg, countSourceFiles(".", reg))
	if err != nil {
		if errors.Is(err, tui.ErrCancelled) {
			// Nothing has been written at this point -- apply and SaveConfig are both
			// after the last question -- so this really is "nothing happened".
			fmt.Fprintln(os.Stderr, "Cancelled. Nothing was written.")
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	for _, note := range append(notes, answers.Notes...) {
		fmt.Fprintf(os.Stderr, "\n%s\n", note)
	}

	answers.apply(cfg)
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: the answers do not make a valid config: %v\n", err)
		os.Exit(1)
	}
	if err := helper.SaveConfig(cfg, configPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: saving %s: %v\n", configPath, err)
		os.Exit(1)
	}
	fmt.Printf("Saved your answers to %s\n\n", configPath)

	manager := initScan(reg)

	// The scan comes FIRST, and the integration files second. The contract's per-language
	// sections are read out of the database (TopologyLanguages), so a setup that ran before
	// the scan would write the language-free contract on every fresh checkout -- correct, but
	// a worse first CLAUDE.md than the one this command is in a position to write.
	//
	// autoYes, because the wizard is itself the confirmation: a [y/N] on os.Stdin two
	// questions after a full-screen flow is a second, worse prompt, and the markdown files
	// are merged in place rather than clobbered.
	fmt.Println()
	runSetupFor(answers, *global)

	if answers.DescribeNow {
		fmt.Println()
		sweepDescriptions(manager, reg, cfg)
	} else {
		fmt.Fprint(os.Stderr, "\nLeaving descriptions to the read path: each read and search will "+
			"describe what it is\nabout to show. Run `arac descriptions generate` any time to "+
			"sweep the rest in one go.\n")
	}
	fmt.Printf("\nDone. `arac init` is a one-off; `arac setup` re-writes these files after a config change.\n")
}

// nonInteractiveInitError is what a pipe, a cron job or CI gets instead of a wizard.
//
// It names the command that does the same job without questions rather than only refusing:
// the reason someone hits this is almost always a script that wanted the integration files,
// and those have never needed a person.
const nonInteractiveInitError = `arac init is interactive and there is no terminal here.

To write the integration files without questions:
  arac setup                 both harnesses
  arac setup --claude        Claude Code only
  arac setup --opencode      OpenCode only

They render from .aracne/config.json, whose four setup keys are:
  "mode"                 mcp | cli | intercept_id | intercept_line_ranges
  "contract_verbosity"   low | high
  "descriptions"         {"provider": "anthropic", "api_key_env": "ANTHROPIC_API_KEY"}
                         {"provider": "cli", "cli_provider_command": "claude -p"}
  "llm": {"<any>": {"agents": {"descriptions-generation-executor": {"model": "..."}}}}
`

// askInitQuestions runs the flow, opening the terminal and giving it back whatever happens.
//
// The Session is opened and closed HERE rather than in RunInit so that the defer covers every
// return path out of the questions -- including a validation error, which would otherwise exit
// the process with the alternate screen still up and the terminal still raw.
func askInitQuestions(cfg *helper.Config, sourceFiles int) (initAnswers, error) {
	s, err := tui.Open()
	if err != nil {
		return initAnswers{}, err
	}
	defer s.Close()
	return runInitQuestions(s, cfg, sourceFiles)
}

// runInitQuestions is the question order, with the terminal already borrowed.
//
// Split from askInitQuestions so a test can drive it with a scripted keyboard: everything that
// decides what a run means -- which branch question three takes, whether question four is
// asked at all -- lives here, and none of it needs a pty to be worth testing.
func runInitQuestions(s *tui.Session, cfg *helper.Config, sourceFiles int) (initAnswers, error) {
	var a initAnswers

	_, harness, err := tui.Select(s, harnessQuestion())
	if err != nil {
		return a, err
	}
	a.Harness = harness

	mode := modeQuestion()
	// The mode the project is already on is the one under the cursor. A re-run of `arac init`
	// on a configured repository should not quietly offer to change the answer it is showing.
	mode.Default = indexOfValue(mode, cfg.EffectiveMode())
	_, chosenMode, err := tui.Select(s, mode)
	if err != nil {
		return a, err
	}
	a.Mode = chosenMode

	if err := askDescriber(s, &a); err != nil {
		return a, err
	}

	// Question five's default is a wall-clock judgement, not a cost one: below the cap a
	// sweep is minutes and leaves the repo fully described, which is the better place to be;
	// above it the sweep is a long unattended job whose benefit all arrives at the end, while
	// the lazy fill delivers the same descriptions in the order the work touches them.
	_, when, err := tui.Select(s, describeNowQuestion(sourceFiles <= describeEverythingFileCap))
	if err != nil {
		return a, err
	}
	a.DescribeNow = when == "now"

	verbosity := verbosityQuestion()
	verbosity.Default = indexOfValue(verbosity, cfg.EffectiveContractVerbosity())
	_, chosenVerbosity, err := tui.Select(s, verbosity)
	if err != nil {
		return a, err
	}
	a.Verbosity = chosenVerbosity
	return a, nil
}

// askDescriber is question three and question four: who writes the descriptions, and with what.
//
// They are one function because the second depends on the first in a way that is not a plain
// sequence -- the CLI branch can answer the model question inside the command answer, and then
// question four is not asked at all.
func askDescriber(s *tui.Session, a *initAnswers) error {
	_, kind, err := tui.Select(s, describerQuestion())
	if err != nil {
		return err
	}

	if kind == helper.ProviderNameCLI {
		a.Provider = helper.ProviderNameCLI
		if err := askCLIBranch(s, a); err != nil {
			return err
		}
		return nil
	}

	_, format, err := tui.Select(s, apiFormatQuestion())
	if err != nil {
		return err
	}
	a.Provider = format
	_, env, err := tui.Select(s, apiKeyEnvQuestion(format))
	if err != nil {
		return err
	}
	a.APIKeyEnv = env
	if os.Getenv(env) == "" {
		// Not refused, only noted: the key may live in a shell profile, a direnv file or
		// CI, none of which this process can see, and the answer is being recorded for
		// every future run rather than only for this one. Held until the alternate screen
		// comes down, because anything printed under it is erased with it.
		a.Notes = append(a.Notes, missingKeyNote(env))
	}
	_, model, err := tui.Select(s, modelQuestion(format))
	if err != nil {
		return err
	}
	a.Model = model
	return nil
}

// askCLIBranch asks for the command, and asks for a model only if the command has not already
// named one.
func askCLIBranch(s *tui.Session, a *initAnswers) error {
	_, command, err := tui.Select(s, cliCommandQuestion())
	if err != nil {
		return err
	}
	if _, err := helper.SplitCommand(command); err != nil {
		// The freeform field takes any text, so an unbalanced quote gets this far. Ask
		// again rather than saving a command that resolves to no command at all.
		q := cliCommandQuestion()
		q.EmptyError = fmt.Sprintf("%v -- try again.", err)
		if _, command, err = tui.Select(s, q); err != nil {
			return err
		}
	}
	a.CLICommand = command

	// A command that already says --model has answered question four. Asking again would
	// collect a second model, write it to a config key the CLI transport does not read, and
	// leave two answers in the file with nothing on screen to say which one runs.
	if model := modelInCommand(command); model != "" {
		a.Model = model
		return nil
	}
	_, model, err := tui.Select(s, modelQuestion(helper.ProviderNameCLI))
	if err != nil {
		return err
	}
	a.Model = model
	// And make the answer real: the CLI transport reads its model from the command, so a
	// model pinned only in the config would be a setting that silently does nothing.
	a.CLICommand = commandWithModel(a.CLICommand, model)
	return nil
}

func missingKeyNote(env string) string {
	return fmt.Sprintf("Note: %s is not set in this shell. Export it before describing:\n  export %s=...", env, env)
}

// indexOfValue finds the option whose recorded value is v, so a question can open on the
// answer the project already has. Returns 0 -- the first option, which is every question's
// documented default -- when there is no match.
func indexOfValue(q tui.Question, v string) int {
	for i, opt := range q.Options {
		if opt.Value == v || (opt.Value == "" && opt.Label == v) {
			return i
		}
	}
	return 0
}

// describeEverythingFileCap is the source-file count at which question five's offered default
// flips from "now" to "lazily".
//
// It is a file count and not a resource count because the scan has not run yet: the questions
// come first, by design, and the walk that counts files is the only size signal available
// before them. Around ten describable resources per source file is what aracne's own corpora
// come out at, so this is the old 8,000-resource cap expressed in the units available here.
// Either answer stays available at either size -- this only decides which one the Enter key
// means.
const describeEverythingFileCap = 800

// initScan builds the topology, with the progress bar the wizard has already earned the right
// to draw: it is on a terminal by definition, and a first scan of an unindexed repository is
// the longest thing this command does.
func initScan(reg *scanner.Registry) *topology.TopologyManager {
	const dbPath = ".aracne/topology.db"
	cfg := helper.LoadConfig(helper.ConfigPath(dbPath))

	manager := topology.New()
	os.MkdirAll(".aracne", 0755)
	manager.Load(dbPath)

	// The same two filters RunScan installs before it walks anything: an ignored or hidden
	// tree has to be invisible to the file count and to the scan alike.
	domain.SetActivePathVisibility(domain.BuildPathVisibility(".", cfg.Paths))
	domain.SetActiveIgnore(domain.BuildIgnoreMatcher(".", cfg.Scan.Ignore))

	fmt.Println("Scanning the project...")
	scanner.SetProgressEnabled(true)
	start := time.Now()
	if _, err := manager.IncrementalScan(".", reg); err != nil {
		fmt.Fprintf(os.Stderr, "Error scanning project: %v\n", err)
		os.Exit(1)
	}
	scanner.SetProgressEnabled(false)
	fmt.Printf("Topology built in %s\n", time.Since(start).Round(time.Millisecond))
	return manager
}

// runSetupFor writes the integration the harness answer selected.
func runSetupFor(a initAnswers, global bool) {
	runSetup(a.writesClaude(), a.writesOpenCode(), global, true)
}

// sweepDescriptions runs the generation the wizard's fifth question asked for.
//
// It is the ordinary `arac descriptions generate` path -- the same runner, the same batching,
// the same progress bar -- and not a second implementation of it. A failure here is reported
// and does not fail the command: the integration files are already written and the config is
// already saved, so the repository is set up whether or not the sweep finished, and the lazy
// fill covers whatever it did not reach.
func sweepDescriptions(manager *topology.TopologyManager, reg *scanner.Registry, cfg *helper.Config) {
	descCfg := cfg.EffectiveLazyDescriptions(helper.DefaultLazyHarness)
	runner, describedWith, err := newDescriptionRunner(manager, reg, cfg, descCfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Skipping the sweep: %v\n", err)
		return
	}

	filter := cfg.EffectiveContextFilter()
	includeNotVisible := cfg.Descriptions.IncludeNotVisible
	pending, err := pendingDescriptionResources(manager, cfg.Descriptions.Kinds, filter, includeNotVisible, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Skipping the sweep: %v\n", err)
		return
	}
	if len(pending) == 0 {
		fmt.Println("Every resource already has a description.")
		return
	}

	batchSize := cfg.AgentParam(helper.DefaultLazyHarness, helper.DescriptionsExecutorAgent,
		"max-batch-size", helper.DefaultDescriptionBatchSize)
	fmt.Printf("Describing %d resources with %s...\n", len(pending), describedWith)

	var bar progress.Reporter
	bar.SetEnabled(true)
	if err := runDescriptionGeneration(manager, runner, cfg.Descriptions.Kinds, batchSize,
		defaultDescriptionParallel, defaultDescriptionMaxRetries, cfg.Descriptions.StyleExemplars,
		filter, includeNotVisible, false, &bar); err != nil {
		fmt.Fprintf(os.Stderr, "\nThe sweep stopped: %v\n", err)
		fmt.Fprintln(os.Stderr, "Descriptions already written stay written. Re-run "+
			"`arac descriptions generate` to finish.")
		return
	}
	fmt.Println("done")
}
