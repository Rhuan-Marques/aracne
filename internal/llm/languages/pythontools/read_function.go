package pythontools

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"ltp/internal/topology/python"
)

type ReadFunction struct {
	mgr *python.PythonManager
}

func NewReadFunction(mgr *python.PythonManager) *ReadFunction {
	return &ReadFunction{mgr: mgr}
}

func (r *ReadFunction) Name() string {
	return "read_function"
}

func (r *ReadFunction) Description() string {
	return "Read a Python function's full source code and its interconnected context (called functions, classes, external variables) from the project topology. Prefer this over 'read' when investigating a specific function."
}

func (r *ReadFunction) Parameters() []Parameter {
	return []Parameter{
		{Name: "name", Type: "string", Description: "The function name (e.g. 'parse_file', '__init__', 'main')", Required: true},
	}
}

func (r *ReadFunction) Run(args json.RawMessage) (string, error) {
	var params struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if params.Name == "" {
		return "", fmt.Errorf("missing required argument: name")
	}

	ctx, err := r.mgr.ReadFunction(params.Name)
	if err == nil {
		return FormatPythonFunctionContext(ctx), nil
	}

	ids, err := r.mgr.FindFunctionsByName(params.Name)
	if err != nil {
		return "", fmt.Errorf("search functions: %w", err)
	}
	if len(ids) == 0 {
		return "", fmt.Errorf("function %q not found in topology", params.Name)
	}
	if len(ids) > 1 {
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		var b strings.Builder
		b.WriteString(fmt.Sprintf("Multiple functions named %q found:\n", params.Name))
		for _, id := range ids {
			b.WriteString(fmt.Sprintf("- %s\n", id))
		}
		return b.String(), nil
	}

	ctx, err = r.mgr.ReadFunction(string(ids[0]))
	if err != nil {
		return "", fmt.Errorf("read function: %w", err)
	}

	return FormatPythonFunctionContext(ctx), nil
}
