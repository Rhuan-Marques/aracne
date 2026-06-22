package golang

import "aracne/internal/topology/domain"

type FunctionID = string
type StructID = string
type InterfaceID = string
type ExternalVarID = string
type FileID = string
type PackagePath = string
type DependancyPath = string
type NamedTypeID = string

// Represents a variable with its name, type, and resolved canonical type ID for cross-package type resolution.
type VariableDefinition struct {
	Name   string
	Typing string
	// TypingID is the canonical topology resource ID (pkgPath.Name) of the
	// type named by Typing, resolved at PARSE TIME against the defining file's
	// import map. It is "" for builtin/composite/unresolvable types. Storing the
	// resolved id here lets cross-package consumers (e.g. a caller inferring the
	// return type of an imported function) resolve the type without the callee's
	// import context. It is a candidate id: consumers must still verify it exists.
	TypingID string
}

// Defines a function signature with its name, input parameters, and output return types.
type FunctionDefinition struct {
	Name   string
	Input  []VariableDefinition
	Output []VariableDefinition
}

// Wraps a package path dependency for tracking external package imports.
type Dependancy struct {
	PackagePath DependancyPath
}

// Complete Go codebase topology containing functions, structs, interfaces, types, variables, files, packages, dependencies, and analysis warnings/errors.
type GolangTopology struct {
	Root         string
	Functions    map[FunctionID]GolangFunction
	Structs      map[StructID]GolangStruct
	Interfaces   map[InterfaceID]GolangInterface
	NamedTypes   map[NamedTypeID]GolangNamedType
	ExternalVars map[ExternalVarID]GolangExternalVar
	Files        map[FileID]GolangFile
	Packages     map[PackagePath]GolangPackage
	Dependencies []Dependancy
	Warnings     map[string]domain.TopologyWarning
	Errors       map[string]string
}

// Represents a Go function with signature, inputs, outputs, and optionally a receiver struct.
type GolangFunction struct {
	ID          FunctionID
	Name        string
	Description string
	Input       []VariableDefinition
	Output      []VariableDefinition
	Loc         domain.Location
	Connections map[ConnectionKind][]string
	MethodFrom  *StructID
}

// Represents a Go struct type with its fields, location, connections, and optional constructor function.
type GolangStruct struct {
	ID          StructID
	Name        string
	Description string
	Params      []VariableDefinition
	Loc         domain.Location
	Connections map[ConnectionKind][]string
	Constructor *FunctionID
}

// Represents a Go interface with its methods and implementation relationships.
type GolangInterface struct {
	ID          InterfaceID
	Name        string
	Description string
	Methods     []FunctionDefinition
	Loc         domain.Location
	Connections map[ConnectionKind][]string
}

// Represents a Go named type with its underlying type and location in source.
type GolangNamedType struct {
	ID          NamedTypeID
	Name        string
	Description string
	Underlying  string
	Loc         domain.Location
	Connections map[ConnectionKind][]string
}

// Represents an external variable with its type, value, and location in Go source code.
type GolangExternalVar struct {
	ID          ExternalVarID
	Name        string
	Description string
	Typing      string
	Value       *any
	Location    domain.Location
}

// Represents a Go source file with its package, connections to other resources.
type GolangFile struct {
	ID          FileID
	Name        string
	Description string
	FromPackage PackagePath
	Connections map[ConnectionKind][]string
}

// Represents a Go package with its path, description, and connections to other resources.
type GolangPackage struct {
	Path        PackagePath
	Description string
	Connections map[ConnectionKind][]string
}
