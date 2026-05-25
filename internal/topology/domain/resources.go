// Package navigator defines the serializable domain types that represent a complete
// Go project topology. All types in this package map directly to the JSON output
// format, forming an interconnected graph of packages, files, types, and their
// relationships discovered during analysis.
package domain

// ExternalVarID uniquely identifies a package-level variable or constant in the topology graph.
type ExternalVarID string

// FilePath is an absolute filesystem path to a Go source file.
type FilePath string

// PackagePath is the Go import path of a package belonging to the analyzed repository.
type PackagePath string

// DependancyPath is the Go import path of an external dependency.
type DependancyPath string

// FunctionID uniquely identifies a function or method in the topology graph.
type FunctionID string

// StructID uniquely identifies a struct type in the topology graph.
type StructID string

// InterfaceID uniquely identifies an interface type in the topology graph.
type InterfaceID string

// TopologyWarning is emitted when an incremental update detects a removed or
// signature-changed element that may require manual attention elsewhere.
type TopologyWarning struct {
	Resource         ResourceName
	AffectedFunctions []FunctionID
	Message          string
}

// CodeEntry holds the raw source code for a specific source location range.
type CodeEntry struct {
	Location Location
	Cut      string
}

// FunctionCut pairs a complete Function with its full source code cut.
type FunctionCut struct {
	Function
	Cut string
}

// StructCut pairs a complete Struct with its full source code cut.
type StructCut struct {
	Struct
	Cut string
}

// SimplifiedFunction is a reduced function view suitable for contextual
// display: ID, signature, description, and source location only.
type SimplifiedFunction struct {
	ID          FunctionID
	Name        string
	Description string
	Input       []VariableDefinition
	Output      []VariableDefinition
	Location    Location
}

// StructUsage describes how a function uses a specific struct: if no
// Methods are populated the struct is referenced by type only; otherwise
// Methods lists the struct methods that the function calls.
type StructUsage struct {
	ID          StructID
	Name        string
	Description string
	Location    Location
	Methods     []SimplifiedFunction
}

// InterfaceUsage describes the function's relationship with an interface.
// If the function calls methods on implementing structs, Implementations
// details which structs and which of their methods are invoked.
type InterfaceUsage struct {
	ID              InterfaceID
	Name            string
	Description     string
	Location        Location
	Implementations []InterfaceImplementation
}

// InterfaceImplementation represents a struct that satisfies an interface,
// paired with the specific methods that the calling function invokes.
type InterfaceImplementation struct {
	StructID    StructID
	Name        string
	Description string
	Location    Location
	Methods     []SimplifiedFunction
}

// SimplifiedExtVar is a reduced external variable view for contextual
// display. Large values are truncated to 500 characters.
type SimplifiedExtVar struct {
	ID          ExternalVarID
	Name        string
	Description string
	Value       string
	Location    Location
}

// ContextBlock is a flat, ordered entry in the FunctionContext block list.
// Blocks are sorted by (FilePath, Line) for proximity-based rendering so
// that elements from the same source file appear adjacent in source order.
type ContextBlock struct {
	Kind     string   // e.g. "function", "parent_struct", "called_func", "struct", "struct_method", "interface", "interface_impl", "impl_method", "extvar"
	FilePath FilePath
	Line     int
	Title    string
	Cut      string   // populated only for full-cut blocks (function, parent_struct)
}

// SimplifiedInterface is a reduced interface view for the struct context,
// indicating whether the struct still needs to implement its methods.
type SimplifiedInterface struct {
	ID              InterfaceID
	Name            string
	Description     string
	Location        Location
	NeedToImplement bool
}

// StructContext is the top-level output of TopologyManager.ReadStruct.
type StructContext struct {
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

// FunctionContext is the top-level output of TopologyManager.ReadFunction.
// It provides both categorized fields for direct data access and a flat
// Blocks list that can be rendered in source-line proximity order.
type FunctionContext struct {
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

// Location records the source position of a declared element within a file.
type Location struct {
	StartsAt int
	EndsAt   int
	Path     FilePath
}

// VariableDefinition describes a typed variable parameter or struct field.
type VariableDefinition struct {
	Name   string
	Typing string
}

// FunctionDefinition represents a method signature within an interface or a standalone function type.
type FunctionDefinition struct {
	Name   string
	Input  []VariableDefinition
	Output []VariableDefinition
}

// Dependancy records a single external package that the analyzed repository imports.
type Dependancy struct {
	PackagePath DependancyPath
}

type ResourceName string

var (
	EXTERNAL_VAR_RESOURCE ResourceName = "ExternalVar"
	FUNCTION_RESOURCE     ResourceName = "Function"
	FILE_RESOURCE         ResourceName = "File"
	STRUCT_RESOURCE       ResourceName = "Struct"
	PACKAGE_RESOURCE      ResourceName = "Package"
	INTERFACE_RESOURCE    ResourceName = "Interface"
	DEPENDENCY_RESOURCE   ResourceName = "Dependency"
)

type Resource interface {
	ResourceName() ResourceName
}

// ExternalVar represents a package-level variable or constant declaration.
type ExternalVar struct {
	ID          ExternalVarID
	Name        string
	Description string
	Typing      string
	Value       *any
	Location
}

func (*ExternalVar) ResourceName() ResourceName {
	return EXTERNAL_VAR_RESOURCE
}

// Function represents a declared function or method in the source code.
type Function struct {
	ID               FunctionID
	Name             string
	Description      string
	Input            []VariableDefinition
	Output           []VariableDefinition
	Loc              Location
	MethodFrom       *StructID
	ExternalVarsUsed []ExternalVarID
	FunctionsUsed    []FunctionID
	StructsUsed      []StructID
	InterfacesUsed   []InterfaceID
	DependanciesUsed []DependancyPath
	PackagesUsed     []PackagePath
}

func (*Function) ResourceName() ResourceName {
	return FUNCTION_RESOURCE
}

// Struct represents a struct type declaration with its field definitions.
type Struct struct {
	ID               StructID
	Name             string
	Description      string
	Params           []VariableDefinition
	Methods          []FunctionID
	Constructor      *FunctionID
	Loc              Location
	Implements       *InterfaceID
	DependanciesUsed []DependancyPath
	PackagesUsed     []PackagePath
}

func (*Struct) ResourceName() ResourceName {
	return STRUCT_RESOURCE
}

// Interface represents an interface type declaration.
type Interface struct {
	ID               InterfaceID
	Name             string
	Description      string
	ImplementedBy    []StructID
	Methods          []FunctionDefinition
	Loc              Location
	DependanciesUsed []DependancyPath
	PackagesUsed     []PackagePath
}

func (*Interface) ResourceName() ResourceName {
	return INTERFACE_RESOURCE
}

// File represents a single Go source file discovered during directory walking.
type File struct {
	Path                 FilePath
	Name                 string
	Description          string
	Functions            []FunctionID
	ExternalVars         []ExternalVarID
	Structs              []StructID
	Interfaces           []InterfaceID
	PackagesImported     []PackagePath
	DependanciesImported []Dependancy
	FromPackage          PackagePath
}

func (*File) ResourceName() ResourceName {
	return FILE_RESOURCE
}

// Package groups all files and topology elements that belong to the same Go package.
type Package struct {
	Path         PackagePath
	Functions    []FunctionID
	Structs      []StructID
	Interfaces   []InterfaceID
	ExternalVars []ExternalVarID
	Files        []FilePath
}

func (*Package) ResourceName() ResourceName {
	return PACKAGE_RESOURCE
}

// Topology is the root container for the full project analysis result.
type Topology struct {
	Root         string
	Packages     map[PackagePath]Package
	Files        map[FilePath]File
	Struct       map[StructID]Struct
	Interfaces   map[InterfaceID]Interface
	Functions    map[FunctionID]Function
	ExternalVars map[ExternalVarID]ExternalVar
	Dependancies []Dependancy
	Errors       map[FilePath]string
}
