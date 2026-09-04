package java

import "github.com/Rhuan-Marques/aracne/internal/topology/domain"

// FunctionCut combines a JavaMethod with its source code snippet (Cut) for
// context display.
type FunctionCut struct {
	JavaMethod
	Cut string
}

// StructCut combines a JavaClass (class/enum/record) with its source code
// snippet (Cut) for context display.
type StructCut struct {
	JavaClass
	Cut string
}

// InterfaceCut combines a JavaInterface (interface/annotation) with its source
// code snippet (Cut) for context display.
type InterfaceCut struct {
	JavaInterface
	Cut string
}

// ModuleCut combines a JavaModule (a file) with its source code snippet (Cut)
// for context display.
type ModuleCut struct {
	JavaModule
	Cut string
}

// SimplifiedFunction is a lightweight reference to a Java method/constructor used
// in context lists.
type SimplifiedFunction struct {
	ID          FunctionID
	Name        string
	Description string
	Input       []VariableDefinition
	Output      []VariableDefinition
	Location    domain.Location
}

// SimplifiedStruct is a lightweight reference to a Java class/enum/record.
type SimplifiedStruct struct {
	ID          StructID
	Name        string
	Description string
	Location    domain.Location
}

// StructUsage represents a Java class/enum/record referenced by another resource,
// carrying its simplified method signatures.
type StructUsage struct {
	ID          StructID
	Name        string
	Description string
	Location    domain.Location
	Methods     []SimplifiedFunction
}

// SimplifiedInterface is a lightweight reference to a Java interface/annotation.
type SimplifiedInterface struct {
	ID          InterfaceID
	Name        string
	Description string
	Location    domain.Location
}

// ResourceUsage describes a Java resource that uses some target (e.g. a
// dependency), with its ID, kind, name, description, and source location.
type ResourceUsage struct {
	ID          string
	Kind        domain.ResourceKind
	Name        string
	Description string
	Location    domain.Location
}

// JavaFunctionContext is the full context for a method/constructor: its own code
// cut, optional parent class, called methods, used classes/interfaces, and
// external dependencies.
type JavaFunctionContext struct {
	Function     *FunctionCut
	ParentStruct *StructCut
	// OversizedParent is the enclosing type when it was too large to inline above the member
	// (read.context_filter.max_inline_parent_lines). It renders as an ordinary named+described
	// neighbour instead of as source.
	OversizedParent *StructCut
	CalledFunctions []SimplifiedFunction
	StructsUsed     []StructUsage
	InterfacesUsed  []SimplifiedInterface
	Dependencies    []DependencyPath
}

// JavaStructContext is the full context for a class/enum/record: its own code
// cut, optional constructor, enum variants, record components, methods,
// implemented interfaces, superclasses, used classes, and external dependencies.
type JavaStructContext struct {
	Struct       *StructCut
	Constructor  *FunctionCut
	IsEnum       bool
	Variants     []string
	IsRecord     bool
	Components   []VariableDefinition
	Methods      []SimplifiedFunction
	Implements   []SimplifiedInterface
	Inherits     []SimplifiedStruct
	StructsUsed  []StructUsage
	Dependencies []DependencyPath
}

// JavaInterfaceContext is the full context for an interface/annotation: its own
// code cut, the supertypes it extends, the classes/enums/records that implement
// it, and whether it is an annotation type.
type JavaInterfaceContext struct {
	Interface     *InterfaceCut
	Supertypes    []SimplifiedInterface
	ImplementedBy []SimplifiedStruct
	IsAnnotation  bool
}

// JavaModuleContext is the context view of a module (a file): its own code cut,
// package, defined methods/classes/interfaces, imported modules, and external
// dependencies.
type JavaModuleContext struct {
	Module       *ModuleCut
	Package      string
	Functions    []SimplifiedFunction
	Structs      []StructUsage
	Interfaces   []SimplifiedInterface
	Imports      []ModuleID
	Dependencies []DependencyPath
}

// JavaDependencyContext is the context for an external dependency: its coordinate
// and the resources that use it.
type JavaDependencyContext struct {
	Dependency DependencyPath
	UsedBy     []ResourceUsage
}
