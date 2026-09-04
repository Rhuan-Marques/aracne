package golang

import (
	"encoding/json"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// Converts a generic domain topology to a Go-specific topology by mapping resources into typed collections.
func FromGeneric(topo *domain.Topology) *GolangTopology {
	if topo == nil {
		return nil
	}
	gt := &GolangTopology{
		Root:         topo.Root,
		Functions:    make(map[FunctionID]GolangFunction),
		Structs:      make(map[StructID]GolangStruct),
		Interfaces:   make(map[InterfaceID]GolangInterface),
		NamedTypes:   make(map[NamedTypeID]GolangNamedType),
		ExternalVars: make(map[ExternalVarID]GolangExternalVar),
		Files:        make(map[FileID]GolangFile),
		Packages:     make(map[PackagePath]GolangPackage),
		Warnings:     topo.Warnings,
		Errors:       topo.Errors,
	}

	for id, res := range topo.Resources {
		if res.Language != "" && res.Language != "go" {
			continue
		}
		switch res.Kind {
		case domain.ResourceFunction, domain.ResourceMethod:
			f := GolangFunction{
				ID:          FunctionID(id),
				Name:        res.Name,
				Description: res.Description,
				Loc:         res.Location,
				Connections: mapKindConn(res.Connections),
			}
			if input, ok := res.Properties["input"]; ok {
				jsonConvert(input, &f.Input)
			}
			if output, ok := res.Properties["output"]; ok {
				jsonConvert(output, &f.Output)
			}
			if mf, ok := res.Properties["method_from"]; ok && mf != nil {
				if s, ok := mf.(string); ok {
					sid := StructID(s)
					f.MethodFrom = &sid
				}
			}
			gt.Functions[f.ID] = f

		case domain.ResourceStruct:
			s := GolangStruct{
				ID:          StructID(id),
				Name:        res.Name,
				Description: res.Description,
				Loc:         res.Location,
				Connections: mapKindConn(res.Connections),
			}
			if params, ok := res.Properties["params"]; ok {
				jsonConvert(params, &s.Params)
			}
			if ctor, ok := res.Properties["constructor"]; ok && ctor != nil {
				if c, ok := ctor.(string); ok {
					cid := FunctionID(c)
					s.Constructor = &cid
				}
			}
			gt.Structs[s.ID] = s

		case domain.ResourceNamedType:
			n := GolangNamedType{
				ID:          NamedTypeID(id),
				Name:        res.Name,
				Description: res.Description,
				Loc:         res.Location,
				Connections: mapKindConn(res.Connections),
			}
			if underlying, ok := res.Properties["underlying"]; ok {
				if u, ok := underlying.(string); ok {
					n.Underlying = u
				}
			}
			gt.NamedTypes[n.ID] = n

		case domain.ResourceInterface:
			iface := GolangInterface{
				ID:          InterfaceID(id),
				Name:        res.Name,
				Description: res.Description,
				Loc:         res.Location,
				Connections: mapKindConn(res.Connections),
			}
			if methods, ok := res.Properties["methods"]; ok {
				jsonConvert(methods, &iface.Methods)
			}
			gt.Interfaces[iface.ID] = iface

		case domain.ResourceVariable:
			v := GolangExternalVar{
				ID:          ExternalVarID(id),
				Name:        res.Name,
				Description: res.Description,
			}
			if typing, ok := res.Properties["typing"]; ok {
				if t, ok := typing.(string); ok {
					v.Typing = t
				}
			}
			if val, ok := res.Properties["value"]; ok {
				var x any
				jsonConvert(val, &x)
				v.Value = &x
			}
			v.Location = res.Location
			gt.ExternalVars[v.ID] = v

		case domain.ResourceFile:
			f := GolangFile{
				ID:          FileID(id),
				Name:        res.Name,
				Description: res.Description,
				Connections: mapKindConn(res.Connections),
			}
			if fp, ok := res.Properties["from_package"]; ok {
				if p, ok := fp.(string); ok {
					f.FromPackage = PackagePath(p)
				}
			}
			gt.Files[f.ID] = f

		case domain.ResourcePackage:
			p := GolangPackage{
				Path:        PackagePath(id),
				Description: res.Description,
				Connections: mapKindConn(res.Connections),
			}
			gt.Packages[p.Path] = p

		case domain.ResourceDependency:
			gt.Dependencies = append(gt.Dependencies, Dependancy{
				PackagePath: DependancyPath(id),
			})
		}
	}

	return gt
}

// Converts a GolangTopology to a language-agnostic domain.Topology for cross-language compatibility.
func ToGeneric(gt *GolangTopology) *domain.Topology {
	if gt == nil {
		return nil
	}
	topo := &domain.Topology{
		Root:      gt.Root,
		Language:  "go",
		Languages: []string{"go"},
		Resources: make(map[string]domain.Resource),
		Warnings:  gt.Warnings,
		Errors:    gt.Errors,
	}

	for id, fn := range gt.Functions {
		props := map[string]any{
			"input":  fn.Input,
			"output": fn.Output,
		}
		if fn.MethodFrom != nil {
			props["method_from"] = string(*fn.MethodFrom)
		}
		kind := domain.ResourceFunction
		if fn.MethodFrom != nil {
			kind = domain.ResourceMethod
		}
		topo.Resources[string(id)] = domain.Resource{
			ID:          string(id),
			Kind:        kind,
			Name:        fn.Name,
			Description: fn.Description,
			Location:    fn.Loc,
			Properties:  props,
			Connections: stringMapConn(fn.Connections),
		}
	}

	for id, s := range gt.Structs {
		props := map[string]any{
			"params": s.Params,
		}
		if s.Constructor != nil {
			props["constructor"] = string(*s.Constructor)
		}
		topo.Resources[string(id)] = domain.Resource{
			ID:          string(id),
			Kind:        domain.ResourceStruct,
			Name:        s.Name,
			Description: s.Description,
			Location:    s.Loc,
			Properties:  props,
			Connections: stringMapConn(s.Connections),
		}
	}

	for id, n := range gt.NamedTypes {
		props := map[string]any{
			"underlying": n.Underlying,
		}
		topo.Resources[string(id)] = domain.Resource{
			ID:          string(id),
			Kind:        domain.ResourceNamedType,
			Name:        n.Name,
			Description: n.Description,
			Location:    n.Loc,
			Properties:  props,
			Connections: stringMapConn(n.Connections),
		}
	}

	for id, iface := range gt.Interfaces {
		topo.Resources[string(id)] = domain.Resource{
			ID:          string(id),
			Kind:        domain.ResourceInterface,
			Name:        iface.Name,
			Description: iface.Description,
			Location:    iface.Loc,
			Properties: map[string]any{
				"methods": iface.Methods,
			},
			Connections: stringMapConn(iface.Connections),
		}
	}

	for id, v := range gt.ExternalVars {
		props := map[string]any{
			"typing": v.Typing,
		}
		if v.Value != nil {
			props["value"] = v.Value
		}
		topo.Resources[string(id)] = domain.Resource{
			ID:          string(id),
			Kind:        domain.ResourceVariable,
			Name:        v.Name,
			Description: v.Description,
			Location:    v.Location,
			Properties:  props,
			Connections: make(map[string][]string),
		}
	}

	for id, f := range gt.Files {
		props := map[string]any{
			"from_package": string(f.FromPackage),
		}
		topo.Resources[string(id)] = domain.Resource{
			ID:          string(id),
			Kind:        domain.ResourceFile,
			Name:        f.Name,
			Description: f.Description,
			Properties:  props,
			Connections: stringMapConn(f.Connections),
		}
	}

	for id, p := range gt.Packages {
		topo.Resources[string(id)] = domain.Resource{
			ID:          string(id),
			Kind:        domain.ResourcePackage,
			Name:        string(p.Path),
			Description: p.Description,
			Connections: stringMapConn(p.Connections),
		}
	}

	for _, d := range gt.Dependencies {
		did := string(d.PackagePath)
		if _, exists := topo.Resources[did]; !exists {
			topo.Resources[did] = domain.Resource{
				ID:   did,
				Kind: domain.ResourceDependency,
				Name: did,
			}
		}
	}

	for id, res := range topo.Resources {
		res.Language = "go"
		topo.Resources[id] = res
	}

	return topo
}

// Converts string-keyed connection map to ConnectionKind-keyed map.
func mapKindConn(conns map[string][]string) map[ConnectionKind][]string {
	if conns == nil {
		return make(map[ConnectionKind][]string)
	}
	result := make(map[ConnectionKind][]string, len(conns))
	for k, v := range conns {
		result[ConnectionKind(k)] = v
	}
	return result
}

// Converts between types by marshaling to JSON and unmarshaling to target.
func jsonConvert(from any, to any) {
	b, err := json.Marshal(from)
	if err != nil {
		return
	}
	json.Unmarshal(b, to)
}

// Converts a map of ConnectionKind to string slices into a map of string keys to string slices.
func stringMapConn(conns map[ConnectionKind][]string) map[string][]string {
	if conns == nil {
		return make(map[string][]string)
	}
	result := make(map[string][]string, len(conns))
	for k, v := range conns {
		result[string(k)] = v
	}
	return result
}
