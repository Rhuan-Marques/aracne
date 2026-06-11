package gotools

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"aracne/internal/topology/golang"
)

type ReadFile struct {
	mgr *golang.GoManager
}

func NewReadFile(mgr *golang.GoManager) *ReadFile {
	return &ReadFile{mgr: mgr}
}

func (r *ReadFile) Name() string {
	return "read_file"
}

func (r *ReadFile) Description() string {
	return "Read a Go file's full source code and its interconnected context (package, functions, structs, interfaces, imports) from the project topology."
}

func (r *ReadFile) Parameters() []Parameter {
	return []Parameter{
		{Name: "name", Type: "string", Description: "The file path or name (e.g. 'manager.go', 'internal/cli/init.go')", Required: true},
	}
}

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
