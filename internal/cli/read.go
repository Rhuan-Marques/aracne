package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"aracne/internal/helper"
	"aracne/internal/llm/languages/gotools"
	"aracne/internal/llm/languages/jstools"
	"aracne/internal/llm/languages/pythontools"
	"aracne/internal/topology"
	"aracne/internal/topology/domain"
	"aracne/internal/topology/golang"
	"aracne/internal/topology/javascript"
	"aracne/internal/topology/python"
)

func RunRead() {
	args := os.Args[2:]
	id, forcedKind, startLine, endLine, parseErr := parseReadArgs(args)
	if parseErr != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", parseErr)
		printReadUsage()
		os.Exit(1)
	}

	id = helper.NormalizeResourceID(id)

	manager, reg := InitRegistry(".aracne/topology.db")
	runReadScan(manager, reg)

	if startLine > 0 || endLine > 0 {
		readFileRange(manager, id, startLine, endLine)
		return
	}

	topo, err := manager.ReadAll()
	if err != nil {
		if forcedKind != "" {
			fmt.Fprintf(os.Stderr, "Error: The %s %s does not exist\n", forcedKind, id)
			os.Exit(1)
		}
		readRawFile(manager, id)
		return
	}

	res, resourceID, ok := findReadResource(topo, id)
	if !ok {
		if forcedKind != "" {
			fmt.Fprintf(os.Stderr, "Error: The %s %s does not exist\n", forcedKind, id)
			os.Exit(1)
		}
		readRawFile(manager, id)
		return
	}

	if forcedKind != "" && res.Kind != forcedKind {
		fmt.Fprintf(os.Stderr, "Error: The %s %s does not exist. Did you mean to read the %s %s?\n", forcedKind, id, res.Kind, resourceID)
		os.Exit(1)
	}

	lang := GetLanguage(manager)

	switch res.Kind {
	case domain.ResourceFunction, domain.ResourceMethod:
		readAsFunction(manager, lang, resourceID)
	case domain.ResourceType:
		readAsStruct(manager, lang, resourceID)
	case domain.ResourceInterface:
		readAsInterface(manager, lang, resourceID)
	case domain.ResourceNamedType:
		readAsNamedType(manager, lang, resourceID)
	case domain.ResourceFile:
		readFileWithContext(manager, topo, &res, resourceID)
	case domain.ResourceVariable:
		readAsVariable(manager, topo, lang, resourceID)
	case domain.ResourceDependency:
		readAsDependency(manager, topo, lang, resourceID)
	case domain.ResourcePackage:
		readPackageContext(manager, topo, lang, resourceID)
	default:
		readAsCut(manager, lang, resourceID, res.Kind)
	}
}

func parseReadArgs(args []string) (string, domain.ResourceKind, int, int, error) {
	if len(args) < 1 {
		return "", "", 0, 0, fmt.Errorf("missing resource ID")
	}

	var id string
	var forcedKind domain.ResourceKind
	var startLine, endLine int

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--kind":
			if i+1 >= len(args) {
				return "", "", 0, 0, fmt.Errorf("--kind requires a value")
			}
			kind := MapResourceKind(args[i+1])
			if kind == "" {
				return "", "", 0, 0, fmt.Errorf("unknown resource kind %q", args[i+1])
			}
			forcedKind = kind
			i++
		case strings.HasPrefix(arg, "--kind="):
			value := strings.TrimPrefix(arg, "--kind=")
			kind := MapResourceKind(value)
			if kind == "" {
				return "", "", 0, 0, fmt.Errorf("unknown resource kind %q", value)
			}
			forcedKind = kind
		case arg == "--lines":
			if i+1 >= len(args) {
				return "", "", 0, 0, fmt.Errorf("--lines requires a value")
			}
			s, e, perr := parseLineRange(args[i+1])
			if perr != nil {
				return "", "", 0, 0, perr
			}
			startLine, endLine = s, e
			i++
		case strings.HasPrefix(arg, "--lines="):
			s, e, perr := parseLineRange(strings.TrimPrefix(arg, "--lines="))
			if perr != nil {
				return "", "", 0, 0, perr
			}
			startLine, endLine = s, e
		case strings.HasPrefix(arg, "-"):
			return "", "", 0, 0, fmt.Errorf("unknown flag %q", arg)
		default:
			if id != "" {
				return "", "", 0, 0, fmt.Errorf("unexpected extra argument %q", arg)
			}
			id = arg
		}
	}

	if id == "" {
		return "", "", 0, 0, fmt.Errorf("missing resource ID")
	}
	return id, forcedKind, startLine, endLine, nil
}

// parseLineRange parses a --lines value: "start:end" (either side optional),
// or a bare "N" for a single line. Returns 1-indexed bounds where 0 means unset.
func parseLineRange(value string) (int, int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, 0, fmt.Errorf("--lines requires a value like 10:40, 10:, :40, or 25")
	}
	parseInt := func(s string) (int, error) {
		s = strings.TrimSpace(s)
		if s == "" {
			return 0, nil
		}
		n, err := strconv.Atoi(s)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("invalid line number %q in --lines", s)
		}
		return n, nil
	}
	if startStr, endStr, ok := strings.Cut(value, ":"); ok {
		start, err := parseInt(startStr)
		if err != nil {
			return 0, 0, err
		}
		end, err := parseInt(endStr)
		if err != nil {
			return 0, 0, err
		}
		if start == 0 && end == 0 {
			return 0, 0, fmt.Errorf("--lines requires at least one bound")
		}
		if start > 0 && end > 0 && end < start {
			return 0, 0, fmt.Errorf("--lines end %d is before start %d", end, start)
		}
		return start, end, nil
	}
	n, err := parseInt(value)
	if err != nil {
		return 0, 0, err
	}
	return n, n, nil
}

func printReadUsage() {
	fmt.Fprintln(os.Stderr, "Usage: arac read [--kind <kind>] [--lines <start>:<end>] <resource-id>")
	fmt.Fprintln(os.Stderr, "Kinds: function, method, type, named_type, interface, variable, file, package, dependency")
	fmt.Fprintln(os.Stderr, "Examples:")
	fmt.Fprintln(os.Stderr, "  arac read internal/cli/read.go")
	fmt.Fprintln(os.Stderr, "  arac read internal/cli/read.go --lines 10:40")
	fmt.Fprintln(os.Stderr, "  arac read --kind function aracne/internal/topology/golang.(GoManager).ReadFunction")
	fmt.Fprintln(os.Stderr, "  arac read aracne/internal/topology/golang.GoManager --kind type")
}

func findReadResource(topo *domain.Topology, id string) (domain.Resource, string, bool) {
	res, ok := topo.Resources[id]
	if ok {
		return res, id, true
	}

	absPath, absErr := filepath.Abs(id)
	if absErr == nil {
		if fr, found := topo.Resources[absPath]; found && fr.Kind == domain.ResourceFile {
			return fr, absPath, true
		}
	}

	return domain.Resource{}, "", false
}

// readFileRange prints only the requested line range of a file (no topology
// context). It resolves the path to a topology file when possible, otherwise
// treats the id as a filesystem path.
func readFileRange(mgr *topology.TopologyManager, id string, start, end int) {
	path := id
	if topo, err := mgr.ReadAll(); err == nil {
		if res, resourceID, ok := findReadResource(topo, id); ok && res.Kind == domain.ResourceFile {
			path = resourceID
		}
	}
	content, last, err := helper.ReadFileRange(path, start, end)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	first := start
	if first <= 0 {
		first = 1
	}
	fmt.Printf("%s (lines %d-%d)\n%s\n", filepath.Base(path), first, last, content)
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
	} else if topo.Language == "javascript" || topo.Language == "typescript" {
		jt := javascript.FromGeneric(topo)
		formatJSFileContext(jt, fileID)
	} else {
		gt := golang.FromGeneric(topo)
		formatGoFileContext(gt, fileID)
	}
}

func formatJSFileContext(jt *javascript.JavaScriptTopology, fileID string) {
	mod, ok := jt.Modules[javascript.ModuleID(fileID)]
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
		cid javascript.ClassID
	}
	classMethods := make(map[javascript.ClassID][]info)
	var vars []info

	for _, fid := range mod.Functions() {
		fn, exists := jt.Functions[fid]
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
		cls, exists := jt.Classes[cid]
		if !exists {
			continue
		}
		classEntries = append(classEntries, struct {
			info
			cid javascript.ClassID
		}{info: info{Name: cls.Name, Desc: cls.Description, Line: cls.Loc.StartsAt}, cid: cid})
	}

	for _, vid := range mod.ExternalVars() {
		v, exists := jt.ExternalVars[vid]
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

// cliContextFilter builds the read.context_filter option from the project
// config so the CLI read command renders neighbors like the MCP read tools.
func cliContextFilter(mgr *topology.TopologyManager) topology.TopologyOption {
	cfg := helper.LoadConfig(helper.ConfigPath(mgr.DbPath()))
	return topology.WithContextFilter(cfg.EffectiveContextFilter())
}

func readAsFunction(mgr *topology.TopologyManager, lang string, id string) {
	filter := cliContextFilter(mgr)
	if lang == "python" {
		pythonManager := python.NewPythonManager(mgr)
		ctx, err := pythonManager.ReadFunction(id, filter)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Print(pythontools.FormatPythonFunctionContext(ctx))
		return
	}
	if lang == "javascript" || lang == "typescript" {
		jsManager := javascript.NewJavaScriptManager(mgr)
		ctx, err := jsManager.ReadFunction(id, filter)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Print(jstools.FormatJavaScriptFunctionContext(ctx))
		return
	}
	goManager := golang.NewGoManager(mgr)
	ctx, err := goManager.ReadFunction(id, filter)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(gotools.FormatGoFunctionContext(ctx))
}

func readAsStruct(mgr *topology.TopologyManager, lang string, id string) {
	filter := cliContextFilter(mgr)
	if lang == "python" {
		pythonManager := python.NewPythonManager(mgr)
		ctx, err := pythonManager.ReadClass(id, filter)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Print(pythontools.FormatPythonClassContext(ctx))
		return
	}
	if lang == "javascript" || lang == "typescript" {
		jsManager := javascript.NewJavaScriptManager(mgr)
		ctx, err := jsManager.ReadClass(id, filter)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Print(jstools.FormatJavaScriptClassContext(ctx))
		return
	}
	goManager := golang.NewGoManager(mgr)
	ctx, err := goManager.ReadStruct(id, filter)
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
	if lang == "javascript" || lang == "typescript" {
		ctx, err := javascript.NewJavaScriptManager(mgr).ReadInterface(id, cliContextFilter(mgr))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Print(jstools.FormatJavaScriptInterfaceContext(ctx))
		return
	}
	goManager := golang.NewGoManager(mgr)
	ctx, err := goManager.ReadInterface(id, cliContextFilter(mgr))
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
	if lang == "javascript" || lang == "typescript" {
		ctx, err := javascript.NewJavaScriptManager(mgr).ReadNamedType(id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Print(jstools.FormatJavaScriptNamedTypeContext(ctx))
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
	} else if lang == "javascript" || lang == "typescript" {
		jt := javascript.FromGeneric(topo)
		v, ok := jt.ExternalVars[javascript.ExternalVarID(id)]
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
	} else if lang == "javascript" || lang == "typescript" {
		jt := javascript.FromGeneric(topo)
		for _, fn := range jt.Functions {
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
		for _, c := range jt.Classes {
			if evids, ok := c.Connections[javascript.ConnUsesExtVar]; ok {
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
	} else if lang == "javascript" || lang == "typescript" {
		jt := javascript.FromGeneric(topo)
		for _, fn := range jt.Functions {
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
		for _, c := range jt.Classes {
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

	// Only Go has a package resource. Python/JS/TS center on the module (file),
	// so they never reach here (no package node exists to dispatch on).
	gt := golang.FromGeneric(topo)
	formatGoPackageContext(gt, pkgID)
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


