package pythontools

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"aracne/internal/topology/python"
)

type ReadFile struct {
	mgr *python.PythonManager
}

func NewReadFile(mgr *python.PythonManager) *ReadFile {
	return &ReadFile{mgr: mgr}
}

func (r *ReadFile) Name() string {
	return "read_file"
}

func (r *ReadFile) Description() string {
	return "Read a Python module's full source code and its interconnected context (package, functions, classes, imports) from the project topology."
}

func (r *ReadFile) Parameters() []Parameter {
	return []Parameter{
		{Name: "name", Type: "string", Description: "The module path (e.g. 'main.py', 'package/module.py')", Required: true},
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

	ctx, err := r.mgr.ReadModule(params.Name)
	if err == nil {
		return FormatPythonModuleContext(ctx), nil
	}

	topo, err := r.mgr.Generic().ReadAll()
	if err != nil {
		return "", fmt.Errorf("read topology: %w", err)
	}
	gt := python.FromGeneric(topo)

	var results []string
	for id, mod := range gt.Modules {
		if mod.Name == params.Name || string(id) == params.Name {
			results = append(results, string(id))
		}
	}
	if len(results) == 0 {
		return "", fmt.Errorf("module %q not found in topology", params.Name)
	}
	if len(results) > 1 {
		sort.Strings(results)
		var b strings.Builder
		b.WriteString(fmt.Sprintf("Multiple modules matching %q found:\n", params.Name))
		for _, id := range results {
			b.WriteString(fmt.Sprintf("- %s\n", id))
		}
		return b.String(), nil
	}

	ctx, err = r.mgr.ReadModule(results[0])
	if err != nil {
		return "", fmt.Errorf("read module: %w", err)
	}

	return FormatPythonModuleContext(ctx), nil
}
