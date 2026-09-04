package javascript

import "github.com/Rhuan-Marques/aracne/internal/topology/domain"

type FunctionID = string
type ClassID = string
type InterfaceID = string
type NamedTypeID = string
type ExternalVarID = string
type ModuleID = string
type PackagePath = string
type DependancyPath = string

// Represents a variable with its name, type annotation, and resolved topology resource ID (for classes, interfaces, or named types).
type VariableDefinition struct {
	Name   string
	Typing string
	// TypingID is the canonical topology resource ID that Typing resolves to (a class,
	// interface, or named type), or "" when the type is a built-in/external/unresolved.
	// It is resolved in the DEFINING file's import context during resolveTopology, so a
	// caller in another file can follow a return type without the callee's imports.
	TypingID string
	// Optional and Variadic are what the declaration says about how the parameter may be
	// passed. TypeScript marks an optional parameter with "?" or a default, and neither is
	// recoverable from Name and Typing -- without them `f(a: number, b?: string)` and
	// `f(a: number, b: string)` are the same shape, so any check on how many arguments a
	// call passes would warn on every call that legitimately omits the optional one.
	Optional bool
	Variadic bool // a rest parameter
}

// Represents a function signature with its name, input, and output variable definitions.
type FunctionDefinition struct {
	Name   string
	Input  []VariableDefinition
	Output []VariableDefinition
}

// Holds the complete JavaScript/TypeScript topology graph: functions, classes, interfaces, named types, modules, external vars, and their dependencies.
type JavaScriptTopology struct {
	Root         string
	Functions    map[FunctionID]JavaScriptFunction
	Classes      map[ClassID]JavaScriptClass
	Interfaces   map[InterfaceID]JavaScriptInterface
	NamedTypes   map[NamedTypeID]JavaScriptNamedType
	ExternalVars map[ExternalVarID]JavaScriptExternalVar
	Modules      map[ModuleID]JavaScriptModule
	Dependencies []JavaScriptDependancy
	Errors       map[string]string
}

// Represents a JavaScript/TypeScript function with signature, location, decorators, accessibility, and async/generator/method flags.
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

// Represents a JavaScript/TypeScript class with its metadata including inheritance, interfaces, constructor, and decorators.
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
	// MergedInterfaceMethods/Properties hold members folded in from a same-name
	// interface declaration (TypeScript class + interface declaration merging).
	// The class stays the primary resource; these inline signatures are surfaced
	// as the class resource's "methods"/"properties" properties.
	MergedInterfaceMethods    []FunctionDefinition
	MergedInterfaceProperties []VariableDefinition
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

// Represents a JavaScript/TypeScript module with identity, name, default export binding, and connection metadata.
type JavaScriptModule struct {
	ID          ModuleID
	Name        string
	Description string
	FromPackage PackagePath
	// DefaultExport is the local name bound to this module's `export default`
	// (or CommonJS `module.exports = name`), used to resolve default imports.
	DefaultExport string
	// ReExportsNamed maps an exported name to the (module, original name) it
	// forwards to via a named ESM re-export (`export {Orig as Exported} from
	// "./src"`). A consumer importing the exported name resolves through to the
	// source symbol, with name translation the whole-module re-export edge can't
	// express.
	ReExportsNamed map[string]ReExportTarget
	Connections    map[ConnectionKind][]string
}

// ReExportTarget is the destination of a named re-export: the source module and
// the original symbol name within it (which may be "default").
type ReExportTarget struct {
	Module ModuleID
	Name   string
}

// Represents an external variable with ID, name, type, value, and export/location metadata.
type JavaScriptExternalVar struct {
	ID          ExternalVarID
	Name        string
	Description string
	Typing      string
	Value       *any
	Exported    bool
	Location    domain.Location
}

// Represents a module dependency with its package path.
type JavaScriptDependancy struct {
	PackagePath DependancyPath
}
