package pyscanner

import (
	"strings"

	"llm-topology/internal/topology/python"
)

func analyzeFunctionBody(body *pyFunc, pr *ParseResult, gt *python.PythonTopology, funcInput []python.VariableDefinition, receiverClass *python.ClassID) map[python.ConnectionKind][]string {
	conn := make(map[python.ConnectionKind][]string)

	if body == nil {
		return conn
	}

	add := func(kind python.ConnectionKind, id string) {
		ids := conn[kind]
		for _, existing := range ids {
			if existing == id {
				return
			}
		}
		conn[kind] = append(ids, id)
	}

	resolveBodyReferences(body, pr, gt, add)

	return conn
}

func resolveBodyReferences(body *pyFunc, pr *ParseResult, gt *python.PythonTopology, add func(kind python.ConnectionKind, id string)) {
	seen := make(map[string]bool)
	for _, dec := range body.Decorators {
		resolveDecoratorRef(dec, pr, gt, add, seen)
	}

	seen2 := make(map[string]bool)
	for _, param := range body.Params {
		if param.Typing != "" {
			resolveTypeRef(param.Typing, pr, gt, add, seen2)
		}
	}
	for _, result := range body.Results {
		if result.Typing != "" {
			resolveTypeRef(result.Typing, pr, gt, add, seen2)
		}
	}
}

func resolveDecoratorRef(dec string, pr *ParseResult, gt *python.PythonTopology, add func(kind python.ConnectionKind, id string), seen map[string]bool) {
	parts := strings.Split(dec, ".")
	if len(parts) >= 2 {
		alias := parts[0]
		if impPath, ok := pr.ImportMap[alias]; ok {
			if isInternal(impPath, pr.ModuleRoot) {
				add(python.ConnUsesPkg, string(impPath))
			} else {
				add(python.ConnUsesDep, string(impPath))
			}
		}
	}
}

func resolveTypeRef(typeName string, pr *ParseResult, gt *python.PythonTopology, add func(kind python.ConnectionKind, id string), seen map[string]bool) {
	clean := strings.TrimPrefix(typeName, "*")
	clean = strings.TrimSuffix(clean, "?")
	if strings.Contains(clean, "[") {
		clean = clean[:strings.Index(clean, "[")]
	}

	if strings.Contains(clean, ".") {
		parts := strings.Split(clean, ".")
		if len(parts) >= 2 {
			alias := parts[0]
			if impPath, ok := pr.ImportMap[alias]; ok {
				if isInternal(impPath, pr.ModuleRoot) {
					add(python.ConnUsesPkg, string(impPath))
					classID := python.ClassID(string(impPath) + "." + parts[1])
					if _, exists := gt.Classes[classID]; exists {
						add(python.ConnUsesClass, string(classID))
					}
				} else {
					add(python.ConnUsesDep, string(impPath))
				}
			}
		}
		return
	}

	classID := python.ClassID(string(pr.PkgPath) + "." + clean)
	if _, exists := gt.Classes[classID]; exists {
		add(python.ConnUsesClass, string(classID))
	}

	funcID := PythonFunctionID(string(pr.PkgPath) + "." + clean)
	if _, exists := gt.Functions[python.FunctionID(funcID)]; exists && !seen[funcID] {
		seen[funcID] = true
		add(python.ConnCalls, funcID)
	}
}

func detectConstructors(gt *python.PythonTopology) {
	for _, fn := range gt.Functions {
		if fn.MethodFrom == nil {
			continue
		}
		if fn.Name == "__init__" {
			cls := gt.Classes[*fn.MethodFrom]
			cls.Constructor = &fn.ID
			gt.Classes[*fn.MethodFrom] = cls
		}
	}
}

type PythonFunctionID = string

func collectDependencies(gt *python.PythonTopology) {
	seen := make(map[python.DependancyPath]bool)
	for _, mod := range gt.Modules {
		for _, dep := range mod.DependenciesImported() {
			if !seen[dep.PackagePath] {
				seen[dep.PackagePath] = true
				gt.Dependencies = append(gt.Dependencies, dep)
			}
		}
	}
}

func populateClassMethods(gt *python.PythonTopology) {
	for _, f := range gt.Functions {
		if f.MethodFrom != nil {
			cls := gt.Classes[*f.MethodFrom]
			if cls.Connections == nil {
				cls.Connections = make(map[python.ConnectionKind][]string)
			}
			cls.Connections[python.ConnHasMethod] = append(cls.Connections[python.ConnHasMethod], string(f.ID))
			gt.Classes[*f.MethodFrom] = cls
		}
	}
}

func uniqueConns(conns map[python.ConnectionKind][]string) map[python.ConnectionKind][]string {
	result := make(map[python.ConnectionKind][]string, len(conns))
	for k, v := range conns {
		seen := make(map[string]bool)
		var deduped []string
		for _, id := range v {
			if !seen[id] {
				seen[id] = true
				deduped = append(deduped, id)
			}
		}
		if len(deduped) > 0 {
			result[k] = deduped
		}
	}
	return result
}
