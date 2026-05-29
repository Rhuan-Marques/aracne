package golang

import "llm-topology/internal/topology/domain"

type FunctionID = string
type StructID = string
type InterfaceID = string
type ExternalVarID = string
type FileID = string
type PackagePath = string
type DependancyPath = string
type NamedTypeID = string

type VariableDefinition struct {
	Name   string
	Typing string
}

type FunctionDefinition struct {
	Name   string
	Input  []VariableDefinition
	Output []VariableDefinition
}

type Dependancy struct {
	PackagePath DependancyPath
}

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

type GolangStruct struct {
	ID          StructID
	Name        string
	Description string
	Params      []VariableDefinition
	Loc         domain.Location
	Connections map[ConnectionKind][]string
	Constructor *FunctionID
}

type GolangInterface struct {
	ID          InterfaceID
	Name        string
	Description string
	Methods     []FunctionDefinition
	Loc         domain.Location
	Connections map[ConnectionKind][]string
}

type GolangNamedType struct {
	ID          NamedTypeID
	Name        string
	Description string
	Underlying  string
	Loc         domain.Location
	Connections map[ConnectionKind][]string
}

type GolangExternalVar struct {
	ID          ExternalVarID
	Name        string
	Description string
	Typing      string
	Value       *any
	Location    domain.Location
}

type GolangFile struct {
	ID          FileID
	Name        string
	Description string
	FromPackage PackagePath
	Connections map[ConnectionKind][]string
}

type GolangPackage struct {
	Path        PackagePath
	Description string
	Connections map[ConnectionKind][]string
}
