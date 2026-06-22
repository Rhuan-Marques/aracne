package golang

type ConnectionKind string

var (
	ConnCalls         ConnectionKind = "calls"
	ConnUsesStruct    ConnectionKind = "uses_struct"
	ConnUsesNamedType ConnectionKind = "uses_named_type"
	ConnUsesIface     ConnectionKind = "uses_interface"
	ConnUsesExtVar    ConnectionKind = "uses_extvar"
	ConnUsesPkg       ConnectionKind = "uses_package"
	ConnUsesDep       ConnectionKind = "uses_dependency"
	ConnHasMethod     ConnectionKind = "methods"
	ConnImplements    ConnectionKind = "implements"
	ConnImplBy        ConnectionKind = "implemented_by"
	ConnConstructor   ConnectionKind = "constructor"
	ConnHasFunc       ConnectionKind = "has_function"
	ConnHasStruct     ConnectionKind = "has_struct"
	ConnHasNamedType  ConnectionKind = "has_named_type"
	ConnHasIface      ConnectionKind = "has_interface"
	ConnHasVar        ConnectionKind = "has_extvar"
	ConnHasFile       ConnectionKind = "has_file"
	ConnImportsPkg    ConnectionKind = "imports_package"
	ConnImportsDep    ConnectionKind = "imports_dependency"
)

// Returns all functions called by this Go function.
func (f *GolangFunction) Calls() []FunctionID {
	return castSlice[FunctionID](f.Connections[ConnCalls])
}

// Returns the structs referenced by this function.
func (f *GolangFunction) UsesStruct() []StructID {
	return castSlice[StructID](f.Connections[ConnUsesStruct])
}

// Returns the list of named types used by this function.
func (f *GolangFunction) UsesNamedType() []NamedTypeID {
	return castSlice[NamedTypeID](f.Connections[ConnUsesNamedType])
}

// Returns the list of interfaces used by this function.
func (f *GolangFunction) UsesInterface() []InterfaceID {
	return castSlice[InterfaceID](f.Connections[ConnUsesIface])
}

// Returns the list of external variables used by this function.
func (f *GolangFunction) UsesExtVar() []ExternalVarID {
	return castSlice[ExternalVarID](f.Connections[ConnUsesExtVar])
}

// Returns the list of package paths used by this function.
func (f *GolangFunction) UsesPkg() []PackagePath {
	return castSlice[PackagePath](f.Connections[ConnUsesPkg])
}

// Returns the list of dependency paths used by this function.
func (f *GolangFunction) UsesDep() []DependancyPath {
	return castSlice[DependancyPath](f.Connections[ConnUsesDep])
}

// Returns the list of methods defined on this struct.
func (s *GolangStruct) Methods() []FunctionID {
	return castSlice[FunctionID](s.Connections[ConnHasMethod])
}

// Returns all interfaces implemented by the struct.
func (s *GolangStruct) Implements() []InterfaceID {
	return castSlice[InterfaceID](s.Connections[ConnImplements])
}

// Returns the list of named types referenced by this struct.
func (s *GolangStruct) UsesNamedType() []NamedTypeID {
	return castSlice[NamedTypeID](s.Connections[ConnUsesNamedType])
}

// Returns the list of packages imported by this struct.
func (s *GolangStruct) UsesPkg() []PackagePath {
	return castSlice[PackagePath](s.Connections[ConnUsesPkg])
}

// Returns the list of dependency paths this struct uses.
func (s *GolangStruct) UsesDep() []DependancyPath {
	return castSlice[DependancyPath](s.Connections[ConnUsesDep])
}

// Returns the structs that implement this interface.
func (i *GolangInterface) ImplementedBy() []StructID {
	return castSlice[StructID](i.Connections[ConnImplBy])
}

// Returns the named types referenced by this interface.
func (i *GolangInterface) UsesNamedType() []NamedTypeID {
	return castSlice[NamedTypeID](i.Connections[ConnUsesNamedType])
}

// Returns the packages referenced by this interface.
func (i *GolangInterface) UsesPkg() []PackagePath {
	return castSlice[PackagePath](i.Connections[ConnUsesPkg])
}

// Returns the external dependencies referenced by this interface.
func (i *GolangInterface) UsesDep() []DependancyPath {
	return castSlice[DependancyPath](i.Connections[ConnUsesDep])
}

// Returns named types that a named type uses.
func (n *GolangNamedType) UsesNamedType() []NamedTypeID {
	return castSlice[NamedTypeID](n.Connections[ConnUsesNamedType])
}

// Returns packages that a named type uses.
func (n *GolangNamedType) UsesPkg() []PackagePath {
	return castSlice[PackagePath](n.Connections[ConnUsesPkg])
}

// Returns dependency paths that a named type uses.
func (n *GolangNamedType) UsesDep() []DependancyPath {
	return castSlice[DependancyPath](n.Connections[ConnUsesDep])
}

// Returns the list of functions defined in the file.
func (fl *GolangFile) Functions() []FunctionID {
	return castSlice[FunctionID](fl.Connections[ConnHasFunc])
}

// Returns all structs defined in the Go file.
func (fl *GolangFile) Structs() []StructID {
	return castSlice[StructID](fl.Connections[ConnHasStruct])
}

// Returns all named types defined in the Go file.
func (fl *GolangFile) NamedTypes() []NamedTypeID {
	return castSlice[NamedTypeID](fl.Connections[ConnHasNamedType])
}

// Returns all interfaces defined in the Go file.
func (fl *GolangFile) Interfaces() []InterfaceID {
	return castSlice[InterfaceID](fl.Connections[ConnHasIface])
}

// Returns the list of external variables defined in the file.
func (fl *GolangFile) ExternalVars() []ExternalVarID {
	return castSlice[ExternalVarID](fl.Connections[ConnHasVar])
}

// Returns all packages imported by the Go file.
func (fl *GolangFile) PackagesImported() []PackagePath {
	return castSlice[PackagePath](fl.Connections[ConnImportsPkg])
}

// Returns the list of package dependencies imported by the file.
func (fl *GolangFile) DependenciesImported() []Dependancy {
	var result []Dependancy
	for _, d := range fl.Connections[ConnImportsDep] {
		result = append(result, Dependancy{PackagePath: DependancyPath(d)})
	}
	return result
}

// Returns the files contained in a package.
func (p *GolangPackage) Files() []FileID {
	return castSlice[FileID](p.Connections[ConnHasFile])
}

// Returns all functions defined in the package.
func (p *GolangPackage) HasFunctions() []FunctionID {
	return castSlice[FunctionID](p.Connections[ConnHasFunc])
}

// Returns all structs defined in the package.
func (p *GolangPackage) HasStructs() []StructID {
	return castSlice[StructID](p.Connections[ConnHasStruct])
}

// Returns all named types defined in the package.
func (p *GolangPackage) HasNamedTypes() []NamedTypeID {
	return castSlice[NamedTypeID](p.Connections[ConnHasNamedType])
}

// Returns all interfaces defined in the package.
func (p *GolangPackage) HasInterfaces() []InterfaceID {
	return castSlice[InterfaceID](p.Connections[ConnHasIface])
}

// Returns external variables defined in a package.
func (p *GolangPackage) HasExternalVars() []ExternalVarID {
	return castSlice[ExternalVarID](p.Connections[ConnHasVar])
}

// Generic helper that casts a string slice to another string-based type.
func castSlice[T ~string](ids []string) []T {
	result := make([]T, len(ids))
	for i, id := range ids {
		result[i] = T(id)
	}
	return result
}
