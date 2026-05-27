package golang

import "llm-topology/internal/topology/domain"

type FunctionID = string
type StructID = string
type InterfaceID = string
type ExternalVarID = string
type FileID = string
type PackagePath = string
type DependancyPath = string

// VariableDefinition holds the name and type string for a parsed variable, used to represent function parameters, results, and struct fields in the topology model.
type VariableDefinition struct {
	Name   string
	Typing string
}

// Represents a function's signature with Name, Input parameters, and Output parameters, each as VariableDefinition slices. Used to describe function signatures in the topology model.
type FunctionDefinition struct {
	Name   string
	Input  []VariableDefinition
	Output []VariableDefinition
}

// Dependancy represents a single external dependency used by a Go resource. It contains the import package path in its PackagePath field.
type Dependancy struct {
	PackagePath DependancyPath
}

// Represents the complete Go project topology in-memory. Contains maps of functions, structs, interfaces, external variables, files, packages, and dependencies indexed by their typed IDs.
type GolangTopology struct {
	Root         string
	Functions    map[FunctionID]GolangFunction
	Structs      map[StructID]GolangStruct
	Interfaces   map[InterfaceID]GolangInterface
	ExternalVars map[ExternalVarID]GolangExternalVar
	Files        map[FileID]GolangFile
	Packages     map[PackagePath]GolangPackage
	Dependencies []Dependancy
	Errors       map[string]string
}

// Represents a Go function or method in the topology, containing its name, description, input/output parameters, source location, connections, and an optional parent struct ID for methods.
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

// Represents a parsed Go struct with its ID, name, fields, source location, connections, and optional constructor reference.
type GolangStruct struct {
	ID          StructID
	Name        string
	Description string
	Params      []VariableDefinition
	Loc         domain.Location
	Connections map[ConnectionKind][]string
	Constructor *FunctionID
}

// Represents a Go interface in the topology model. Contains ID, Name, Description, a list of Methods (FunctionDefinition), source Location, and a Connections map for tracking relationships to other resources.
type GolangInterface struct {
	ID          InterfaceID
	Name        string
	Description string
	Methods     []FunctionDefinition
	Loc         domain.Location
	Connections map[ConnectionKind][]string
}

// Represents an external (package-level) variable in the Go source, storing its name, type, description, optional value, and source location.
type GolangExternalVar struct {
	ID          ExternalVarID
	Name        string
	Description string
	Typing      string
	Value       *any
	Location    domain.Location
}

// Represents a Go source file in the topology model with its ID, name, description, containing package, and typed connection maps to other topology resources.
type GolangFile struct {
	ID          FileID
	Name        string
	Description string
	FromPackage PackagePath
	Connections map[ConnectionKind][]string
}

// Represents a Go package in the topology model. Contains the package import Path, a Description extracted from doc comments, and a Connections map tracking relationships to other resources.
type GolangPackage struct {
	Path        PackagePath
	Description string
	Connections map[ConnectionKind][]string
}
