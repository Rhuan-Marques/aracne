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

	"aracne/internal/llm/tools"
	"aracne/internal/topology"
	"aracne/internal/topology/scanner"
)

type BashTool struct {
	workspace string
	mgr       *topology.TopologyManager
	reg       *scanner.Registry
}

type GlobTool struct {
	workspace string
}

type AskUserQuestionTool struct{}

func NewBashTool(workspace string, mgr *topology.TopologyManager, reg *scanner.Registry) *BashTool {
	return &BashTool{workspace: workspace, mgr: mgr, reg: reg}
}

func NewGlobTool(workspace string) *GlobTool {
	return &GlobTool{workspace: workspace}
}

func (b *BashTool) Name() string { return "bash" }

func (b *BashTool) Description() string {
	return "Execute a terminal command in the workspace. A successful command triggers an incremental topology scan."
}

func (b *BashTool) Parameters() []tools.Parameter {
	return []tools.Parameter{
		{Name: "command", Type: "string", Description: "Command to execute", Required: true},
		{Name: "workdir", Type: "string", Description: "Working directory. Defaults to the workspace root", Required: false},
		{Name: "timeout_ms", Type: "number", Description: "Timeout in milliseconds. Defaults to 120000", Required: false},
	}
}

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

func (g *GlobTool) Name() string { return "glob" }

func (g *GlobTool) Description() string {
	return "Find files by glob pattern. Supports *, ?, and ** patterns."
}

func (g *GlobTool) Parameters() []tools.Parameter {
	return []tools.Parameter{
		{Name: "pattern", Type: "string", Description: "Glob pattern, for example **/*.go", Required: true},
		{Name: "path", Type: "string", Description: "Directory to search. Defaults to the workspace root", Required: false},
	}
}

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

func (a *AskUserQuestionTool) Name() string { return "ask_user_question" }

func (a *AskUserQuestionTool) Description() string {
	return "Ask the user a question and wait for their answer before continuing."
}

func (a *AskUserQuestionTool) Parameters() []tools.Parameter {
	return []tools.Parameter{
		{Name: "question", Type: "string", Description: "Question to ask the user", Required: true},
		{Name: "options", Type: "array", Description: "Optional answer choices", Required: false},
		{Name: "multiple", Type: "boolean", Description: "Whether multiple options may be selected", Required: false},
	}
}

func (a *AskUserQuestionTool) Run(args json.RawMessage) (string, error) {
	return "", fmt.Errorf("ask_user_question is handled by the chat session")
}

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

func trimToolOutput(text string) string {
	const limit = 64000
	if len(text) <= limit {
		return strings.TrimRight(text, "\r\n")
	}
	return strings.TrimRight(text[:limit], "\r\n") + fmt.Sprintf("\n... output truncated (%d bytes total)", len(text))
}
