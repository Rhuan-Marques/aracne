package rust

import (
	"encoding/json"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// Converts a generic domain topology to a Rust-specific topology. Only resources
// tagged "rust" (or untagged) are mapped, so a multi-language graph does not
// bleed other languages' nodes into the Rust view.
func FromGeneric(topo *domain.Topology) *RustTopology {
	if topo == nil {
		return nil
	}
	gt := &RustTopology{
		Root:       topo.Root,
		Functions:  make(map[FunctionID]RustFunction),
		Structs:    make(map[StructID]RustStruct),
		Traits:     make(map[TraitID]RustTrait),
		NamedTypes: make(map[NamedTypeID]RustNamedType),
		Variables:  make(map[VariableID]RustVariable),
		Modules:    make(map[ModuleID]RustModule),
		Errors:     topo.Errors,
	}

	for id, res := range topo.Resources {
		if res.Language != "" && res.Language != "rust" {
			continue
		}
		switch res.Kind {
		case domain.ResourceFunction, domain.ResourceMethod:
			f := RustFunction{
				ID:          FunctionID(id),
				Name:        res.Name,
				Description: res.Description,
				Loc:         res.Location,
				Connections: mapKindConn(res.Connections),
			}
			if v, ok := res.Properties["input"]; ok {
				jsonConvert(v, &f.Input)
			}
			if v, ok := res.Properties["output"]; ok {
				jsonConvert(v, &f.Output)
			}
			f.IsAsync = boolProp(res, "is_async")
			f.IsUnsafe = boolProp(res, "is_unsafe")
			f.IsConst = boolProp(res, "is_const")
			f.IsAssociated = boolProp(res, "is_associated")
			f.IsMacro = boolProp(res, "is_macro")
			f.Exported = boolProp(res, "exported")
			f.Receiver = strProp(res, "receiver")
			f.Visibility = strProp(res, "visibility")
			if mf, ok := res.Properties["method_from"]; ok && mf != nil {
				if sid, ok := mf.(string); ok {
					s := StructID(sid)
					f.MethodFrom = &s
				}
			}
			gt.Functions[f.ID] = f

		case domain.ResourceStruct:
			s := RustStruct{
				ID:          StructID(id),
				Name:        res.Name,
				Description: res.Description,
				Loc:         res.Location,
				Connections: mapKindConn(res.Connections),
			}
			if v, ok := res.Properties["fields"]; ok {
				jsonConvert(v, &s.Fields)
			}
			if v, ok := res.Properties["variants"]; ok {
				jsonConvert(v, &s.Variants)
			}
			if v, ok := res.Properties["derives"]; ok {
				jsonConvert(v, &s.Derives)
			}
			if v, ok := res.Properties["generics"]; ok {
				jsonConvert(v, &s.Generics)
			}
			s.IsEnum = boolProp(res, "is_enum")
			s.IsUnion = boolProp(res, "is_union")
			s.IsTuple = boolProp(res, "is_tuple")
			s.IsUnit = boolProp(res, "is_unit")
			s.Exported = boolProp(res, "exported")
			s.Visibility = strProp(res, "visibility")
			if ctor, ok := res.Properties["constructor"]; ok && ctor != nil {
				if cid, ok := ctor.(string); ok {
					fid := FunctionID(cid)
					s.Constructor = &fid
				}
			}
			gt.Structs[s.ID] = s

		case domain.ResourceInterface:
			t := RustTrait{
				ID:          TraitID(id),
				Name:        res.Name,
				Description: res.Description,
				Loc:         res.Location,
				Connections: mapKindConn(res.Connections),
			}
			if v, ok := res.Properties["methods"]; ok {
				jsonConvert(v, &t.Methods)
			}
			if v, ok := res.Properties["assoc_types"]; ok {
				jsonConvert(v, &t.AssocTypes)
			}
			if v, ok := res.Properties["assoc_consts"]; ok {
				jsonConvert(v, &t.AssocConsts)
			}
			if v, ok := res.Properties["bounds"]; ok {
				jsonConvert(v, &t.Bounds)
			}
			if v, ok := res.Properties["generics"]; ok {
				jsonConvert(v, &t.Generics)
			}
			t.Exported = boolProp(res, "exported")
			t.Visibility = strProp(res, "visibility")
			gt.Traits[t.ID] = t

		case domain.ResourceNamedType:
			n := RustNamedType{
				ID:          NamedTypeID(id),
				Name:        res.Name,
				Description: res.Description,
				Loc:         res.Location,
				Connections: mapKindConn(res.Connections),
			}
			n.Underlying = strProp(res, "underlying")
			n.Exported = boolProp(res, "exported")
			n.Visibility = strProp(res, "visibility")
			if v, ok := res.Properties["generics"]; ok {
				jsonConvert(v, &n.Generics)
			}
			gt.NamedTypes[n.ID] = n

		case domain.ResourceVariable:
			v := RustVariable{
				ID:          VariableID(id),
				Name:        res.Name,
				Description: res.Description,
				Location:    res.Location,
			}
			v.Typing = strProp(res, "typing")
			v.IsConst = boolProp(res, "is_const")
			v.IsStatic = boolProp(res, "is_static")
			v.Mutable = boolProp(res, "mutable")
			v.Exported = boolProp(res, "exported")
			v.Visibility = strProp(res, "visibility")
			if val, ok := res.Properties["value"]; ok && val != nil {
				var x any
				jsonConvert(val, &x)
				v.Value = &x
			}
			gt.Variables[v.ID] = v

		case domain.ResourceFile:
			mod := RustModule{
				ID:          ModuleID(id),
				Name:        res.Name,
				Description: res.Description,
				Connections: mapKindConn(res.Connections),
			}
			mod.ModulePath = strProp(res, "module_path")
			gt.Modules[mod.ID] = mod

		case domain.ResourceDependency:
			gt.Dependencies = append(gt.Dependencies, RustDependency{CratePath: DependencyPath(id)})
		}
	}

	return gt
}

// Converts a RustTopology into a generic domain.Topology, tagging every resource
// with the given language ("rust").
func ToGeneric(gt *RustTopology, language string) *domain.Topology {
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
			"is_unsafe":     fn.IsUnsafe,
			"is_const":      fn.IsConst,
			"is_associated": fn.IsAssociated,
			"is_macro":      fn.IsMacro,
			"receiver":      fn.Receiver,
			"visibility":    fn.Visibility,
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

	for id, s := range gt.Structs {
		props := map[string]any{
			"fields":     s.Fields,
			"variants":   s.Variants,
			"derives":    s.Derives,
			"generics":   s.Generics,
			"is_enum":    s.IsEnum,
			"is_union":   s.IsUnion,
			"is_tuple":   s.IsTuple,
			"is_unit":    s.IsUnit,
			"visibility": s.Visibility,
			"exported":   s.Exported,
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

	for id, t := range gt.Traits {
		props := map[string]any{
			"methods":      t.Methods,
			"assoc_types":  t.AssocTypes,
			"assoc_consts": t.AssocConsts,
			"bounds":       t.Bounds,
			"generics":     t.Generics,
			"visibility":   t.Visibility,
			"exported":     t.Exported,
		}
		topo.Resources[string(id)] = domain.Resource{
			ID:          string(id),
			Kind:        domain.ResourceInterface,
			Name:        t.Name,
			Description: t.Description,
			Location:    t.Loc,
			Properties:  props,
			Connections: stringMapConn(t.Connections),
		}
	}

	for id, n := range gt.NamedTypes {
		props := map[string]any{
			"underlying": n.Underlying,
			"generics":   n.Generics,
			"visibility": n.Visibility,
			"exported":   n.Exported,
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

	for id, v := range gt.Variables {
		props := map[string]any{
			"typing":     v.Typing,
			"is_const":   v.IsConst,
			"is_static":  v.IsStatic,
			"mutable":    v.Mutable,
			"visibility": v.Visibility,
			"exported":   v.Exported,
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
			"module_path": m.ModulePath,
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
		did := string(d.CratePath)
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

// Reads a boolean property, defaulting to false.
func boolProp(res domain.Resource, key string) bool {
	if v, ok := res.Properties[key]; ok && v != nil {
		b, _ := v.(bool)
		return b
	}
	return false
}

// Reads a string property, defaulting to "".
func strProp(res domain.Resource, key string) string {
	if v, ok := res.Properties[key]; ok && v != nil {
		s, _ := v.(string)
		return s
	}
	return ""
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

// Converts between arbitrary types via JSON marshaling and unmarshaling.
func jsonConvert(from any, to any) {
	b, err := json.Marshal(from)
	if err != nil {
		return
	}
	// Marshal succeeded, so b is valid JSON: Unmarshal can only fail if `from` and `to`
	// disagree on a field's type, which is a bug in this mapper rather than anything the
	// scanned source can cause. `to` is left as it was.
	_ = json.Unmarshal(b, to)
}
