// Package topology provides the TopologyManager which orchestrates the full
// lifecycle of a project topology: scanning a Go repository to build it,
// loading it from a previously saved SQLite database, and writing it back to disk.
package topology

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"llm-topology/internal/helper"
	"llm-topology/internal/topology/domain"
	"llm-topology/internal/topology/scanner"
)

// TopologyManager owns a dbPath and exposes operations to scan a Go project,
// persist the result to an SQLite database, and reload from a saved file.
// All topology data is read from and written to the SQLite database directly.
type TopologyManager struct {
	dbPath string
}

// New creates a TopologyManager with no db path set.
func New() *TopologyManager {
	return &TopologyManager{}
}

// FullScan performs a complete analysis of the Go project at the given root
// path and writes the result directly to the manager's SQLite database.
func (m *TopologyManager) FullScan(root string) error {
	topo, err := scanner.Scan(root)
	if err != nil {
		return err
	}
	return helper.WriteDb(topo, m.dbPath)
}

// Load sets the manager's database path to the given path without reading it.
func (m *TopologyManager) Load(path string) error {
	m.dbPath = path
	return nil
}

// ReadAll reads the full topology from the configured database and returns it.
// If WithResourceFilter is passed, only maps for the listed resource kinds are
// populated; all others remain nil/empty.
func (m *TopologyManager) ReadAll(opts ...TopologyOption) (*domain.Topology, error) {
	opt := &TopologyOptions{}
	for _, o := range opts {
		o(opt)
	}
	topo, err := helper.ReadDb(m.dbPath)
	if err != nil {
		return nil, err
	}
	if opt.resourceFilter != nil {
		if !opt.hasResource(domain.FUNCTION_RESOURCE) {
			topo.Functions = nil
		}
		if !opt.hasResource(domain.STRUCT_RESOURCE) {
			topo.Struct = nil
		}
		if !opt.hasResource(domain.INTERFACE_RESOURCE) {
			topo.Interfaces = nil
		}
		if !opt.hasResource(domain.EXTERNAL_VAR_RESOURCE) {
			topo.ExternalVars = nil
		}
		if !opt.hasResource(domain.FILE_RESOURCE) {
			topo.Files = nil
		}
		if !opt.hasResource(domain.PACKAGE_RESOURCE) {
			topo.Packages = nil
		}
	}
	if opt.hasDescription != nil {
		wantDesc := *opt.hasDescription
		if topo.Functions != nil {
			for id, fn := range topo.Functions {
				if (fn.Description != "") != wantDesc {
					delete(topo.Functions, id)
				}
			}
		}
		if topo.Struct != nil {
			for id, s := range topo.Struct {
				if (s.Description != "") != wantDesc {
					delete(topo.Struct, id)
				}
			}
		}
		if topo.Interfaces != nil {
			for id, iface := range topo.Interfaces {
				if (iface.Description != "") != wantDesc {
					delete(topo.Interfaces, id)
				}
			}
		}
		if topo.ExternalVars != nil {
			for id, v := range topo.ExternalVars {
				if (v.Description != "") != wantDesc {
					delete(topo.ExternalVars, id)
				}
			}
		}
		if topo.Files != nil {
			for id, f := range topo.Files {
				if (f.Description != "") != wantDesc {
					delete(topo.Files, id)
				}
			}
		}
		if topo.Packages != nil {
			for id, pkg := range topo.Packages {
				if (pkg.Description != "") != wantDesc {
					delete(topo.Packages, id)
				}
			}
		}
	}
	return topo, nil
}

// Write copies the topology database file from the current dbPath to the given path.
func (m *TopologyManager) Write(path string) error {
	if m.dbPath == "" {
		return nil
	}
	src, err := os.Open(m.dbPath)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.Create(path)
	if err != nil {
		return err
	}
	defer dst.Close()
	_, err = io.Copy(dst, src)
	return err
}

// UpdateFile re-parses the specified .go file, updates all corresponding
// records in the SQLite database in-place, and returns a list of warnings
// for any removed or signature-changed functions that may need manual
// attention. Descriptions from the previous topology are preserved when
// the new source omits doc comments.
func (m *TopologyManager) UpdateFile(path string) []domain.TopologyWarning {
	topo, err := helper.ReadDb(m.dbPath)
	if err != nil {
		return nil
	}

	warnings := scanner.UpdateFileInTopology(topo, path, topo.Root)

	if err := helper.WriteDb(topo, m.dbPath); err != nil {
		return nil
	}

	return warnings
}

// Cut reads the source file at the given Location and returns a CodeEntry
// containing the raw source lines between StartsAt and EndsAt (inclusive).
func (m *TopologyManager) Cut(loc domain.Location) (*domain.CodeEntry, error) {
	data, err := os.ReadFile(string(loc.Path))
	if err != nil {
		return nil, fmt.Errorf("read file %s: %w", loc.Path, err)
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if loc.StartsAt < 1 || loc.StartsAt > len(lines) {
		return nil, fmt.Errorf("StartsAt %d out of range (1-%d)", loc.StartsAt, len(lines))
	}
	if loc.EndsAt > len(lines) {
		return nil, fmt.Errorf("EndsAt %d out of range (max %d)", loc.EndsAt, len(lines))
	}
	cut := strings.Join(lines[loc.StartsAt-1:loc.EndsAt], "\n")
	return &domain.CodeEntry{Location: loc, Cut: cut}, nil
}

// ReadFunction returns full contextual information for the function
// identified by the given FunctionID string. This includes the function's
// full source cut, its parent struct (if a method), simplified views of
// all called functions, struct usages, interface usages, external variable
// references, and a flat block list in file-line order for proximity-based
// rendering. WithResourceFilter can be passed to limit which resource
// categories appear in the response.
func (m *TopologyManager) ReadFunction(id string, opts ...TopologyOption) (*domain.FunctionContext, error) {
	opt := &TopologyOptions{}
	for _, o := range opts {
		o(opt)
	}

	topo, err := helper.ReadDb(m.dbPath)
	if err != nil {
		return nil, err
	}

	funcID := domain.FunctionID(id)
	fn, ok := topo.Functions[funcID]
	if !ok {
		return nil, fmt.Errorf("function %s not found in topology", id)
	}

	ctx := &domain.FunctionContext{}

	funcCut, err := m.Cut(fn.Loc)
	if err != nil {
		return nil, err
	}
	ctx.Function = &domain.FunctionCut{Function: fn, Cut: funcCut.Cut}

	if opt.hasResource(domain.STRUCT_RESOURCE) && fn.MethodFrom != nil {
		if parent, ok := topo.Struct[*fn.MethodFrom]; ok {
			parentCut, err := m.Cut(parent.Loc)
			if err != nil {
				return nil, err
			}
			ctx.ParentStruct = &domain.StructCut{Struct: parent, Cut: parentCut.Cut}
		}
	}

	if opt.hasResource(domain.FUNCTION_RESOURCE) {
		for _, calledID := range fn.FunctionsUsed {
			called, ok := topo.Functions[calledID]
			if !ok || called.MethodFrom != nil {
				continue
			}
			ctx.CalledFunctions = append(ctx.CalledFunctions, domain.SimplifiedFunction{
				ID:          called.ID,
				Name:        called.Name,
				Description: called.Description,
				Input:       called.Input,
				Output:      called.Output,
				Location:    called.Loc,
			})
		}
	}

	if opt.hasResource(domain.STRUCT_RESOURCE) {
		for _, structID := range fn.StructsUsed {
			s, ok := topo.Struct[structID]
			if !ok {
				continue
			}
			usage := domain.StructUsage{
				ID:          s.ID,
				Name:        s.Name,
				Description: s.Description,
				Location:    s.Loc,
			}
			for _, calledID := range fn.FunctionsUsed {
				called, ok := topo.Functions[calledID]
				if !ok {
					continue
				}
				if called.MethodFrom != nil && *called.MethodFrom == structID {
					usage.Methods = append(usage.Methods, domain.SimplifiedFunction{
						ID:          called.ID,
						Name:        called.Name,
						Description: called.Description,
						Input:       called.Input,
						Output:      called.Output,
						Location:    called.Loc,
					})
				}
			}
			ctx.StructsUsed = append(ctx.StructsUsed, usage)
		}
	}

	if opt.hasResource(domain.INTERFACE_RESOURCE) {
		for _, ifaceID := range fn.InterfacesUsed {
			iface, ok := topo.Interfaces[ifaceID]
			if !ok {
				continue
			}
			usage := domain.InterfaceUsage{
				ID:          iface.ID,
				Name:        iface.Name,
				Description: iface.Description,
				Location:    iface.Loc,
			}
			for _, implStructID := range iface.ImplementedBy {
				implStruct, ok := topo.Struct[implStructID]
				if !ok {
					continue
				}
				impl := domain.InterfaceImplementation{
					StructID:    implStruct.ID,
					Name:        implStruct.Name,
					Description: implStruct.Description,
					Location:    implStruct.Loc,
				}
				for _, calledID := range fn.FunctionsUsed {
					called, ok := topo.Functions[calledID]
					if !ok {
						continue
					}
					if called.MethodFrom != nil && *called.MethodFrom == implStructID {
						impl.Methods = append(impl.Methods, domain.SimplifiedFunction{
							ID:          called.ID,
							Name:        called.Name,
							Description: called.Description,
							Input:       called.Input,
							Output:      called.Output,
							Location:    called.Loc,
						})
					}
				}
				usage.Implementations = append(usage.Implementations, impl)
			}
			ctx.InterfacesUsed = append(ctx.InterfacesUsed, usage)
		}
	}

	if opt.hasResource(domain.EXTERNAL_VAR_RESOURCE) {
		const maxValueLen = 500
		for _, varID := range fn.ExternalVarsUsed {
			v, ok := topo.ExternalVars[varID]
			if !ok {
				continue
			}
			sv := domain.SimplifiedExtVar{
				ID:          v.ID,
				Name:        v.Name,
				Description: v.Description,
				Location:    v.Location,
			}
			if v.Value != nil {
				b, _ := json.Marshal(*v.Value)
				valStr := string(b)
				if len(valStr) > maxValueLen {
					valStr = valStr[:maxValueLen] + "..."
				}
				sv.Value = valStr
			}
			ctx.ExtVarsUsed = append(ctx.ExtVarsUsed, sv)
		}
	}

	if opt.hasResource(domain.DEPENDENCY_RESOURCE) {
		depSet := make(map[domain.DependancyPath]bool)
		for _, d := range fn.DependanciesUsed {
			depSet[d] = true
		}
		if fn.MethodFrom != nil {
			if parent, ok := topo.Struct[*fn.MethodFrom]; ok {
				for _, d := range parent.DependanciesUsed {
					depSet[d] = true
				}
			}
		}
		for d := range depSet {
			ctx.Dependencies = append(ctx.Dependencies, d)
		}
	}

	if opt.hasResource(domain.PACKAGE_RESOURCE) {
		pkgSet := make(map[domain.PackagePath]bool)
		for _, p := range fn.PackagesUsed {
			pkgSet[p] = true
		}
		if fn.MethodFrom != nil {
			if parent, ok := topo.Struct[*fn.MethodFrom]; ok {
				for _, p := range parent.PackagesUsed {
					pkgSet[p] = true
				}
			}
		}
		for p := range pkgSet {
			ctx.PackagesUsed = append(ctx.PackagesUsed, p)
		}
	}

	var blocks []domain.ContextBlock

	blocks = append(blocks, domain.ContextBlock{
		Kind: "function", FileID: fn.Loc.Path, Line: fn.Loc.StartsAt,
		Title: fmt.Sprintf("func %s", fn.Name), Cut: funcCut.Cut,
	})

	if ctx.ParentStruct != nil {
		blocks = append(blocks, domain.ContextBlock{
			Kind: "parent_struct", FileID: ctx.ParentStruct.Loc.Path,
			Line:  ctx.ParentStruct.Loc.StartsAt,
			Title: fmt.Sprintf("struct %s", ctx.ParentStruct.Name),
			Cut:   ctx.ParentStruct.Cut,
		})
	}

	for _, cf := range ctx.CalledFunctions {
		blocks = append(blocks, domain.ContextBlock{
			Kind: "called_func", FileID: cf.Location.Path,
			Line: cf.Location.StartsAt, Title: fmt.Sprintf("func %s", cf.Name),
		})
	}

	for _, su := range ctx.StructsUsed {
		blocks = append(blocks, domain.ContextBlock{
			Kind: "struct", FileID: su.Location.Path,
			Line: su.Location.StartsAt, Title: fmt.Sprintf("struct %s", su.Name),
		})
		for _, sm := range su.Methods {
			blocks = append(blocks, domain.ContextBlock{
				Kind: "struct_method", FileID: sm.Location.Path,
				Line: sm.Location.StartsAt, Title: fmt.Sprintf("%s.%s", su.Name, sm.Name),
			})
		}
	}

	for _, iu := range ctx.InterfacesUsed {
		blocks = append(blocks, domain.ContextBlock{
			Kind: "interface", FileID: iu.Location.Path,
			Line: iu.Location.StartsAt, Title: fmt.Sprintf("interface %s", iu.Name),
		})
		for _, impl := range iu.Implementations {
			blocks = append(blocks, domain.ContextBlock{
				Kind: "interface_impl", FileID: impl.Location.Path,
				Line: impl.Location.StartsAt, Title: fmt.Sprintf("struct %s", impl.Name),
			})
			for _, m := range impl.Methods {
				blocks = append(blocks, domain.ContextBlock{
					Kind: "impl_method", FileID: m.Location.Path,
					Line: m.Location.StartsAt, Title: fmt.Sprintf("%s.%s", impl.Name, m.Name),
				})
			}
		}
	}

	for _, ev := range ctx.ExtVarsUsed {
		blocks = append(blocks, domain.ContextBlock{
			Kind: "extvar", FileID: ev.Location.Path,
			Line: ev.Location.StartsAt, Title: fmt.Sprintf("var %s", ev.Name),
		})
	}

	sort.SliceStable(blocks, func(i, j int) bool {
		if blocks[i].FileID != blocks[j].FileID {
			return blocks[i].FileID < blocks[j].FileID
		}
		return blocks[i].Line < blocks[j].Line
	})

	ctx.Blocks = blocks

	return ctx, nil
}

// ReadStruct returns full contextual information for the struct identified by
// the given StructID string. This includes the struct's full source cut, its
// constructor (if one exists), interfaces it implements (with need-to-implement
// flags), its methods, and all "used" references (structs, interfaces, ext vars,
// dependencies, packages) computed as the union of the struct's own references
// and its constructor's references. WithResourceFilter can be passed to limit
// which resource categories appear in the response.
func (m *TopologyManager) ReadStruct(id string, opts ...TopologyOption) (*domain.StructContext, error) {
	opt := &TopologyOptions{}
	for _, o := range opts {
		o(opt)
	}

	topo, err := helper.ReadDb(m.dbPath)
	if err != nil {
		return nil, err
	}

	structID := domain.StructID(id)
	s, ok := topo.Struct[structID]
	if !ok {
		return nil, fmt.Errorf("struct %s not found in topology", id)
	}

	ctx := &domain.StructContext{}

	structCut, err := m.Cut(s.Loc)
	if err != nil {
		return nil, err
	}
	ctx.Struct = &domain.StructCut{Struct: s, Cut: structCut.Cut}

	actualMethodIDs := collectStructMethodIDs(topo, structID)

	var constructorFunc *domain.Function
	if opt.hasResource(domain.FUNCTION_RESOURCE) && s.Constructor != nil {
		if cfn, ok := topo.Functions[*s.Constructor]; ok {
			constructorFunc = &cfn
			cCut, err := m.Cut(cfn.Loc)
			if err != nil {
				return nil, err
			}
			ctx.Constructor = &domain.FunctionCut{Function: cfn, Cut: cCut.Cut}
		}
	}

	constructorStructRefs := make(map[domain.StructID]bool)
	constructorIfaceRefs := make(map[domain.InterfaceID]bool)
	constructorExtVarRefs := make(map[domain.ExternalVarID]bool)
	constructorDepRefs := make(map[domain.DependancyPath]bool)
	constructorPkgRefs := make(map[domain.PackagePath]bool)
	if constructorFunc != nil {
		for _, sid := range constructorFunc.StructsUsed {
			constructorStructRefs[sid] = true
		}
		for _, iid := range constructorFunc.InterfacesUsed {
			constructorIfaceRefs[iid] = true
		}
		for _, evid := range constructorFunc.ExternalVarsUsed {
			constructorExtVarRefs[evid] = true
		}
		for _, d := range constructorFunc.DependanciesUsed {
			constructorDepRefs[d] = true
		}
		for _, p := range constructorFunc.PackagesUsed {
			constructorPkgRefs[p] = true
		}
	}

	// ── Interfaces the struct implements ──
	if opt.hasResource(domain.INTERFACE_RESOURCE) {
		for _, iface := range topo.Interfaces {
			implements := false
			for _, implStructID := range iface.ImplementedBy {
				if implStructID == structID {
					implements = true
					break
				}
			}
			if !implements {
				continue
			}

			needToImplement := false
			for _, reqMethod := range iface.Methods {
				found := false
				for _, actualID := range actualMethodIDs {
					if actualFn, ok := topo.Functions[actualID]; ok && actualFn.Name == reqMethod.Name {
						found = true
						break
					}
				}
				if !found {
					needToImplement = true
					break
				}
			}

			ctx.Interfaces = append(ctx.Interfaces, domain.SimplifiedInterface{
				ID:              iface.ID,
				Name:            iface.Name,
				Description:     iface.Description,
				Location:        iface.Loc,
				NeedToImplement: needToImplement,
			})
		}
	}

	// ── Methods of the struct ──
	if opt.hasResource(domain.FUNCTION_RESOURCE) {
		for _, methodID := range actualMethodIDs {
			if fn, ok := topo.Functions[methodID]; ok {
				ctx.Methods = append(ctx.Methods, domain.SimplifiedFunction{
					ID:          fn.ID,
					Name:        fn.Name,
					Description: fn.Description,
					Input:       fn.Input,
					Output:      fn.Output,
					Location:    fn.Loc,
				})
			}
		}
	}

	// ── StructsUsed (from constructor) ──
	if opt.hasResource(domain.STRUCT_RESOURCE) {
		for refStructID := range constructorStructRefs {
			su, ok := topo.Struct[refStructID]
			if !ok {
				continue
			}
			usage := domain.StructUsage{
				ID:          su.ID,
				Name:        su.Name,
				Description: su.Description,
				Location:    su.Loc,
			}
			for _, calledID := range constructorFunc.FunctionsUsed {
				called, ok := topo.Functions[calledID]
				if !ok {
					continue
				}
				if called.MethodFrom != nil && *called.MethodFrom == refStructID {
					usage.Methods = append(usage.Methods, domain.SimplifiedFunction{
						ID:          called.ID,
						Name:        called.Name,
						Description: called.Description,
						Input:       called.Input,
						Output:      called.Output,
						Location:    called.Loc,
					})
				}
			}
			ctx.StructsUsed = append(ctx.StructsUsed, usage)
		}
	}

	// ── InterfacesUsed (from constructor) ──
	if opt.hasResource(domain.INTERFACE_RESOURCE) {
		for refIfaceID := range constructorIfaceRefs {
			iface, ok := topo.Interfaces[refIfaceID]
			if !ok {
				continue
			}
			usage := domain.InterfaceUsage{
				ID:          iface.ID,
				Name:        iface.Name,
				Description: iface.Description,
				Location:    iface.Loc,
			}
			for _, implStructID := range iface.ImplementedBy {
				implStruct, ok := topo.Struct[implStructID]
				if !ok {
					continue
				}
				impl := domain.InterfaceImplementation{
					StructID:    implStruct.ID,
					Name:        implStruct.Name,
					Description: implStruct.Description,
					Location:    implStruct.Loc,
				}
				for _, calledID := range constructorFunc.FunctionsUsed {
					called, ok := topo.Functions[calledID]
					if !ok {
						continue
					}
					if called.MethodFrom != nil && *called.MethodFrom == implStructID {
						impl.Methods = append(impl.Methods, domain.SimplifiedFunction{
							ID:          called.ID,
							Name:        called.Name,
							Description: called.Description,
							Input:       called.Input,
							Output:      called.Output,
							Location:    called.Loc,
						})
					}
				}
				usage.Implementations = append(usage.Implementations, impl)
			}
			ctx.InterfacesUsed = append(ctx.InterfacesUsed, usage)
		}
	}

	// ── ExtVarsUsed (from constructor) ──
	if opt.hasResource(domain.EXTERNAL_VAR_RESOURCE) {
		const maxValueLen = 500
		for refExtVarID := range constructorExtVarRefs {
			v, ok := topo.ExternalVars[refExtVarID]
			if !ok {
				continue
			}
			sv := domain.SimplifiedExtVar{
				ID:          v.ID,
				Name:        v.Name,
				Description: v.Description,
				Location:    v.Location,
			}
			if v.Value != nil {
				b, _ := json.Marshal(*v.Value)
				valStr := string(b)
				if len(valStr) > maxValueLen {
					valStr = valStr[:maxValueLen] + "..."
				}
				sv.Value = valStr
			}
			ctx.ExtVarsUsed = append(ctx.ExtVarsUsed, sv)
		}
	}

	// ── Dependencies (union) ──
	if opt.hasResource(domain.DEPENDENCY_RESOURCE) {
		depSet := make(map[domain.DependancyPath]bool)
		for _, d := range s.DependanciesUsed {
			depSet[d] = true
		}
		for d := range constructorDepRefs {
			depSet[d] = true
		}
		for d := range depSet {
			ctx.Dependencies = append(ctx.Dependencies, d)
		}
	}

	// ── PackagesUsed (union) ──
	if opt.hasResource(domain.PACKAGE_RESOURCE) {
		pkgSet := make(map[domain.PackagePath]bool)
		for _, p := range s.PackagesUsed {
			pkgSet[p] = true
		}
		for p := range constructorPkgRefs {
			pkgSet[p] = true
		}
		for p := range pkgSet {
			ctx.PackagesUsed = append(ctx.PackagesUsed, p)
		}
	}

	// ── Blocks ──
	var blocks []domain.ContextBlock

	blocks = append(blocks, domain.ContextBlock{
		Kind: "struct", FileID: s.Loc.Path, Line: s.Loc.StartsAt,
		Title: fmt.Sprintf("struct %s", s.Name), Cut: structCut.Cut,
	})

	if ctx.Constructor != nil {
		blocks = append(blocks, domain.ContextBlock{
			Kind: "constructor", FileID: ctx.Constructor.Loc.Path,
			Line:  ctx.Constructor.Loc.StartsAt,
			Title: fmt.Sprintf("func %s", ctx.Constructor.Name),
			Cut:   ctx.Constructor.Cut,
		})
	}

	for _, iface := range ctx.Interfaces {
		blocks = append(blocks, domain.ContextBlock{
			Kind: "interface", FileID: iface.Location.Path,
			Line: iface.Location.StartsAt, Title: fmt.Sprintf("interface %s", iface.Name),
		})
	}

	for _, m := range ctx.Methods {
		blocks = append(blocks, domain.ContextBlock{
			Kind: "method", FileID: m.Location.Path,
			Line: m.Location.StartsAt, Title: fmt.Sprintf("%s.%s", s.Name, m.Name),
		})
	}

	for _, su := range ctx.StructsUsed {
		blocks = append(blocks, domain.ContextBlock{
			Kind: "struct", FileID: su.Location.Path,
			Line: su.Location.StartsAt, Title: fmt.Sprintf("struct %s", su.Name),
		})
		for _, sm := range su.Methods {
			blocks = append(blocks, domain.ContextBlock{
				Kind: "struct_method", FileID: sm.Location.Path,
				Line: sm.Location.StartsAt, Title: fmt.Sprintf("%s.%s", su.Name, sm.Name),
			})
		}
	}

	for _, iu := range ctx.InterfacesUsed {
		blocks = append(blocks, domain.ContextBlock{
			Kind: "interface", FileID: iu.Location.Path,
			Line: iu.Location.StartsAt, Title: fmt.Sprintf("interface %s", iu.Name),
		})
		for _, impl := range iu.Implementations {
			blocks = append(blocks, domain.ContextBlock{
				Kind: "interface_impl", FileID: impl.Location.Path,
				Line: impl.Location.StartsAt, Title: fmt.Sprintf("struct %s", impl.Name),
			})
			for _, m := range impl.Methods {
				blocks = append(blocks, domain.ContextBlock{
					Kind: "impl_method", FileID: m.Location.Path,
					Line: m.Location.StartsAt, Title: fmt.Sprintf("%s.%s", impl.Name, m.Name),
				})
			}
		}
	}

	for _, ev := range ctx.ExtVarsUsed {
		blocks = append(blocks, domain.ContextBlock{
			Kind: "extvar", FileID: ev.Location.Path,
			Line: ev.Location.StartsAt, Title: fmt.Sprintf("var %s", ev.Name),
		})
	}

	sort.SliceStable(blocks, func(i, j int) bool {
		if blocks[i].FileID != blocks[j].FileID {
			return blocks[i].FileID < blocks[j].FileID
		}
		return blocks[i].Line < blocks[j].Line
	})

	ctx.Blocks = blocks

	return ctx, nil
}

func collectStructMethodIDs(topo *domain.Topology, structID domain.StructID) []domain.FunctionID {
	var ids []domain.FunctionID
	for id, f := range topo.Functions {
		if f.MethodFrom != nil && *f.MethodFrom == structID {
			ids = append(ids, id)
		}
	}
	return ids
}

func (m *TopologyManager) FindFunctionsByName(name string) ([]domain.FunctionID, error) {
	topo, err := helper.ReadDb(m.dbPath)
	if err != nil {
		return nil, err
	}
	var results []domain.FunctionID
	for id, fn := range topo.Functions {
		if fn.Name == name {
			results = append(results, id)
		}
	}
	return results, nil
}

func (m *TopologyManager) FindStructsByName(name string) ([]domain.StructID, error) {
	topo, err := helper.ReadDb(m.dbPath)
	if err != nil {
		return nil, err
	}
	var results []domain.StructID
	for id, s := range topo.Struct {
		if s.Name == name {
			results = append(results, id)
		}
	}
	return results, nil
}

func (m *TopologyManager) UpdateDescription(id string, resourceName domain.ResourceName, description string) error {
	return helper.UpdateDescription(m.dbPath, resourceName, id, description)
}

func (m *TopologyManager) ReadResourceAndCut(id string, resourceName domain.ResourceName) (*domain.CodeEntry, error) {
	topo, err := helper.ReadDb(m.dbPath)
	if err != nil {
		return nil, err
	}

	switch resourceName {
	case domain.FUNCTION_RESOURCE:
		fn, ok := topo.Functions[domain.FunctionID(id)]
		if !ok {
			return nil, fmt.Errorf("function not found: %s", id)
		}
		return m.Cut(fn.Loc)
	case domain.STRUCT_RESOURCE:
		s, ok := topo.Struct[domain.StructID(id)]
		if !ok {
			return nil, fmt.Errorf("struct not found: %s", id)
		}
		return m.Cut(s.Loc)
	case domain.INTERFACE_RESOURCE:
		iface, ok := topo.Interfaces[domain.InterfaceID(id)]
		if !ok {
			return nil, fmt.Errorf("interface not found: %s", id)
		}
		return m.Cut(iface.Loc)
	case domain.EXTERNAL_VAR_RESOURCE:
		v, ok := topo.ExternalVars[domain.ExternalVarID(id)]
		if !ok {
			return nil, fmt.Errorf("external var not found: %s", id)
		}
		return m.Cut(v.Location)
	case domain.FILE_RESOURCE:
		return nil, fmt.Errorf("ReadResourceAndCut not supported for File")
	default:
		return nil, fmt.Errorf("unknown resource: %s", resourceName)
	}
}
