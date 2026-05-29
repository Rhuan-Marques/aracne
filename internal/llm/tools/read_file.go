package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type ReadFile struct{}

func (r *ReadFile) Name() string {
	return "read_file"
}

func (r *ReadFile) Description() string {
	return "Read a file from the local filesystem and return its full contents"
}

func (r *ReadFile) Parameters() []Parameter {
	return []Parameter{
		{Name: "file_path", Type: "string", Description: "The absolute path to the file to read", Required: true},
	}
}

func (r *ReadFile) Run(args json.RawMessage) (string, error) {
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

	name := filepath.Base(params.FilePath)
	return fmt.Sprintf("%s\n%s", name, string(data)), nil
}
