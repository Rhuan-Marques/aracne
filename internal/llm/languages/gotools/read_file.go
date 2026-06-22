package gotools

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"aracne/internal/topology/golang"
)

// Tool that reads Go files with interconnected topology context by name or path.
type ReadFile struct {
	mgr *golang.GoManager
}

// Creates a new ReadFile instance for reading file contents from the Go topology.
func NewReadFile(mgr *golang.GoManager) *ReadFile {
	return &ReadFile{mgr: mgr}
}

// Returns the tool name "read_file".
func (r *ReadFile) Name() string {
	return "read_file"
}

// Returns the tool description for reading Go files with their interconnected topology context.
func (r *ReadFile) Description() string {
	return "Read a Go file's full source code and its interconnected context (package, functions, structs, interfaces, imports) from the project topology."
}

// Returns the required parameters for ReadFile: a file name/path string.
func (r *ReadFile) Parameters() []Parameter {
	return []Parameter{
		{Name: "name", Type: "string", Description: "The file path or name (e.g. 'manager.go', 'internal/cli/init.go')", Required: true},
	}
}

// Executes a file read by name, returning formatted Go file context or searching topology if not found directly.
func (r *ReadFile) Run(args json.RawMessage) (string, error) {
	var params struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if params.Name == "" {
		return "", fmt.Errorf("missing required argument: name")
	}

	ctx, err := r.mgr.ReadFile(params.Name)
	if err == nil {
		return FormatGoFileContext(ctx), nil
	}

	ids, err := r.mgr.FindFilesByName(params.Name)
	if err != nil {
		return "", fmt.Errorf("search files: %w", err)
	}
	if len(ids) == 0 {
		return "", fmt.Errorf("file %q not found in topology", params.Name)
	}
	if len(ids) > 1 {
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		var b strings.Builder
		b.WriteString(fmt.Sprintf("Multiple files matching %q found:\n", params.Name))
		for _, id := range ids {
			b.WriteString(fmt.Sprintf("- %s\n", id))
		}
		return b.String(), nil
	}

	ctx, err = r.mgr.ReadFile(string(ids[0]))
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}

	return FormatGoFileContext(ctx), nil
}
