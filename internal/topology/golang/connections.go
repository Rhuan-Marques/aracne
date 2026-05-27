package golang

type ConnectionKind string

// Connection kind constant for "has_function" relationships linking files/packages to their contained functions.
// Connection kind constant indicating that a resource uses/imports a Go package.
// Connection kind constant for "imports_package" relationships linking files to their imported packages.
// Connection kind constant "uses_struct" used to link a function to the structs it references or uses.
// ConnectionKind constant representing a "has_extvar" relationship between a file and its external variables.
// Connection kind constant for the "has_interface" edge linking a package/file to its declared interfaces.
// ConnectionKind constant representing a "constructor" relationship between a struct and its constructor function.
// ConnectionKind constant "calls" representing a function-to-function call relationship in the topology graph.
// Connection kind constant for "uses_dependency" relationships linking resources to their imported external dependencies.
// Connection kind constant for the "uses_interface" edge linking a function to the interfaces it references.
// ConnectionKind constant "methods" representing the connection from a struct to its associated methods.
// ConnectionKind constant representing a "implements" relationship between a struct and an interface.
// Connection kind constant indicating that a struct is implemented by another type (inverse of implements).
// ConnectionKind constant representing a "uses_extvar" relationship between a function/struct and an external variable it references.
// ConnectionKind constant "has_file" representing the connection from a package to its source files.
// Connection kind constant "has_struct" used to link a package or file to the structs it defines.
// Connection kind constant "imports_dependency" used to track which external dependencies a package imports.
var (
	ConnCalls        ConnectionKind = "calls"
	ConnUsesStruct   ConnectionKind = "uses_struct"
	ConnUsesIface    ConnectionKind = "uses_interface"
	ConnUsesExtVar   ConnectionKind = "uses_extvar"
	ConnUsesPkg      ConnectionKind = "uses_package"
	ConnUsesDep      ConnectionKind = "uses_dependency"
	ConnHasMethod    ConnectionKind = "methods"
	ConnImplements   ConnectionKind = "implements"
	ConnImplBy       ConnectionKind = "implemented_by"
	ConnConstructor  ConnectionKind = "constructor"
	ConnHasFunc      ConnectionKind = "has_function"
	ConnHasStruct    ConnectionKind = "has_struct"
	ConnHasIface     ConnectionKind = "has_interface"
	ConnHasVar       ConnectionKind = "has_extvar"
	ConnHasFile      ConnectionKind = "has_file"
	ConnImportsPkg   ConnectionKind = "imports_package"
	ConnImportsDep   ConnectionKind = "imports_dependency"
)

// Returns the list of FunctionID values for functions called directly by this function's body.
func (f *GolangFunction) Calls() []FunctionID {
	return castSlice[FunctionID](f.Connections[ConnCalls])
}

// Returns the list of StructIDs that this function references via the uses_struct connection. No parameters, returns []StructID.
func (f *GolangFunction) UsesStruct() []StructID {
	return castSlice[StructID](f.Connections[ConnUsesStruct])
}

// Returns the list of InterfaceIDs that this function references via the uses_interface connection. No parameters. Returns []InterfaceID.
func (f *GolangFunction) UsesInterface() []InterfaceID {
	return castSlice[InterfaceID](f.Connections[ConnUsesIface])
}

// Returns the ExternalVarIDs referenced by this function, read from ConnUsesExtVar connections.
func (f *GolangFunction) UsesExtVar() []ExternalVarID {
	return castSlice[ExternalVarID](f.Connections[ConnUsesExtVar])
}

// Returns the list of PackagePath values that this function uses, extracted from the ConnUsesPkg connection kind. Receiver is GolangFunction.
func (f *GolangFunction) UsesPkg() []PackagePath {
	return castSlice[PackagePath](f.Connections[ConnUsesPkg])
}

// Returns the list of DependancyPaths used by this function via the uses_dependency connection. No parameters, returns []DependancyPath.
func (f *GolangFunction) UsesDep() []DependancyPath {
	return castSlice[DependancyPath](f.Connections[ConnUsesDep])
}

// Returns the list of FunctionIDs for the methods associated with this struct via the methods connection. No parameters. Returns []FunctionID.
func (s *GolangStruct) Methods() []FunctionID {
	return castSlice[FunctionID](s.Connections[ConnHasMethod])
}

// Returns the InterfaceIDs that this struct implements, read from the ConnImplements connection entries.
func (s *GolangStruct) Implements() []InterfaceID {
	return castSlice[InterfaceID](s.Connections[ConnImplements])
}

// Returns the list of PackagePath values for packages used by this struct.
func (s *GolangStruct) UsesPkg() []PackagePath {
	return castSlice[PackagePath](s.Connections[ConnUsesPkg])
}

// Returns the list of DependancyPath entries that this struct depends on, extracted from its ConnUsesDep connections.
func (s *GolangStruct) UsesDep() []DependancyPath {
	return castSlice[DependancyPath](s.Connections[ConnUsesDep])
}

// Returns the list of StructIDs that implement this interface via the implemented_by connection. No parameters. Returns []StructID.
func (i *GolangInterface) ImplementedBy() []StructID {
	return castSlice[StructID](i.Connections[ConnImplBy])
}

// Returns the package paths referenced by this interface, read from ConnUsesPkg connections.
func (i *GolangInterface) UsesPkg() []PackagePath {
	return castSlice[PackagePath](i.Connections[ConnUsesPkg])
}

// Returns the list of DependancyPaths used by this interface via the uses_dependency connection. No parameters. Returns []DependancyPath.
func (i *GolangInterface) UsesDep() []DependancyPath {
	return castSlice[DependancyPath](i.Connections[ConnUsesDep])
}

// Returns the FunctionIDs declared in this file by reading ConnHasFunc connections.
func (fl *GolangFile) Functions() []FunctionID {
	return castSlice[FunctionID](fl.Connections[ConnHasFunc])
}

// Returns the StructIDs declared in this file by reading ConnHasStruct connections.
func (fl *GolangFile) Structs() []StructID {
	return castSlice[StructID](fl.Connections[ConnHasStruct])
}

// Returns the InterfaceIDs of all interfaces declared in this file by extracting them from the Connections map under the ConnHasIface key.
func (fl *GolangFile) Interfaces() []InterfaceID {
	return castSlice[InterfaceID](fl.Connections[ConnHasIface])
}

// Returns the list of ExternalVarIDs that are referenced within this file via the has_extvar connection. No parameters. Returns []ExternalVarID.
func (fl *GolangFile) ExternalVars() []ExternalVarID {
	return castSlice[ExternalVarID](fl.Connections[ConnHasVar])
}

// Returns the package paths imported by this file by reading the ConnImportsPkg connection list.
func (fl *GolangFile) PackagesImported() []PackagePath {
	return castSlice[PackagePath](fl.Connections[ConnImportsPkg])
}

// Returns the list of external dependencies imported by this file, read from ConnImportsDep connections.
func (fl *GolangFile) DependenciesImported() []Dependancy {
	var result []Dependancy
	for _, d := range fl.Connections[ConnImportsDep] {
		result = append(result, Dependancy{PackagePath: DependancyPath(d)})
	}
	return result
}

// Returns the list of FileIDs belonging to this package by reading the ConnHasFile connections from the topology.
func (p *GolangPackage) Files() []FileID {
	return castSlice[FileID](p.Connections[ConnHasFile])
}

// Returns the list of FunctionIDs belonging to this package by reading the ConnHasFunc connection entries.
func (p *GolangPackage) HasFunctions() []FunctionID {
	return castSlice[FunctionID](p.Connections[ConnHasFunc])
}

// Returns the list of StructIDs belonging to this package by reading the ConnHasStruct connections from the topology.
func (p *GolangPackage) HasStructs() []StructID {
	return castSlice[StructID](p.Connections[ConnHasStruct])
}

// Returns a list of InterfaceIDs that this package has connections to via the ConnHasIface relationship.
func (p *GolangPackage) HasInterfaces() []InterfaceID {
	return castSlice[InterfaceID](p.Connections[ConnHasIface])
}

// Returns the IDs of all external variables declared in this package by reading the ConnHasVar connection list.
func (p *GolangPackage) HasExternalVars() []ExternalVarID {
	return castSlice[ExternalVarID](p.Connections[ConnHasVar])
}

// Generic function that converts a plain string slice to a slice of any ~string type.
func castSlice[T ~string](ids []string) []T {
	result := make([]T, len(ids))
	for i, id := range ids {
		result[i] = T(id)
	}
	return result
}
