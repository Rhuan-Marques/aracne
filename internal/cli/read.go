package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"ltp/internal/helper"
	"ltp/internal/llm/languages/gotools"
	"ltp/internal/llm/languages/pythontools"
	"ltp/internal/topology"
	"ltp/internal/topology/domain"
	"ltp/internal/topology/golang"
	"ltp/internal/topology/python"
)

func RunRead() {
	args := os.Args[2:]
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: ltp read <resource-id>")
		fmt.Fprintln(os.Stderr, "Examples:")
		fmt.Fprintln(os.Stderr, "  ltp read internal/cli/read.go")
		fmt.Fprintln(os.Stderr, "  ltp read ltp/internal/topology/golang.(GoManager).ReadFunction")
		fmt.Fprintln(os.Stderr, "  ltp read ltp/internal/topology/golang.GoManager")
		os.Exit(1)
	}
	id := args[0]

	manager, _ := InitRegistry(".ltp/topology.db")

	topo, err := manager.ReadAll()
	if err != nil {
		readRawFile(manager, id)
		return
	}

	res, ok := topo.Resources[id]
	if !ok {
		absPath, absErr := filepath.Abs(id)
		if absErr == nil {
			if fr, found := topo.Resources[absPath]; found && fr.Kind == domain.ResourceFile {
				readFileWithContext(manager, topo, &fr, absPath)
				return
			}
		}
		readRawFile(manager, id)
		return
	}

	lang := GetLanguage(manager)

	switch res.Kind {
	case domain.ResourceFunction, domain.ResourceMethod:
		readAsFunction(manager, lang, id)
	case domain.ResourceType:
		readAsStruct(manager, lang, id)
	case domain.ResourceInterface:
		readAsInterface(manager, lang, id)
	case domain.ResourceNamedType:
		readAsNamedType(manager, lang, id)
	case domain.ResourceFile:
		readFileWithContext(manager, topo, &res, id)
	case domain.ResourceVariable:
		readAsVariable(manager, topo, lang, id)
	case domain.ResourceDependency:
		readAsDependency(manager, topo, lang, id)
	case domain.ResourcePackage:
		readPackageContext(manager, topo, lang, id)
	default:
		readAsCut(manager, lang, id, res.Kind)
	}
}

func readRawFile(mgr *topology.TopologyManager, path string) {
	cfg := helper.EnsureConfig(helper.ConfigPath(mgr.DbPath()))
	maxSize := cfg.EffectiveMaxFileSize()

	fi, err := os.Stat(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: resource %q not found in topology and cannot be read as file: %v\n", path, err)
		os.Exit(1)
	}
	if fi.Size() > maxSize {
		fmt.Printf("File %q is too large (%d bytes, max %d). Cannot display raw content.\n", path, fi.Size(), maxSize)
		return
	}

	data, readErr := os.ReadFile(path)
	if readErr != nil {
		fmt.Fprintf(os.Stderr, "Error reading %q: %v\n", path, readErr)
		os.Exit(1)
	}

	if isBinary(data) {
		fmt.Printf("Binary file %q cannot be displayed as text.\n", path)
		return
	}

	name := filepath.Base(path)
	fmt.Printf("%s\n%s", name, string(data))
}

func readFileWithContext(mgr *topology.TopologyManager, topo *domain.Topology, fileRes *domain.Resource, fileID string) {
	cfg := helper.EnsureConfig(helper.ConfigPath(mgr.DbPath()))
	maxSize := cfg.EffectiveMaxFileSize()

	fi, err := os.Stat(fileID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading file %q: %v\n", fileID, err)
		os.Exit(1)
	}
	if fi.Size() > maxSize {
		fmt.Printf("File %q is too large (%d bytes, max %d). Cannot display content.\n", fileID, fi.Size(), maxSize)
		return
	}

	data, readErr := os.ReadFile(fileID)
	if readErr != nil {
		fmt.Fprintf(os.Stderr, "Error reading %q: %v\n", fileID, readErr)
		os.Exit(1)
	}

	if isBinary(data) {
		fmt.Printf("Binary file %q cannot be displayed as text.\n", fileID)
		return
	}

	name := filepath.Base(fileID)
	fmt.Printf("%s\n%s", name, string(data))

	if topo.Language == "python" {
		pt := python.FromGeneric(topo)
		formatPythonFileContext(pt, fileID)
	} else {
		gt := golang.FromGeneric(topo)
		formatGoFileContext(gt, fileID)
	}
}

func formatGoFileContext(gt *golang.GolangTopology, fileID string) {
	gf, ok := gt.Files[golang.FileID(fileID)]
	if !ok {
		return
	}

	type info struct {
		ID   string
		Name string
		Desc string
		Line int
	}

	var standalone []info
	var structs []info
	structMethods := make(map[golang.StructID][]info)
	var ifaces []info
	var namedTypes []info
	var vars []info

	for _, fid := range gf.Functions() {
		fn, exists := gt.Functions[fid]
		if !exists {
			continue
		}
		inf := info{ID: string(fid), Name: fn.Name, Desc: fn.Description, Line: fn.Loc.StartsAt}
		if fn.MethodFrom != nil {
			sid := *fn.MethodFrom
			structMethods[sid] = append(structMethods[sid], inf)
		} else {
			standalone = append(standalone, inf)
		}
	}

	for _, sid := range gf.Structs() {
		s, exists := gt.Structs[sid]
		if !exists {
			continue
		}
		structs = append(structs, info{ID: string(sid), Name: s.Name, Desc: s.Description, Line: s.Loc.StartsAt})
	}

	for _, iid := range gf.Interfaces() {
		iface, exists := gt.Interfaces[iid]
		if !exists {
			continue
		}
		ifaces = append(ifaces, info{Name: iface.Name, Desc: iface.Description, Line: iface.Loc.StartsAt})
	}

	for _, ntid := range gf.NamedTypes() {
		nt, exists := gt.NamedTypes[ntid]
		if !exists {
			continue
		}
		namedTypes = append(namedTypes, info{Name: nt.Name, Desc: nt.Description, Line: nt.Loc.StartsAt})
	}

	for _, vid := range gf.ExternalVars() {
		v, exists := gt.ExternalVars[vid]
		if !exists {
			continue
		}
		vars = append(vars, info{Name: v.Name, Desc: v.Description, Line: v.Location.StartsAt})
	}

	sortByLine := func(s []info) {
		sort.Slice(s, func(i, j int) bool { return s[i].Line < s[j].Line })
	}
	sortByLine(standalone)
	sortByLine(structs)
	sortByLine(ifaces)
	sortByLine(namedTypes)
	sortByLine(vars)
	for _, v := range structMethods {
		sortByLine(v)
	}

	total := len(standalone) + len(structs) + len(ifaces) + len(namedTypes) + len(vars)
	if total == 0 {
		return
	}
	fmt.Print("\n\n# File Context\n")

	if len(standalone) > 0 {
		fmt.Print("\n## Functions\n")
		for _, f := range standalone {
			desc := f.Desc
			if desc == "" {
				desc = "(no description)"
			}
			fmt.Printf("    %s (line %d): %s\n", f.Name, f.Line, desc)
		}
	}

	if len(structs) > 0 {
		fmt.Print("\n## Types\n")
		for _, s := range structs {
			desc := s.Desc
			if desc == "" {
				desc = "(no description)"
			}
			fmt.Printf("    %s (line %d): %s\n", s.Name, s.Line, desc)
			sid := golang.StructID(s.ID)
			for _, m := range structMethods[sid] {
				mdesc := m.Desc
				if mdesc == "" {
					mdesc = "(no description)"
				}
				fmt.Printf("        %s.%s (line %d): %s\n", s.Name, m.Name, m.Line, mdesc)
			}
		}
	}

	if len(ifaces) > 0 {
		fmt.Print("\n## Interfaces\n")
		for _, i := range ifaces {
			desc := i.Desc
			if desc == "" {
				desc = "(no description)"
			}
			fmt.Printf("    %s (line %d): %s\n", i.Name, i.Line, desc)
		}
	}

	if len(namedTypes) > 0 {
		fmt.Print("\n## Named Types\n")
		for _, nt := range namedTypes {
			desc := nt.Desc
			if desc == "" {
				desc = "(no description)"
			}
			fmt.Printf("    %s (line %d): %s\n", nt.Name, nt.Line, desc)
		}
	}

	if len(vars) > 0 {
		fmt.Print("\n## Variables\n")
		for _, v := range vars {
			desc := v.Desc
			if desc == "" {
				desc = "(no description)"
			}
			fmt.Printf("    %s (line %d): %s\n", v.Name, v.Line, desc)
		}
	}
}

func formatPythonFileContext(pt *python.PythonTopology, fileID string) {
	mod, ok := pt.Modules[python.ModuleID(fileID)]
	if !ok {
		return
	}

	type info struct {
		Name string
		Desc string
		Line int
	}

	var functions []info
	var classEntries []struct {
		info
		cid python.ClassID
	}
	classMethods := make(map[python.ClassID][]info)
	var vars []info

	for _, fid := range mod.Functions() {
		fn, exists := pt.Functions[fid]
		if !exists {
			continue
		}
		inf := info{Name: fn.Name, Desc: fn.Description, Line: fn.Loc.StartsAt}
		if fn.MethodFrom != nil {
			cid := *fn.MethodFrom
			classMethods[cid] = append(classMethods[cid], inf)
		} else {
			functions = append(functions, inf)
		}
	}

	for _, cid := range mod.Classes() {
		cls, exists := pt.Classes[cid]
		if !exists {
			continue
		}
		classEntries = append(classEntries, struct {
			info
			cid python.ClassID
		}{info: info{Name: cls.Name, Desc: cls.Description, Line: cls.Loc.StartsAt}, cid: cid})
	}

	for _, vid := range mod.ExternalVars() {
		v, exists := pt.ExternalVars[vid]
		if !exists {
			continue
		}
		vars = append(vars, info{Name: v.Name, Desc: v.Description, Line: v.Location.StartsAt})
	}

	sortByLine := func(s []info) {
		sort.Slice(s, func(i, j int) bool { return s[i].Line < s[j].Line })
	}
	sortByLine(functions)
	sort.Slice(classEntries, func(i, j int) bool { return classEntries[i].Line < classEntries[j].Line })
	for _, v := range classMethods {
		sortByLine(v)
	}
	sortByLine(vars)

	total := len(functions) + len(classEntries) + len(vars)
	if total == 0 {
		return
	}
	fmt.Print("\n\n# File Context\n")

	if len(functions) > 0 {
		fmt.Print("\n## Functions\n")
		for _, f := range functions {
			desc := f.Desc
			if desc == "" {
				desc = "(no description)"
			}
			fmt.Printf("    %s (line %d): %s\n", f.Name, f.Line, desc)
		}
	}

	if len(classEntries) > 0 {
		fmt.Print("\n## Classes\n")
		for _, ce := range classEntries {
			desc := ce.Desc
			if desc == "" {
				desc = "(no description)"
			}
			fmt.Printf("    %s (line %d): %s\n", ce.Name, ce.Line, desc)
			for _, m := range classMethods[ce.cid] {
				mdesc := m.Desc
				if mdesc == "" {
					mdesc = "(no description)"
				}
				fmt.Printf("        %s.%s (line %d): %s\n", ce.Name, m.Name, m.Line, mdesc)
			}
		}
	}

	if len(vars) > 0 {
		fmt.Print("\n## Variables\n")
		for _, v := range vars {
			desc := v.Desc
			if desc == "" {
				desc = "(no description)"
			}
			fmt.Printf("    %s (line %d): %s\n", v.Name, v.Line, desc)
		}
	}
}

func readAsFunction(mgr *topology.TopologyManager, lang string, id string) {
	if lang == "python" {
		pythonManager := python.NewPythonManager(mgr)
		ctx, err := pythonManager.ReadFunction(id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Print(pythontools.FormatPythonFunctionContext(ctx))
		return
	}
	goManager := golang.NewGoManager(mgr)
	ctx, err := goManager.ReadFunction(id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(gotools.FormatGoFunctionContext(ctx))
}

func readAsStruct(mgr *topology.TopologyManager, lang string, id string) {
	if lang == "python" {
		pythonManager := python.NewPythonManager(mgr)
		ctx, err := pythonManager.ReadClass(id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Print(pythontools.FormatPythonClassContext(ctx))
		return
	}
	goManager := golang.NewGoManager(mgr)
	ctx, err := goManager.ReadStruct(id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(gotools.FormatGoStructContext(ctx))
}

func readAsInterface(mgr *topology.TopologyManager, lang string, id string) {
	if lang == "python" {
		readAsCut(mgr, lang, id, domain.ResourceInterface)
		return
	}
	goManager := golang.NewGoManager(mgr)
	ctx, err := goManager.ReadInterface(id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(gotools.FormatGoInterfaceContext(ctx))
}

func readAsNamedType(mgr *topology.TopologyManager, lang string, id string) {
	if lang == "python" {
		readAsCut(mgr, lang, id, domain.ResourceNamedType)
		return
	}
	goManager := golang.NewGoManager(mgr)
	ctx, err := goManager.ReadNamedType(id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(gotools.FormatGoNamedTypeContext(ctx))
}

func readAsVariable(mgr *topology.TopologyManager, topo *domain.Topology, lang string, id string) {
	var loc domain.Location

	if lang == "python" {
		pt := python.FromGeneric(topo)
		v, ok := pt.ExternalVars[python.ExternalVarID(id)]
		if !ok {
			readAsCut(mgr, lang, id, domain.ResourceVariable)
			return
		}
		loc = v.Location
	} else {
		gt := golang.FromGeneric(topo)
		v, ok := gt.ExternalVars[golang.ExternalVarID(id)]
		if !ok {
			readAsCut(mgr, lang, id, domain.ResourceVariable)
			return
		}
		loc = v.Location
	}

	entry, err := mgr.Cut(loc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	name := filepath.Base(loc.Path)
	fmt.Printf("%s\n%s", name, entry.Cut)

	var usedBy []string

	if lang == "python" {
		pt := python.FromGeneric(topo)
		for _, fn := range pt.Functions {
			for _, evid := range fn.UsesExtVar() {
				if string(evid) == id {
					d := fn.Description
					if d == "" {
						d = "(no description)"
					}
					usedBy = append(usedBy, fmt.Sprintf("\t%s %s", fn.ID, d))
					break
				}
			}
		}
		for _, c := range pt.Classes {
			if evids, ok := c.Connections[python.ConnUsesExtVar]; ok {
				for _, evid := range evids {
					if evid == id {
						d := c.Description
						if d == "" {
							d = "(no description)"
						}
						usedBy = append(usedBy, fmt.Sprintf("\t%s %s", c.ID, d))
						break
					}
				}
			}
		}
	} else {
		gt := golang.FromGeneric(topo)
		for _, fn := range gt.Functions {
			for _, evid := range fn.UsesExtVar() {
				if string(evid) == id {
					d := fn.Description
					if d == "" {
						d = "(no description)"
					}
					usedBy = append(usedBy, fmt.Sprintf("\t%s %s", fn.ID, d))
					break
				}
			}
		}
		for _, s := range gt.Structs {
			if evids, ok := s.Connections[golang.ConnUsesExtVar]; ok {
				for _, evid := range evids {
					if evid == id {
						d := s.Description
						if d == "" {
							d = "(no description)"
						}
						usedBy = append(usedBy, fmt.Sprintf("\t%s %s", s.ID, d))
						break
					}
				}
			}
		}
	}

	if len(usedBy) > 0 {
		sort.Strings(usedBy)
		fmt.Print("\n# Used By\n")
		for _, u := range usedBy {
			fmt.Println(u)
		}
	}
}

func readAsDependency(mgr *topology.TopologyManager, topo *domain.Topology, lang string, id string) {
	fmt.Printf("Dependency: %s\n", id)

	var usedBy []string

	if lang == "python" {
		pt := python.FromGeneric(topo)
		for _, fn := range pt.Functions {
			for _, d := range fn.UsesDep() {
				if string(d) == id {
					desc := fn.Description
					if desc == "" {
						desc = "(no description)"
					}
					usedBy = append(usedBy, fmt.Sprintf("\t%s %s", fn.ID, desc))
					break
				}
			}
		}
		for _, c := range pt.Classes {
			for _, d := range c.UsesDep() {
				if string(d) == id {
					desc := c.Description
					if desc == "" {
						desc = "(no description)"
					}
					usedBy = append(usedBy, fmt.Sprintf("\t%s %s", c.ID, desc))
					break
				}
			}
		}
	} else {
		gt := golang.FromGeneric(topo)
		for _, fn := range gt.Functions {
			for _, d := range fn.UsesDep() {
				if string(d) == id {
					desc := fn.Description
					if desc == "" {
						desc = "(no description)"
					}
					usedBy = append(usedBy, fmt.Sprintf("\t%s %s", fn.ID, desc))
					break
				}
			}
		}
		for _, s := range gt.Structs {
			for _, d := range s.UsesDep() {
				if string(d) == id {
					desc := s.Description
					if desc == "" {
						desc = "(no description)"
					}
					usedBy = append(usedBy, fmt.Sprintf("\t%s %s", s.ID, desc))
					break
				}
			}
		}
	}

	if len(usedBy) > 0 {
		sort.Strings(usedBy)
		fmt.Print("\n# Used By\n")
		for _, u := range usedBy {
			fmt.Println(u)
		}
	}
}

func readAsCut(mgr *topology.TopologyManager, lang string, id string, kind domain.ResourceKind) {
	if lang == "python" {
		pythonManager := python.NewPythonManager(mgr)
		entry, err := pythonManager.ReadResourceAndCut(id, kind)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Print(entry.Cut)
		return
	}
	goManager := golang.NewGoManager(mgr)
	entry, err := goManager.ReadResourceAndCut(id, kind)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(entry.Cut)
}

func isBinary(data []byte) bool {
	n := len(data)
	if n > 512 {
		n = 512
	}
	return bytes.IndexByte(data[:n], 0) >= 0
}

func readPackageContext(mgr *topology.TopologyManager, topo *domain.Topology, lang string, pkgID string) {
	fmt.Printf("Package: %s\n", pkgID)

	if topo.Language == "python" {
		pt := python.FromGeneric(topo)
		formatPythonPackageContext(pt, pkgID)
	} else {
		gt := golang.FromGeneric(topo)
		formatGoPackageContext(gt, pkgID)
	}
}

func formatGoPackageContext(gt *golang.GolangTopology, pkgID string) {
	pkg, ok := gt.Packages[golang.PackagePath(pkgID)]
	if !ok {
		return
	}

	type entry struct {
		Name string
		Desc string
		Loc  domain.Location
	}

	lookup := func(loc domain.Location) string {
		rel, err := filepath.Rel(gt.Root, loc.Path)
		if err != nil {
			return fmt.Sprintf("line %d", loc.StartsAt)
		}
		return fmt.Sprintf("%s:%d", rel, loc.StartsAt)
	}

	// Collect standalone functions and methods keyed by parent struct
	var standalone []entry
	structMethods := make(map[golang.StructID][]entry)

	for _, fid := range pkg.HasFunctions() {
		fn, exists := gt.Functions[fid]
		if !exists {
			continue
		}
		e := entry{Name: fn.Name, Desc: fn.Description, Loc: fn.Loc}
		if fn.MethodFrom != nil {
			sid := *fn.MethodFrom
			structMethods[sid] = append(structMethods[sid], e)
		} else {
			standalone = append(standalone, e)
		}
	}

	// Collect structs and also track their name<->id mapping
	var structEntries []struct {
		entry
		sid golang.StructID
	}
	structName := map[golang.StructID]string{}

	for _, sid := range pkg.HasStructs() {
		s, exists := gt.Structs[sid]
		if !exists {
			continue
		}
		structEntries = append(structEntries, struct {
			entry
			sid golang.StructID
		}{entry: entry{Name: s.Name, Desc: s.Description, Loc: s.Loc}, sid: sid})
		structName[sid] = s.Name
	}

	// Collect interfaces
	var ifaces []entry
	for _, iid := range pkg.HasInterfaces() {
		iface, exists := gt.Interfaces[iid]
		if !exists {
			continue
		}
		ifaces = append(ifaces, entry{Name: iface.Name, Desc: iface.Description, Loc: iface.Loc})
	}

	// Named types
	var namedTypes []entry
	for _, ntid := range pkg.HasNamedTypes() {
		nt, exists := gt.NamedTypes[ntid]
		if !exists {
			continue
		}
		namedTypes = append(namedTypes, entry{Name: nt.Name, Desc: nt.Description, Loc: nt.Loc})
	}

	// Variables
	var vars []entry
	for _, vid := range pkg.HasExternalVars() {
		v, exists := gt.ExternalVars[vid]
		if !exists {
			continue
		}
		vars = append(vars, entry{Name: v.Name, Desc: v.Description, Loc: v.Location})
	}

	sortByLine := func(s []entry) {
		sort.Slice(s, func(i, j int) bool { return s[i].Loc.StartsAt < s[j].Loc.StartsAt })
	}
	sortByLine(standalone)
	sort.Slice(structEntries, func(i, j int) bool { return structEntries[i].Loc.StartsAt < structEntries[j].Loc.StartsAt })
	sortByLine(ifaces)
	sortByLine(namedTypes)
	sortByLine(vars)
	for _, v := range structMethods {
		sortByLine(v)
	}

	total := len(standalone) + len(structEntries) + len(ifaces) + len(namedTypes) + len(vars)
	if total == 0 {
		return
	}
	fmt.Print("\n# Context\n")

	if len(standalone) > 0 {
		fmt.Print("\n## Functions\n")
		for _, e := range standalone {
			desc := e.Desc
			if desc == "" {
				desc = "(no description)"
			}
			fmt.Printf("    %s (%s): %s\n", e.Name, lookup(e.Loc), desc)
		}
	}

	if len(structEntries) > 0 {
		fmt.Print("\n## Types\n")
		for _, se := range structEntries {
			desc := se.Desc
			if desc == "" {
				desc = "(no description)"
			}
			fmt.Printf("    %s (%s): %s\n", se.Name, lookup(se.Loc), desc)
			for _, m := range structMethods[se.sid] {
				mdesc := m.Desc
				if mdesc == "" {
					mdesc = "(no description)"
				}
				fmt.Printf("        %s.%s (%s): %s\n", se.Name, m.Name, lookup(m.Loc), mdesc)
			}
		}
	}

	if len(ifaces) > 0 {
		fmt.Print("\n## Interfaces\n")
		for _, e := range ifaces {
			desc := e.Desc
			if desc == "" {
				desc = "(no description)"
			}
			fmt.Printf("    %s (%s): %s\n", e.Name, lookup(e.Loc), desc)
		}
	}

	if len(namedTypes) > 0 {
		fmt.Print("\n## Named Types\n")
		for _, e := range namedTypes {
			desc := e.Desc
			if desc == "" {
				desc = "(no description)"
			}
			fmt.Printf("    %s (%s): %s\n", e.Name, lookup(e.Loc), desc)
		}
	}

	if len(vars) > 0 {
		fmt.Print("\n## Variables\n")
		for _, e := range vars {
			desc := e.Desc
			if desc == "" {
				desc = "(no description)"
			}
			fmt.Printf("    %s (%s): %s\n", e.Name, lookup(e.Loc), desc)
		}
	}
}

func formatPythonPackageContext(pt *python.PythonTopology, pkgID string) {
	pkg, ok := pt.Packages[python.PackagePath(pkgID)]
	if !ok {
		return
	}

	type entry struct {
		Name string
		Desc string
		Loc  domain.Location
	}

	lookup := func(loc domain.Location) string {
		rel, err := filepath.Rel(pt.Root, loc.Path)
		if err != nil {
			return fmt.Sprintf("line %d", loc.StartsAt)
		}
		return fmt.Sprintf("%s:%d", rel, loc.StartsAt)
	}

	var standalone []entry
	classMethods := make(map[python.ClassID][]entry)
	var classEntries []struct {
		entry
		cid python.ClassID
	}
	var vars []entry

	for _, fid := range pkg.HasFunctions() {
		fn, exists := pt.Functions[fid]
		if !exists {
			continue
		}
		e := entry{Name: fn.Name, Desc: fn.Description, Loc: fn.Loc}
		if fn.MethodFrom != nil {
			cid := *fn.MethodFrom
			classMethods[cid] = append(classMethods[cid], e)
		} else {
			standalone = append(standalone, e)
		}
	}

	for _, cid := range pkg.HasClasses() {
		cls, exists := pt.Classes[cid]
		if !exists {
			continue
		}
		classEntries = append(classEntries, struct {
			entry
			cid python.ClassID
		}{entry: entry{Name: cls.Name, Desc: cls.Description, Loc: cls.Loc}, cid: cid})
	}

	for _, vid := range pkg.HasExternalVars() {
		v, exists := pt.ExternalVars[vid]
		if !exists {
			continue
		}
		vars = append(vars, entry{Name: v.Name, Desc: v.Description, Loc: v.Location})
	}

	sortByLine := func(s []entry) {
		sort.Slice(s, func(i, j int) bool { return s[i].Loc.StartsAt < s[j].Loc.StartsAt })
	}
	sortByLine(standalone)
	sort.Slice(classEntries, func(i, j int) bool { return classEntries[i].Loc.StartsAt < classEntries[j].Loc.StartsAt })
	sortByLine(vars)
	for _, v := range classMethods {
		sortByLine(v)
	}

	total := len(standalone) + len(classEntries) + len(vars)
	if total == 0 {
		return
	}
	fmt.Print("\n# Context\n")

	if len(standalone) > 0 {
		fmt.Print("\n## Functions\n")
		for _, e := range standalone {
			desc := e.Desc
			if desc == "" {
				desc = "(no description)"
			}
			fmt.Printf("    %s (%s): %s\n", e.Name, lookup(e.Loc), desc)
		}
	}

	if len(classEntries) > 0 {
		fmt.Print("\n## Classes\n")
		for _, ce := range classEntries {
			desc := ce.Desc
			if desc == "" {
				desc = "(no description)"
			}
			fmt.Printf("    %s (%s): %s\n", ce.Name, lookup(ce.Loc), desc)
			for _, m := range classMethods[ce.cid] {
				mdesc := m.Desc
				if mdesc == "" {
					mdesc = "(no description)"
				}
				fmt.Printf("        %s.%s (%s): %s\n", ce.Name, m.Name, lookup(m.Loc), mdesc)
			}
		}
	}

	if len(vars) > 0 {
		fmt.Print("\n## Variables\n")
		for _, e := range vars {
			desc := e.Desc
			if desc == "" {
				desc = "(no description)"
			}
			fmt.Printf("    %s (%s): %s\n", e.Name, lookup(e.Loc), desc)
		}
	}
}
