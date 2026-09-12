package rust

import "github.com/Rhuan-Marques/aracne/internal/topology/domain"

// FunctionCut combines a RustFunction with its source code snippet (Cut) for
// context display.
type FunctionCut struct {
	RustFunction
	Cut string
}

// StructCut combines a RustStruct (struct/enum/union) with its source code
// snippet (Cut) for context display.
type StructCut struct {
	RustStruct
	Cut string
}

// TraitCut combines a RustTrait with its source code snippet (Cut) for context
// display.
type TraitCut struct {
	RustTrait
	Cut string
}

// NamedTypeCut combines a RustNamedType (type alias) with its source code
// snippet (Cut) for context display.
type NamedTypeCut struct {
	RustNamedType
	Cut string
}

// ModuleCut combines a RustModule (a file) with its source code snippet (Cut)
// for context display.
type ModuleCut struct {
	RustModule
	Cut string
}

// FullBlock carries everything needed to render a neighbour as a full source cut (its crate
// dependencies, an optional enclosing type, and the resource's own cut) without its own
// CONTEXT section. Path labels the fenced code block.
type FullBlock struct {
	Path      string
	Deps      []DependencyPath
	ParentCut string
	Cut       string
}

// SimplifiedFunction is a lightweight reference to a Rust function/method used
// in context lists.
type SimplifiedFunction struct {
	ID          FunctionID
	Name        string
	Description string
	Input       []VariableDefinition
	Output      []VariableDefinition
	Location    domain.Location
	// Visibility is the read.context_filter verdict for this neighbour (hidden / normal /
	// full), not the Rust `pub` visibility -- that is RustFunction.Visibility.
	Visibility domain.Visibility
	Full       *FullBlock
}

// SimplifiedStruct is a lightweight reference to a Rust struct/enum/union.
type SimplifiedStruct struct {
	ID          StructID
	Name        string
	Description string
	Location    domain.Location
}

// StructUsage represents a Rust struct/enum referenced by another resource,
// carrying its simplified method signatures.
type StructUsage struct {
	ID          StructID
	Name        string
	Description string
	Location    domain.Location
	Methods     []SimplifiedFunction
	// Visibility is the read.context_filter verdict, as on SimplifiedFunction.
	Visibility domain.Visibility
	Full       *FullBlock
}

// SimplifiedTrait is a lightweight reference to a Rust trait.
type SimplifiedTrait struct {
	ID          TraitID
	Name        string
	Description string
	Location    domain.Location
	// Visibility is the read.context_filter verdict, as on SimplifiedFunction. Set only where
	// the trait is a neighbour the filter governs (a trait a function uses); the relationship
	// lists -- implements, supertraits -- leave it zero and always render.
	Visibility domain.Visibility
}

// SimplifiedNamedType is a lightweight reference to a Rust type alias.
type SimplifiedNamedType struct {
	ID          NamedTypeID
	Name        string
	Description string
	Location    domain.Location
	// Visibility is the read.context_filter verdict, as on SimplifiedFunction.
	Visibility domain.Visibility
}

// SimplifiedVariable is a lightweight reference to a Rust module-level const or
// static.
type SimplifiedVariable struct {
	ID          VariableID
	Name        string
	Description string
	Value       string
	Location    domain.Location
	// Visibility is the read.context_filter verdict, as on SimplifiedFunction.
	Visibility domain.Visibility
	Full       *FullBlock
}

// ResourceUsage describes a Rust resource that uses some target (named type or
// dependency), with its ID, kind, name, description, and source location.
type ResourceUsage struct {
	ID          string
	Kind        domain.ResourceKind
	Name        string
	Description string
	Location    domain.Location
}

// RustFunctionContext is the full context for a function/method: its own code
// cut, optional parent struct (for methods), called functions, used
// structs/traits/named-types/variables, and external crate dependencies.
type RustFunctionContext struct {
	Function     *FunctionCut
	ParentStruct *StructCut
	// OversizedParent is the enclosing type when it was too large to inline above the member
	// (read.context_filter.max_inline_parent_lines). It renders as an ordinary named+described
	// neighbour instead of as source.
	OversizedParent *StructCut
	CalledFunctions []SimplifiedFunction
	StructsUsed     []StructUsage
	TraitsUsed      []SimplifiedTrait
	NamedTypesUsed  []SimplifiedNamedType
	VarsUsed        []SimplifiedVariable
	Dependencies    []DependencyPath
	Incoming        []domain.ResourceRef
}

// RustStructContext is the full context for a struct/enum/union: its own code
// cut, optional constructor, enum variants, impl methods, implemented traits,
// used structs/named-types, and external crate dependencies.
type RustStructContext struct {
	Struct         *StructCut
	Constructor    *FunctionCut
	IsEnum         bool
	Variants       []string
	Methods        []SimplifiedFunction
	Implements     []SimplifiedTrait
	StructsUsed    []StructUsage
	NamedTypesUsed []SimplifiedNamedType
	Dependencies   []DependencyPath
	Incoming       []domain.ResourceRef
}

// RustInterfaceContext is the full context for a trait (the interface kind): its
// own code cut, supertraits it inherits, and the structs/enums that implement
// it.
type RustInterfaceContext struct {
	Trait        *TraitCut
	Supertraits  []SimplifiedTrait
	Implementors []SimplifiedStruct
	Incoming     []domain.ResourceRef
}

// RustNamedTypeContext is the context for a type alias: its own code cut and the
// resources that use it.
type RustNamedTypeContext struct {
	NamedType *NamedTypeCut
	UsedBy    []ResourceUsage
}

// RustModuleContext is the context view of a module (a file): its own code cut,
// module path, defined functions/structs/traits/named-types/variables, imported
// modules, and external crate dependencies.
type RustModuleContext struct {
	Module       *ModuleCut
	ModulePath   string
	Functions    []SimplifiedFunction
	Structs      []StructUsage
	Traits       []SimplifiedTrait
	NamedTypes   []SimplifiedNamedType
	Variables    []SimplifiedVariable
	Imports      []ModuleID
	Dependencies []DependencyPath
}

// RustDependencyContext is the context for an external crate dependency: its
// path and the resources that use it.
type RustDependencyContext struct {
	Dependency DependencyPath
	UsedBy     []ResourceUsage
}
