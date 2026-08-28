package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"aracne/internal/topology"
	"aracne/internal/topology/scanner"
)

// LLM tool for writing files with scanner registry and topology manager integration.
type Write struct {
	mgr *topology.TopologyManager
	reg *scanner.Registry
}

// Creates a Write tool for editing and writing files with topology updates.
func NewWrite(mgr *topology.TopologyManager, reg *scanner.Registry) *Write {
	return &Write{mgr: mgr, reg: reg}
}

// Returns the tool name: "write"
func (w *Write) Name() string {
	return "write"
}

// Returns the tool description: "Write a new file, creating parent directories if needed."
func (w *Write) Description() string {
	return "Write a file, creating parent directories and overwriting if present. The topology is updated automatically."
}

// Returns required parameters: file_path (string) and content (string)
func (w *Write) Parameters() []Parameter {
	return []Parameter{
		{Name: "file_path", Type: "string", Description: "The absolute path to the file to write", Required: true},
		{Name: "content", Type: "string", Description: "The content to write to the file", Required: true},
	}
}

// Parses JSON args, validates file_path, and executes file write with optional file locking
func (w *Write) Run(args json.RawMessage) (string, error) {
	var params struct {
		FilePath string `json:"file_path"`
		Content  string `json:"content"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	if params.FilePath == "" {
		return "", fmt.Errorf("missing required argument: file_path")
	}

	// Serialize with concurrent edits/writes of the same file (see Edit). When
	// mgr is nil there is nothing to lock or update, so apply directly.
	if w.mgr == nil {
		return w.apply(params.FilePath, params.Content)
	}
	return w.mgr.WithFileLock(params.FilePath, func(_ bool) (string, error) {
		return w.apply(params.FilePath, params.Content)
	})
}

// Creates directories, writes file content, updates topology, and returns warnings if any
func (w *Write) apply(filePath, contentStr string) (string, error) {
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("create directories: %w", err)
	}

	if err := os.WriteFile(filePath, []byte(contentStr), 0644); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}

	if w.mgr != nil {
		warnings, err := w.mgr.UpdateFile(filePath, w.reg)
		if err != nil {
			return "", fmt.Errorf("update topology: %w", err)
		}
		if len(warnings) > 0 {
			var msgs []string
			for _, w := range warnings {
				msgs = append(msgs, fmt.Sprintf("  - [%s] %s (source: %s, target: %s)", w.Kind, w.Message, w.SourceID, w.TargetID))
			}
			return "write succeeded\n\nTopology warnings (functions that may need manual review):\n" + strings.Join(msgs, "\n"), nil
		}
	}
	return "write succeeded", nil
}
