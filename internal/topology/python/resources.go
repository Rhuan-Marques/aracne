package python

import "github.com/Rhuan-Marques/aracne/internal/topology/domain"

type FunctionID = string
type ClassID = string
type ExternalVarID = string
type ModuleID = string
type PackagePath = string
type DependancyPath = string

// Defines a variable with its name, type annotation, and optional resolved canonical class ID for cross-package type resolution.
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
	// Optional, Variadic and KeyOnly are what Python's declaration says about how the
	// parameter may be passed, and none of it is recoverable from Name and Typing alone.
	// Without them `def f(a, b=1)` and `def f(a, b)` are the same shape, so any check on
	// how many arguments a call passes would warn on every call that legitimately omits a
	// defaulted parameter -- which is most of them.
	Optional bool // has a default
	Variadic bool // *args or **kwargs
	KeyOnly  bool // declared after a bare * or *args, so never positional
}

// Signature metadata for a Python function including name, input and output variable definitions.
type FunctionDefinition struct {
	Name   string
	Input  []VariableDefinition
	Output []VariableDefinition
}

// Complete topology graph of a Python codebase with all functions, classes, external variables, modules, dependencies, and errors.
type PythonTopology struct {
	Root         string
	Functions    map[FunctionID]PythonFunction
	Classes      map[ClassID]PythonClass
	ExternalVars map[ExternalVarID]PythonExternalVar
	Modules      map[ModuleID]PythonModule
	Dependencies []PythonDependancy
	Errors       map[string]string
}

// Represents a Python function with metadata including name, decorators, inputs/outputs, and connections.
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
	// Python docstring positions (1-based; 0 = absent) used by descriptions apply.
	BodyLine int
	DocStart int
	DocEnd   int
}

// Describes a Python class with its name, base classes, parameters, methods, and connections.
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
	// Python docstring positions (1-based; 0 = absent) used by descriptions apply.
	BodyLine int
	DocStart int
	DocEnd   int
}

// Represents a Python module with its ID, name, description, parent package, and connection metadata.
type PythonModule struct {
	ID          ModuleID
	Name        string
	Description string
	FromPackage PackagePath
	Connections map[ConnectionKind][]string
}

// Represents an external variable with its name, type, value, and location.
type PythonExternalVar struct {
	ID          ExternalVarID
	Name        string
	Description string
	Typing      string
	Value       *any
	Location    domain.Location
}

// Represents a Python package dependency path.
type PythonDependancy struct {
	PackagePath DependancyPath
}
