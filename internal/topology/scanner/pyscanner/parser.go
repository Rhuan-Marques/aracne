package pyscanner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"aracne/internal/topology/domain"
	"aracne/internal/topology/python"
)

type pyImport struct {
	Name   string `json:"name"`
	Alias  string `json:"alias"`
	Module string `json:"module,omitempty"`
	Level  int    `json:"level,omitempty"`
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

type pyBodyCall struct {
	Func       string `json:"func"`
	ObjectName string `json:"object_name"`
	MethodName string `json:"method_name"`
	LineNo     int    `json:"lineno"`
}

type pyBodyAssign struct {
	Name      string `json:"name"`
	ValueType string `json:"value_type"`
	LineNo    int    `json:"lineno"`
}

type pyFunc struct {
	Name       string         `json:"name"`
	Docstring  string         `json:"docstring"`
	Decorators []string       `json:"decorators"`
	IsAsync    bool           `json:"is_async"`
	IsProperty bool           `json:"is_property"`
	IsAbstract bool           `json:"is_abstract"`
	Params     []pyVarDef     `json:"params"`
	Results    []pyVarDef     `json:"results"`
	Lineno     int            `json:"lineno"`
	EndLineno  int            `json:"end_lineno"`
	Parent     *string        `json:"parent"`
	BodyCalls  []pyBodyCall   `json:"body_calls"`
	BodyAssign []pyBodyAssign `json:"body_assignments"`
}

type pyVarDef struct {
	Name   string `json:"name"`
	Typing string `json:"typing"`
}

type pyVar struct {
	Name      string `json:"name"`
	Typing    string `json:"typing"`
	Value     string `json:"value"`
	Lineno    int    `json:"lineno"`
	EndLineno int    `json:"end_lineno"`
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
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		cmd := exec.CommandContext(ctx, exe, "-c", pythonParseScript, absPath, dir)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		runErr := cmd.Run()
		cancel()
		if runErr == nil {
			// Decode only stdout: anything the interpreter writes to stderr
			// (deprecation/syntax warnings, site noise) must not corrupt the
			// JSON payload.
			var result pyFileResult
			if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
				return nil, fmt.Errorf("parse json: %w\nstderr: %s", err, stderr.String())
			}
			return &result, nil
		}
		if ctx.Err() == context.DeadlineExceeded {
			cmdErr = fmt.Errorf("%s parse timed out after 30s for %s", exe, absPath)
			continue
		}
		cmdErr = fmt.Errorf("%s parse failed: %s: %w", exe, stderr.String(), runErr)
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
	ClassVarRefs    []ClassVarRef
}

type FunctionParse struct {
	Function    python.PythonFunction
	Body        *pyFunc
	BodyCalls   []pyBodyCall
	BodyAssigns []pyBodyAssign
}

type ClassVarRef struct {
	ClassID  python.ClassID
	RefValue string
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
		// Relative imports (level > 0, e.g. `from . import x`) are internal by
		// definition, regardless of how their dotted name resolves.
		if imp.Level > 0 || isInternal(imp.Name, moduleRoot) || (imp.Module != "" && isInternal(imp.Module, moduleRoot)) {
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

		for _, cv := range cls.ClassVars {
			if isResolvableRef(cv.Value) {
				pr.ClassVarRefs = append(pr.ClassVarRefs, ClassVarRef{
					ClassID:  c.ID,
					RefValue: cv.Value,
				})
			}
		}

		for _, method := range cls.Methods {
			f := convertFunction(method, filePath, pkgPath, &c.ID, pr.ImportMap)
			pr.Functions = append(pr.Functions, FunctionParse{Function: f, Body: &method, BodyCalls: method.BodyCalls, BodyAssigns: method.BodyAssign})
		}
	}

	for _, fn := range raw.Functions {
		f := convertFunction(fn, filePath, pkgPath, nil, pr.ImportMap)
		pr.Functions = append(pr.Functions, FunctionParse{Function: f, Body: &fn, BodyCalls: fn.BodyCalls, BodyAssigns: fn.BodyAssign})
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
				StartsAt: v.Lineno,
				EndsAt:   v.EndLineno,
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

func convertFunction(fn pyFunc, filePath string, pkgPath python.PackagePath, classID *python.ClassID, importMap map[string]string) python.PythonFunction {
	var input []python.VariableDefinition
	for _, p := range fn.Params {
		if p.Name == "self" || p.Name == "cls" {
			continue
		}
		input = append(input, python.VariableDefinition{Name: p.Name, Typing: p.Typing, TypingID: canonicalTypeID(p.Typing, pkgPath, importMap)})
	}

	var output []python.VariableDefinition
	for _, r := range fn.Results {
		output = append(output, python.VariableDefinition{Name: r.Name, Typing: r.Typing, TypingID: canonicalTypeID(r.Typing, pkgPath, importMap)})
	}

	var id python.FunctionID
	if classID != nil {
		// classID already carries the package prefix; do not double it.
		id = python.FunctionID(string(*classID) + "." + fn.Name)
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

// pyRefValueRE matches a bare identifier or dotted path that could name a
// resolvable resource (class/function/variable).
var pyRefValueRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`)

// pyNonRefValues are tokens the AST script emits for literals/collections that
// look like identifiers but never name a topology resource.
var pyNonRefValues = map[string]bool{
	"None": true, "True": true, "False": true,
	"list": true, "tuple": true, "dict": true, "set": true,
	"str": true, "expr": true,
}

// isResolvableRef reports whether a class-var value is worth attempting to
// resolve to a class/function/variable reference. Literal values (numbers,
// quoted strings, collapsed collection tokens) are skipped so they no longer
// generate spurious reference edges.
func isResolvableRef(value string) bool {
	if value == "" || pyNonRefValues[value] {
		return false
	}
	return pyRefValueRE.MatchString(value)
}
