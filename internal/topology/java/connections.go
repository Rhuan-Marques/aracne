package java

type ConnectionKind string

// Edge-type names are aligned with the Go/JS scanners so the shared viz and
// context-graph render Java edges the same way. ConnImportsModule is the
// module->module (file->file) import edge surfaced in "Packages & Modules".
var (
	ConnCalls         ConnectionKind = "calls"
	ConnUsesStruct    ConnectionKind = "uses_struct"
	ConnUsesNamedType ConnectionKind = "uses_named_type"
	ConnUsesTrait     ConnectionKind = "uses_interface"
	ConnUsesVar       ConnectionKind = "uses_extvar"
	ConnUsesDep       ConnectionKind = "uses_dependency"
	ConnHasMethod     ConnectionKind = "methods"
	ConnImplements    ConnectionKind = "implements"
	ConnImplementedBy ConnectionKind = "implemented_by"
	ConnInherits      ConnectionKind = "inherits"
	ConnInheritedBy   ConnectionKind = "inherited_by"
	ConnConstructor   ConnectionKind = "constructor"
	ConnHasFunc       ConnectionKind = "has_function"
	ConnHasStruct     ConnectionKind = "has_struct"
	ConnHasNamedType  ConnectionKind = "has_named_type"
	ConnHasTrait      ConnectionKind = "has_interface"
	ConnHasVar        ConnectionKind = "has_extvar"
	ConnImportsModule ConnectionKind = "imports_module"
	ConnImportsDep    ConnectionKind = "imports_dependency"
)

// Returns methods/constructors called by this Java method.
func (f *JavaMethod) Calls() []FunctionID {
	return castSlice[FunctionID](f.Connections[ConnCalls])
}

// Returns classes/enums/records used by this Java method.
func (f *JavaMethod) UsesStruct() []StructID {
	return castSlice[StructID](f.Connections[ConnUsesStruct])
}

// Returns interfaces/annotations referenced by this Java method.
func (f *JavaMethod) UsesInterface() []InterfaceID {
	return castSlice[InterfaceID](f.Connections[ConnUsesTrait])
}

// Returns external dependencies used by this Java method.
func (f *JavaMethod) UsesDep() []DependencyPath {
	return castSlice[DependencyPath](f.Connections[ConnUsesDep])
}

// Returns the methods/constructors attached to this class/enum/record.
func (s *JavaClass) Methods() []FunctionID {
	return castSlice[FunctionID](s.Connections[ConnHasMethod])
}

// Returns the interfaces this class/enum/record implements.
func (s *JavaClass) Implements() []InterfaceID {
	return castSlice[InterfaceID](s.Connections[ConnImplements])
}

// Returns the classes this class extends (superclasses).
func (s *JavaClass) Inherits() []StructID {
	return castSlice[StructID](s.Connections[ConnInherits])
}

// Returns the classes that extend this class (subclasses).
func (s *JavaClass) InheritedBy() []StructID {
	return castSlice[StructID](s.Connections[ConnInheritedBy])
}

// Returns classes/enums/records used by this class/enum/record.
func (s *JavaClass) UsesStruct() []StructID {
	return castSlice[StructID](s.Connections[ConnUsesStruct])
}

// Returns interfaces/annotations used by this class/enum/record.
func (s *JavaClass) UsesInterface() []InterfaceID {
	return castSlice[InterfaceID](s.Connections[ConnUsesTrait])
}

// Returns external dependencies used by this class/enum/record.
func (s *JavaClass) UsesDep() []DependencyPath {
	return castSlice[DependencyPath](s.Connections[ConnUsesDep])
}

// Returns the classes/enums/records that implement this interface.
func (t *JavaInterface) ImplementedBy() []StructID {
	return castSlice[StructID](t.Connections[ConnImplementedBy])
}

// Returns the supertypes (parent interfaces) this interface extends.
func (t *JavaInterface) Inherits() []InterfaceID {
	return castSlice[InterfaceID](t.Connections[ConnInherits])
}

// Returns the interfaces that extend this interface (subinterfaces).
func (t *JavaInterface) InheritedBy() []InterfaceID {
	return castSlice[InterfaceID](t.Connections[ConnInheritedBy])
}

// Returns the methods declared in this module via has_function edges.
func (m *JavaModule) Functions() []FunctionID {
	return castSlice[FunctionID](m.Connections[ConnHasFunc])
}

// Returns the classes/enums/records defined in this module.
func (m *JavaModule) Structs() []StructID {
	return castSlice[StructID](m.Connections[ConnHasStruct])
}

// Returns the interfaces/annotations defined in this module.
func (m *JavaModule) Interfaces() []InterfaceID {
	return castSlice[InterfaceID](m.Connections[ConnHasTrait])
}

// Returns the modules (files) imported by this module via internal imports.
func (m *JavaModule) ModulesImported() []ModuleID {
	return castSlice[ModuleID](m.Connections[ConnImportsModule])
}

// Returns the external dependencies imported by this module.
func (m *JavaModule) DependenciesImported() []JavaDependency {
	var result []JavaDependency
	for _, d := range m.Connections[ConnImportsDep] {
		result = append(result, JavaDependency{Coordinate: DependencyPath(d)})
	}
	return result
}

// Generic helper that casts a slice of strings to a slice of a compatible string-based type.
func castSlice[T ~string](ids []string) []T {
	result := make([]T, len(ids))
	for i, id := range ids {
		result[i] = T(id)
	}
	return result
}
