package golang

import (
	"encoding/json"

	"llm-topology/internal/topology/domain"
)

func FromGeneric(topo *domain.Topology) *GolangTopology {
	if topo == nil {
		return nil
	}
	gt := &GolangTopology{
		Root:         topo.Root,
		Functions:    make(map[FunctionID]GolangFunction),
		Structs:      make(map[StructID]GolangStruct),
		Interfaces:   make(map[InterfaceID]GolangInterface),
		ExternalVars: make(map[ExternalVarID]GolangExternalVar),
		Files:        make(map[FileID]GolangFile),
		Packages:     make(map[PackagePath]GolangPackage),
		Errors:       topo.Errors,
	}

	for id, res := range topo.Resources {
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
				sid := StructID(mf.(string))
				f.MethodFrom = &sid
			}
			gt.Functions[f.ID] = f

		case domain.ResourceType:
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
				cid := FunctionID(ctor.(string))
				s.Constructor = &cid
			}
			gt.Structs[s.ID] = s

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
				v.Typing = typing.(string)
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
				f.FromPackage = PackagePath(fp.(string))
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

func ToGeneric(gt *GolangTopology) *domain.Topology {
	if gt == nil {
		return nil
	}
	topo := &domain.Topology{
		Root:      gt.Root,
		Language:  "go",
		Resources: make(map[string]domain.Resource),
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
			Kind:        domain.ResourceType,
			Name:        s.Name,
			Description: s.Description,
			Location:    s.Loc,
			Properties:  props,
			Connections: stringMapConn(s.Connections),
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

	return topo
}

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

func jsonConvert(from any, to any) {
	b, err := json.Marshal(from)
	if err != nil {
		return
	}
	json.Unmarshal(b, to)
}

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
