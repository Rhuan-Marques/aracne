package javascript

import "aracne/internal/topology/domain"

// Wrapper combining JavaScriptFunction with a code snippet (Cut) for context display.
type FunctionCut struct {
	JavaScriptFunction
	Cut string
}

// A JavaScript class with a code snippet (Cut) for display or context.
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

// Simplified representation of a JavaScript function with ID, name, input/output parameters, visibility, and optional full block details.
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

// Represents a JavaScript class with its metadata, methods, and optional detailed context block.
type ClassUsage struct {
	ID          ClassID
	Name        string
	Description string
	Location    domain.Location
	Methods     []SimplifiedFunction
	Visibility  domain.Visibility
	Full        *FullBlock
}

// Simplified representation of a JavaScript class with ID, name, description, and location.
type SimplifiedClass struct {
	ID          ClassID
	Name        string
	Description string
	Location    domain.Location
}

// Simplified representation of an external JavaScript variable with ID, name, value, visibility, and optional full block details.
type SimplifiedExtVar struct {
	ID          ExternalVarID
	Name        string
	Description string
	Value       string
	Location    domain.Location
	Visibility  domain.Visibility
	Full        *FullBlock
}

// A code context block with kind, location, line number, title, and code snippet.
type ContextBlock struct {
	Kind   string
	FileID ModuleID
	Line   int
	Title  string
	Cut    string
}

// Simplified representation of a JavaScript named type with ID, name, description, and location.
type SimplifiedNamedType struct {
	ID          NamedTypeID
	Name        string
	Description string
	Location    domain.Location
}

// Full context for a function including parent class, called functions, used types/classes/interfaces, external vars, dependencies, and incoming references.
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

// Full context graph for a JavaScript class including constructor, methods, base classes, dependencies, and incoming references.
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

// A JavaScript module with a source code snippet (cut) showing relevant context.
type ModuleCut struct {
	JavaScriptModule
	Cut string
}

// Context view of a JavaScript/TypeScript module with functions, classes, external vars, imports, dependencies, and code blocks.
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

// Context for a dependency including what uses it, blocks, and resource references.
type JavaScriptDependencyContext struct {
	Dependency DependancyPath
	UsedBy     []ResourceUsage
	Blocks     []ContextBlock
}

// Wrapper combining JavaScriptInterface with a code snippet (Cut) for context display.
type InterfaceCut struct {
	JavaScriptInterface
	Cut string
}

// A JavaScript named type with a source code snippet (cut) showing relevant context.
type NamedTypeCut struct {
	JavaScriptNamedType
	Cut string
}

// Simplified representation of a JavaScript interface with ID, name, description, and location.
type SimplifiedInterface struct {
	ID          InterfaceID
	Name        string
	Description string
	Location    domain.Location
}

// Context view of a JavaScript/TypeScript interface with base interfaces, implementing classes, incoming refs, and code blocks.
type JavaScriptInterfaceContext struct {
	Interface       *InterfaceCut
	BaseInterfaces  []SimplifiedInterface
	Implementations []SimplifiedClass
	Incoming        []domain.ResourceRef
	Blocks          []ContextBlock
}

// Context view of a JavaScript/TypeScript named type (type alias or interface) with usage references and code blocks.
type JavaScriptNamedTypeContext struct {
	NamedType *NamedTypeCut
	UsedBy    []ResourceUsage
	Blocks    []ContextBlock
}

// Describes a JavaScript resource usage with its ID, kind, name, description, and source location.
type ResourceUsage struct {
	ID          string
	Kind        domain.ResourceKind
	Name        string
	Description string
	Location    domain.Location
}
