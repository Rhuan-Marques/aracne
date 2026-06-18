package python

import "aracne/internal/topology/domain"

type FunctionCut struct {
	PythonFunction
	Cut string
}

type ClassCut struct {
	PythonClass
	Cut string
}

// FullBlock carries everything needed to render a neighbor as a full source
// cut (relevant imports, an optional parent class, and the resource's own cut)
// without its own CONTEXT section. Path labels the fenced code block.
type FullBlock struct {
	Path      string
	Imports   []PackagePath
	Deps      []DependancyPath
	ParentCut string
	Cut       string
}

type SimplifiedFunction struct {
	ID          FunctionID
	Name        string
	Description string
	Input       []VariableDefinition
	Output      []VariableDefinition
	Location    domain.Location
	Visibility  domain.Visibility
	Full        *FullBlock
}

type ClassUsage struct {
	ID          ClassID
	Name        string
	Description string
	Location    domain.Location
	Methods     []SimplifiedFunction
	Visibility  domain.Visibility
	Full        *FullBlock
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
	Visibility  domain.Visibility
	Full        *FullBlock
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
	Incoming        []domain.ResourceRef
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
	Incoming     []domain.ResourceRef
	Blocks       []ContextBlock
}

type ModuleCut struct {
	PythonModule
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
