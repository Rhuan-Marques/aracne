package javascript

import (
	"encoding/json"

	"aracne/internal/topology/domain"
)

// Converts a generic domain topology to a JavaScript-specific topology, mapping all resources and properties.
func FromGeneric(topo *domain.Topology) *JavaScriptTopology {
	if topo == nil {
		return nil
	}
	gt := &JavaScriptTopology{
		Root:         topo.Root,
		Functions:    make(map[FunctionID]JavaScriptFunction),
		Classes:      make(map[ClassID]JavaScriptClass),
		Interfaces:   make(map[InterfaceID]JavaScriptInterface),
		NamedTypes:   make(map[NamedTypeID]JavaScriptNamedType),
		ExternalVars: make(map[ExternalVarID]JavaScriptExternalVar),
		Modules:      make(map[ModuleID]JavaScriptModule),
		Errors:       topo.Errors,
	}

	for id, res := range topo.Resources {
		// The javascript package is the shared JS/TS (ECMAScript) domain; accept both.
		if res.Language != "" && res.Language != "javascript" && res.Language != "typescript" {
			continue
		}
		switch res.Kind {
		case domain.ResourceFunction, domain.ResourceMethod:
			f := JavaScriptFunction{
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
			if ia, ok := res.Properties["is_async"]; ok && ia != nil {
				f.IsAsync, _ = ia.(bool)
			}
			if ig, ok := res.Properties["is_generator"]; ok && ig != nil {
				f.IsGenerator, _ = ig.(bool)
			}
			if is, ok := res.Properties["is_static"]; ok && is != nil {
				f.IsStatic, _ = is.(bool)
			}
			if k, ok := res.Properties["kind"]; ok && k != nil {
				f.Kind, _ = k.(string)
			}
			if ab, ok := res.Properties["is_abstract"]; ok && ab != nil {
				f.IsAbstract, _ = ab.(bool)
			}
			if dec, ok := res.Properties["decorators"]; ok {
				jsonConvert(dec, &f.Decorators)
			}
			if acc, ok := res.Properties["accessibility"]; ok && acc != nil {
				f.Accessibility, _ = acc.(string)
			}
			if ex, ok := res.Properties["exported"]; ok && ex != nil {
				f.Exported, _ = ex.(bool)
			}
			if mf, ok := res.Properties["method_from"]; ok && mf != nil {
				if cid, ok := mf.(string); ok {
					c := ClassID(cid)
					f.MethodFrom = &c
				}
			}
			gt.Functions[f.ID] = f

		case domain.ResourceStruct:
			c := JavaScriptClass{
				ID:          ClassID(id),
				Name:        res.Name,
				Description: res.Description,
				Loc:         res.Location,
				Connections: mapKindConn(res.Connections),
			}
			if bases, ok := res.Properties["bases"]; ok {
				jsonConvert(bases, &c.Bases)
			}
			if impl, ok := res.Properties["implements"]; ok {
				jsonConvert(impl, &c.ImplementsRaw)
			}
			if ab, ok := res.Properties["is_abstract"]; ok && ab != nil {
				c.IsAbstract, _ = ab.(bool)
			}
			if dec, ok := res.Properties["decorators"]; ok {
				jsonConvert(dec, &c.Decorators)
			}
			if ctor, ok := res.Properties["constructor"]; ok && ctor != nil {
				if cid, ok := ctor.(string); ok {
					fid := FunctionID(cid)
					c.Constructor = &fid
				}
			}
			if ex, ok := res.Properties["exported"]; ok && ex != nil {
				c.Exported, _ = ex.(bool)
			}
			gt.Classes[c.ID] = c

		case domain.ResourceInterface:
			iface := JavaScriptInterface{
				ID:          InterfaceID(id),
				Name:        res.Name,
				Description: res.Description,
				Loc:         res.Location,
				Connections: mapKindConn(res.Connections),
			}
			if methods, ok := res.Properties["methods"]; ok {
				jsonConvert(methods, &iface.Methods)
			}
			if props, ok := res.Properties["properties"]; ok {
				jsonConvert(props, &iface.Properties)
			}
			if bases, ok := res.Properties["bases"]; ok {
				jsonConvert(bases, &iface.Bases)
			}
			gt.Interfaces[iface.ID] = iface

		case domain.ResourceNamedType:
			n := JavaScriptNamedType{
				ID:          NamedTypeID(id),
				Name:        res.Name,
				Description: res.Description,
				Loc:         res.Location,
				Connections: mapKindConn(res.Connections),
			}
			if k, ok := res.Properties["kind"]; ok && k != nil {
				n.Kind, _ = k.(string)
			}
			if u, ok := res.Properties["underlying"]; ok && u != nil {
				n.Underlying, _ = u.(string)
			}
			if m, ok := res.Properties["members"]; ok {
				jsonConvert(m, &n.Members)
			}
			gt.NamedTypes[n.ID] = n

		case domain.ResourceVariable:
			v := JavaScriptExternalVar{
				ID:          ExternalVarID(id),
				Name:        res.Name,
				Description: res.Description,
			}
			if typing, ok := res.Properties["typing"]; ok && typing != nil {
				v.Typing, _ = typing.(string)
			}
			if ex, ok := res.Properties["exported"]; ok && ex != nil {
				v.Exported, _ = ex.(bool)
			}
			if val, ok := res.Properties["value"]; ok {
				var x any
				jsonConvert(val, &x)
				v.Value = &x
			}
			v.Location = res.Location
			gt.ExternalVars[v.ID] = v

		case domain.ResourceFile:
			mod := JavaScriptModule{
				ID:          ModuleID(id),
				Name:        res.Name,
				Description: res.Description,
				Connections: mapKindConn(res.Connections),
			}
			if fp, ok := res.Properties["from_package"]; ok && fp != nil {
				mod.FromPackage, _ = fp.(string)
			}
			if de, ok := res.Properties["default_export"]; ok && de != nil {
				mod.DefaultExport, _ = de.(string)
			}
			if rn, ok := res.Properties["re_exports_named"]; ok && rn != nil {
				jsonConvert(rn, &mod.ReExportsNamed)
			}
			gt.Modules[mod.ID] = mod

		case domain.ResourceDependency:
			gt.Dependencies = append(gt.Dependencies, JavaScriptDependancy{
				PackagePath: DependancyPath(id),
			})
		}
	}

	return gt
}

// Converts a JavaScriptTopology into a generic domain.Topology, mapping functions, classes, interfaces, named types, variables, modules, and dependencies with their properties and connections.
func ToGeneric(gt *JavaScriptTopology, language string) *domain.Topology {
	if gt == nil {
		return nil
	}
	topo := &domain.Topology{
		Root:      gt.Root,
		Language:  language,
		Languages: []string{language},
		Resources: make(map[string]domain.Resource),
		Errors:    gt.Errors,
	}

	for id, fn := range gt.Functions {
		props := map[string]any{
			"input":         fn.Input,
			"output":        fn.Output,
			"is_async":      fn.IsAsync,
			"is_generator":  fn.IsGenerator,
			"is_static":     fn.IsStatic,
			"is_abstract":   fn.IsAbstract,
			"kind":          fn.Kind,
			"decorators":    fn.Decorators,
			"accessibility": fn.Accessibility,
			"exported":      fn.Exported,
		}
		kind := domain.ResourceFunction
		if fn.MethodFrom != nil {
			kind = domain.ResourceMethod
			props["method_from"] = string(*fn.MethodFrom)
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
			"bases":       c.Bases,
			"implements":  c.ImplementsRaw,
			"is_abstract": c.IsAbstract,
			"decorators":  c.Decorators,
			"exported":    c.Exported,
		}
		if c.Constructor != nil {
			props["constructor"] = string(*c.Constructor)
		}
		// Members folded in from a same-name interface (class + interface
		// declaration merging) are surfaced alongside the class's own metadata.
		if len(c.MergedInterfaceMethods) > 0 {
			props["methods"] = c.MergedInterfaceMethods
		}
		if len(c.MergedInterfaceProperties) > 0 {
			props["properties"] = c.MergedInterfaceProperties
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

	for id, iface := range gt.Interfaces {
		props := map[string]any{
			"methods":    iface.Methods,
			"properties": iface.Properties,
			"bases":      iface.Bases,
		}
		topo.Resources[string(id)] = domain.Resource{
			ID:          string(id),
			Kind:        domain.ResourceInterface,
			Name:        iface.Name,
			Description: iface.Description,
			Location:    iface.Loc,
			Properties:  props,
			Connections: stringMapConn(iface.Connections),
		}
	}

	for id, n := range gt.NamedTypes {
		props := map[string]any{
			"kind":       n.Kind,
			"underlying": n.Underlying,
			"members":    n.Members,
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

	for id, v := range gt.ExternalVars {
		props := map[string]any{
			"typing":   v.Typing,
			"exported": v.Exported,
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
			"from_package":   string(m.FromPackage),
			"default_export": m.DefaultExport,
		}
		if len(m.ReExportsNamed) > 0 {
			props["re_exports_named"] = m.ReExportsNamed
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
		res.Language = language
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

// Converts between arbitrary types via JSON marshaling and unmarshaling
func jsonConvert(from any, to any) {
	b, err := json.Marshal(from)
	if err != nil {
		return
	}
	json.Unmarshal(b, to)
}

// Converts ConnectionKind-keyed map to string-keyed connection map.
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
