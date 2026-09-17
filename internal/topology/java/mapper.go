package java

import (
	"encoding/json"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// Converts a generic domain topology to a Java-specific topology. Only resources
// tagged "java" (or untagged) are mapped, so a multi-language graph does not
// bleed other languages' nodes into the Java view. Unrecognized Connection keys
// (e.g. the module-owned __extends_records / __impl_records structural records)
// are carried through untouched.
func FromGeneric(topo *domain.Topology) *JavaTopology {
	if topo == nil {
		return nil
	}
	gt := &JavaTopology{
		Root:       topo.Root,
		Classes:    make(map[StructID]JavaClass),
		Interfaces: make(map[InterfaceID]JavaInterface),
		Methods:    make(map[FunctionID]JavaMethod),
		Modules:    make(map[ModuleID]JavaModule),
		Errors:     topo.Errors,
	}

	for id, res := range topo.Resources {
		if res.Language != "" && res.Language != "java" {
			continue
		}
		switch res.Kind {
		case domain.ResourceFunction, domain.ResourceMethod:
			f := JavaMethod{
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
			if v, ok := res.Properties["throws"]; ok {
				jsonConvert(v, &f.Throws)
			}
			f.IsConstructor = boolProp(res, "is_constructor")
			f.IsStatic = boolProp(res, "is_static")
			f.IsAbstract = boolProp(res, "is_abstract")
			f.IsDefault = boolProp(res, "is_default")
			f.IsSynthetic = boolProp(res, "is_synthetic")
			f.Exported = boolProp(res, "exported")
			f.Visibility = strProp(res, "visibility")
			if mf, ok := res.Properties["method_from"]; ok && mf != nil {
				if sid, ok := mf.(string); ok {
					s := StructID(sid)
					f.MethodFrom = &s
				}
			}
			gt.Methods[f.ID] = f

		case domain.ResourceStruct:
			s := JavaClass{
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
			if v, ok := res.Properties["components"]; ok {
				jsonConvert(v, &s.Components)
			}
			if v, ok := res.Properties["permits"]; ok {
				jsonConvert(v, &s.Permits)
			}
			if v, ok := res.Properties["bases"]; ok {
				jsonConvert(v, &s.Bases)
			}
			if v, ok := res.Properties["interfaces"]; ok {
				jsonConvert(v, &s.Interfaces)
			}
			if v, ok := res.Properties["generics"]; ok {
				jsonConvert(v, &s.Generics)
			}
			s.IsEnum = boolProp(res, "is_enum")
			s.IsRecord = boolProp(res, "is_record")
			s.IsAbstract = boolProp(res, "is_abstract")
			s.IsSealed = boolProp(res, "is_sealed")
			s.IsFinal = boolProp(res, "is_final")
			s.IsStatic = boolProp(res, "is_static")
			s.IsAnonymous = boolProp(res, "is_anonymous")
			s.IsLocal = boolProp(res, "is_local")
			s.Exported = boolProp(res, "exported")
			s.Visibility = strProp(res, "visibility")
			if ctor, ok := res.Properties["constructor"]; ok && ctor != nil {
				if cid, ok := ctor.(string); ok {
					fid := FunctionID(cid)
					s.Constructor = &fid
				}
			}
			gt.Classes[s.ID] = s

		case domain.ResourceInterface:
			t := JavaInterface{
				ID:          InterfaceID(id),
				Name:        res.Name,
				Description: res.Description,
				Loc:         res.Location,
				Connections: mapKindConn(res.Connections),
			}
			if v, ok := res.Properties["methods"]; ok {
				jsonConvert(v, &t.Methods)
			}
			if v, ok := res.Properties["generics"]; ok {
				jsonConvert(v, &t.Generics)
			}
			t.IsAnnotation = boolProp(res, "is_annotation")
			t.Exported = boolProp(res, "exported")
			t.Visibility = strProp(res, "visibility")
			gt.Interfaces[t.ID] = t

		case domain.ResourceFile:
			mod := JavaModule{
				ID:          ModuleID(id),
				Name:        res.Name,
				Description: res.Description,
				Connections: mapKindConn(res.Connections),
			}
			mod.Package = strProp(res, "package")
			gt.Modules[mod.ID] = mod

		case domain.ResourceDependency:
			gt.Dependencies = append(gt.Dependencies, JavaDependency{Coordinate: DependencyPath(id)})
		}
	}

	return gt
}

// Converts a JavaTopology into a generic domain.Topology, tagging every resource
// with the given language ("java"). Unrecognized Connection keys carried on each
// resource (e.g. __extends_records / __impl_records) are preserved untouched.
func ToGeneric(gt *JavaTopology, language string) *domain.Topology {
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

	for id, fn := range gt.Methods {
		props := map[string]any{
			"input":          fn.Input,
			"output":         fn.Output,
			"throws":         fn.Throws,
			"is_constructor": fn.IsConstructor,
			"is_static":      fn.IsStatic,
			"is_abstract":    fn.IsAbstract,
			"is_default":     fn.IsDefault,
			"is_synthetic":   fn.IsSynthetic,
			"visibility":     fn.Visibility,
			"exported":       fn.Exported,
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

	for id, s := range gt.Classes {
		props := map[string]any{
			"fields":       s.Fields,
			"variants":     s.Variants,
			"components":   s.Components,
			"permits":      s.Permits,
			"bases":        s.Bases,
			"interfaces":   s.Interfaces,
			"generics":     s.Generics,
			"is_enum":      s.IsEnum,
			"is_record":    s.IsRecord,
			"is_abstract":  s.IsAbstract,
			"is_sealed":    s.IsSealed,
			"is_final":     s.IsFinal,
			"is_static":    s.IsStatic,
			"is_anonymous": s.IsAnonymous,
			"is_local":     s.IsLocal,
			"visibility":   s.Visibility,
			"exported":     s.Exported,
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

	for id, t := range gt.Interfaces {
		props := map[string]any{
			"methods":       t.Methods,
			"generics":      t.Generics,
			"is_annotation": t.IsAnnotation,
			"visibility":    t.Visibility,
			"exported":      t.Exported,
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

	for id, m := range gt.Modules {
		props := map[string]any{
			"package": m.Package,
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
		did := string(d.Coordinate)
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
