package gotools

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"ltp/internal/topology/golang"
)

type ReadInterface struct {
	mgr *golang.GoManager
}

func NewReadInterface(mgr *golang.GoManager) *ReadInterface {
	return &ReadInterface{mgr: mgr}
}

func (r *ReadInterface) Name() string {
	return "read_interface"
}

func (r *ReadInterface) Description() string {
	return "Read a Go interface's full source code and its interconnected context (implementing structs, methods, dependencies) from the project topology."
}

func (r *ReadInterface) Parameters() []Parameter {
	return []Parameter{
		{Name: "name", Type: "string", Description: "The interface name (e.g. 'Reader', 'Writer')", Required: true},
	}
}

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

	ctx, err := r.mgr.ReadInterface(params.Name)
	if err == nil {
		return FormatGoInterfaceContext(ctx), nil
	}

	ids, err := r.mgr.FindInterfacesByName(params.Name)
	if err != nil {
		return "", fmt.Errorf("search interfaces: %w", err)
	}
	if len(ids) == 0 {
		return "", fmt.Errorf("interface %q not found in topology", params.Name)
	}
	if len(ids) > 1 {
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		var b strings.Builder
		b.WriteString(fmt.Sprintf("Multiple interfaces named %q found:\n", params.Name))
		for _, id := range ids {
			b.WriteString(fmt.Sprintf("- %s\n", id))
		}
		return b.String(), nil
	}

	ctx, err = r.mgr.ReadInterface(string(ids[0]))
	if err != nil {
		return "", fmt.Errorf("read interface: %w", err)
	}

	return FormatGoInterfaceContext(ctx), nil
}
