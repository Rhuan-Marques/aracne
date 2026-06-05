package golang

import "ltp/internal/topology/domain"

// Wraps a GolangFunction with its source code cut string, providing both the function metadata and the actual source lines for display or analysis.
type FunctionCut struct {
	GolangFunction
	Cut string
}

// Holds a GolangStruct together with its raw source code cut (the lines of code implementing the struct) for context display.
type StructCut struct {
	GolangStruct
	Cut string
}

// Holds a GolangInterface together with its raw source code cut for context display.
type InterfaceCut struct {
	GolangInterface
	Cut string
}

// Holds a GolangNamedType together with its raw source code cut for context display.
type NamedTypeCut struct {
	GolangNamedType
	Cut string
}

// Represents a lightweight summary of a function for context display, containing its ID, name, description, input/output parameters, and source location.
type SimplifiedFunction struct {
	ID          FunctionID
	Name        string
	Description string
	Input       []VariableDefinition
	Output      []VariableDefinition
	Location    domain.Location
}

// Represents a struct used by a function or constructor, containing its ID, name, description, source location, and which methods were called on it.
type StructUsage struct {
	ID          StructID
	Name        string
	Description string
	Location    domain.Location
	Methods     []SimplifiedFunction
}

// Represents an interface used by a function or struct, including its ID, name, description, location, and a list of implementing structs with their relevant methods.
type InterfaceUsage struct {
	ID              InterfaceID
	Name            string
	Description     string
	Location        domain.Location
	Implementations []InterfaceImplementation
}

// Represents a struct's implementation of an interface. Key fields include the struct ID and name, a description, the source location, and the list of simplified functions implementing the interface methods.
type InterfaceImplementation struct {
	StructID    StructID
	Name        string
	Description string
	Location    domain.Location
	Methods     []SimplifiedFunction
}

// Simplified representation of an external variable for context output, containing ID, name, description, value (truncated), and source location.
type SimplifiedExtVar struct {
	ID          ExternalVarID
	Name        string
	Description string
	Value       string
	Location    domain.Location
}

// Represents a sorted block of contextual code with a Kind label, file ID, line number, title, and source cut, used for proximity rendering in function/struct context views.
type ContextBlock struct {
	Kind   string
	FileID FileID
	Line   int
	Title  string
	Cut    string
}

// Lightweight representation of an interface for context output, including a NeedToImplement flag indicating whether a struct needs to satisfy this interface.
type SimplifiedInterface struct {
	ID              InterfaceID
	Name            string
	Description     string
	Location        domain.Location
	NeedToImplement bool
}

// Represents a resource that uses another resource, for reverse-reference context output.
type ResourceUsage struct {
	ID          string
	Kind        domain.ResourceKind
	Name        string
	Description string
	Location    domain.Location
}

// GoFunctionContext is the enriched context returned by ReadFunction. It bundles the function cut, parent struct (for methods), called functions, struct/interface usage, external variables, dependencies, packages, and sorted context blocks for proximity rendering in LLM prompts.
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

// Holds the complete enriched context of a Go struct: the struct cut, constructor, implemented interfaces, methods, used structs/interfaces/extvars, dependencies, packages, and a sorted list of context blocks for rendering.
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

// Holds the complete enriched context of a Go interface: its source cut, implementing structs and methods, dependencies, packages, and sorted context blocks.
type GoInterfaceContext struct {
	Interface       *InterfaceCut
	Implementations []InterfaceImplementation
	Dependencies    []DependancyPath
	PackagesUsed    []PackagePath
	Blocks          []ContextBlock
}

// Holds the complete enriched context of a Go named type: its source cut, resources that use it, dependencies, packages, and sorted context blocks.
type GoNamedTypeContext struct {
	NamedType    *NamedTypeCut
	UsedBy       []ResourceUsage
	Dependencies []DependancyPath
	PackagesUsed []PackagePath
	Blocks       []ContextBlock
}

// Represents a warning emitted during topology updates when functions are removed or signatures change, containing the affected resource kind, function IDs, and a human-readable message.
type TopologyWarning struct {
	Resource          domain.ResourceKind
	AffectedFunctions []FunctionID
	Message           string
}
