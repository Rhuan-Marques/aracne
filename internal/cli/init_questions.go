package cli

import (
	"strconv"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/tui"
)

// The seven questions `arac init` asks, and the rules that turn their answers into a config.
//
// They live apart from the flow in init.go for one reason: the flow needs a terminal and the
// rules do not. Everything here is a value in and a value out -- which harness, which mode,
// which model, what to write into .aracne/config.json -- so the parts that are easy to get
// subtly wrong (the model hidden inside a CLI command, the model that has to be appended back
// onto one) are testable without a pty.

// initAnswers is everything the wizard collected. It is deliberately a plain struct and not a
// *helper.Config being mutated question by question: an interrupted wizard must leave the
// config it found untouched, and the way to guarantee that is for nothing to be written until
// every question has been answered.
type initAnswers struct {
	// Harness is which integration to write: "claude_code", "opencode" or "both". It is the
	// one answer that is NOT persisted -- it selects which tree runSetup writes, and a
	// project that wants the other one runs `arac setup --opencode`.
	Harness string
	// Mode is one of the four helper.Mode* values.
	Mode string
	// Provider is a helper.ProviderName* value.
	Provider string
	// APIKeyEnv is set on the API branch, CLICommand on the CLI branch. Never both.
	APIKeyEnv  string
	CLICommand string
	// Model is what the descriptions-generation-executor describes with.
	Model string
	// Verbosity is helper.ContractVerbosityLow or High.
	Verbosity string
	// Notes are things worth saying that came up mid-question -- an API key variable that
	// is not set in this shell, so far. They are collected rather than printed because the
	// wizard owns the alternate screen while it asks, and anything written under it is
	// erased when the screen comes down.
	Notes []string
	// DescribeNow is the one answer that is not a setting: it decides whether this run
	// sweeps the repository, and nothing records it. Lazy generation is already on by
	// default, so "lazily" is not a mode being chosen -- it is this sweep being dropped.
	DescribeNow bool
	// PreloadTools is question seven, and it is only MEANINGFUL when asksPreloadTools is
	// true -- a plain bool rather than a *bool because the two answers that decide whether
	// it was asked (Harness and Mode) are already in this struct, so a second field saying
	// "and we did ask" would be a fact derivable from the other two and free to disagree
	// with them.
	PreloadTools bool
}

// The harness answers. "both" is what a bare `arac init` used to do unconditionally, and it is
// kept as a choice rather than as the default: writing an OpenCode tree into a repository that
// has never opened OpenCode is clutter the user did not ask for, and they can see it here.
const (
	harnessClaudeCode = "claude_code"
	harnessOpenCode   = "opencode"
	harnessBoth       = "both"
)

// writesClaude / writesOpenCode read the harness answer, so the three-way string is decoded in
// one place rather than compared against in four.
func (a initAnswers) writesClaude() bool {
	return a.Harness == harnessClaudeCode || a.Harness == harnessBoth
}

func (a initAnswers) writesOpenCode() bool {
	return a.Harness == harnessOpenCode || a.Harness == harnessBoth
}

// asksPreloadTools reports whether question seven applies to these answers.
//
// BOTH halves are required, and each on its own would be a question with no effect. The
// variable is a key in .claude/settings.json, so an OpenCode-only project has nowhere to put
// it; and it is about the schemas of MCP tools, which outside ModeMCP the main agent is served
// none of. "Both" counts as Claude Code -- the Claude tree is written either way.
//
// The flow and apply share this so the two cannot drift: a question the wizard skipped must
// also be a key apply does not write, or a cli-mode run would stamp an answer nobody gave.
func (a initAnswers) asksPreloadTools() bool {
	return a.writesClaude() && a.Mode == helper.ModeMCP
}

// apply writes the answers into cfg. It is the only function that mutates a config here, and
// it runs once, after the last question.
func (a initAnswers) apply(cfg *helper.Config) {
	cfg.Mode = a.Mode
	cfg.ContractVerbosity = a.Verbosity
	cfg.Descriptions.Provider = a.Provider
	// Exactly one of these, and the other cleared: a config carrying both a key variable and
	// a command says two different things about who writes its descriptions, and the reader
	// who finds it has no way to tell which one the wizard meant.
	if a.Provider == helper.ProviderNameCLI {
		cfg.Descriptions.CLIProviderCommand = a.CLICommand
		cfg.Descriptions.APIKeyEnv = ""
	} else {
		cfg.Descriptions.APIKeyEnv = a.APIKeyEnv
		cfg.Descriptions.CLIProviderCommand = ""
	}
	setDescriptionsExecutorModel(cfg, a.Model)
	// Left ALONE when the question did not apply, rather than written false. nil is
	// "aracne does not manage ENABLE_TOOL_SEARCH" and false is "aracne manages it, and
	// withdraws it" -- so stamping false on a cli-mode run would hand aracne ownership of a
	// variable the operator may have set for their own reasons, and the next setup would
	// delete it. A re-run that skips the question keeps whatever the previous one recorded.
	if a.asksPreloadTools() {
		preload := a.PreloadTools
		cfg.PreloadMCPTools = &preload
	}
}

// setDescriptionsExecutorModel pins the model both description entry points use.
//
// It goes in llm.<any>, not in a per-harness block. EffectiveAgent merges <any> underneath
// whichever harness is asked for, so one write covers Claude Code and OpenCode both -- and the
// lazy fill on the read path, which resolves against claude_code by default
// (helper.DefaultLazyHarness), sees it without the wizard having to guess which harness a
// future `arac read` will be running under.
//
// DefaultConfig already creates this agent with Model: "<inherits>", which
// EffectiveLazyDescriptions reads as unset, so this overwrites a placeholder rather than
// growing the config a new key.
func setDescriptionsExecutorModel(cfg *helper.Config, model string) {
	model = strings.TrimSpace(model)
	if model == "" {
		return
	}
	if cfg.LLM.Any.Agents == nil {
		cfg.LLM.Any.Agents = map[string]helper.AgentConfig{}
	}
	agent := cfg.LLM.Any.Agents[helper.DescriptionsExecutorAgent]
	agent.Model = model
	cfg.LLM.Any.Agents[helper.DescriptionsExecutorAgent] = agent
}

// The cheap tier of each provider, offered as the default answer to the model question.
//
// The CLI answer is deliberately a different STRING for the same model. An API branch writes
// this value into the `model` field of a request to the vendor's endpoint, which wants the full
// published id; the CLI branch writes it into `--model` on a command line, where the Claude CLI
// resolves its own short aliases and a full id is not guaranteed to mean anything. Passing an
// alias to an HTTP API is a 404 at the first batch, which is a slow way to find out -- so the
// two are separate cases rather than one value doing both jobs.
//
// AND THE API SPELLING HAS TO BE A REAL ID. This returned "claude-haiku-5", which is not a
// model -- while the paragraph above argued that the API branch needs "the full published id".
// It was the DEFAULT answer to question four on the Anthropic branch, so the happy path of the
// wizard ended in a sweep that 404'd on its first batch, after the wizard had already reported
// success. Kept in step with lazydesc.modelAliases, which is where the same names resolve for
// the read path.
func defaultModelFor(provider string) string {
	switch provider {
	case helper.ProviderNameOpenAI:
		return "gpt-5.4-mini"
	case helper.ProviderNameDeepSeek:
		return "deepseek-v4-flash"
	case helper.ProviderNameCLI:
		return "haiku"
	default:
		return "claude-haiku-4-5"
	}
}

// modelInCommand finds a --model already named in a CLI provider command.
//
// It exists so the wizard does not ask a question the user has just answered. Someone who
// types `claude -p --model sonnet` has said which model writes their descriptions; asking them
// again, and then writing a different answer into a config key the CLI transport ignores,
// would leave two models in the file with no way to see which one runs.
//
// Both spellings, because both work on every CLI this is likely to front.
func modelInCommand(command string) string {
	argv, err := helper.SplitCommand(command)
	if err != nil {
		return ""
	}
	for i, arg := range argv {
		if after, ok := strings.CutPrefix(arg, "--model="); ok {
			return strings.TrimSpace(after)
		}
		if arg == "--model" && i+1 < len(argv) {
			return strings.TrimSpace(argv[i+1])
		}
	}
	return ""
}

// commandWithModel appends `--model <model>` to a Claude CLI invocation, and leaves everything
// else alone.
//
// The narrowness is the point. `descriptions-generation-executor.model` is read by the API
// transports and by nothing on the CLI path -- the command is a black box aracne pipes a batch
// into -- so pinning a model there and stopping would be a setting that silently does nothing.
// Appending the flag makes the answer real for the one CLI whose flag we actually know.
// `codex exec` and a hand-written script are left untouched: guessing a flag onto someone
// else's program is how a wizard turns a working command into one that exits 2.
//
// Narrow about the PROGRAM, not about its other flags. This used to demand exactly two argv
// words, so `claude -p --max-turns 1` -- the command the docs recommend -- and
// `/usr/local/bin/claude -p` came back unchanged and the model answer silently did nothing.
// Any `claude` in print mode takes the flag; a `--` is where its options stop, so a command
// with one is left alone rather than given a --model that would read as a prompt word.
func commandWithModel(command, model string) string {
	model = strings.TrimSpace(model)
	if model == "" || modelInCommand(command) != "" {
		return command
	}
	argv, err := helper.SplitCommand(command)
	if err != nil || len(argv) < 2 || baseName(argv[0]) != "claude" {
		return command
	}
	printMode := false
	for _, arg := range argv[1:] {
		if arg == "--" {
			return command
		}
		printMode = printMode || arg == "-p" || arg == "--print"
	}
	if !printMode {
		return command
	}
	return command + " --model " + model
}

// ---------------------------------------------------------------------------
// The questions
// ---------------------------------------------------------------------------

// totalInitSteps is the denominator of the "(n/7)" counter. It counts the questions a run can
// ask, not the ones it does: the API and CLI branches ask a different fourth question, one of
// them sometimes skips the fifth, and the seventh is asked only on the one combination it
// means anything on -- but a counter that changed its denominator halfway through would read
// as the wizard growing while you answer it.
//
// The conditional question is LAST for this reason. Anywhere else it would leave a visible
// hole in the middle of the sequence on every run that skips it; at the end, a run that does
// not ask it simply stops at 6/7.
const totalInitSteps = 7

func harnessQuestion() tui.Question {
	return tui.Question{
		Title: "Which harness are you setting up?",
		Step:  "1/" + strconv.Itoa(totalInitSteps),
		Intro: []string{
			"This decides which integration files are written. You can run `arac setup` later to change this.",
		},
		Options: []tui.Option{
			{
				Label:   "Claude Code",
				Value:   harnessClaudeCode,
				Summary: "Writes the Claude Code integration.",
				Detail: []string{
					"  .claude/commands/    the descriptions slash commands",
					"  .claude/agents/      the description executor",
					"  .claude/settings.json + hooks/   the guard hook",
					"  CLAUDE.md            the contract",
					"",
					"The guard hook is what keeps the topology current: it re-scans " +
						"before the tool call that is about to read it.",
				},
			},
			{
				Label:   "OpenCode",
				Value:   harnessOpenCode,
				Summary: "Writes the OpenCode integration.",
				Detail: []string{
					"  .opencode/opencode.json   the MCP server and permissions",
					"  .opencode/commands/       the descriptions commands",
					"  .opencode/agents/         the description executor",
					"  .opencode/plugins/        the pre-tool scan plugin",
					"  AGENTS.md                 the contract",
				},
			},
			{
				Label:   "Both",
				Value:   harnessBoth,
				Summary: "Writes both trees.",
				Detail: []string{
					"Pick this if you switch between the two, " +
						"or if you are not sure yet. They don't affect each other, " +
						"and an unused one costs nothing but the files.",
				},
			},
		},
	}
}

func modeQuestion() tui.Question {
	return tui.Question{
		Title: "How should the model reach your code?",
		Step:  "2/" + strconv.Itoa(totalInitSteps),
		Intro: []string{
			"This decides how your model interacts with Aracne. Choose cli if unsure.",
		},
		Options: []tui.Option{
			{
				Label:   "cli",
				Value:   helper.ModeCLI,
				Summary: "No MCP tools. The contract points at `arac read <id>`.",
				Detail: []string{
					"Shell reads run as themselves; the model is taught to prefer " +
						"resource IDs and reaches them through the terminal.",
					"",
					"The default, and the cheapest: nothing is added to the tool " +
						"schema of every request.",
					"",
					"  $ arac read internal/cli.RunSetup",
					"  internal/cli.RunSetup  (function)",
					"  # CONTEXT: helper.EnsureConfig, announceMode ...",
				},
			},
			{
				Label:   "mcp",
				Value:   helper.ModeMCP,
				Summary: "One `read` MCP tool, served by `arac serve`.",
				Detail: []string{
					"Shell reads still run as themselves. The model gets a real tool " +
						"rather than a command, which some harnesses drive better -- and " +
						"pays for its schema on every request.",
					"",
					"blocked_tools is active in this mode and in cli: the guard can deny " +
						"a native read because there is somewhere else to send it.",
				},
			},
			{
				Label:   "intercept_id",
				Value:   helper.ModeInterceptID,
				Summary: "cat/head/tail/sed -n are answered from the topology.",
				Detail: []string{
					"They take a resource ID where they take a path, so the model's " +
						"existing habits reach the graph without being taught a new verb.",
					"",
					"  $ cat internal/cli.RunSetup",
					"",
					"blocked_tools is inert here: nothing needs denying when the read " +
						"itself is the aracne one.",
				},
			},
			{
				Label:   "intercept_line_ranges",
				Value:   helper.ModeInterceptLineRanges,
				Summary: "The same, addressed as path:start-end.",
				Detail: []string{
					"Every declaration is named by the exact lines it spans, and reading " +
						"that span returns byte-for-byte what the resource read would have.",
					"",
					"  $ sed -n 36,84p internal/cli/setup.go",
					"",
					"Chosen on evidence: over one A/B run the model addressed code as " +
						"file+line in all 408 shell commands and used a resource ID zero " +
						"times.",
				},
			},
		},
	}
}

func describerQuestion() tui.Question {
	return tui.Question{
		Title: "Who writes your descriptions?",
		Step:  "3/" + strconv.Itoa(totalInitSteps),
		Intro: []string{
			"Aracne describes every function, struct and interface so a read can show you what " +
				"its neighbours are without opening them. Something has to write those eventually.",
		},
		Options: []tui.Option{
			{
				Label:   "An API key",
				Value:   "api",
				Summary: "Call a provider's HTTP API.",
				Detail: []string{
					"Bills the key's balance, per token.",
					"Fastest and safest if you have one lying around.",
				},
			},
			{
				Label:   "A CLI command",
				Value:   helper.ProviderNameCLI,
				Summary: "Run a command that is already logged in.",
				Detail: []string{
					"Run a CLI command that works as an LLM. This can use your SUBSCRIPTION TOKENS " +
						"instead of money directly.",
					"",
					"No API key needed. `claude -p` on a Claude Code subscription is " +
						"the common case.",
					"",
					"Note: check with your subscription plan provider before trusting this. Some " +
						"providers consider interactions like these to be exploits, and might apply " +
						"a different price or draw from a separate pool of money.",
				},
			},
		},
	}
}

func apiFormatQuestion() tui.Question {
	return tui.Question{
		Title: "Which format does that API speak?",
		Step:  "3/" + strconv.Itoa(totalInitSteps),
		Intro: []string{
			"The wire format, not necessarily the company: for example, many different " +
				"companies use APIs that follow the OpenAI format.",
			"If in doubt, OpenAI is probably the answer!",
		},
		Options: []tui.Option{
			{
				Label:   helper.ProviderNameAnthropic,
				Summary: "The Anthropic Messages API.",
				Detail:  []string{"Claude's own format for API message input and output. More flexible and complete."},
			},
			{
				Label:   helper.ProviderNameOpenAI,
				Summary: "The OpenAI chat-completions API, and everything compatible with it.",
				Detail: []string{
					"The right answer for most third-party vendors and gateways, " +
						"whatever they are called.",
					"",
					"Describes with gpt-5.4-mini unless you name another model.",
				},
			},
			{
				Label:   helper.ProviderNameDeepSeek,
				Summary: "The DeepSeek API.",
				Detail:  []string{"Similar to OpenAI's, with a few differences."},
			},
		},
	}
}

func apiKeyEnvQuestion(provider string) tui.Question {
	fallback := helper.DefaultAPIKeyEnv(provider)
	return tui.Question{
		Title: "Which environment variable holds the key?",
		Step:  "3/" + strconv.Itoa(totalInitSteps),
		Intro: []string{
			"Asked rather than fixed, because the format and the credential are separate " +
				"facts: an OpenAI-format gateway bills its own key, and a project pointed " +
				"at one should not have to store that credential in a variable named after " +
				"a company it is not paying.",
		},
		Options: []tui.Option{
			{
				Label:   fallback,
				Summary: "The usual variable for " + provider + ".",
				Detail:  []string{"What aracne would read if you named nothing."},
			},
			{
				Label:    "Other:",
				Freeform: true,
				Summary:  "Type the variable your key is in.",
				Detail: []string{
					"The name only -- not the key itself. Aracne passes the variable along at " +
						"describe time, so it's not saved anywhere else.",
				},
			},
		},
		FreeformPrompt: "Other:",
		EmptyError:     "Name a variable, or pick the default above.",
	}
}

func cliCommandQuestion() tui.Question {
	return tui.Question{
		Title: "Which command should write them?",
		Step:  "3/" + strconv.Itoa(totalInitSteps),
		Intro: []string{
			"It is run once per batch, with the batch on its stdin and the descriptions " +
				"read back off its stdout.",
		},
		Options: []tui.Option{
			{
				Label:   defaultDescribeCLICommand,
				Summary: "The Claude CLI, in print mode.",
				Detail: []string{
					"`claude` must be on PATH and already logged in -- running `claude` " +
						"once, interactively, is enough.",
				},
			},
			{
				Label:    "Other:",
				Freeform: true,
				Summary:  "Any command that answers a prompt on stdout.",
				Detail: []string{
					"  codex exec",
					"  claude -p --model haiku --max-turns 1",
					"  ./scripts/describe.sh",
					"",
					"A CLI that needs a flag to be non-interactive needs that flag here.",
				},
			},
		},
		FreeformPrompt: "Other:",
		EmptyError:     "Type a command, or pick the default above.",
	}
}

func modelQuestion(provider string) tui.Question {
	fallback := defaultModelFor(provider)

	// The two branches spend the answer differently, and the copy has to say which, because
	// "where does this end up" is what tells someone whether the name they are about to type
	// is a published model id or whatever their CLI happens to call it.
	summary := "The cheap tier of " + provider + "."
	detail := []string{
		"Sent as the model on every describe request, so it wants the full name " +
			"your provider publishes.",
	}
	if provider == helper.ProviderNameCLI {
		summary = "The Claude CLI's cheap tier."
		detail = []string{
			"Added to the command as `--model haiku` when that command is the " +
				"Claude CLI, which takes its model on the command line rather than " +
				"from the config.",
			"",
			"Any other command is left exactly as you typed it -- aracne will not " +
				"guess a flag onto a program it does not know.",
		}
	}

	return tui.Question{
		Title: "Which model writes them?",
		Step:  "4/" + strconv.Itoa(totalInitSteps),
		Intro: []string{
			"Descriptions are short and there are thousands of them, so this is the one " +
				"place a cheap model is advised.",
		},
		Options: []tui.Option{
			{
				Label:   fallback,
				Summary: summary,
				Detail:  detail,
			},
			{
				Label:    "Other:",
				Freeform: true,
				Summary:  "Name the model yourself.",
				Detail: []string{
					"Whatever your provider calls it. A bigger model writes better " +
						"descriptions and costs more per resource; on a repository of " +
						"any size that multiplies quickly.",
				},
			},
		},
		FreeformPrompt: "Other:",
		EmptyError:     "Name a model, or pick the default above.",
	}
}

func describeNowQuestion(defaultNow bool) tui.Question {
	def := 1
	if defaultNow {
		def = 0
	}
	return tui.Question{
		Title:   "Describe the repository now?",
		Step:    "5/" + strconv.Itoa(totalInitSteps),
		Default: def,
		Intro: []string{
			"Lazy generation is on either way (you can turn it off in the config file).",
			"This decides whether to sweep now or let it build organically.",
		},
		Options: []tui.Option{
			{
				Label:   "Now",
				Value:   "now",
				Summary: "One sweep, before this command returns.",
				Detail: []string{
					"The whole cost is paid up front, and aracne is as effective as it can be " +
						"right away.",
					"",
					"This is not the only way to sweep: `/descriptions-generate` in your " +
						"harness describes them all in one go too, on the harness's own " +
						"agents rather than the describer configured here. To do it that " +
						"way, answer Lazily now and run it once aracne is set up.",
				},
			},
			{
				Label:   "Lazily",
				Value:   "lazily",
				Summary: "Nothing is spent now.",
				Detail: []string{
					"Each read and search describes the handful of nodes it is about to " +
						"show, so the repo warms up as you work in it -- cold reads are " +
						"slower until it does.",
					"",
					"You can sweep all the missing ones in one go later, any time: " +
						"`/descriptions-generate` in your harness, or " +
						"`arac descriptions generate` from the shell.",
				},
			},
		},
	}
}

func verbosityQuestion() tui.Question {
	return tui.Question{
		Title: "Aracne Contract Length",
		Step:  "6/" + strconv.Itoa(totalInitSteps),
		Intro: []string{
			"The contract is the block aracne writes into CLAUDE.md / AGENTS.md.",
		},
		Options: []tui.Option{
			{
				Label:   helper.ContractVerbosityLow,
				Summary: "Less railroading, just functionality.",
				Detail: []string{
					"Ideal for flagship models, since those already generalise and optimise very well.",
				},
			},
			{
				Label:   helper.ContractVerbosityHigh,
				Summary: "Full: with examples and behavioural rules.",
				Detail: []string{
					"Ideal for lower-end models, like Haikus, DeepSeeks or GPT minis. These " +
						"models are cheap and Aracne can make them more capable, but the " +
						"contract has to spell things out for them.",
				},
			},
		},
	}
}

// The two answers to question seven. They are spelled as words rather than as "yes"/"no"
// because the question's title can be read either way round -- "load them up front?" and
// "leave the search on?" are the same question with opposite polarity -- and a stored "yes"
// would not say which one it was answering.
const (
	preloadToolsEager    = "preload"
	preloadToolsDeferred = "defer"
)

// preloadToolsQuestion is asked only in ModeMCP, and only when a Claude Code tree is being
// written. See initAnswers.asksPreloadTools.
//
// WHY THIS IS A QUESTION AND NOT A DEFAULT. Claude Code defers tool schemas past a size
// threshold: a deferred tool arrives as a bare name, with no parameters and no description,
// and using it costs a schema lookup before the first call. That is a tax on exactly the tools
// this mode exists to serve, paid against a `grep` that is sitting right there fully
// described -- and the cheaper-looking option wins more often than it should. Turning the
// deferral off removes the asymmetry.
//
// It is not free, and the copy has to say so: the switch is Claude Code's, not aracne's, so it
// applies to EVERY tool the session has. A project with several other MCP servers pays for all
// of their schemas on every request to stop paying the lookup on ours.
func preloadToolsQuestion() tui.Question {
	return tui.Question{
		Title: "Keep aracne's tools loaded in context?",
		Step:  "7/" + strconv.Itoa(totalInitSteps),
		Intro: []string{
			"Claude Code hides tool schemas behind a search once a session has enough of " +
				"them. A hidden tool reaches the model as a name and nothing else, and " +
				"has to be looked up before it can be called.",
		},
		Options: []tui.Option{
			{
				Label:   "Load them up front",
				Value:   preloadToolsEager,
				Summary: "Writes ENABLE_TOOL_SEARCH=false into .claude/settings.json.",
				Detail: []string{
					"Aracne's tools arrive with their descriptions, like every native " +
						"tool, so reaching for one is never the more expensive move.",
					"",
					"The switch belongs to Claude Code and is not per-server: every " +
						"tool in the session is loaded up front, including any from " +
						"other MCP servers. On a session with a lot of them that is " +
						"real context spent on every request.",
					"",
					"Takes effect when Claude Code next starts.",
				},
			},
			{
				Label:   "Leave the search on",
				Value:   preloadToolsDeferred,
				Summary: "Claude Code's own default. Nothing is written.",
				Detail: []string{
					"The cheaper baseline, and the right answer if this project already " +
						"runs several MCP servers.",
					"",
					"An ENABLE_TOOL_SEARCH you set yourself is left alone either way -- " +
						"aracne only ever removes the value it wrote.",
				},
			},
		},
	}
}
