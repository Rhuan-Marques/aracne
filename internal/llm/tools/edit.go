package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"aracne/internal/topology"
	"aracne/internal/topology/scanner"
)

// Tool implementation for the "edit" command. Wraps a TopologyManager and scanner Registry to perform file edits and auto-update the topology database in response.
type Edit struct {
	mgr *topology.TopologyManager
	reg *scanner.Registry
}

// Creates a new Edit tool instance with the given topology manager and scanner registry. Returns a pointer to the initialized Edit struct.
func NewEdit(mgr *topology.TopologyManager, reg *scanner.Registry) *Edit {
	return &Edit{mgr: mgr, reg: reg}
}

// Returns the tool name "edit" used to register the Edit tool in the MCP tool registry.
func (e *Edit) Name() string {
	return "edit"
}

// Returns the description string for the Edit tool, explaining it replaces exact text in a file with automatic topology updates.
func (e *Edit) Description() string {
	return "Edit a file by replacing exact text with new text. Provide the file path, the exact string to find, and the replacement. The project topology is automatically updated."
}

// Returns the parameter schema for the Edit tool, defining file_path, old_string, and new_string as required string parameters.
func (e *Edit) Parameters() []Parameter {
	return []Parameter{
		{Name: "file_path", Type: "string", Description: "The absolute path to the file to edit", Required: true},
		{Name: "old_string", Type: "string", Description: "The exact text to search for and replace", Required: true},
		{Name: "new_string", Type: "string", Description: "The replacement text", Required: true},
	}
}

// Executes the edit tool: reads a file, replaces the first occurrence of old_string with new_string, writes it back, and if a topology manager is available, triggers an update-file to refresh the topology with any resulting warnings.
func (e *Edit) Run(args json.RawMessage) (string, error) {
	var params struct {
		FilePath  string `json:"file_path"`
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	if params.FilePath == "" || params.OldString == "" {
		return "", fmt.Errorf("missing required arguments: file_path, old_string")
	}

	// Serialize edits to the same file so parallel agents cannot lose each
	// other's changes. When mgr is nil (no topology) there is nothing to lock
	// or update, so apply directly.
	if e.mgr == nil {
		return e.apply(params.FilePath, params.OldString, params.NewString, false)
	}
	return e.mgr.WithFileLock(params.FilePath, func(waited bool) (string, error) {
		return e.apply(params.FilePath, params.OldString, params.NewString, waited)
	})
}

// apply performs the read-modify-write and topology update. waited reports
// whether this edit had to queue behind another agent's edit of the same file;
// if so and the old_string no longer matches, that other agent almost certainly
// changed the file, so the error tells this agent to re-read and retry.
func (e *Edit) apply(filePath, oldString, newString string, waited bool) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	content := string(data)
	if !strings.Contains(content, oldString) {
		normalizedContent := strings.ReplaceAll(content, "\r\n", "\n")
		normalizedOld := strings.ReplaceAll(oldString, "\r\n", "\n")
		if !strings.Contains(normalizedContent, normalizedOld) {
			if waited {
				return "", fmt.Errorf("old_string not found in %s — another agent changed this file while your edit was queued; re-read the resource and retry with the current text", filePath)
			}
			return "", fmt.Errorf("old_string not found in %s", filePath)
		}
		oldString = normalizedOld
		content = normalizedContent
	}

	newContent := strings.Replace(content, oldString, newString, 1)
	if err := os.WriteFile(filePath, []byte(newContent), 0644); err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}

	if e.mgr != nil {
		warnings, err := e.mgr.UpdateFile(filePath, e.reg)
		if err != nil {
			return "", fmt.Errorf("update topology: %w", err)
		}
		if len(warnings) > 0 {
			var msgs []string
			for _, w := range warnings {
				msgs = append(msgs, fmt.Sprintf("  - [%s] %s (source: %s, target: %s)", w.Kind, w.Message, w.SourceID, w.TargetID))
			}
			return "edit succeeded\n\nTopology warnings (functions that may need manual review):\n" + strings.Join(msgs, "\n"), nil
		}
	}
	return "edit succeeded", nil
}
