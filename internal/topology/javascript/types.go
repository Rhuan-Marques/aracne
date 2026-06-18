package javascript

import "aracne/internal/topology/domain"

type FunctionCut struct {
	JavaScriptFunction
	Cut string
}

type ClassCut struct {
	JavaScriptClass
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
	ID          ClassID
	Name        string
	Description string
	Location    domain.Location
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

type SimplifiedNamedType struct {
	ID          NamedTypeID
	Name        string
	Description string
	Location    domain.Location
}

type JavaScriptFunctionContext struct {
	Function        *FunctionCut
	ParentClass     *ClassCut
	CalledFunctions []SimplifiedFunction
	ClassesUsed     []ClassUsage
	InterfacesUsed  []SimplifiedInterface
	NamedTypesUsed  []SimplifiedNamedType
	ExtVarsUsed     []SimplifiedExtVar
	Dependencies    []DependancyPath
	ModulesUsed     []PackagePath
	Incoming        []domain.ResourceRef
	Blocks          []ContextBlock
}

type JavaScriptClassContext struct {
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
	JavaScriptModule
	Cut string
}

type PackageCut struct {
	JavaScriptPackage
	Cut string
}

type JavaScriptModuleContext struct {
	Module       *ModuleCut
	FromPackage  PackagePath
	Functions    []SimplifiedFunction
	Classes      []ClassUsage
	ExtVars      []SimplifiedExtVar
	Imports      []PackagePath
	Dependencies []DependancyPath
	Blocks       []ContextBlock
}

type JavaScriptPackageContext struct {
	Package      *PackageCut
	Files        []ModuleID
	Functions    []SimplifiedFunction
	Classes      []ClassUsage
	ExtVars      []SimplifiedExtVar
	Dependencies []DependancyPath
	Blocks       []ContextBlock
}

type JavaScriptDependencyContext struct {
	Dependency DependancyPath
	UsedBy     []ResourceUsage
	Blocks     []ContextBlock
}

type InterfaceCut struct {
	JavaScriptInterface
	Cut string
}

type NamedTypeCut struct {
	JavaScriptNamedType
	Cut string
}

type SimplifiedInterface struct {
	ID          InterfaceID
	Name        string
	Description string
	Location    domain.Location
}

type JavaScriptInterfaceContext struct {
	Interface       *InterfaceCut
	BaseInterfaces  []SimplifiedInterface
	Implementations []SimplifiedClass
	Incoming        []domain.ResourceRef
	Blocks          []ContextBlock
}

type JavaScriptNamedTypeContext struct {
	NamedType *NamedTypeCut
	UsedBy    []ResourceUsage
	Blocks    []ContextBlock
}

type ResourceUsage struct {
	ID          string
	Kind        domain.ResourceKind
	Name        string
	Description string
	Location    domain.Location
}
