package tools

import (
	"encoding/json"
	"fmt"
	"os"
)

// MCP/agent tool that reads raw file contents from disk by absolute path.
type Read struct{}

// Returns the tool name string "read" for the Read tool.
func (r *Read) Name() string {
	return "read"
}

// Returns the human-readable description string for the Read tool: "Read the contents of a file at the given path." No parameters, returns string.
func (r *Read) Description() string {
	return "Read the contents of a file at the given path."
}

// Returns the parameter definitions for the read tool, requiring only the file_path argument.
func (r *Read) Parameters() []Parameter {
	return []Parameter{
		{Name: "file_path", Type: "string", Description: "The absolute path to the file to read", Required: true},
	}
}

// Reads a file from disk by its absolute path and returns its raw contents as a string. Returns an error if the file is missing or unreadable.
func (r *Read) Run(args json.RawMessage) (string, error) {
	var params struct {
		FilePath string `json:"file_path"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if params.FilePath == "" {
		return "", fmt.Errorf("missing required argument: file_path")
	}

	data, err := os.ReadFile(params.FilePath)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	return string(data), nil
}
