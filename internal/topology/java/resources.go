package java

import "github.com/Rhuan-Marques/aracne/internal/topology/domain"

// Resource ID aliases. All IDs are Java fully-qualified names rooted at the
// in-file `package ...;` declaration and separated by ".", e.g.
// "com.aracne.shapes.Circle" (class), "com.aracne.shapes.Circle.area(int)"
// (method, always carrying its parenthesized signature). File (module) nodes are
// keyed by absolute file path instead.
type FunctionID = string
type StructID = string
type InterfaceID = string
type ModuleID = string
type DependencyPath = string

// Represents a variable (parameter, return value, or class field/record
// component) with its name, type annotation, and the resolved topology resource
// ID the type maps to.
type VariableDefinition struct {
	Name   string
	Typing string
	// TypingID is the canonical topology resource ID that Typing resolves to (a
	// class, enum, record, or interface), or "" for built-in/external/unresolved
	// types. Resolved in the DEFINING module's import context during
	// resolveTopology, so a caller in another file can follow a return type
	// without the callee's imports.
	TypingID string
}

// Represents an interface method summary (the declaration inside an interface or
// annotation body). HasDefault marks methods that ship a default body; IsStatic
// marks interface static methods; IsAbstract marks abstract declarations.
type FunctionDefinition struct {
	Name       string
	Input      []VariableDefinition
	Output     []VariableDefinition
	HasDefault bool
	IsStatic   bool
	IsAbstract bool
}

// Holds the complete Java topology graph: methods/constructors/accessors,
// classes (incl. enums, records, abstract classes, anonymous/local classes),
// interfaces (incl. annotation types), modules (files), and their dependencies.
type JavaTopology struct {
	Root         string
	Classes      map[StructID]JavaClass
	Interfaces   map[InterfaceID]JavaInterface
	Methods      map[FunctionID]JavaMethod
	Modules      map[ModuleID]JavaModule
	Dependencies []JavaDependency
	Errors       map[string]string
}

// Represents a Java class, enum, record, abstract class, or anonymous/local
// class. Enums set IsEnum (with Variants listed); records set IsRecord (with
// Components listed). Methods attach via has_method edges (MethodFrom on the
// JavaMethod), and inheritance/implements edges are resolved cross-file in the
// matcher.
type JavaClass struct {
	ID          StructID
	Name        string
	Description string
	Fields      []VariableDefinition
	Loc         domain.Location
	Connections map[ConnectionKind][]string
	Constructor *FunctionID
	IsEnum      bool
	IsRecord    bool
	IsAbstract  bool
	IsSealed    bool
	IsFinal     bool
	IsStatic    bool
	IsAnonymous bool
	IsLocal     bool
	// Variants holds enum constant names (when IsEnum).
	Variants []string
	// Components holds record header components (when IsRecord).
	Components []VariableDefinition
	// Permits holds the names from a `permits` clause (when IsSealed).
	Permits []string
	// Generics holds declared type-parameter names, e.g. ["T"].
	Generics   []string
	Visibility string
	Exported   bool
}

// Represents a Java interface or annotation type (@interface). Method summaries
// are stored inline (like Go interfaces). Supertypes (`extends`) are resolved
// into inherits/inherited_by edges by the matcher; IsAnnotation marks an
// annotation type.
type JavaInterface struct {
	ID           InterfaceID
	Name         string
	Description  string
	Methods      []FunctionDefinition
	Generics     []string
	Loc          domain.Location
	Connections  map[ConnectionKind][]string
	IsAnnotation bool
	Visibility   string
	Exported     bool
}

// Represents a Java method, constructor, accessor, or initializer block.
// MethodFrom is ALWAYS set and points at the owning class or interface FQN.
type JavaMethod struct {
	ID          FunctionID
	Name        string
	Description string
	Input       []VariableDefinition
	Output      []VariableDefinition
	Throws      []string
	Loc         domain.Location
	Connections map[ConnectionKind][]string
	// MethodFrom is the owner FQN (a class OR an interface); always set.
	MethodFrom    *StructID
	IsConstructor bool
	IsStatic      bool
	IsAbstract    bool
	IsDefault     bool
	IsSynthetic   bool
	Visibility    string
	Exported      bool
}

// Represents a Java module: a single source file. Keyed by absolute path; its
// Package is the dotted package declared at the top of the file, under which its
// type declarations are namespaced.
type JavaModule struct {
	ID          ModuleID
	Name        string
	Description string
	Package     string
	Connections map[ConnectionKind][]string
}

// Represents an external dependency (a Maven/Gradle coordinate or an imported
// non-project package root such as java./javax./jakarta.).
type JavaDependency struct {
	Coordinate DependencyPath
}
