package mermaid

import (
	"fmt"
	"os"
	"strings"

	"llm-topology/internal/topology/domain"
)

type gen struct {
	topo    *domain.Topology
	filter  map[domain.ResourceName]bool
	nodeID  map[string]string
	counter int
	b       strings.Builder
}

func (g *gen) nextID(prefix string) string {
	g.counter++
	return fmt.Sprintf("%s%d", prefix, g.counter)
}

var resourceColor = map[domain.ResourceName]string{
	domain.PACKAGE_RESOURCE:    "#1565C0",
	domain.FILE_RESOURCE:       "#2E7D32",
	domain.FUNCTION_RESOURCE:   "#E65100",
	domain.STRUCT_RESOURCE:     "#6A1B9A",
	domain.INTERFACE_RESOURCE:  "#AD1457",
	domain.EXTERNAL_VAR_RESOURCE: "#424242",
	domain.DEPENDENCY_RESOURCE: "#F9A825",
}

var resourceFill = map[domain.ResourceName]string{
	domain.PACKAGE_RESOURCE:    "#E3F2FD",
	domain.FILE_RESOURCE:       "#E8F5E9",
	domain.FUNCTION_RESOURCE:   "#FFF3E0",
	domain.STRUCT_RESOURCE:     "#F3E5F5",
	domain.INTERFACE_RESOURCE:  "#FCE4EC",
	domain.EXTERNAL_VAR_RESOURCE: "#F5F5F5",
	domain.DEPENDENCY_RESOURCE: "#FFFDE7",
}

func (g *gen) resourceKey(rtype domain.ResourceName, id string) string {
	return string(rtype) + ":" + id
}

func safeID(s string) string {
	result := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-' {
			result = append(result, c)
		} else {
			result = append(result, '_')
		}
	}
	if len(result) > 40 {
		result = result[:40]
	}
	if len(result) == 0 {
		return "x"
	}
	return string(result)
}

func quote(s string) string {
	return "\"" + strings.ReplaceAll(s, "\"", "'") + "\""
}

func shortDesc(desc string) string {
	if desc == "" {
		return ""
	}
	max := 60
	runes := []rune(desc)
	if len(runes) > max {
		return string(runes[:max]) + "..."
	}
	return desc
}

func nodeLabel(name, desc string) string {
	if desc == "" {
		return fmt.Sprintf("%s", name)
	}
	return fmt.Sprintf("%s: %s", name, shortDesc(desc))
}

func hasAny(m map[domain.ResourceName]bool, rs ...domain.ResourceName) bool {
	if len(m) == 0 {
		return true
	}
	for _, r := range rs {
		if m[r] {
			return true
		}
	}
	return false
}

func Generate(topo *domain.Topology, resourceFilter ...domain.ResourceName) string {
	g := &gen{
		topo:   topo,
		filter: make(map[domain.ResourceName]bool),
		nodeID: make(map[string]string),
	}

	if len(resourceFilter) == 0 {
		for _, r := range []domain.ResourceName{
			domain.PACKAGE_RESOURCE, domain.FILE_RESOURCE,
			domain.FUNCTION_RESOURCE, domain.STRUCT_RESOURCE,
			domain.INTERFACE_RESOURCE, domain.EXTERNAL_VAR_RESOURCE,
			domain.DEPENDENCY_RESOURCE,
		} {
			g.filter[r] = true
		}
	} else {
		for _, r := range resourceFilter {
			g.filter[r] = true
		}
	}

	g.b.WriteString("flowchart TD\n")

	// Class definitions
	for _, r := range []domain.ResourceName{
		domain.PACKAGE_RESOURCE, domain.FILE_RESOURCE,
		domain.FUNCTION_RESOURCE, domain.STRUCT_RESOURCE,
		domain.INTERFACE_RESOURCE, domain.EXTERNAL_VAR_RESOURCE,
		domain.DEPENDENCY_RESOURCE,
	} {
		if g.filter[r] {
			fill := resourceFill[r]
			stroke := resourceColor[r]
			g.b.WriteString(fmt.Sprintf("    classDef %s fill:%s,stroke:%s,stroke-width:2px;\n", r, fill, stroke))
		}
	}

	// ── Build subgraph hierarchy: Package > File > elements ──
	//
	// For each package, create a subgraph containing its files.
	// Within each file subgraph, place nodes for the file's elements
	// (functions, structs, interfaces, external vars).

	var pkgNames []domain.PackagePath
	// Sort by path for deterministic output
	for p := range topo.Packages {
		pkgNames = append(pkgNames, p)
	}
	// Simple string sort
	for i := 0; i < len(pkgNames); i++ {
		for j := i + 1; j < len(pkgNames); j++ {
			if string(pkgNames[i]) > string(pkgNames[j]) {
				pkgNames[i], pkgNames[j] = pkgNames[j], pkgNames[i]
			}
		}
	}

	for _, pkgPath := range pkgNames {
		pkg := topo.Packages[pkgPath]

		showPkg := g.filter[domain.PACKAGE_RESOURCE]

		hasVisible := false
		for _, fileID := range pkg.Files {
			if f, ok := topo.Files[fileID]; ok {
				if len(g.visibleFileChildren(f)) > 0 {
					hasVisible = true
					break
				}
			}
		}
		if string(pkgPath) == "" {
			continue
		}
		if !hasVisible && !showPkg {
			continue
		}

		if showPkg {
			nid := g.nextID("pkg")
			g.nodeID[g.resourceKey(domain.PACKAGE_RESOURCE, string(pkgPath))] = nid
			g.b.WriteString(fmt.Sprintf("    subgraph %s[%s]\n", nid, quote(string(pkgPath))))
		}

		// Files inside package
		for _, fileID := range pkg.Files {
			f, ok := topo.Files[fileID]
			if !ok {
				continue
			}
			g.buildFile(fileID, f)
		}

		if showPkg {
			g.b.WriteString("    end\n")
		}
	}

	// ── Top-level elements not owned by any package ──
	// (unlikely but handle gracefully)

	// ── Arrow relationships ──
	g.writeArrows()

	return g.b.String()
}

func GenerateToFile(path string, topo *domain.Topology, resourceFilter ...domain.ResourceName) error {
	out := Generate(topo, resourceFilter...)
	return os.WriteFile(path, []byte(out), 0666)
}

func (g *gen) visibleFileChildren(f domain.File) []string {
	var ids []string
	if g.filter[domain.FUNCTION_RESOURCE] {
		for _, fnID := range f.Functions {
			if _, ok := g.topo.Functions[fnID]; ok {
				ids = append(ids, string(fnID))
			}
		}
	}
	if g.filter[domain.STRUCT_RESOURCE] {
		for _, sID := range f.Structs {
			if _, ok := g.topo.Struct[sID]; ok {
				ids = append(ids, string(sID))
			}
		}
	}
	if g.filter[domain.INTERFACE_RESOURCE] {
		for _, iID := range f.Interfaces {
			if _, ok := g.topo.Interfaces[iID]; ok {
				ids = append(ids, string(iID))
			}
		}
	}
	if g.filter[domain.EXTERNAL_VAR_RESOURCE] {
		for _, evID := range f.ExternalVars {
			if _, ok := g.topo.ExternalVars[evID]; ok {
				ids = append(ids, string(evID))
			}
		}
	}
	return ids
}

func (g *gen) buildFile(fileID domain.FileID, f domain.File) {
	showFile := g.filter[domain.FILE_RESOURCE]

	if !showFile && len(g.visibleFileChildren(f)) == 0 {
		return
	}

	fileLabel := f.Name
	if fileLabel == "" {
		fileLabel = string(fileID)
	}

	var fid string
	if showFile {
		fid = g.nextID("f")
		g.nodeID[g.resourceKey(domain.FILE_RESOURCE, string(fileID))] = fid
		g.b.WriteString(fmt.Sprintf("    subgraph %s[%s]\n", fid, quote(fileLabel)))
	}

	g.renderFileElements(f)

	if showFile {
		g.b.WriteString("    end\n")
	}
}

func (g *gen) renderFileElements(f domain.File) {
	if g.filter[domain.FUNCTION_RESOURCE] {
		for _, fnID := range f.Functions {
			fn, ok := g.topo.Functions[fnID]
			if !ok {
				continue
			}
			nid := g.nextID("fn")
			g.nodeID[g.resourceKey(domain.FUNCTION_RESOURCE, string(fnID))] = nid
			g.b.WriteString(fmt.Sprintf("    %s[%s]:::%s\n", nid, quote(nodeLabel(fn.Name, fn.Description)), domain.FUNCTION_RESOURCE))
		}
	}

	if g.filter[domain.STRUCT_RESOURCE] {
		for _, sID := range f.Structs {
			s, ok := g.topo.Struct[sID]
			if !ok {
				continue
			}
			nid := g.nextID("s")
			g.nodeID[g.resourceKey(domain.STRUCT_RESOURCE, string(sID))] = nid
			g.b.WriteString(fmt.Sprintf("    %s[%s]:::%s\n", nid, quote(nodeLabel(s.Name, s.Description)), domain.STRUCT_RESOURCE))
		}
	}

	if g.filter[domain.INTERFACE_RESOURCE] {
		for _, iID := range f.Interfaces {
			iface, ok := g.topo.Interfaces[iID]
			if !ok {
				continue
			}
			nid := g.nextID("i")
			g.nodeID[g.resourceKey(domain.INTERFACE_RESOURCE, string(iID))] = nid
			g.b.WriteString(fmt.Sprintf("    %s[%s]:::%s\n", nid, quote(nodeLabel(iface.Name, iface.Description)), domain.INTERFACE_RESOURCE))
		}
	}

	if g.filter[domain.EXTERNAL_VAR_RESOURCE] {
		for _, evID := range f.ExternalVars {
			ev, ok := g.topo.ExternalVars[evID]
			if !ok {
				continue
			}
			nid := g.nextID("ev")
			g.nodeID[g.resourceKey(domain.EXTERNAL_VAR_RESOURCE, string(evID))] = nid
			g.b.WriteString(fmt.Sprintf("    %s[%s]:::%s\n", nid, quote(nodeLabel(ev.Name, ev.Description)), domain.EXTERNAL_VAR_RESOURCE))
		}
	}
}

func (g *gen) writeArrows() {
	// File → Package imports (from file_imports_pkg)
	if g.filter[domain.FILE_RESOURCE] && g.filter[domain.PACKAGE_RESOURCE] {
		for fileID, f := range g.topo.Files {
			srcID, ok := g.nodeID[g.resourceKey(domain.FILE_RESOURCE, string(fileID))]
			if !ok {
				continue
			}
			for _, pkg := range f.PackagesImported {
				dstID, ok := g.nodeID[g.resourceKey(domain.PACKAGE_RESOURCE, string(pkg))]
				if !ok {
					continue
				}
				g.b.WriteString(fmt.Sprintf("    %s -->|\"Imports\"| %s\n", srcID, dstID))
			}
		}
	}

	// File → Dependency imports (from file_imports_dep)
	if g.filter[domain.FILE_RESOURCE] && g.filter[domain.DEPENDENCY_RESOURCE] {
		for fileID, f := range g.topo.Files {
			srcID, ok := g.nodeID[g.resourceKey(domain.FILE_RESOURCE, string(fileID))]
			if !ok {
				continue
			}
			for _, dep := range f.DependanciesImported {
				depID := g.nextID("dep")
				g.b.WriteString(fmt.Sprintf("    %s[%s]:::%s\n", depID, quote(string(dep.PackagePath)), domain.DEPENDENCY_RESOURCE))
				g.b.WriteString(fmt.Sprintf("    %s -->|\"Imports\"| %s\n", srcID, depID))
			}
		}
	}

	// Function → Function calls
	if g.filter[domain.FUNCTION_RESOURCE] {
		for fnID, fn := range g.topo.Functions {
			srcID, ok := g.nodeID[g.resourceKey(domain.FUNCTION_RESOURCE, string(fnID))]
			if !ok {
				continue
			}
			for _, calledID := range fn.FunctionsUsed {
				dstID, ok := g.nodeID[g.resourceKey(domain.FUNCTION_RESOURCE, string(calledID))]
				if !ok {
					continue
				}
				g.b.WriteString(fmt.Sprintf("    %s -->|\"Calls\"| %s\n", srcID, dstID))
			}
		}
	}

	// Function → Struct uses
	if g.filter[domain.FUNCTION_RESOURCE] && g.filter[domain.STRUCT_RESOURCE] {
		for fnID, fn := range g.topo.Functions {
			srcID, ok := g.nodeID[g.resourceKey(domain.FUNCTION_RESOURCE, string(fnID))]
			if !ok {
				continue
			}
			for _, sID := range fn.StructsUsed {
				dstID, ok := g.nodeID[g.resourceKey(domain.STRUCT_RESOURCE, string(sID))]
				if !ok {
					continue
				}
				g.b.WriteString(fmt.Sprintf("    %s -->|\"Uses\"| %s\n", srcID, dstID))
			}
		}
	}

	// Function → Interface uses
	if g.filter[domain.FUNCTION_RESOURCE] && g.filter[domain.INTERFACE_RESOURCE] {
		for fnID, fn := range g.topo.Functions {
			srcID, ok := g.nodeID[g.resourceKey(domain.FUNCTION_RESOURCE, string(fnID))]
			if !ok {
				continue
			}
			for _, iID := range fn.InterfacesUsed {
				dstID, ok := g.nodeID[g.resourceKey(domain.INTERFACE_RESOURCE, string(iID))]
				if !ok {
					continue
				}
				g.b.WriteString(fmt.Sprintf("    %s -->|\"Uses\"| %s\n", srcID, dstID))
			}
		}
	}

	// Function → ExtVar uses
	if g.filter[domain.FUNCTION_RESOURCE] && g.filter[domain.EXTERNAL_VAR_RESOURCE] {
		for fnID, fn := range g.topo.Functions {
			srcID, ok := g.nodeID[g.resourceKey(domain.FUNCTION_RESOURCE, string(fnID))]
			if !ok {
				continue
			}
			for _, evID := range fn.ExternalVarsUsed {
				dstID, ok := g.nodeID[g.resourceKey(domain.EXTERNAL_VAR_RESOURCE, string(evID))]
				if !ok {
					continue
				}
				g.b.WriteString(fmt.Sprintf("    %s -->|\"Uses\"| %s\n", srcID, dstID))
			}
		}
	}

	// Struct → Interface implements
	if g.filter[domain.STRUCT_RESOURCE] && g.filter[domain.INTERFACE_RESOURCE] {
		for _, iface := range g.topo.Interfaces {
			dstID, ok := g.nodeID[g.resourceKey(domain.INTERFACE_RESOURCE, string(iface.ID))]
			if !ok {
				continue
			}
			for _, sID := range iface.ImplementedBy {
				srcID, ok := g.nodeID[g.resourceKey(domain.STRUCT_RESOURCE, string(sID))]
				if !ok {
					continue
				}
				g.b.WriteString(fmt.Sprintf("    %s -.->|\"Implements\"| %s\n", srcID, dstID))
			}
		}
	}
}
