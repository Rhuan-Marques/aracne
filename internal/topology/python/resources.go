package python

import "aracne/internal/topology/domain"

type FunctionID = string
type ClassID = string
type ExternalVarID = string
type ModuleID = string
type PackagePath = string
type DependancyPath = string

type VariableDefinition struct {
	Name   string
	Typing string
	// TypingID is the canonical topology class ID (pkgPath.Name) of the type
	// named by Typing, resolved at PARSE TIME against the defining file's import
	// map. It is "" for builtin/generic/unresolvable types. Storing it lets
	// cross-package consumers (e.g. a caller inferring an imported function's
	// return type) resolve the type without the callee's import context. It is a
	// candidate id: consumers must still verify it exists.
	TypingID string
}

type FunctionDefinition struct {
	Name   string
	Input  []VariableDefinition
	Output []VariableDefinition
}

type PythonTopology struct {
	Root         string
	Functions    map[FunctionID]PythonFunction
	Classes      map[ClassID]PythonClass
	ExternalVars map[ExternalVarID]PythonExternalVar
	Modules      map[ModuleID]PythonModule
	Packages     map[PackagePath]PythonPackage
	Dependencies []PythonDependancy
	Errors       map[string]string
}

type PythonFunction struct {
	ID          FunctionID
	Name        string
	Description string
	Decorators  []string
	Input       []VariableDefinition
	Output      []VariableDefinition
	Loc         domain.Location
	Connections map[ConnectionKind][]string
	MethodFrom  *ClassID
	IsAsync     bool
}

type PythonClass struct {
	ID                 ClassID
	Name               string
	Description        string
	Bases              []string
	Params             []VariableDefinition
	Loc                domain.Location
	Connections        map[ConnectionKind][]string
	Constructor        *FunctionID
	IsABC              bool
	IsProtocol         bool
	HasAbstractMethods bool
}

type PythonModule struct {
	ID          ModuleID
	Name        string
	Description string
	FromPackage PackagePath
	Connections map[ConnectionKind][]string
}

type PythonPackage struct {
	Path        PackagePath
	Description string
	Connections map[ConnectionKind][]string
}

type PythonExternalVar struct {
	ID          ExternalVarID
	Name        string
	Description string
	Typing      string
	Value       *any
	Location    domain.Location
}

type PythonDependancy struct {
	PackagePath DependancyPath
}
