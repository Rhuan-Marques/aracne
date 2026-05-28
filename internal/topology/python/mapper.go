package python

import (
	"encoding/json"

	"llm-topology/internal/topology/domain"
)

func FromGeneric(topo *domain.Topology) *PythonTopology {
	if topo == nil {
		return nil
	}
	gt := &PythonTopology{
		Root:         topo.Root,
		Functions:    make(map[FunctionID]PythonFunction),
		Classes:      make(map[ClassID]PythonClass),
		ExternalVars: make(map[ExternalVarID]PythonExternalVar),
		Modules:      make(map[ModuleID]PythonModule),
		Packages:     make(map[PackagePath]PythonPackage),
		Errors:       topo.Errors,
	}

	for id, res := range topo.Resources {
		switch res.Kind {
		case domain.ResourceFunction, domain.ResourceMethod:
			f := PythonFunction{
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
			if dec, ok := res.Properties["decorators"]; ok {
				jsonConvert(dec, &f.Decorators)
			}
			if ia, ok := res.Properties["is_async"]; ok && ia != nil {
				f.IsAsync = ia.(bool)
			}
			if mf, ok := res.Properties["method_from"]; ok && mf != nil {
				cid := ClassID(mf.(string))
				f.MethodFrom = &cid
			}
			gt.Functions[f.ID] = f

		case domain.ResourceType:
			c := PythonClass{
				ID:          ClassID(id),
				Name:        res.Name,
				Description: res.Description,
				Loc:         res.Location,
				Connections: mapKindConn(res.Connections),
			}
			if params, ok := res.Properties["params"]; ok {
				jsonConvert(params, &c.Params)
			}
			if bases, ok := res.Properties["bases"]; ok {
				jsonConvert(bases, &c.Bases)
			}
			if ctor, ok := res.Properties["constructor"]; ok && ctor != nil {
				cid := FunctionID(ctor.(string))
				c.Constructor = &cid
			}
			if ia, ok := res.Properties["is_abc"]; ok && ia != nil {
				c.IsABC = ia.(bool)
			}
			if ip, ok := res.Properties["is_protocol"]; ok && ip != nil {
				c.IsProtocol = ip.(bool)
			}
			if ham, ok := res.Properties["has_abstract_methods"]; ok && ham != nil {
				c.HasAbstractMethods = ham.(bool)
			}
			gt.Classes[c.ID] = c

		case domain.ResourceVariable:
			v := PythonExternalVar{
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
			mod := PythonModule{
				ID:          ModuleID(id),
				Name:        res.Name,
				Description: res.Description,
				Connections: mapKindConn(res.Connections),
			}
			if fp, ok := res.Properties["from_package"]; ok {
				mod.FromPackage = PackagePath(fp.(string))
			}
			gt.Modules[mod.ID] = mod

		case domain.ResourcePackage:
			p := PythonPackage{
				Path:        PackagePath(id),
				Description: res.Description,
				Connections: mapKindConn(res.Connections),
			}
			gt.Packages[p.Path] = p

		case domain.ResourceDependency:
			gt.Dependencies = append(gt.Dependencies, PythonDependancy{
				PackagePath: DependancyPath(id),
			})
		}
	}

	return gt
}

func ToGeneric(gt *PythonTopology) *domain.Topology {
	if gt == nil {
		return nil
	}
	topo := &domain.Topology{
		Root:      gt.Root,
		Language:  "python",
		Resources: make(map[string]domain.Resource),
		Errors:    gt.Errors,
	}

	for id, fn := range gt.Functions {
		props := map[string]any{
			"input":      fn.Input,
			"output":     fn.Output,
			"decorators": fn.Decorators,
			"is_async":   fn.IsAsync,
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

	for id, c := range gt.Classes {
		props := map[string]any{
			"params":               c.Params,
			"bases":                c.Bases,
			"is_abc":               c.IsABC,
			"is_protocol":          c.IsProtocol,
			"has_abstract_methods": c.HasAbstractMethods,
		}
		if c.Constructor != nil {
			props["constructor"] = string(*c.Constructor)
		}
		topo.Resources[string(id)] = domain.Resource{
			ID:          string(id),
			Kind:        domain.ResourceType,
			Name:        c.Name,
			Description: c.Description,
			Location:    c.Loc,
			Properties:  props,
			Connections: stringMapConn(c.Connections),
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

	for id, m := range gt.Modules {
		props := map[string]any{
			"from_package": string(m.FromPackage),
		}
		topo.Resources[string(id)] = domain.Resource{
			ID:          string(id),
			Kind:        domain.ResourceFile,
			Name:        m.Name,
			Description: m.Description,
			Properties:  props,
			Connections: stringMapConn(m.Connections),
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
