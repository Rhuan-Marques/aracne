package python

import "github.com/Rhuan-Marques/aracne/internal/topology/domain"

// A Python function with its code snippet cut from the source.
type FunctionCut struct {
	PythonFunction
	Cut string
}

// Represents a Python class with its source location cut range.
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

// Represents a Python function with its ID, name, description, input/output parameters, location, visibility, and optional full block details.
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

// Represents a Python class with its methods, visibility, and full block context for topology queries.
type ClassUsage struct {
	ID          ClassID
	Name        string
	Description string
	Location    domain.Location
	Methods     []SimplifiedFunction
	Visibility  domain.Visibility
	Full        *FullBlock
}

// Simplified representation of a Python class with its ID, name, description, location, and interface implementation tracking.
type SimplifiedClass struct {
	ID                     ClassID
	Name                   string
	Description            string
	Location               domain.Location
	NeedToImplement        bool
	NeedToImplementMethods []string
}

// Represents an external variable with its ID, name, description, value, location, visibility, and optional full block details.
type SimplifiedExtVar struct {
	ID          ExternalVarID
	Name        string
	Description string
	Value       string
	Location    domain.Location
	Visibility  domain.Visibility
	Full        *FullBlock
}

// Metadata for a code context block including kind, file location, line number, and code snippet.
type ContextBlock struct {
	Kind   string
	FileID ModuleID
	Line   int
	Title  string
	Cut    string
}

// Provides complete context for a Python function including called functions, classes, dependencies, and incoming references.
type PythonFunctionContext struct {
	Function    *FunctionCut
	ParentClass *ClassCut
	// OversizedParent is the enclosing type when it was too large to inline above the member
	// (read.context_filter.max_inline_parent_lines). It renders as an ordinary named+described
	// neighbour instead of as source.
	OversizedParent *ClassCut
	CalledFunctions []SimplifiedFunction
	ClassesUsed     []ClassUsage
	ExtVarsUsed     []SimplifiedExtVar
	Dependencies    []DependancyPath
	ModulesUsed     []PackagePath
	Incoming        []domain.ResourceRef
	Blocks          []ContextBlock
}

// Holds context for a Python class including its constructor, bases, methods, dependencies, and incoming references.
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

// Represents a Python module with an associated code cut/snippet.
type ModuleCut struct {
	PythonModule
	Cut string
}

// Context view of a Python module containing its functions, classes, external variables, imports, dependencies, and code blocks.
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

// Holds a dependency path with its usages and code blocks.
type PythonDependencyContext struct {
	Dependency DependancyPath
	UsedBy     []ResourceUsage
	Blocks     []ContextBlock
}

// Records a resource usage with its ID, kind, name, description, and source location.
type ResourceUsage struct {
	ID          string
	Kind        domain.ResourceKind
	Name        string
	Description string
	Location    domain.Location
}

// Describes a topology warning with the affected resource kind, list of impacted functions, and a warning message.
type TopologyWarning struct {
	Resource          domain.ResourceKind
	AffectedFunctions []FunctionID
	Message           string
}
