package javascript

import "aracne/internal/topology/domain"

type FunctionID = string
type ClassID = string
type InterfaceID = string
type NamedTypeID = string
type ExternalVarID = string
type ModuleID = string
type PackagePath = string
type DependancyPath = string

type VariableDefinition struct {
	Name   string
	Typing string
	// TypingID is the canonical topology resource ID that Typing resolves to (a class,
	// interface, or named type), or "" when the type is a built-in/external/unresolved.
	// It is resolved in the DEFINING file's import context during resolveTopology, so a
	// caller in another file can follow a return type without the callee's imports.
	TypingID string
}

type FunctionDefinition struct {
	Name   string
	Input  []VariableDefinition
	Output []VariableDefinition
}

type JavaScriptTopology struct {
	Root         string
	Functions    map[FunctionID]JavaScriptFunction
	Classes      map[ClassID]JavaScriptClass
	Interfaces   map[InterfaceID]JavaScriptInterface
	NamedTypes   map[NamedTypeID]JavaScriptNamedType
	ExternalVars map[ExternalVarID]JavaScriptExternalVar
	Modules      map[ModuleID]JavaScriptModule
	Packages     map[PackagePath]JavaScriptPackage
	Dependencies []JavaScriptDependancy
	Errors       map[string]string
}

type JavaScriptFunction struct {
	ID          FunctionID
	Name        string
	Description string
	Input       []VariableDefinition
	Output      []VariableDefinition
	Loc         domain.Location
	Connections map[ConnectionKind][]string
	MethodFrom  *ClassID
	IsAsync     bool
	IsGenerator bool
	IsStatic    bool
	// Kind is one of "function", "arrow", "method", "getter", "setter".
	Kind string
	// IsAbstract marks a TypeScript `abstract` method (declaration only, no body).
	IsAbstract bool
	// Decorators holds applied decorator names (TypeScript), e.g. "Component".
	Decorators []string
	// Accessibility is the TypeScript member modifier: "public"/"private"/"protected" (or "").
	Accessibility string
	Exported      bool
}

type JavaScriptClass struct {
	ID          ClassID
	Name        string
	Description string
	// Bases holds the superclass expression of an `extends` clause (at most one in JS).
	Bases []string
	// ImplementsRaw holds the interface names from a TypeScript `implements` clause (raw,
	// pre-resolution); the matcher turns these into implements/implemented_by edges
	// (accessible via the Implements() method). Mirrors Bases/Inherits().
	ImplementsRaw []string
	IsAbstract    bool
	Decorators    []string
	Loc           domain.Location
	Connections   map[ConnectionKind][]string
	Constructor   *FunctionID
	Exported      bool
}

// JavaScriptInterface models a TypeScript interface (JavaScript has none). Methods and
// property signatures are stored inline (like Go interfaces) rather than as separate
// resources. Bases holds the names from an `extends` clause (interfaces can extend many).
type JavaScriptInterface struct {
	ID          InterfaceID
	Name        string
	Description string
	Methods     []FunctionDefinition
	Properties  []VariableDefinition
	Bases       []string
	Loc         domain.Location
	Connections map[ConnectionKind][]string
}

// JavaScriptNamedType models a TypeScript `type` alias or `enum`. Kind is "type" or "enum";
// Underlying is the aliased type expression (or "enum"); Members lists enum member names.
type JavaScriptNamedType struct {
	ID          NamedTypeID
	Name        string
	Description string
	Kind        string
	Underlying  string
	Members     []string
	Loc         domain.Location
	Connections map[ConnectionKind][]string
}

type JavaScriptModule struct {
	ID          ModuleID
	Name        string
	Description string
	FromPackage PackagePath
	// DefaultExport is the local name bound to this module's `export default`
	// (or CommonJS `module.exports = name`), used to resolve default imports.
	DefaultExport string
	Connections   map[ConnectionKind][]string
}

type JavaScriptPackage struct {
	Path        PackagePath
	Description string
	Connections map[ConnectionKind][]string
}

type JavaScriptExternalVar struct {
	ID          ExternalVarID
	Name        string
	Description string
	Typing      string
	Value       *any
	Exported    bool
	Location    domain.Location
}

type JavaScriptDependancy struct {
	PackagePath DependancyPath
}
