package pythontools

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"aracne/internal/topology/python"
)

// LLM tool handler that reads Python classes from topology by name with source code and connected context, supporting exact and fuzzy lookup.
type ReadStruct struct {
	mgr *python.PythonManager
}

// Creates a ReadStruct handler for reading Python classes from the topology.
func NewReadStruct(mgr *python.PythonManager) *ReadStruct {
	return &ReadStruct{mgr: mgr}
}

// Returns the tool name "read_struct" for Python class inspection.
func (r *ReadStruct) Name() string {
	return "read_struct"
}

// Returns the tool description explaining read_struct reads Python classes with their source code and connected context.
func (r *ReadStruct) Description() string {
	return "Read a Python class's full source code and its interconnected context (base classes, methods, constructor, used types) from the project topology. Prefer this over 'read' when investigating a specific class."
}

// Returns parameter schema with required class name argument for ReadStruct tool.
func (r *ReadStruct) Parameters() []Parameter {
	return []Parameter{
		{Name: "name", Type: "string", Description: "The class name (e.g. 'PythonManager', 'TopologyManager', 'MyClass')", Required: true},
	}
}

// Retrieves and formats a Python class from the topology by name, supporting exact lookup or fuzzy search when needed
func (r *ReadStruct) Run(args json.RawMessage) (string, error) {
	var params struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if params.Name == "" {
		return "", fmt.Errorf("missing required argument: name")
	}

	ctx, err := r.mgr.ReadClass(params.Name)
	if err == nil {
		return FormatPythonClassContext(ctx), nil
	}

	ids, err := r.mgr.FindClassesByName(params.Name)
	if err != nil {
		return "", fmt.Errorf("search classes: %w", err)
	}
	if len(ids) == 0 {
		return "", fmt.Errorf("class %q not found in topology", params.Name)
	}
	if len(ids) > 1 {
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		var b strings.Builder
		b.WriteString(fmt.Sprintf("Multiple classes named %q found:\n", params.Name))
		for _, id := range ids {
			b.WriteString(fmt.Sprintf("- %s\n", id))
		}
		return b.String(), nil
	}

	ctx, err = r.mgr.ReadClass(string(ids[0]))
	if err != nil {
		return "", fmt.Errorf("read class: %w", err)
	}

	return FormatPythonClassContext(ctx), nil
}
