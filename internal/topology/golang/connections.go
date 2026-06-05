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

func (f *GolangFunction) Calls() []FunctionID {
	return castSlice[FunctionID](f.Connections[ConnCalls])
}

func (f *GolangFunction) UsesStruct() []StructID {
	return castSlice[StructID](f.Connections[ConnUsesStruct])
}

func (f *GolangFunction) UsesNamedType() []NamedTypeID {
	return castSlice[NamedTypeID](f.Connections[ConnUsesNamedType])
}

func (f *GolangFunction) UsesInterface() []InterfaceID {
	return castSlice[InterfaceID](f.Connections[ConnUsesIface])
}

func (f *GolangFunction) UsesExtVar() []ExternalVarID {
	return castSlice[ExternalVarID](f.Connections[ConnUsesExtVar])
}

func (f *GolangFunction) UsesPkg() []PackagePath {
	return castSlice[PackagePath](f.Connections[ConnUsesPkg])
}

func (f *GolangFunction) UsesDep() []DependancyPath {
	return castSlice[DependancyPath](f.Connections[ConnUsesDep])
}

func (s *GolangStruct) Methods() []FunctionID {
	return castSlice[FunctionID](s.Connections[ConnHasMethod])
}

func (s *GolangStruct) Implements() []InterfaceID {
	return castSlice[InterfaceID](s.Connections[ConnImplements])
}

func (s *GolangStruct) UsesNamedType() []NamedTypeID {
	return castSlice[NamedTypeID](s.Connections[ConnUsesNamedType])
}

func (s *GolangStruct) UsesPkg() []PackagePath {
	return castSlice[PackagePath](s.Connections[ConnUsesPkg])
}

func (s *GolangStruct) UsesDep() []DependancyPath {
	return castSlice[DependancyPath](s.Connections[ConnUsesDep])
}

func (i *GolangInterface) ImplementedBy() []StructID {
	return castSlice[StructID](i.Connections[ConnImplBy])
}

func (i *GolangInterface) UsesNamedType() []NamedTypeID {
	return castSlice[NamedTypeID](i.Connections[ConnUsesNamedType])
}

func (i *GolangInterface) UsesPkg() []PackagePath {
	return castSlice[PackagePath](i.Connections[ConnUsesPkg])
}

func (i *GolangInterface) UsesDep() []DependancyPath {
	return castSlice[DependancyPath](i.Connections[ConnUsesDep])
}

func (n *GolangNamedType) UsesNamedType() []NamedTypeID {
	return castSlice[NamedTypeID](n.Connections[ConnUsesNamedType])
}

func (n *GolangNamedType) UsesPkg() []PackagePath {
	return castSlice[PackagePath](n.Connections[ConnUsesPkg])
}

func (n *GolangNamedType) UsesDep() []DependancyPath {
	return castSlice[DependancyPath](n.Connections[ConnUsesDep])
}

func (fl *GolangFile) Functions() []FunctionID {
	return castSlice[FunctionID](fl.Connections[ConnHasFunc])
}

func (fl *GolangFile) Structs() []StructID {
	return castSlice[StructID](fl.Connections[ConnHasStruct])
}

func (fl *GolangFile) NamedTypes() []NamedTypeID {
	return castSlice[NamedTypeID](fl.Connections[ConnHasNamedType])
}

func (fl *GolangFile) Interfaces() []InterfaceID {
	return castSlice[InterfaceID](fl.Connections[ConnHasIface])
}

func (fl *GolangFile) ExternalVars() []ExternalVarID {
	return castSlice[ExternalVarID](fl.Connections[ConnHasVar])
}

func (fl *GolangFile) PackagesImported() []PackagePath {
	return castSlice[PackagePath](fl.Connections[ConnImportsPkg])
}

func (fl *GolangFile) DependenciesImported() []Dependancy {
	var result []Dependancy
	for _, d := range fl.Connections[ConnImportsDep] {
		result = append(result, Dependancy{PackagePath: DependancyPath(d)})
	}
	return result
}

func (p *GolangPackage) Files() []FileID {
	return castSlice[FileID](p.Connections[ConnHasFile])
}

func (p *GolangPackage) HasFunctions() []FunctionID {
	return castSlice[FunctionID](p.Connections[ConnHasFunc])
}

func (p *GolangPackage) HasStructs() []StructID {
	return castSlice[StructID](p.Connections[ConnHasStruct])
}

func (p *GolangPackage) HasNamedTypes() []NamedTypeID {
	return castSlice[NamedTypeID](p.Connections[ConnHasNamedType])
}

func (p *GolangPackage) HasInterfaces() []InterfaceID {
	return castSlice[InterfaceID](p.Connections[ConnHasIface])
}

func (p *GolangPackage) HasExternalVars() []ExternalVarID {
	return castSlice[ExternalVarID](p.Connections[ConnHasVar])
}

func castSlice[T ~string](ids []string) []T {
	result := make([]T, len(ids))
	for i, id := range ids {
		result[i] = T(id)
	}
	return result
}
