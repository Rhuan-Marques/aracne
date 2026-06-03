package pyscanner

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"ltp/internal/topology/domain"
	"ltp/internal/topology/python"
)

type pyImport struct {
	Name   string `json:"name"`
	Alias  string `json:"alias"`
	Module string `json:"module,omitempty"`
}

type pyClass struct {
	Name               string   `json:"name"`
	Docstring          string   `json:"docstring"`
	Bases              []string `json:"bases"`
	Decorators         []string `json:"decorators"`
	Methods            []pyFunc `json:"methods"`
	ClassVars          []pyVar  `json:"class_vars"`
	IsABC              bool     `json:"is_abc"`
	IsProtocol         bool     `json:"is_protocol"`
	HasAbstractMethods bool     `json:"has_abstract_methods"`
	Lineno             int      `json:"lineno"`
	EndLineno          int      `json:"end_lineno"`
}

type pyFunc struct {
	Name       string     `json:"name"`
	Docstring  string     `json:"docstring"`
	Decorators []string   `json:"decorators"`
	IsAsync    bool       `json:"is_async"`
	IsProperty bool       `json:"is_property"`
	IsAbstract bool       `json:"is_abstract"`
	Params     []pyVarDef `json:"params"`
	Results    []pyVarDef `json:"results"`
	Lineno     int        `json:"lineno"`
	EndLineno  int        `json:"end_lineno"`
	Parent     *string    `json:"parent"`
}

type pyVarDef struct {
	Name   string `json:"name"`
	Typing string `json:"typing"`
}

type pyVar struct {
	Name   string `json:"name"`
	Typing string `json:"typing"`
	Value  string `json:"value"`
}

type pyFileResult struct {
	Docstring string            `json:"docstring"`
	Imports   []pyImport        `json:"imports"`
	ImportMap map[string]string `json:"import_map"`
	Functions []pyFunc          `json:"functions"`
	Classes   []pyClass         `json:"classes"`
	Variables []pyVar           `json:"variables"`
}

func parsePythonFile(filePath string) (*pyFileResult, error) {
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return nil, fmt.Errorf("resolve path: %w", err)
	}

	dir := filepath.Dir(absPath)

	pythonExes := []string{"python3", "python"}
	var cmdErr error
	for _, exe := range pythonExes {
		cmd := exec.Command(exe, "-c", pythonParseScript, absPath, dir)
		output, err := cmd.CombinedOutput()
		if err == nil {
			var result pyFileResult
			if err := json.Unmarshal(output, &result); err != nil {
				return nil, fmt.Errorf("parse json: %w\noutput: %s", err, string(output))
			}
			return &result, nil
		}
		cmdErr = fmt.Errorf("%s parse failed: %s: %w", exe, string(output), err)
	}
	return nil, cmdErr
}

type ParseResult struct {
	FileID          string
	FileDescription string
	PkgPath         python.PackagePath
	ModuleRoot      string
	InternalImports []python.PackagePath
	ExternalImports []python.PythonDependancy
	Classes         []python.PythonClass
	Functions       []FunctionParse
	ExternalVars    []python.PythonExternalVar
	ImportMap       map[string]string
}

type FunctionParse struct {
	Function python.PythonFunction
	Body     *pyFunc
}

func ParseFile(filePath string, pkgPath python.PackagePath, moduleRoot string) (*ParseResult, error) {
	raw, err := parsePythonFile(filePath)
	if err != nil {
		return nil, err
	}

	pr := &ParseResult{
		FileID:          filePath,
		FileDescription: raw.Docstring,
		PkgPath:         pkgPath,
		ModuleRoot:      moduleRoot,
		ImportMap:       raw.ImportMap,
	}

	for _, imp := range raw.Imports {
		if isInternal(imp.Name, moduleRoot) || imp.Module != "" && isInternal(imp.Module, moduleRoot) {
			pr.InternalImports = append(pr.InternalImports, python.PackagePath(imp.Name))
		} else {
			pr.ExternalImports = append(pr.ExternalImports, python.PythonDependancy{
				PackagePath: python.DependancyPath(imp.Name),
			})
		}
	}

	for _, cls := range raw.Classes {
		c := convertClass(cls, filePath, pkgPath)
		pr.Classes = append(pr.Classes, c)

		for _, method := range cls.Methods {
			f := convertFunction(method, filePath, pkgPath, &c.ID)
			pr.Functions = append(pr.Functions, FunctionParse{Function: f, Body: &method})
		}
	}

	for _, fn := range raw.Functions {
		f := convertFunction(fn, filePath, pkgPath, nil)
		pr.Functions = append(pr.Functions, FunctionParse{Function: f, Body: &fn})
	}

	for _, v := range raw.Variables {
		id := python.ExternalVarID(string(pkgPath) + "." + v.Name)
		var val *any
		if v.Value != "" && v.Value != "None" {
			var x any = v.Value
			val = &x
		}
		pr.ExternalVars = append(pr.ExternalVars, python.PythonExternalVar{
			ID:          id,
			Name:        v.Name,
			Description: "",
			Typing:      v.Typing,
			Value:       val,
			Location: domain.Location{
				Path:     filePath,
				StartsAt: 0,
				EndsAt:   0,
			},
		})
	}

	return pr, nil
}

func convertClass(cls pyClass, filePath string, pkgPath python.PackagePath) python.PythonClass {
	id := python.ClassID(string(pkgPath) + "." + cls.Name)
	return python.PythonClass{
		ID:                 id,
		Name:               cls.Name,
		Description:        cls.Docstring,
		Bases:              cls.Bases,
		Params:             nil,
		Loc:                domain.Location{Path: filePath, StartsAt: cls.Lineno, EndsAt: cls.EndLineno},
		Connections:        make(map[python.ConnectionKind][]string),
		IsABC:              cls.IsABC,
		IsProtocol:         cls.IsProtocol,
		HasAbstractMethods: cls.HasAbstractMethods,
	}
}

func convertFunction(fn pyFunc, filePath string, pkgPath python.PackagePath, classID *python.ClassID) python.PythonFunction {
	var input []python.VariableDefinition
	for _, p := range fn.Params {
		if p.Name == "self" || p.Name == "cls" {
			continue
		}
		input = append(input, python.VariableDefinition{Name: p.Name, Typing: p.Typing})
	}

	var output []python.VariableDefinition
	for _, r := range fn.Results {
		output = append(output, python.VariableDefinition{Name: r.Name, Typing: r.Typing})
	}

	var id python.FunctionID
	if classID != nil {
		id = python.FunctionID(string(pkgPath) + "." + string(*classID) + "." + fn.Name)
	} else {
		id = python.FunctionID(string(pkgPath) + "." + fn.Name)
	}

	return python.PythonFunction{
		ID:          id,
		Name:        fn.Name,
		Description: fn.Docstring,
		Decorators:  fn.Decorators,
		Input:       input,
		Output:      output,
		Loc:         domain.Location{Path: filePath, StartsAt: fn.Lineno, EndsAt: fn.EndLineno},
		Connections: make(map[python.ConnectionKind][]string),
		MethodFrom:  classID,
		IsAsync:     fn.IsAsync,
	}
}

func isInternal(importPath, moduleRoot string) bool {
	parts := strings.Split(importPath, ".")
	if len(parts) == 0 {
		return false
	}
	return strings.EqualFold(parts[0], filepath.Base(moduleRoot))
}
