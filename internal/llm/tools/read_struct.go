package tools

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"llm-topology/internal/topology"
)

type ReadStruct struct {
	mgr *topology.TopologyManager
}

func NewReadStruct(mgr *topology.TopologyManager) *ReadStruct {
	return &ReadStruct{mgr: mgr}
}

func (r *ReadStruct) Name() string {
	return "read_struct"
}

func (r *ReadStruct) Description() string {
	return "Read a struct's full source code and its interconnected context (interfaces, methods, constructor, used types) from the project topology. Prefer this over 'read' when investigating a specific struct."
}

func (r *ReadStruct) Parameters() []Parameter {
	return []Parameter{
		{Name: "name", Type: "string", Description: "The struct name (e.g. 'TopologyManager', 'ReadFunction')", Required: true},
	}
}

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

	ctx, err := r.mgr.ReadStruct(params.Name)
	if err == nil {
		return formatStructContext(ctx), nil
	}

	ids, err := r.mgr.FindStructsByName(params.Name)
	if err != nil {
		return "", fmt.Errorf("search structs: %w", err)
	}
	if len(ids) == 0 {
		return "", fmt.Errorf("struct %q not found in topology", params.Name)
	}
	if len(ids) > 1 {
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		var b strings.Builder
		b.WriteString(fmt.Sprintf("Multiple structs named %q found:\n", params.Name))
		for _, id := range ids {
			b.WriteString(fmt.Sprintf("- %s\n", id))
		}
		return b.String(), nil
	}

	ctx, err = r.mgr.ReadStruct(string(ids[0]))
	if err != nil {
		return "", fmt.Errorf("read struct: %w", err)
	}

	return formatStructContext(ctx), nil
}
