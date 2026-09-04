package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/llm/tools"
	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner"
)

// Executes bash commands in a workspace with topology scanning support.
type BashTool struct {
	workspace string
	mgr       *topology.TopologyManager
	reg       *scanner.Registry
}

// File glob matching tool with workspace context.
type GlobTool struct {
	workspace string
}

// Native tool for agents to ask the user a question.
type AskUserQuestionTool struct{}

// Constructs a BashTool with workspace, topology manager, and scanner registry.
func NewBashTool(workspace string, mgr *topology.TopologyManager, reg *scanner.Registry) *BashTool {
	return &BashTool{workspace: workspace, mgr: mgr, reg: reg}
}

// Creates a GlobTool for pattern-based file matching within a workspace.
func NewGlobTool(workspace string) *GlobTool {
	return &GlobTool{workspace: workspace}
}

// Returns the tool name "bash"
func (b *BashTool) Name() string { return "bash" }

// Returns the description for BashTool: executes terminal commands in the workspace and triggers incremental topology scan on success.
func (b *BashTool) Description() string {
	return "Execute a terminal command in the workspace. A successful command triggers an incremental topology scan."
}

// Returns parameters schema: command (string, required), workdir (string, optional), timeout_ms (number, optional)
func (b *BashTool) Parameters() []tools.Parameter {
	return []tools.Parameter{
		{Name: "command", Type: "string", Description: "Command to execute", Required: true},
		{Name: "workdir", Type: "string", Description: "Working directory. Defaults to the workspace root", Required: false},
		{Name: "timeout_ms", Type: "number", Description: "Timeout in milliseconds. Defaults to 120000", Required: false},
	}
}

// Executes shell command with timeout support, runs incremental topology scan after completion, returns output or error
func (b *BashTool) Run(args json.RawMessage) (string, error) {
	var params struct {
		Command   string `json:"command"`
		Workdir   string `json:"workdir"`
		TimeoutMS int    `json:"timeout_ms"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if strings.TrimSpace(params.Command) == "" {
		return "", fmt.Errorf("missing required argument: command")
	}
	if params.TimeoutMS <= 0 {
		params.TimeoutMS = 120000
	}
	workdir := params.Workdir
	if workdir == "" {
		workdir = b.workspace
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(params.TimeoutMS)*time.Millisecond)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "powershell", "-NoLogo", "-NoProfile", "-Command", params.Command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-lc", params.Command)
	}
	cmd.Dir = workdir
	out, err := cmd.CombinedOutput()
	text := string(out)
	if ctx.Err() == context.DeadlineExceeded {
		return trimToolOutput(text) + "\ncommand timed out", fmt.Errorf("command timed out")
	}
	if err != nil {
		return trimToolOutput(text), fmt.Errorf("command failed: %w", err)
	}
	if b.mgr != nil && b.reg != nil {
		if warnings, scanErr := b.mgr.IncrementalScan(b.workspace, b.reg); scanErr != nil {
			text += fmt.Sprintf("\n\nTopology scan failed: %v", scanErr)
		} else if len(warnings) > 0 {
			text += fmt.Sprintf("\n\nTopology scan completed with %d warning(s).", len(warnings))
		} else {
			text += "\n\nTopology scan completed."
		}
	}
	return trimToolOutput(text), nil
}

// Returns tool name 'glob'
func (g *GlobTool) Name() string { return "glob" }

// Returns description of glob pattern file search capability
func (g *GlobTool) Description() string {
	return "Find files by glob pattern. Supports *, ?, and ** patterns."
}

// Returns parameter schema for glob file search with pattern and path
func (g *GlobTool) Parameters() []tools.Parameter {
	return []tools.Parameter{
		{Name: "pattern", Type: "string", Description: "Glob pattern, for example **/*.go", Required: true},
		{Name: "path", Type: "string", Description: "Directory to search. Defaults to the workspace root", Required: false},
	}
}

// Finds files matching a glob pattern relative to workspace root
func (g *GlobTool) Run(args json.RawMessage) (string, error) {
	var params struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if strings.TrimSpace(params.Pattern) == "" {
		return "", fmt.Errorf("missing required argument: pattern")
	}
	root := params.Path
	if root == "" {
		root = g.workspace
	}
	patternRe, err := globRegexp(filepath.ToSlash(params.Pattern))
	if err != nil {
		return "", err
	}
	var matches []string
	walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if patternRe.MatchString(rel) {
			matches = append(matches, rel)
		}
		return nil
	})
	if walkErr != nil {
		return "", walkErr
	}
	sort.Strings(matches)
	return strings.Join(matches, "\n"), nil
}

// Returns the tool name "ask_user_question".
func (a *AskUserQuestionTool) Name() string { return "ask_user_question" }

// Returns the description for the AskUserQuestionTool: "Ask the user a question and wait for their answer before continuing."
func (a *AskUserQuestionTool) Description() string {
	return "Ask the user a question and wait for their answer before continuing."
}

// Returns tool parameters: question (required), options (optional array), and multiple (optional boolean).
func (a *AskUserQuestionTool) Parameters() []tools.Parameter {
	return []tools.Parameter{
		{Name: "question", Type: "string", Description: "Question to ask the user", Required: true},
		{Name: "options", Type: "array", Description: "Optional answer choices", Required: false},
		{Name: "multiple", Type: "boolean", Description: "Whether multiple options may be selected", Required: false},
	}
}

// Always returns an error, delegating handling to the chat session.
func (a *AskUserQuestionTool) Run(args json.RawMessage) (string, error) {
	return "", fmt.Errorf("ask_user_question is handled by the chat session")
}

// Converts a glob pattern (with *, **, ?) to a compiled regexp for matching file paths.
func globRegexp(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		ch := pattern[i]
		switch ch {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				b.WriteString(".*")
				i++
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		case '.', '+', '(', ')', '|', '[', ']', '{', '}', '^', '$', '\\':
			b.WriteByte('\\')
			b.WriteByte(ch)
		default:
			b.WriteByte(ch)
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

// Truncates tool output to 64KB with an ellipsis suffix indicating total bytes if exceeded.
func trimToolOutput(text string) string {
	const limit = 64000
	if len(text) <= limit {
		return strings.TrimRight(text, "\r\n")
	}
	return strings.TrimRight(text[:limit], "\r\n") + fmt.Sprintf("\n... output truncated (%d bytes total)", len(text))
}
