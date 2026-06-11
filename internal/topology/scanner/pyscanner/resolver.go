package pyscanner

import (
	"path/filepath"
	"strings"

	"aracne/internal/topology/python"
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

	bodyCalls := body.BodyCalls
	if bodyCalls == nil {
		bodyCalls = []pyBodyCall{}
	}
	bodyAssigns := body.BodyAssign
	if bodyAssigns == nil {
		bodyAssigns = []pyBodyAssign{}
	}
	resolveBodyCallRefs(bodyCalls, bodyAssigns, pr, gt, add, funcInput)

	return conn
}

func resolveBodyCallRefs(bodyCalls []pyBodyCall, bodyAssigns []pyBodyAssign, pr *ParseResult, gt *python.PythonTopology, add func(kind python.ConnectionKind, id string), funcInput []python.VariableDefinition) {
	varTypeMap := make(map[string]python.ClassID)

	// Build from parameters with type annotations
	for _, param := range funcInput {
		if param.Typing == "" || param.Name == "" || param.Name == "self" || param.Name == "cls" {
			continue
		}
		classID := python.ClassID(string(pr.PkgPath) + "." + param.Typing)
		if _, exists := gt.Classes[classID]; exists {
			varTypeMap[param.Name] = classID
		} else if strings.Contains(param.Typing, ".") {
			parts := strings.SplitN(param.Typing, ".", 2)
			if impPath, ok := pr.ImportMap[parts[0]]; ok {
				classID = python.ClassID(impPath + "." + parts[1])
				if _, exists := gt.Classes[classID]; exists {
					varTypeMap[param.Name] = classID
				}
			}
		}
	}

	// Build from local assignments
	for _, assign := range bodyAssigns {
		if assign.ValueType == "" || assign.Name == "" {
			continue
		}
		classID := python.ClassID(string(pr.PkgPath) + "." + assign.ValueType)
		if _, exists := gt.Classes[classID]; exists {
			varTypeMap[assign.Name] = classID
		} else if strings.Contains(assign.ValueType, ".") {
			parts := strings.SplitN(assign.ValueType, ".", 2)
			if impPath, ok := pr.ImportMap[parts[0]]; ok {
				classID = python.ClassID(impPath + "." + parts[1])
				if _, exists := gt.Classes[classID]; exists {
					varTypeMap[assign.Name] = classID
				}
			}
		} else {
			funcID := python.FunctionID(string(pr.PkgPath) + "." + assign.ValueType)
			if fn, exists := gt.Functions[funcID]; exists && len(fn.Output) > 0 {
				retType := fn.Output[0].Typing
				if retType != "" {
					retClassID := python.ClassID(string(pr.PkgPath) + "." + retType)
					if _, ok := gt.Classes[retClassID]; ok {
						varTypeMap[assign.Name] = retClassID
					}
				}
			}
		}
	}

	// Resolve method calls
	for _, call := range bodyCalls {
		if call.ObjectName == "" || call.MethodName == "" {
			// Direct function call - try to resolve
			funcID := python.FunctionID(string(pr.PkgPath) + "." + call.Func)
			if _, exists := gt.Functions[funcID]; exists {
				add(python.ConnCalls, string(funcID))
			}
			continue
		}

		classID, ok := varTypeMap[call.ObjectName]
		if !ok {
			continue
		}

		cls, ok := gt.Classes[classID]
		if !ok {
			continue
		}

		add(python.ConnUsesClass, string(classID))

		// Find the method on the class
		for _, mid := range cls.Methods() {
			m, ok := gt.Functions[mid]
			if ok && m.Name == call.MethodName {
				add(python.ConnCalls, string(mid))
				break
			}
		}
	}
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

	rootBase := filepath.Base(pr.ModuleRoot)

	if strings.Contains(clean, ".") {
		parts := strings.Split(clean, ".")
		if len(parts) >= 2 {
			alias := parts[0]
			if impPath, ok := pr.ImportMap[alias]; ok {
				if isInternal(impPath, pr.ModuleRoot) {
					add(python.ConnUsesPkg, string(impPath))
					symbol := strings.Join(parts[1:], ".")
					if kind, id := tryResolveSymbol(string(impPath), symbol, rootBase, gt); kind != "" {
						add(kind, id)
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

	if impPath, ok := pr.ImportMap[clean]; ok {
		if kind, id := tryResolveSymbol(impPath, "", rootBase, gt); kind != "" {
			add(kind, id)
		}
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

func resolveClassVarRefs(pr *ParseResult, gt *python.PythonTopology) {
	for _, ref := range pr.ClassVarRefs {
		cls, exists := gt.Classes[ref.ClassID]
		if !exists {
			continue
		}
		if cls.Connections == nil {
			cls.Connections = make(map[python.ConnectionKind][]string)
		}

		add := func(kind python.ConnectionKind, id string) {
			for _, existing := range cls.Connections[kind] {
				if existing == id {
					return
				}
			}
			cls.Connections[kind] = append(cls.Connections[kind], id)
		}

		resolveValueRef(ref.RefValue, pr, gt, add)
		gt.Classes[ref.ClassID] = cls
	}
}

func resolveValueRef(value string, pr *ParseResult, gt *python.PythonTopology, add func(kind python.ConnectionKind, id string)) {
	if value == "" || value == "None" || value == "True" || value == "False" {
		return
	}

	rootBase := filepath.Base(pr.ModuleRoot)

	if strings.Contains(value, ".") {
		parts := strings.Split(value, ".")
		if len(parts) >= 2 {
			alias := parts[0]
			if impPath, ok := pr.ImportMap[alias]; ok {
				if isInternal(impPath, pr.ModuleRoot) {
					add(python.ConnUsesPkg, string(impPath))
				} else {
					add(python.ConnUsesDep, string(impPath))
				}
				symbol := strings.Join(parts[1:], ".")
				if kind, id := tryResolveSymbol(impPath, symbol, rootBase, gt); kind != "" {
					add(kind, id)
				}
				return
			}
		}
		return
	}

	if impPath, ok := pr.ImportMap[value]; ok {
		if isInternal(impPath, pr.ModuleRoot) {
			add(python.ConnUsesPkg, string(impPath))
		} else {
			add(python.ConnUsesDep, string(impPath))
		}
		if kind, id := tryResolveSymbol(impPath, "", rootBase, gt); kind != "" {
			add(kind, id)
		}
		return
	}

	funcID := python.FunctionID(string(pr.PkgPath) + "." + value)
	if _, exists := gt.Functions[python.FunctionID(funcID)]; exists {
		add(python.ConnCalls, funcID)
		return
	}

	classID := python.ClassID(string(pr.PkgPath) + "." + value)
	if _, exists := gt.Classes[classID]; exists {
		add(python.ConnUsesClass, string(classID))
		return
	}

	extVarID := python.ExternalVarID(string(pr.PkgPath) + "." + value)
	if _, exists := gt.ExternalVars[extVarID]; exists {
		add(python.ConnUsesExtVar, string(extVarID))
		return
	}
}

func tryResolveSymbol(impPath string, symbol string, rootBase string, gt *python.PythonTopology) (python.ConnectionKind, string) {
	fullPath := impPath
	if symbol != "" {
		fullPath = impPath + "." + symbol
	}

	candidates := []string{fullPath, rootBase + "." + fullPath}

	for _, candidate := range candidates {
		if _, exists := gt.Classes[python.ClassID(candidate)]; exists {
			return python.ConnUsesClass, candidate
		}
		if _, exists := gt.Functions[python.FunctionID(candidate)]; exists {
			return python.ConnCalls, candidate
		}
	}

	return "", ""
}
