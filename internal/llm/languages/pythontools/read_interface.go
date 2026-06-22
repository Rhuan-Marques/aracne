package pythontools

import (
	"encoding/json"
	"fmt"
	"strings"

	"aracne/internal/topology/python"
)

// LLM tool handler that reads Python class interface definitions from topology, including type (ABC/Protocol) and base classes.
type ReadInterface struct {
	mgr *python.PythonManager
}

// Creates a ReadInterface handler for reading Python interface definitions.
func NewReadInterface(mgr *python.PythonManager) *ReadInterface {
	return &ReadInterface{mgr: mgr}
}

// Returns the tool name "read_interface".
func (r *ReadInterface) Name() string {
	return "read_interface"
}

// Returns a description of the read_interface tool for accessing Python class interface information.
func (r *ReadInterface) Description() string {
	return "Read a Python class's interface information (ABCs, Protocols, base classes, and methods that need implementing) from the project topology."
}

// Returns parameter schema with required class name argument for ReadInterface tool.
func (r *ReadInterface) Parameters() []Parameter {
	return []Parameter{
		{Name: "name", Type: "string", Description: "The class name (e.g. 'MyAbstractClass')", Required: true},
	}
}

// Looks up a Python class by name in topology and returns its definition, type (ABC/Protocol), location, and base classes.
func (r *ReadInterface) Run(args json.RawMessage) (string, error) {
	var params struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if params.Name == "" {
		return "", fmt.Errorf("missing required argument: name")
	}

	topo, err := r.mgr.Generic().ReadAll()
	if err != nil {
		return "", fmt.Errorf("read topology: %w", err)
	}
	gt := python.FromGeneric(topo)

	var candidates []python.ClassID
	for id, c := range gt.Classes {
		if c.Name == params.Name {
			candidates = append(candidates, id)
		}
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("class %q not found in topology", params.Name)
	}
	if len(candidates) > 1 {
		var b strings.Builder
		b.WriteString(fmt.Sprintf("Multiple classes named %q found:\n", params.Name))
		for _, id := range candidates {
			b.WriteString(fmt.Sprintf("- %s\n", id))
		}
		return b.String(), nil
	}

	c := gt.Classes[candidates[0]]
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Class: %s\n", c.Name))
	b.WriteString(fmt.Sprintf("Description: %s\n", c.Description))
	if c.IsABC {
		b.WriteString("Type: Abstract Base Class (ABC)\n")
	}
	if c.IsProtocol {
		b.WriteString("Type: Protocol\n")
	}
	b.WriteString(fmt.Sprintf("Location: %s:%d\n", c.Loc.Path, c.Loc.StartsAt))
	if len(c.Bases) > 0 {
		b.WriteString(fmt.Sprintf("Base classes: %s\n", strings.Join(c.Bases, ", ")))
	}
	return b.String(), nil
}
