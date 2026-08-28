package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Implements the Tool interface for listing files and directories, exposing a "ls" tool to the LLM agent.
type Ls struct{}

// Returns the tool name string "ls" for the Ls tool.
func (l *Ls) Name() string { return "ls" }

// Returns the description string for the Ls tool, indicating it lists files and directories at a given path for exploring the project structure.
func (l *Ls) Description() string {
	return "List files and directories at the given path. Use this to explore the project structure."
}

// Returns the parameter definitions for the ls tool: path (optional directory) and recursive (optional boolean flag).
func (l *Ls) Parameters() []Parameter {
	return []Parameter{
		{Name: "path", Type: "string", Description: "Directory path to list (default '.')", Required: false},
		{Name: "recursive", Type: "boolean", Description: "List recursively if true", Required: false},
	}
}

// Executes the ls tool: takes a path and recursive flag, lists files/directories (with trailing slash for dirs), skips hidden directories when recursive, and returns the sorted results joined by newlines.
func (l *Ls) Run(args json.RawMessage) (string, error) {
	var params struct {
		Path      string `json:"path"`
		Recursive bool   `json:"recursive"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if params.Path == "" {
		params.Path = "."
	}

	var entries []string
	if params.Recursive {
		filepath.Walk(params.Path, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				base := filepath.Base(p)
				// p != params.Path so listing inside a dot-named directory works;
				// `p != "."` only covered the relative-cwd case.
				if strings.HasPrefix(base, ".") && p != params.Path {
					return filepath.SkipDir
				}
				rel, _ := filepath.Rel(params.Path, p)
				if rel == "." {
					return nil
				}
				entries = append(entries, rel+"/")
			} else {
				rel, _ := filepath.Rel(params.Path, p)
				entries = append(entries, rel)
			}
			return nil
		})
	} else {
		dirEntries, err := os.ReadDir(params.Path)
		if err != nil {
			return "", fmt.Errorf("read dir: %w", err)
		}
		for _, e := range dirEntries {
			name := e.Name()
			if e.IsDir() {
				name += "/"
			}
			entries = append(entries, name)
		}
	}

	sort.Strings(entries)
	return strings.Join(entries, "\n"), nil
}
