package python

import (
	"encoding/json"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// Converts a generic domain topology to a Python-specific topology with typed resources and connections.
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
		Errors:       topo.Errors,
	}

	for id, res := range topo.Resources {
		if res.Language != "" && res.Language != "python" {
			continue
		}
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
			if ia, ok := res.Properties["is_async"].(bool); ok {
				f.IsAsync = ia
			}
			if mf, ok := res.Properties["method_from"].(string); ok && mf != "" {
				cid := ClassID(mf)
				f.MethodFrom = &cid
			}
			gt.Functions[f.ID] = f

		case domain.ResourceStruct:
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
			if ctor, ok := res.Properties["constructor"].(string); ok && ctor != "" {
				cid := FunctionID(ctor)
				c.Constructor = &cid
			}
			if ia, ok := res.Properties["is_abc"].(bool); ok {
				c.IsABC = ia
			}
			if ip, ok := res.Properties["is_protocol"].(bool); ok {
				c.IsProtocol = ip
			}
			if ham, ok := res.Properties["has_abstract_methods"].(bool); ok {
				c.HasAbstractMethods = ham
			}
			gt.Classes[c.ID] = c

		case domain.ResourceVariable:
			v := PythonExternalVar{
				ID:          ExternalVarID(id),
				Name:        res.Name,
				Description: res.Description,
			}
			if typing, ok := res.Properties["typing"].(string); ok {
				v.Typing = typing
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
			if fp, ok := res.Properties["from_package"].(string); ok {
				mod.FromPackage = PackagePath(fp)
			}
			gt.Modules[mod.ID] = mod

		case domain.ResourceDependency:
			gt.Dependencies = append(gt.Dependencies, PythonDependancy{
				PackagePath: DependancyPath(id),
			})
		}
	}

	return gt
}

// Converts a PythonTopology to a generic domain.Topology with functions, classes, variables, modules, and dependencies normalized as domain resources.
func ToGeneric(gt *PythonTopology) *domain.Topology {
	if gt == nil {
		return nil
	}
	topo := &domain.Topology{
		Root:      gt.Root,
		Language:  "python",
		Languages: []string{"python"},
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
			Kind:        domain.ResourceStruct,
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
		res.Language = "python"
		topo.Resources[id] = res
	}

	return topo
}

// Converts a string-keyed connection map to a ConnectionKind-keyed map.
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

// Converts an object to another type by marshaling to JSON and unmarshaling to the target.
func jsonConvert(from any, to any) {
	b, err := json.Marshal(from)
	if err != nil {
		return
	}
	json.Unmarshal(b, to)
}

// Converts a ConnectionKind-keyed connection map to a string-keyed map.
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
