package tools

import (
	"encoding/json"
	"fmt"
	"os"
)

type Read struct{}

func (r *Read) Name() string {
	return "read"
}

func (r *Read) Description() string {
	return "Read the contents of a file at the given path."
}

func (r *Read) Parameters() []Parameter {
	return []Parameter{
		{Name: "file_path", Type: "string", Description: "The absolute path to the file to read", Required: true},
	}
}

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
