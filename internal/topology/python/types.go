package python

import "ltp/internal/topology/domain"

type FunctionCut struct {
	PythonFunction
	Cut string
}

type ClassCut struct {
	PythonClass
	Cut string
}

type SimplifiedFunction struct {
	ID          FunctionID
	Name        string
	Description string
	Input       []VariableDefinition
	Output      []VariableDefinition
	Location    domain.Location
}

type ClassUsage struct {
	ID          ClassID
	Name        string
	Description string
	Location    domain.Location
	Methods     []SimplifiedFunction
}

type SimplifiedClass struct {
	ID                     ClassID
	Name                   string
	Description            string
	Location               domain.Location
	NeedToImplement        bool
	NeedToImplementMethods []string
}

type SimplifiedExtVar struct {
	ID          ExternalVarID
	Name        string
	Description string
	Value       string
	Location    domain.Location
}

type ContextBlock struct {
	Kind   string
	FileID ModuleID
	Line   int
	Title  string
	Cut    string
}

type PythonFunctionContext struct {
	Function        *FunctionCut
	ParentClass     *ClassCut
	CalledFunctions []SimplifiedFunction
	ClassesUsed     []ClassUsage
	ExtVarsUsed     []SimplifiedExtVar
	Dependencies    []DependancyPath
	ModulesUsed     []PackagePath
	Blocks          []ContextBlock
}

type PythonClassContext struct {
	Class        *ClassCut
	Constructor  *FunctionCut
	BaseClasses  []SimplifiedClass
	Methods      []SimplifiedFunction
	ClassesUsed  []ClassUsage
	ExtVarsUsed  []SimplifiedExtVar
	Dependencies []DependancyPath
	ModulesUsed  []PackagePath
	Blocks       []ContextBlock
}

type ModuleCut struct {
	PythonModule
	Cut string
}

type PackageCut struct {
	PythonPackage
	Cut string
}

type PythonModuleContext struct {
	Module       *ModuleCut
	FromPackage  PackagePath
	Functions    []SimplifiedFunction
	Classes      []ClassUsage
	ExtVars      []SimplifiedExtVar
	Imports      []PackagePath
	Dependencies []DependancyPath
	Blocks       []ContextBlock
}

type PythonPackageContext struct {
	Package      *PackageCut
	Files        []ModuleID
	Functions    []SimplifiedFunction
	Classes      []ClassUsage
	ExtVars      []SimplifiedExtVar
	Dependencies []DependancyPath
	Blocks       []ContextBlock
}

type PythonDependencyContext struct {
	Dependency DependancyPath
	UsedBy     []ResourceUsage
	Blocks     []ContextBlock
}

type ResourceUsage struct {
	ID          string
	Kind        domain.ResourceKind
	Name        string
	Description string
	Location    domain.Location
}

type TopologyWarning struct {
	Resource          domain.ResourceKind
	AffectedFunctions []FunctionID
	Message           string
}
