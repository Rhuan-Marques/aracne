package tools

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"llm-topology/internal/topology"
	"llm-topology/internal/topology/domain"
)

type GenerateDescriptions struct {
	mgr *topology.TopologyManager
}

func NewGenerateDescriptions(mgr *topology.TopologyManager) *GenerateDescriptions {
	return &GenerateDescriptions{mgr: mgr}
}

func (g *GenerateDescriptions) Name() string {
	return "generate_descriptions"
}

func (g *GenerateDescriptions) Description() string {
	return "List all resources (functions, structs, interfaces, external variables, files, packages) that need descriptions. Use this to discover which resources lack documentation, then dispatch 'descriptor' sub-agents for each one to generate descriptions."
}

func (g *GenerateDescriptions) Parameters() []Parameter {
	return []Parameter{}
}

func (g *GenerateDescriptions) Run(args json.RawMessage) (string, error) {
	topo, err := g.mgr.ReadAll(topology.WithHasDescription(false))
	if err != nil {
		return "", fmt.Errorf("read topology: %w", err)
	}

	var b strings.Builder
	b.WriteString("The following resources need descriptions:\n\n")

	if topo.Functions != nil && len(topo.Functions) > 0 {
		b.WriteString("## Functions\n")
		ids := sortedFunctionIDs(topo.Functions)
		for _, id := range ids {
			fn := topo.Functions[id]
			b.WriteString(fmt.Sprintf("- %s (ID: %s, File: %s)\n", fn.Name, id, fn.Loc.Path))
		}
		b.WriteString("\n")
	}

	if topo.Struct != nil && len(topo.Struct) > 0 {
		b.WriteString("## Structs\n")
		ids := sortedStructIDs(topo.Struct)
		for _, id := range ids {
			s := topo.Struct[id]
			b.WriteString(fmt.Sprintf("- %s (ID: %s, File: %s)\n", s.Name, id, s.Loc.Path))
		}
		b.WriteString("\n")
	}

	if topo.Interfaces != nil && len(topo.Interfaces) > 0 {
		b.WriteString("## Interfaces\n")
		ids := sortedInterfaceIDs(topo.Interfaces)
		for _, id := range ids {
			iface := topo.Interfaces[id]
			b.WriteString(fmt.Sprintf("- %s (ID: %s, File: %s)\n", iface.Name, id, iface.Loc.Path))
		}
		b.WriteString("\n")
	}

	if topo.ExternalVars != nil && len(topo.ExternalVars) > 0 {
		b.WriteString("## External Variables\n")
		ids := sortedExtVarIDs(topo.ExternalVars)
		for _, id := range ids {
			v := topo.ExternalVars[id]
			b.WriteString(fmt.Sprintf("- %s (ID: %s, File: %s)\n", v.Name, id, v.Location.Path))
		}
		b.WriteString("\n")
	}

	if topo.Files != nil && len(topo.Files) > 0 {
		b.WriteString("## Files\n")
		ids := sortedFileIDs(topo.Files)
		for _, id := range ids {
			f := topo.Files[id]
			b.WriteString(fmt.Sprintf("- %s (ID: %s)\n", f.Name, id))
		}
		b.WriteString("\n")
	}

	if topo.Packages != nil && len(topo.Packages) > 0 {
		b.WriteString("## Packages\n")
		ids := sortedPackageIDs(topo.Packages)
		for _, id := range ids {
			b.WriteString(fmt.Sprintf("- %s (ID: %s)\n", id, id))
		}
		b.WriteString("\n")
	}

	b.WriteString("For each resource, dispatch a 'descriptor' sub-agent with these tools:\n")
	b.WriteString("- read_resource_and_cut — get the resource's source code and description instructions\n")
	b.WriteString("- update_description — save the generated description\n\n")
	b.WriteString("Each sub-agent will receive one resource ID and type, generate a concise description, and save it.\n")

	return b.String(), nil
}

func sortedFunctionIDs(m map[domain.FunctionID]domain.Function) []domain.FunctionID {
	ids := make([]domain.FunctionID, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func sortedStructIDs(m map[domain.StructID]domain.Struct) []domain.StructID {
	ids := make([]domain.StructID, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func sortedInterfaceIDs(m map[domain.InterfaceID]domain.Interface) []domain.InterfaceID {
	ids := make([]domain.InterfaceID, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func sortedExtVarIDs(m map[domain.ExternalVarID]domain.ExternalVar) []domain.ExternalVarID {
	ids := make([]domain.ExternalVarID, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func sortedFileIDs(m map[domain.FileID]domain.File) []domain.FileID {
	ids := make([]domain.FileID, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func sortedPackageIDs(m map[domain.PackagePath]domain.Package) []domain.PackagePath {
	ids := make([]domain.PackagePath, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
