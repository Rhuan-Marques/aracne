package golang

import "llm-topology/internal/topology/domain"

type FunctionCut struct {
	GolangFunction
	Cut string
}

type StructCut struct {
	GolangStruct
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

type StructUsage struct {
	ID          StructID
	Name        string
	Description string
	Location    domain.Location
	Methods     []SimplifiedFunction
}

type InterfaceUsage struct {
	ID              InterfaceID
	Name            string
	Description     string
	Location        domain.Location
	Implementations []InterfaceImplementation
}

type InterfaceImplementation struct {
	StructID    StructID
	Name        string
	Description string
	Location    domain.Location
	Methods     []SimplifiedFunction
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
	FileID FileID
	Line   int
	Title  string
	Cut    string
}

type SimplifiedInterface struct {
	ID              InterfaceID
	Name            string
	Description     string
	Location        domain.Location
	NeedToImplement bool
}

type GoFunctionContext struct {
	Function        *FunctionCut
	ParentStruct    *StructCut
	CalledFunctions []SimplifiedFunction
	StructsUsed     []StructUsage
	InterfacesUsed  []InterfaceUsage
	ExtVarsUsed     []SimplifiedExtVar
	Dependencies    []DependancyPath
	PackagesUsed    []PackagePath
	Blocks          []ContextBlock
}

type GoStructContext struct {
	Struct         *StructCut
	Constructor    *FunctionCut
	Interfaces     []SimplifiedInterface
	Methods        []SimplifiedFunction
	StructsUsed    []StructUsage
	InterfacesUsed []InterfaceUsage
	ExtVarsUsed    []SimplifiedExtVar
	Dependencies   []DependancyPath
	PackagesUsed   []PackagePath
	Blocks         []ContextBlock
}

type TopologyWarning struct {
	Resource          domain.ResourceKind
	AffectedFunctions []FunctionID
	Message           string
}
