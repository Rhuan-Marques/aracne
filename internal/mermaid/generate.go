package mermaid

import (
	"fmt"
	"os"
	"strings"

	"llm-topology/internal/topology/domain"
)

type gen struct {
	topo    *domain.Topology
	filter  map[domain.ResourceKind]bool
	nodeID  map[string]string
	counter int
	b       strings.Builder
}

func (g *gen) nextID(prefix string) string {
	g.counter++
	return fmt.Sprintf("%s%d", prefix, g.counter)
}

var resourceColor = map[domain.ResourceKind]string{
	domain.ResourcePackage:    "#1565C0",
	domain.ResourceFile:       "#2E7D32",
	domain.ResourceFunction:   "#E65100",
	domain.ResourceMethod:     "#E65100",
	domain.ResourceType:       "#6A1B9A",
	domain.ResourceInterface:  "#AD1457",
	domain.ResourceVariable:   "#424242",
	domain.ResourceDependency: "#F9A825",
}

var resourceFill = map[domain.ResourceKind]string{
	domain.ResourcePackage:    "#E3F2FD",
	domain.ResourceFile:       "#E8F5E9",
	domain.ResourceFunction:   "#FFF3E0",
	domain.ResourceMethod:     "#FFF3E0",
	domain.ResourceType:       "#F3E5F5",
	domain.ResourceInterface:  "#FCE4EC",
	domain.ResourceVariable:   "#F5F5F5",
	domain.ResourceDependency: "#FFFDE7",
}

var resourceLabels = map[domain.ResourceKind]string{
	domain.ResourcePackage:    "Package",
	domain.ResourceFile:       "File",
	domain.ResourceFunction:   "Function",
	domain.ResourceMethod:     "Method",
	domain.ResourceType:       "Struct",
	domain.ResourceInterface:  "Interface",
	domain.ResourceVariable:   "Var",
	domain.ResourceDependency: "Dep",
}

func (g *gen) resourceKey(kind domain.ResourceKind, id string) string {
	return string(kind) + ":" + id
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
		return name
	}
	return fmt.Sprintf("%s: %s", name, shortDesc(desc))
}

func Generate(topo *domain.Topology, resourceFilter ...domain.ResourceKind) string {
	g := &gen{
		topo:   topo,
		filter: make(map[domain.ResourceKind]bool),
		nodeID: make(map[string]string),
	}

	if len(resourceFilter) == 0 {
		for _, r := range []domain.ResourceKind{
			domain.ResourcePackage, domain.ResourceFile,
			domain.ResourceFunction, domain.ResourceType,
			domain.ResourceInterface, domain.ResourceVariable,
			domain.ResourceDependency,
		} {
			g.filter[r] = true
		}
	} else {
		for _, r := range resourceFilter {
			g.filter[r] = true
		}
	}

	g.b.WriteString("flowchart TD\n")

	for _, r := range []domain.ResourceKind{
		domain.ResourcePackage, domain.ResourceFile,
		domain.ResourceFunction, domain.ResourceType,
		domain.ResourceInterface, domain.ResourceVariable,
		domain.ResourceDependency,
	} {
		if g.filter[r] {
			fill := resourceFill[r]
			stroke := resourceColor[r]
			g.b.WriteString(fmt.Sprintf("    classDef %s fill:%s,stroke:%s,stroke-width:2px;\n", r, fill, stroke))
		}
	}

	pkgIDs := g.byKind(domain.ResourcePackage)
	fileIndex := g.byContaining("has_file")

	for _, pkgID := range g.sortedIDs(pkgIDs) {
		pkg := topo.Resources[pkgID]
		showPkg := g.filter[domain.ResourcePackage]
		showFile := g.filter[domain.ResourceFile]

		hasVisible := g.hasVisibleChildren(fileIndex[pkgID])
		if !hasVisible && !showPkg {
			continue
		}

		if showPkg {
			nid := g.nextID("pkg")
			g.nodeID[g.resourceKey(domain.ResourcePackage, pkgID)] = nid
			g.b.WriteString(fmt.Sprintf("    subgraph %s[%s]\n", nid, quote(pkg.Name)))
		}

		for _, fileID := range g.sortedIDs(fileIndex[pkgID]) {
			g.renderFile(fileID, showFile)
		}

		if showPkg {
			g.b.WriteString("    end\n")
		}
	}

	g.writeArrows()
	return g.b.String()
}

func GenerateToFile(path string, topo *domain.Topology, resourceFilter ...domain.ResourceKind) error {
	out := Generate(topo, resourceFilter...)
	return os.WriteFile(path, []byte(out), 0666)
}

func (g *gen) byKind(kind domain.ResourceKind) []string {
	var ids []string
	for id, res := range g.topo.Resources {
		if res.Kind == kind {
			ids = append(ids, id)
		}
	}
	return ids
}

func (g *gen) byContaining(kind string) map[string][]string {
	result := make(map[string][]string)
	for id, res := range g.topo.Resources {
		if targets, ok := res.Connections[kind]; ok {
			for _, t := range targets {
				result[id] = append(result[id], t)
			}
		}
	}
	return result
}

func (g *gen) sortedIDs(ids []string) []string {
	sorted := make([]string, len(ids))
	copy(sorted, ids)
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[i] > sorted[j] {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	return sorted
}

func (g *gen) hasVisibleChildren(fileIDs []string) bool {
	for _, fid := range fileIDs {
		res, ok := g.topo.Resources[fid]
		if !ok {
			continue
		}
		if g.filter[domain.ResourceFunction] {
			if funcIDs := res.Connections["has_function"]; len(funcIDs) > 0 {
				return true
			}
		}
		if g.filter[domain.ResourceType] {
			if ids := res.Connections["has_struct"]; len(ids) > 0 {
				return true
			}
		}
		if g.filter[domain.ResourceInterface] {
			if ids := res.Connections["has_interface"]; len(ids) > 0 {
				return true
			}
		}
		if g.filter[domain.ResourceVariable] {
			if ids := res.Connections["has_extvar"]; len(ids) > 0 {
				return true
			}
		}
	}
	return false
}

func (g *gen) renderFile(fileID string, showFile bool) {
	res, ok := g.topo.Resources[fileID]
	if !ok {
		return
	}

	if !showFile && !g.hasVisibleChildren([]string{fileID}) {
		return
	}

	fileLabel := res.Name
	if fileLabel == "" {
		fileLabel = fileID
	}

	var fid string
	if showFile {
		fid = g.nextID("f")
		g.nodeID[g.resourceKey(domain.ResourceFile, fileID)] = fid
		g.b.WriteString(fmt.Sprintf("    subgraph %s[%s]\n", fid, quote(fileLabel)))
	}

	g.renderElements(res)

	if showFile {
		g.b.WriteString("    end\n")
	}
}

func (g *gen) renderElements(fileRes domain.Resource) {
	if g.filter[domain.ResourceFunction] {
		for _, fnID := range fileRes.Connections["has_function"] {
			fn, ok := g.topo.Resources[fnID]
			if !ok {
				continue
			}
			nid := g.nextID("fn")
			g.nodeID[g.resourceKey(domain.ResourceFunction, fnID)] = nid
			kind := domain.ResourceFunction
			if fn.Kind == domain.ResourceMethod {
				kind = domain.ResourceMethod
			}
			g.b.WriteString(fmt.Sprintf("    %s[%s]:::%s\n", nid, quote(nodeLabel(fn.Name, fn.Description)), kind))
		}
	}

	if g.filter[domain.ResourceType] {
		for _, sID := range fileRes.Connections["has_struct"] {
			s, ok := g.topo.Resources[sID]
			if !ok {
				continue
			}
			nid := g.nextID("s")
			g.nodeID[g.resourceKey(domain.ResourceType, sID)] = nid
			g.b.WriteString(fmt.Sprintf("    %s[%s]:::%s\n", nid, quote(nodeLabel(s.Name, s.Description)), domain.ResourceType))
		}
	}

	if g.filter[domain.ResourceInterface] {
		for _, iID := range fileRes.Connections["has_interface"] {
			iface, ok := g.topo.Resources[iID]
			if !ok {
				continue
			}
			nid := g.nextID("i")
			g.nodeID[g.resourceKey(domain.ResourceInterface, iID)] = nid
			g.b.WriteString(fmt.Sprintf("    %s[%s]:::%s\n", nid, quote(nodeLabel(iface.Name, iface.Description)), domain.ResourceInterface))
		}
	}

	if g.filter[domain.ResourceVariable] {
		for _, evID := range fileRes.Connections["has_extvar"] {
			ev, ok := g.topo.Resources[evID]
			if !ok {
				continue
			}
			nid := g.nextID("ev")
			g.nodeID[g.resourceKey(domain.ResourceVariable, evID)] = nid
			g.b.WriteString(fmt.Sprintf("    %s[%s]:::%s\n", nid, quote(nodeLabel(ev.Name, ev.Description)), domain.ResourceVariable))
		}
	}
}

func (g *gen) writeArrows() {
	for srcID, res := range g.topo.Resources {
		for connType, targets := range res.Connections {
			var arrow, label string

			switch connType {
			case "calls":
				arrow, label = "-->", "Calls"
			case "uses_struct":
				arrow, label = "-->", "Uses"
			case "uses_interface":
				arrow, label = "-->", "Uses"
			case "uses_extvar":
				arrow, label = "-->", "Uses"
			case "uses_package":
				arrow, label = "-->", "Uses"
			case "uses_dependency":
				arrow, label = "-->", "Uses"
			case "imports_package":
				arrow, label = "-->", "Imports"
			case "imports_dependency":
				arrow, label = "-->", "Imports"
			case "implements":
				arrow, label = "-.->", "Implements"
			case "constructor":
				arrow, label = "-->", "Constructs"
			default:
				continue
			}

			srcNode, ok := g.nodeID[g.resourceKey(res.Kind, srcID)]
			if !ok {
				targetCheck := false
				for _, t := range targets {
					if node, _ := g.findTargetNode(t); node != "" {
						targetCheck = true
						break
					}
				}
				if !targetCheck {
					continue
				}
			}

			for _, targetID := range targets {
				dstNode, _ := g.findTargetNode(targetID)
				if dstNode == "" {
					if connType == "imports_dependency" && g.filter[domain.ResourceDependency] {
						depID := g.nextID("dep")
						g.b.WriteString(fmt.Sprintf("    %s[%s]:::%s\n", depID, quote(targetID), domain.ResourceDependency))
						dstNode = depID
						g.nodeID[g.resourceKey(domain.ResourceDependency, targetID)] = depID
					} else {
						continue
					}
				}

				if srcNode != "" {
					g.b.WriteString(fmt.Sprintf("    %s %s|\"%s\"| %s\n", srcNode, arrow, label, dstNode))
				}
			}
		}
	}
}

func (g *gen) findTargetNode(targetID string) (string, domain.ResourceKind) {
	target, ok := g.topo.Resources[targetID]
	if !ok {
		return "", ""
	}
	kind := target.Kind
	if kind == domain.ResourceMethod {
		kind = domain.ResourceFunction
	}
	if node, ok := g.nodeID[g.resourceKey(kind, targetID)]; ok {
		return node, kind
	}
	if node, ok := g.nodeID[g.resourceKey(target.Kind, targetID)]; ok {
		return node, target.Kind
	}
	return "", kind
}
