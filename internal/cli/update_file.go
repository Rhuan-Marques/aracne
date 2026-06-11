package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

func RunUpdateFile(args []string) {
	if len(args) == 1 && args[0] == "--claude-hook" {
		runClaudeUpdateFileHook(os.Stdin, os.Stdout)
		return
	}
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: arac update-file <path> [--db <dbpath>]\n       arac update-file --claude-hook")
		os.Exit(1)
	}
	path := args[0]
	dbPath := ".aracne/topology.db"
	for i := 1; i < len(args); i++ {
		if args[i] == "--db" && i+1 < len(args) {
			dbPath = args[i+1]
			i++
		}
	}
	manager, reg := InitRegistry(dbPath)
	warnings, err := manager.UpdateFile(path, reg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error updating file: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Warning number %d", len(warnings))
	if len(warnings) > 0 {
		for _, w := range warnings {
			fmt.Printf("Warning: [%s] %s (source: %s, target: %s)\n", w.Kind, w.Message, w.SourceID, w.TargetID)
		}
	}
}

func runClaudeUpdateFileHook(input io.Reader, output io.Writer) {
	inputJSON, err := io.ReadAll(input)
	if err != nil || strings.TrimSpace(string(inputJSON)) == "" {
		return
	}

	var event struct {
		ToolInput map[string]interface{} `json:"tool_input"`
	}
	if err := json.Unmarshal(inputJSON, &event); err != nil || event.ToolInput == nil {
		return
	}

	paths := claudeHookPaths(event.ToolInput)
	if len(paths) == 0 {
		return
	}

	manager, reg := InitRegistry(".aracne/topology.db")
	for _, path := range paths {
		warnings, err := manager.UpdateFile(path, reg)
		if err != nil {
			fmt.Fprintf(output, "Aracne update-file failed for %s:\n%v\n", path, err)
			continue
		}
		if len(warnings) > 0 {
			fmt.Fprintf(output, "Aracne warnings for %s:\nWarning number %d", path, len(warnings))
			for _, w := range warnings {
				fmt.Fprintf(output, "Warning: [%s] %s (source: %s, target: %s)\n", w.Kind, w.Message, w.SourceID, w.TargetID)
			}
		}
	}
}

func claudeHookPaths(toolInput map[string]interface{}) []string {
	seen := make(map[string]bool)
	var paths []string
	add := func(value interface{}) {
		path, ok := value.(string)
		if !ok || strings.TrimSpace(path) == "" || seen[path] {
			return
		}
		seen[path] = true
		paths = append(paths, path)
	}

	for _, name := range []string{"file_path", "filePath", "path"} {
		add(toolInput[name])
	}
	if edits, ok := toolInput["edits"].([]interface{}); ok {
		for _, item := range edits {
			edit, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			for _, name := range []string{"file_path", "filePath", "path"} {
				add(edit[name])
			}
		}
	}
	return paths
}
