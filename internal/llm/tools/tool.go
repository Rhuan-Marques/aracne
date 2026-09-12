package tools

import (
	"encoding/json"
	"sort"
)

// Describes a single parameter for an LLM tool, including its name, type, description, and whether it is required.
type Parameter struct {
	Name        string
	Type        string
	Description string
	Required    bool
	// Items is the element type when Type is "array". JSON Schema requires it, and a client
	// that validates the schema rejects an array property without one.
	Items string
}

// Defines the contract for an LLM-callable tool. Requires Name, Description, Parameters, and Run methods — Run receives JSON arguments and returns a string result or error.
type Tool interface {
	Name() string
	Description() string
	Parameters() []Parameter
	Run(args json.RawMessage) (string, error)
}

// Registry of named Tool instances. Maintains a map of tool name to Tool, providing lookup and iteration capabilities for tool discovery in the MCP server and agent modes.
type Registry struct {
	tools map[string]Tool
}

// Creates and returns a new Tool Registry with an empty tools map. Used to register and look up tools by name for MCP and agent execution.
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

// Registers a Tool in the registry by its name, making it available for lookup and invocation.
func (r *Registry) Register(t Tool) {
	r.tools[t.Name()] = t
}

// Looks up a tool by name in the registry. Takes a tool name string. Returns the Tool interface and a boolean indicating whether the tool was found.
func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// Returns a slice of all registered Tool instances from the registry, sorted by name.
//
// Sorted because the map iterates in random order, and this list becomes the tool schemas an
// MCP client (and the agent loop) sends at the head of every request: a different order on
// every tools/list is a different prefix every time, which defeats prompt caching.
func (r *Registry) List() []Tool {
	result := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		result = append(result, t)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name() < result[j].Name() })
	return result
}
