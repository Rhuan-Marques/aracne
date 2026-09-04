package rust

import "github.com/Rhuan-Marques/aracne/internal/topology/domain"

// Resource ID aliases. All IDs are Rust module paths rooted at the Cargo package
// name and separated by "::", e.g. "mycrate::shapes::Circle" (struct),
// "mycrate::shapes::Circle::area" (method), "mycrate::factory::make_circle"
// (free function). Module (file) nodes are keyed by absolute file path instead.
type FunctionID = string
type StructID = string
type TraitID = string
type NamedTypeID = string
type VariableID = string
type ModuleID = string
type DependencyPath = string

// Represents a variable (parameter, return value, or struct field) with its name,
// type annotation, and the resolved topology resource ID the type maps to.
type VariableDefinition struct {
	Name   string
	Typing string
	// TypingID is the canonical topology resource ID that Typing resolves to (a
	// struct, enum, trait, or named type), or "" for built-in/external/unresolved
	// types. Resolved in the DEFINING module's `use` context during
	// resolveTopology, so a caller in another file can follow a return type
	// (Self/constructor inference) without the callee's imports.
	TypingID string
}

// Represents a trait method signature (the declaration inside a trait body),
// with HasDefault marking methods that ship a default body.
type FunctionDefinition struct {
	Name       string
	Input      []VariableDefinition
	Output     []VariableDefinition
	HasDefault bool
}

// Holds the complete Rust topology graph: functions/methods/associated functions/
// macros, structs (incl. enums and unions), traits, type aliases, module-level
// variables, modules (files), and their dependencies.
type RustTopology struct {
	Root         string
	Functions    map[FunctionID]RustFunction
	Structs      map[StructID]RustStruct
	Traits       map[TraitID]RustTrait
	NamedTypes   map[NamedTypeID]RustNamedType
	Variables    map[VariableID]RustVariable
	Modules      map[ModuleID]RustModule
	Dependencies []RustDependency
	Errors       map[string]string
}

// Represents a Rust function, method, associated function, or macro definition.
// MethodFrom is set for impl methods/associated functions and points at the type
// they belong to; it is nil for free functions and macros.
type RustFunction struct {
	ID          FunctionID
	Name        string
	Description string
	Input       []VariableDefinition
	Output      []VariableDefinition
	Loc         domain.Location
	Connections map[ConnectionKind][]string
	MethodFrom  *StructID
	IsAsync     bool
	IsUnsafe    bool
	IsConst     bool
	// IsAssociated marks an associated function (in an impl block but with no
	// `self` receiver), e.g. constructors like `Circle::new`.
	IsAssociated bool
	// Receiver is the method receiver flavor: "" (none/associated), "value"
	// (self), "ref" (&self), or "ref_mut" (&mut self).
	Receiver string
	// IsMacro marks a `macro_rules!` definition (modeled as a function).
	IsMacro bool
	// Visibility is the Rust visibility: "public", "private", "crate", "super",
	// or "restricted:<path>".
	Visibility string
	Exported   bool
}

// Represents a Rust struct, enum, or union. Enums set IsEnum (with Variants
// listed); unions set IsUnion. Methods attach via impl blocks (MethodFrom on the
// RustFunction), resolved cross-file in the matcher.
type RustStruct struct {
	ID          StructID
	Name        string
	Description string
	Fields      []VariableDefinition
	Loc         domain.Location
	Connections map[ConnectionKind][]string
	Constructor *FunctionID
	IsEnum      bool
	IsUnion     bool
	IsTuple     bool
	IsUnit      bool
	// Variants holds enum variant names (when IsEnum).
	Variants []string
	// Derives holds the trait names from `#[derive(...)]` attributes.
	Derives []string
	// Generics holds declared type-parameter names, e.g. ["T"].
	Generics   []string
	Visibility string
	Exported   bool
}

// Represents a Rust trait (the interface kind). Methods are stored inline (like
// Go interfaces). Bounds holds supertrait names (`trait A: B`), resolved into
// inherits/inherited_by edges by the matcher.
type RustTrait struct {
	ID          TraitID
	Name        string
	Description string
	Methods     []FunctionDefinition
	AssocTypes  []string
	AssocConsts []string
	Bounds      []string
	Generics    []string
	Loc         domain.Location
	Connections map[ConnectionKind][]string
	Visibility  string
	Exported    bool
}

// Represents a Rust type alias (`type X = Y`).
type RustNamedType struct {
	ID          NamedTypeID
	Name        string
	Description string
	Underlying  string
	Generics    []string
	Loc         domain.Location
	Connections map[ConnectionKind][]string
	Visibility  string
	Exported    bool
}

// Represents a Rust module-level constant or static.
type RustVariable struct {
	ID          VariableID
	Name        string
	Description string
	Typing      string
	Value       *any
	IsConst     bool
	IsStatic    bool
	Mutable     bool
	Visibility  string
	Exported    bool
	Location    domain.Location
}

// Represents a Rust module: a single source file. Keyed by absolute path; its
// ModulePath is the "::"-qualified Rust path (e.g. "mycrate::shapes") that its
// symbols are namespaced under and that `use` declarations resolve against.
type RustModule struct {
	ID          ModuleID
	Name        string
	Description string
	ModulePath  string
	Connections map[ConnectionKind][]string
}

// Represents an external crate dependency (from Cargo.toml or a `use <crate>::`).
type RustDependency struct {
	CratePath DependencyPath
}
