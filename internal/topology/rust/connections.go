package rust

type ConnectionKind string

// Edge-type names are aligned with the Go/JS scanners so the shared viz and
// context-graph render Rust edges the same way. ConnImportsModule is the
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

// Returns functions called by this Rust function.
func (f *RustFunction) Calls() []FunctionID {
	return castSlice[FunctionID](f.Connections[ConnCalls])
}

// Returns structs/enums used by this Rust function.
func (f *RustFunction) UsesStruct() []StructID {
	return castSlice[StructID](f.Connections[ConnUsesStruct])
}

// Returns named types used by this Rust function.
func (f *RustFunction) UsesNamedType() []NamedTypeID {
	return castSlice[NamedTypeID](f.Connections[ConnUsesNamedType])
}

// Returns traits referenced by this Rust function.
func (f *RustFunction) UsesTrait() []TraitID {
	return castSlice[TraitID](f.Connections[ConnUsesTrait])
}

// Returns module-level variables (consts/statics) used by this Rust function.
func (f *RustFunction) UsesVar() []VariableID {
	return castSlice[VariableID](f.Connections[ConnUsesVar])
}

// Returns external crate dependencies used by this Rust function.
func (f *RustFunction) UsesDep() []DependencyPath {
	return castSlice[DependencyPath](f.Connections[ConnUsesDep])
}

// Returns the methods/associated functions attached to this struct/enum.
func (s *RustStruct) Methods() []FunctionID {
	return castSlice[FunctionID](s.Connections[ConnHasMethod])
}

// Returns the traits this struct/enum implements.
func (s *RustStruct) Implements() []TraitID {
	return castSlice[TraitID](s.Connections[ConnImplements])
}

// Returns external crate dependencies used by this struct/enum.
func (s *RustStruct) UsesDep() []DependencyPath {
	return castSlice[DependencyPath](s.Connections[ConnUsesDep])
}

// Returns structs/enums used by this struct/enum.
func (s *RustStruct) UsesStruct() []StructID {
	return castSlice[StructID](s.Connections[ConnUsesStruct])
}

// Returns named types used by this struct/enum.
func (s *RustStruct) UsesNamedType() []NamedTypeID {
	return castSlice[NamedTypeID](s.Connections[ConnUsesNamedType])
}

// Returns the types that implement this trait.
func (t *RustTrait) ImplementedBy() []StructID {
	return castSlice[StructID](t.Connections[ConnImplementedBy])
}

// Returns the supertraits this trait inherits from.
func (t *RustTrait) Inherits() []TraitID {
	return castSlice[TraitID](t.Connections[ConnInherits])
}

// Returns the traits that inherit from this trait (subtraits).
func (t *RustTrait) InheritedBy() []TraitID {
	return castSlice[TraitID](t.Connections[ConnInheritedBy])
}

// Returns the functions defined in this module.
func (m *RustModule) Functions() []FunctionID {
	return castSlice[FunctionID](m.Connections[ConnHasFunc])
}

// Returns the structs/enums defined in this module.
func (m *RustModule) Structs() []StructID {
	return castSlice[StructID](m.Connections[ConnHasStruct])
}

// Returns the traits defined in this module.
func (m *RustModule) Traits() []TraitID {
	return castSlice[TraitID](m.Connections[ConnHasTrait])
}

// Returns the named types defined in this module.
func (m *RustModule) NamedTypes() []NamedTypeID {
	return castSlice[NamedTypeID](m.Connections[ConnHasNamedType])
}

// Returns the module-level variables defined in this module.
func (m *RustModule) Variables() []VariableID {
	return castSlice[VariableID](m.Connections[ConnHasVar])
}

// Returns the modules (files) imported by this module via internal `use`.
func (m *RustModule) ModulesImported() []ModuleID {
	return castSlice[ModuleID](m.Connections[ConnImportsModule])
}

// Returns the external dependencies imported by this module.
func (m *RustModule) DependenciesImported() []RustDependency {
	var result []RustDependency
	for _, d := range m.Connections[ConnImportsDep] {
		result = append(result, RustDependency{CratePath: DependencyPath(d)})
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
