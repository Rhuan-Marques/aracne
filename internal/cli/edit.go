package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// Reads JSON from stdin containing file path and old/new strings, replaces the old string in the file once, and updates the topology database.
func RunEdit() {
	var input struct {
		FilePath  string `json:"file_path"`
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading stdin: %v\n", err)
		os.Exit(1)
	}
	if err := json.Unmarshal(data, &input); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing JSON: %v\n", err)
		os.Exit(1)
	}
	if input.FilePath == "" || input.OldString == "" || input.NewString == "" {
		fmt.Fprintln(os.Stderr, "Usage: echo '{\"file_path\":\"...\",\"old_string\":\"...\",\"new_string\":\"...\"}' | arac edit")
		os.Exit(1)
	}

	content, err := os.ReadFile(input.FilePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading %s: %v\n", input.FilePath, err)
		os.Exit(1)
	}

	s := string(content)
	if !strings.Contains(s, input.OldString) {
		fmt.Fprintf(os.Stderr, "old_string not found in %s\n", input.FilePath)
		os.Exit(1)
	}

	newContent := strings.Replace(s, input.OldString, input.NewString, 1)
	if err := os.WriteFile(input.FilePath, []byte(newContent), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", input.FilePath, err)
		os.Exit(1)
	}

	manager, reg := InitRegistry(".aracne/topology.db")
	warnings, err := manager.UpdateFile(input.FilePath, reg)
	if err == nil && len(warnings) > 0 {
		for _, w := range warnings {
			fmt.Printf("Warning: [%s] %s (source: %s, target: %s)\n", w.Kind, w.Message, w.SourceID, w.TargetID)
		}
	}
	fmt.Println("edit succeeded")
}
