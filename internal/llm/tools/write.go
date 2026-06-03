package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"ltp/internal/topology"
	"ltp/internal/topology/scanner"
)

type Write struct {
	mgr *topology.TopologyManager
	reg *scanner.Registry
}

func NewWrite(mgr *topology.TopologyManager, reg *scanner.Registry) *Write {
	return &Write{mgr: mgr, reg: reg}
}

func (w *Write) Name() string {
	return "write"
}

func (w *Write) Description() string {
	return "Write a new file, creating parent directories if needed. Provide the file path and the content. The project topology is automatically updated. Overwrites file if it already existed"
}

func (w *Write) Parameters() []Parameter {
	return []Parameter{
		{Name: "file_path", Type: "string", Description: "The absolute path to the file to write", Required: true},
		{Name: "content", Type: "string", Description: "The content to write to the file", Required: true},
	}
}

func (w *Write) Run(args json.RawMessage) (string, error) {
	var params struct {
		FilePath string `json:"file_path"`
		Content  string `json:"content"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	if params.FilePath == "" || params.Content == "" {
		return "", fmt.Errorf("missing required arguments: file_path, content")
	}

	dir := filepath.Dir(params.FilePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("create directories: %w", err)
	}

	if err := os.WriteFile(params.FilePath, []byte(params.Content), 0644); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}

	if w.mgr != nil {
		warnings, err := w.mgr.UpdateFile(params.FilePath, w.reg)
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
