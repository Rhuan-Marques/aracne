package helper

import (
	"database/sql"
	"encoding/json"

	_ "modernc.org/sqlite"
	"llm-topology/internal/topology/domain"
)

// ────────────────────────────────────── Write ──────────────────────────────────────

func WriteDb(topo *domain.Topology, path string) error {
	db, err := sql.Open("sqlite", path+"?cache=shared&_journal_mode=WAL")
	if err != nil {
		return err
	}
	defer db.Close()

	if err := createSchema(db); err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	tx.Exec("DELETE FROM info")
	tx.Exec("DELETE FROM packages")
	tx.Exec("DELETE FROM files")
	tx.Exec("DELETE FROM structs")
	tx.Exec("DELETE FROM interfaces")
	tx.Exec("DELETE FROM functions")
	tx.Exec("DELETE FROM external_vars")
	tx.Exec("DELETE FROM dependencies")
	tx.Exec("DELETE FROM errors")
	tx.Exec("DELETE FROM package_files")
	tx.Exec("DELETE FROM package_structs")
	tx.Exec("DELETE FROM package_interfaces")
	tx.Exec("DELETE FROM package_functions")
	tx.Exec("DELETE FROM package_extvars")
	tx.Exec("DELETE FROM file_structs")
	tx.Exec("DELETE FROM file_interfaces")
	tx.Exec("DELETE FROM file_functions")
	tx.Exec("DELETE FROM file_extvars")
	tx.Exec("DELETE FROM file_imports_pkg")
	tx.Exec("DELETE FROM file_imports_dep")
	tx.Exec("DELETE FROM struct_constructor")
	tx.Exec("DELETE FROM struct_implements")
	tx.Exec("DELETE FROM struct_uses_pkg")
	tx.Exec("DELETE FROM struct_uses_dep")
	tx.Exec("DELETE FROM interface_uses_pkg")
	tx.Exec("DELETE FROM interface_uses_dep")
	tx.Exec("DELETE FROM method_from")
	tx.Exec("DELETE FROM func_calls")
	tx.Exec("DELETE FROM func_uses_struct")
	tx.Exec("DELETE FROM func_uses_interface")
	tx.Exec("DELETE FROM func_uses_extvar")
	tx.Exec("DELETE FROM func_uses_dep")
	tx.Exec("DELETE FROM func_uses_pkg")

	if err := writeEntities(tx, topo); err != nil {
		return err
	}
	if err := writeRelations(tx, topo); err != nil {
		return err
	}

	return tx.Commit()
}

func createSchema(db *sql.DB) error {
	ddl := `
	PRAGMA journal_mode=WAL;
	PRAGMA synchronous=OFF;

	CREATE TABLE IF NOT EXISTS info (key TEXT PRIMARY KEY, value TEXT);

	CREATE TABLE IF NOT EXISTS packages (id TEXT PRIMARY KEY);
	CREATE TABLE IF NOT EXISTS files (id TEXT PRIMARY KEY, name TEXT NOT NULL, description TEXT, from_package TEXT NOT NULL);
	CREATE TABLE IF NOT EXISTS structs (id TEXT PRIMARY KEY, name TEXT NOT NULL, description TEXT, params_json TEXT, starts_at INT NOT NULL, ends_at INT NOT NULL, loc_path TEXT);
	CREATE TABLE IF NOT EXISTS interfaces (id TEXT PRIMARY KEY, name TEXT NOT NULL, description TEXT, methods_json TEXT, starts_at INT NOT NULL, ends_at INT NOT NULL, loc_path TEXT);
	CREATE TABLE IF NOT EXISTS functions (id TEXT PRIMARY KEY, name TEXT NOT NULL, description TEXT, input_json TEXT, output_json TEXT, starts_at INT NOT NULL, ends_at INT NOT NULL, loc_path TEXT);
	CREATE TABLE IF NOT EXISTS external_vars (id TEXT PRIMARY KEY, name TEXT NOT NULL, description TEXT, typing TEXT, value_json TEXT, starts_at INT NOT NULL, ends_at INT NOT NULL, loc_path TEXT);
	CREATE TABLE IF NOT EXISTS dependencies (package_path TEXT PRIMARY KEY);
	CREATE TABLE IF NOT EXISTS errors (file_path TEXT PRIMARY KEY, error_msg TEXT);

	-- Package ←→ File
	CREATE TABLE IF NOT EXISTS package_files (pkg_id TEXT, file_id TEXT, PRIMARY KEY(pkg_id, file_id));
	CREATE INDEX IF NOT EXISTS idx_pf_file ON package_files(file_id);

	-- Package ←→ Elements
	CREATE TABLE IF NOT EXISTS package_structs (pkg_id TEXT, struct_id TEXT, PRIMARY KEY(pkg_id, struct_id));
	CREATE INDEX IF NOT EXISTS idx_ps_struct ON package_structs(struct_id);
	CREATE TABLE IF NOT EXISTS package_interfaces (pkg_id TEXT, interface_id TEXT, PRIMARY KEY(pkg_id, interface_id));
	CREATE INDEX IF NOT EXISTS idx_pi_interface ON package_interfaces(interface_id);
	CREATE TABLE IF NOT EXISTS package_functions (pkg_id TEXT, func_id TEXT, PRIMARY KEY(pkg_id, func_id));
	CREATE INDEX IF NOT EXISTS idx_pf_func ON package_functions(func_id);
	CREATE TABLE IF NOT EXISTS package_extvars (pkg_id TEXT, extvar_id TEXT, PRIMARY KEY(pkg_id, extvar_id));
	CREATE INDEX IF NOT EXISTS idx_pe_extvar ON package_extvars(extvar_id);

	-- File ←→ Elements
	CREATE TABLE IF NOT EXISTS file_structs (file_id TEXT, struct_id TEXT, PRIMARY KEY(file_id, struct_id));
	CREATE INDEX IF NOT EXISTS idx_fs_struct ON file_structs(struct_id);
	CREATE TABLE IF NOT EXISTS file_interfaces (file_id TEXT, interface_id TEXT, PRIMARY KEY(file_id, interface_id));
	CREATE INDEX IF NOT EXISTS idx_fi_iface ON file_interfaces(interface_id);
	CREATE TABLE IF NOT EXISTS file_functions (file_id TEXT, func_id TEXT, PRIMARY KEY(file_id, func_id));
	CREATE INDEX IF NOT EXISTS idx_ff_func ON file_functions(func_id);
	CREATE TABLE IF NOT EXISTS file_extvars (file_id TEXT, extvar_id TEXT, PRIMARY KEY(file_id, extvar_id));
	CREATE INDEX IF NOT EXISTS idx_fe_extvar ON file_extvars(extvar_id);

	-- File → Imports
	CREATE TABLE IF NOT EXISTS file_imports_pkg (file_id TEXT, pkg_path TEXT, PRIMARY KEY(file_id, pkg_path));
	CREATE TABLE IF NOT EXISTS file_imports_dep (file_id TEXT, dep_path TEXT, PRIMARY KEY(file_id, dep_path));

	-- Struct → Constructor (0..1)
	CREATE TABLE IF NOT EXISTS struct_constructor (struct_id TEXT PRIMARY KEY, func_id TEXT);
	CREATE INDEX IF NOT EXISTS idx_sc_func ON struct_constructor(func_id);

	-- Struct ↔ Interface (bidirectional)
	CREATE TABLE IF NOT EXISTS struct_implements (struct_id TEXT, interface_id TEXT, PRIMARY KEY(struct_id, interface_id));
	CREATE INDEX IF NOT EXISTS idx_si_iface ON struct_implements(interface_id);

	-- Function → MethodFrom (0..1)
	CREATE TABLE IF NOT EXISTS method_from (func_id TEXT PRIMARY KEY, struct_id TEXT);
	CREATE INDEX IF NOT EXISTS idx_mf_struct ON method_from(struct_id);

	-- Struct → Uses (bidirectional)
	CREATE TABLE IF NOT EXISTS struct_uses_pkg (struct_id TEXT, pkg_path TEXT, PRIMARY KEY(struct_id, pkg_path));
	CREATE INDEX IF NOT EXISTS idx_sup_pkg ON struct_uses_pkg(pkg_path);
	CREATE TABLE IF NOT EXISTS struct_uses_dep (struct_id TEXT, dep_path TEXT, PRIMARY KEY(struct_id, dep_path));
	CREATE INDEX IF NOT EXISTS idx_sud_dep ON struct_uses_dep(dep_path);

	-- Interface → Uses (bidirectional)
	CREATE TABLE IF NOT EXISTS interface_uses_pkg (interface_id TEXT, pkg_path TEXT, PRIMARY KEY(interface_id, pkg_path));
	CREATE INDEX IF NOT EXISTS idx_iup_pkg ON interface_uses_pkg(pkg_path);
	CREATE TABLE IF NOT EXISTS interface_uses_dep (interface_id TEXT, dep_path TEXT, PRIMARY KEY(interface_id, dep_path));
	CREATE INDEX IF NOT EXISTS idx_iud_dep ON interface_uses_dep(dep_path);

	-- Function → Uses (bidirectional)
	CREATE TABLE IF NOT EXISTS func_calls (caller_id TEXT, callee_id TEXT, PRIMARY KEY(caller_id, callee_id));
	CREATE INDEX IF NOT EXISTS idx_fc_callee ON func_calls(callee_id);
	CREATE TABLE IF NOT EXISTS func_uses_struct (func_id TEXT, struct_id TEXT, PRIMARY KEY(func_id, struct_id));
	CREATE INDEX IF NOT EXISTS idx_fus_struct ON func_uses_struct(struct_id);
	CREATE TABLE IF NOT EXISTS func_uses_interface (func_id TEXT, interface_id TEXT, PRIMARY KEY(func_id, interface_id));
	CREATE INDEX IF NOT EXISTS idx_fui_iface ON func_uses_interface(interface_id);
	CREATE TABLE IF NOT EXISTS func_uses_extvar (func_id TEXT, extvar_id TEXT, PRIMARY KEY(func_id, extvar_id));
	CREATE INDEX IF NOT EXISTS idx_fue_extvar ON func_uses_extvar(extvar_id);
	CREATE TABLE IF NOT EXISTS func_uses_dep (func_id TEXT, dep_path TEXT, PRIMARY KEY(func_id, dep_path));
	CREATE INDEX IF NOT EXISTS idx_fud_dep ON func_uses_dep(dep_path);
	CREATE TABLE IF NOT EXISTS func_uses_pkg (func_id TEXT, pkg_path TEXT, PRIMARY KEY(func_id, pkg_path));
	CREATE INDEX IF NOT EXISTS idx_fup_pkg ON func_uses_pkg(pkg_path);
	`
	_, err := db.Exec(ddl)
	return err
}

func writeEntities(tx *sql.Tx, topo *domain.Topology) error {
	if _, err := tx.Exec("INSERT INTO info VALUES ('root', ?)", topo.Root); err != nil {
		return err
	}

	for id := range topo.Packages {
		if _, err := tx.Exec("INSERT INTO packages VALUES (?)", string(id)); err != nil {
			return err
		}
	}

	if err := execMany(tx, "INSERT INTO files VALUES (?, ?, ?, ?)", func(yield func(...any) bool) {
		for id, f := range topo.Files {
			if !yield(string(id), f.Name, f.Description, string(f.FromPackage)) {
				return
			}
		}
	}); err != nil {
		return err
	}

	if err := execMany(tx, "INSERT INTO structs VALUES (?, ?, ?, ?, ?, ?, ?)", func(yield func(...any) bool) {
		for id, s := range topo.Struct {
			if !yield(string(id), s.Name, s.Description, toJSON(s.Params), s.Loc.StartsAt, s.Loc.EndsAt, string(s.Loc.Path)) {
				return
			}
		}
	}); err != nil {
		return err
	}

	if err := execMany(tx, "INSERT INTO interfaces VALUES (?, ?, ?, ?, ?, ?, ?)", func(yield func(...any) bool) {
		for id, iface := range topo.Interfaces {
			if !yield(string(id), iface.Name, iface.Description, toJSON(iface.Methods), iface.Loc.StartsAt, iface.Loc.EndsAt, string(iface.Loc.Path)) {
				return
			}
		}
	}); err != nil {
		return err
	}

	if err := execMany(tx, "INSERT INTO functions VALUES (?, ?, ?, ?, ?, ?, ?, ?)", func(yield func(...any) bool) {
		for id, f := range topo.Functions {
			if !yield(string(id), f.Name, f.Description, toJSON(f.Input), toJSON(f.Output), f.Loc.StartsAt, f.Loc.EndsAt, string(f.Loc.Path)) {
				return
			}
		}
	}); err != nil {
		return err
	}

	if err := execMany(tx, "INSERT INTO external_vars VALUES (?, ?, ?, ?, ?, ?, ?, ?)", func(yield func(...any) bool) {
		for id, v := range topo.ExternalVars {
			var val string
			if v.Value != nil {
				b, _ := json.Marshal(*v.Value)
				val = string(b)
			}
			if !yield(string(id), v.Name, v.Description, v.Typing, val, v.StartsAt, v.EndsAt, string(v.Path)) {
				return
			}
		}
	}); err != nil {
		return err
	}

	for _, d := range topo.Dependancies {
		if _, err := tx.Exec("INSERT INTO dependencies VALUES (?)", string(d.PackagePath)); err != nil {
			return err
		}
	}

	for path, msg := range topo.Errors {
		if _, err := tx.Exec("INSERT INTO errors VALUES (?, ?)", string(path), msg); err != nil {
			return err
		}
	}

	return nil
}

func writeRelations(tx *sql.Tx, topo *domain.Topology) error {
	// Package ←→ File
	for pkgID, pkg := range topo.Packages {
		for _, f := range pkg.Files {
			if _, err := tx.Exec("INSERT INTO package_files VALUES (?, ?)", string(pkgID), string(f)); err != nil {
				return err
			}
		}
	}
	// Package ←→ Elements
	for pkgID, pkg := range topo.Packages {
		for _, s := range pkg.Structs {
			if _, err := tx.Exec("INSERT INTO package_structs VALUES (?, ?)", string(pkgID), string(s)); err != nil {
				return err
			}
		}
		for _, i := range pkg.Interfaces {
			if _, err := tx.Exec("INSERT INTO package_interfaces VALUES (?, ?)", string(pkgID), string(i)); err != nil {
				return err
			}
		}
		for _, f := range pkg.Functions {
			if _, err := tx.Exec("INSERT INTO package_functions VALUES (?, ?)", string(pkgID), string(f)); err != nil {
				return err
			}
		}
		for _, e := range pkg.ExternalVars {
			if _, err := tx.Exec("INSERT INTO package_extvars VALUES (?, ?)", string(pkgID), string(e)); err != nil {
				return err
			}
		}
	}
	// File ←→ Elements
	for fileID, f := range topo.Files {
		for _, s := range f.Structs {
			if _, err := tx.Exec("INSERT INTO file_structs VALUES (?, ?)", string(fileID), string(s)); err != nil {
				return err
			}
		}
		for _, i := range f.Interfaces {
			if _, err := tx.Exec("INSERT INTO file_interfaces VALUES (?, ?)", string(fileID), string(i)); err != nil {
				return err
			}
		}
		for _, fn := range f.Functions {
			if _, err := tx.Exec("INSERT INTO file_functions VALUES (?, ?)", string(fileID), string(fn)); err != nil {
				return err
			}
		}
		for _, e := range f.ExternalVars {
			if _, err := tx.Exec("INSERT INTO file_extvars VALUES (?, ?)", string(fileID), string(e)); err != nil {
				return err
			}
		}
		for _, p := range f.PackagesImported {
			if _, err := tx.Exec("INSERT INTO file_imports_pkg VALUES (?, ?)", string(fileID), string(p)); err != nil {
				return err
			}
		}
		for _, d := range f.DependanciesImported {
			if _, err := tx.Exec("INSERT INTO file_imports_dep VALUES (?, ?)", string(fileID), string(d.PackagePath)); err != nil {
				return err
			}
		}
	}
	// Struct → Constructor
	for id, s := range topo.Struct {
		if s.Constructor != nil {
			if _, err := tx.Exec("INSERT INTO struct_constructor VALUES (?, ?)", string(id), string(*s.Constructor)); err != nil {
				return err
			}
		}
	}
	// Struct ↔ Interface
	for ifaceID, iface := range topo.Interfaces {
		for _, structID := range iface.ImplementedBy {
			if _, err := tx.Exec("INSERT INTO struct_implements VALUES (?, ?)", string(structID), string(ifaceID)); err != nil {
				return err
			}
		}
	}
	// Struct → Uses
	for id, s := range topo.Struct {
		for _, p := range s.PackagesUsed {
			if _, err := tx.Exec("INSERT INTO struct_uses_pkg VALUES (?, ?)", string(id), string(p)); err != nil {
				return err
			}
		}
		for _, d := range s.DependanciesUsed {
			if _, err := tx.Exec("INSERT INTO struct_uses_dep VALUES (?, ?)", string(id), string(d)); err != nil {
				return err
			}
		}
	}
	// Interface → Uses
	for id, iface := range topo.Interfaces {
		for _, p := range iface.PackagesUsed {
			if _, err := tx.Exec("INSERT INTO interface_uses_pkg VALUES (?, ?)", string(id), string(p)); err != nil {
				return err
			}
		}
		for _, d := range iface.DependanciesUsed {
			if _, err := tx.Exec("INSERT INTO interface_uses_dep VALUES (?, ?)", string(id), string(d)); err != nil {
				return err
			}
		}
	}
	// Function → MethodFrom
	for id, f := range topo.Functions {
		if f.MethodFrom != nil {
			if _, err := tx.Exec("INSERT INTO method_from VALUES (?, ?)", string(id), string(*f.MethodFrom)); err != nil {
				return err
			}
		}
	}
	// Function → Uses
	for id, f := range topo.Functions {
		for _, callee := range f.FunctionsUsed {
			if _, err := tx.Exec("INSERT INTO func_calls VALUES (?, ?)", string(id), string(callee)); err != nil {
				return err
			}
		}
		for _, s := range f.StructsUsed {
			if _, err := tx.Exec("INSERT INTO func_uses_struct VALUES (?, ?)", string(id), string(s)); err != nil {
				return err
			}
		}
		for _, i := range f.InterfacesUsed {
			if _, err := tx.Exec("INSERT INTO func_uses_interface VALUES (?, ?)", string(id), string(i)); err != nil {
				return err
			}
		}
		for _, e := range f.ExternalVarsUsed {
			if _, err := tx.Exec("INSERT INTO func_uses_extvar VALUES (?, ?)", string(id), string(e)); err != nil {
				return err
			}
		}
		for _, d := range f.DependanciesUsed {
			if _, err := tx.Exec("INSERT INTO func_uses_dep VALUES (?, ?)", string(id), string(d)); err != nil {
				return err
			}
		}
		for _, p := range f.PackagesUsed {
			if _, err := tx.Exec("INSERT INTO func_uses_pkg VALUES (?, ?)", string(id), string(p)); err != nil {
				return err
			}
		}
	}
	return nil
}

// ────────────────────────────────────── Read ──────────────────────────────────────

func ReadDb(path string) (*domain.Topology, error) {
	db, err := sql.Open("sqlite", path+"?cache=shared&_journal_mode=WAL")
	if err != nil {
		return nil, err
	}
	defer db.Close()

	topo := &domain.Topology{
		Packages:     make(map[domain.PackagePath]domain.Package),
		Files:        make(map[domain.FilePath]domain.File),
		Struct:       make(map[domain.StructID]domain.Struct),
		Interfaces:   make(map[domain.InterfaceID]domain.Interface),
		Functions:    make(map[domain.FunctionID]domain.Function),
		ExternalVars: make(map[domain.ExternalVarID]domain.ExternalVar),
		Dependancies: make([]domain.Dependancy, 0),
		Errors:       make(map[domain.FilePath]string),
	}

	// Entity tables
	if err := readInfo(db, topo); err != nil {
		return nil, err
	}
	if err := readPackageIds(db, topo); err != nil {
		return nil, err
	}
	if err := readFiles(db, topo); err != nil {
		return nil, err
	}
	if err := readStructEntities(db, topo); err != nil {
		return nil, err
	}
	if err := readInterfaceEntities(db, topo); err != nil {
		return nil, err
	}
	if err := readFunctionEntities(db, topo); err != nil {
		return nil, err
	}
	if err := readExtVarEntities(db, topo); err != nil {
		return nil, err
	}
	if err := readDependencies(db, topo); err != nil {
		return nil, err
	}
	if err := readErrors(db, topo); err != nil {
		return nil, err
	}

	// Relationship tables (batch, build index maps)
	rel, err := loadRelations(db)
	if err != nil {
		return nil, err
	}
	assembleRelations(topo, rel)

	return topo, nil
}

func readInfo(db *sql.DB, topo *domain.Topology) error {
	return db.QueryRow("SELECT value FROM info WHERE key = 'root'").Scan(&topo.Root)
}

func readPackageIds(db *sql.DB, topo *domain.Topology) error {
	rows, err := db.Query("SELECT id FROM packages")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		topo.Packages[domain.PackagePath(id)] = domain.Package{Path: domain.PackagePath(id)}
	}
	return rows.Err()
}

func readFiles(db *sql.DB, topo *domain.Topology) error {
	rows, err := db.Query("SELECT id, name, description, from_package FROM files")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, name, fromPkg string
		var desc sql.NullString
		if err := rows.Scan(&id, &name, &desc, &fromPkg); err != nil {
			return err
		}
		topo.Files[domain.FilePath(id)] = domain.File{
			Path:        domain.FilePath(id),
			Name:        name,
			Description: desc.String,
			FromPackage: domain.PackagePath(fromPkg),
		}
	}
	return rows.Err()
}

func readStructEntities(db *sql.DB, topo *domain.Topology) error {
	rows, err := db.Query("SELECT id, name, description, params_json, starts_at, ends_at, loc_path FROM structs")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, name, locPath string
		var desc, paramsJSON sql.NullString
		var startsAt, endsAt int
		if err := rows.Scan(&id, &name, &desc, &paramsJSON, &startsAt, &endsAt, &locPath); err != nil {
			return err
		}
		topo.Struct[domain.StructID(id)] = domain.Struct{
			ID:          domain.StructID(id),
			Name:        name,
			Description: desc.String,
			Params:      fromJSON[[]domain.VariableDefinition](paramsJSON.String),
			Loc: domain.Location{
				StartsAt: startsAt,
				EndsAt:   endsAt,
				Path:     domain.FilePath(locPath),
			},
		}
	}
	return rows.Err()
}

func readInterfaceEntities(db *sql.DB, topo *domain.Topology) error {
	rows, err := db.Query("SELECT id, name, description, methods_json, starts_at, ends_at, loc_path FROM interfaces")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, name, locPath string
		var desc, methodsJSON sql.NullString
		var startsAt, endsAt int
		if err := rows.Scan(&id, &name, &desc, &methodsJSON, &startsAt, &endsAt, &locPath); err != nil {
			return err
		}
		topo.Interfaces[domain.InterfaceID(id)] = domain.Interface{
			ID:          domain.InterfaceID(id),
			Name:        name,
			Description: desc.String,
			Methods:     fromJSON[[]domain.FunctionDefinition](methodsJSON.String),
			Loc: domain.Location{
				StartsAt: startsAt,
				EndsAt:   endsAt,
				Path:     domain.FilePath(locPath),
			},
		}
	}
	return rows.Err()
}

func readFunctionEntities(db *sql.DB, topo *domain.Topology) error {
	rows, err := db.Query("SELECT id, name, description, input_json, output_json, starts_at, ends_at, loc_path FROM functions")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, name, locPath string
		var desc, inputJSON, outputJSON sql.NullString
		var startsAt, endsAt int
		if err := rows.Scan(&id, &name, &desc, &inputJSON, &outputJSON, &startsAt, &endsAt, &locPath); err != nil {
			return err
		}
		topo.Functions[domain.FunctionID(id)] = domain.Function{
			ID:          domain.FunctionID(id),
			Name:        name,
			Description: desc.String,
			Input:       fromJSON[[]domain.VariableDefinition](inputJSON.String),
			Output:      fromJSON[[]domain.VariableDefinition](outputJSON.String),
			Loc: domain.Location{
				StartsAt: startsAt,
				EndsAt:   endsAt,
				Path:     domain.FilePath(locPath),
			},
		}
	}
	return rows.Err()
}

func readExtVarEntities(db *sql.DB, topo *domain.Topology) error {
	rows, err := db.Query("SELECT id, name, description, typing, value_json, starts_at, ends_at, loc_path FROM external_vars")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, name, typing, locPath string
		var desc, valJSON sql.NullString
		var startsAt, endsAt int
		if err := rows.Scan(&id, &name, &desc, &typing, &valJSON, &startsAt, &endsAt, &locPath); err != nil {
			return err
		}
		v := domain.ExternalVar{
			ID:          domain.ExternalVarID(id),
			Name:        name,
			Description: desc.String,
			Typing:      typing,
			Location: domain.Location{
				StartsAt: startsAt,
				EndsAt:   endsAt,
				Path:     domain.FilePath(locPath),
			},
		}
		if valJSON.Valid && valJSON.String != "" {
			var x any
			json.Unmarshal([]byte(valJSON.String), &x)
			v.Value = &x
		}
		topo.ExternalVars[domain.ExternalVarID(id)] = v
	}
	return rows.Err()
}

func readDependencies(db *sql.DB, topo *domain.Topology) error {
	rows, err := db.Query("SELECT package_path FROM dependencies")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return err
		}
		topo.Dependancies = append(topo.Dependancies, domain.Dependancy{
			PackagePath: domain.DependancyPath(path),
		})
	}
	return rows.Err()
}

func readErrors(db *sql.DB, topo *domain.Topology) error {
	rows, err := db.Query("SELECT file_path, error_msg FROM errors")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var path, msg string
		if err := rows.Scan(&path, &msg); err != nil {
			return err
		}
		topo.Errors[domain.FilePath(path)] = msg
	}
	return rows.Err()
}

// ─────────────────────────── relationship batch loader ───────────────────────────

type relationMap struct {
	pkgFiles       map[string][]string
	pkgStructs     map[string][]string
	pkgInterfaces  map[string][]string
	pkgFunctions   map[string][]string
	pkgExtVars     map[string][]string
	fileStructs    map[string][]string
	fileInterfaces map[string][]string
	fileFunctions  map[string][]string
	fileExtVars    map[string][]string
	fileImpPkg     map[string][]string
	fileImpDep     map[string][]string
	structConstr   map[string]string
	structImpls    map[string][]string
	structUsesPkg  map[string][]string
	structUsesDep  map[string][]string
	ifaceUsesPkg   map[string][]string
	ifaceUsesDep   map[string][]string
	methodFrom     map[string]string
	funcCalls      map[string][]string
	funcUsesStruct map[string][]string
	funcUsesIface  map[string][]string
	funcUsesExtVar map[string][]string
	funcUsesDep    map[string][]string
	funcUsesPkg    map[string][]string
}

func loadRelations(db *sql.DB) (*relationMap, error) {
	r := &relationMap{}

	if err := loadPairs(db, "SELECT pkg_id, file_id FROM package_files", &r.pkgFiles); err != nil {
		return nil, err
	}
	if err := loadPairs(db, "SELECT pkg_id, struct_id FROM package_structs", &r.pkgStructs); err != nil {
		return nil, err
	}
	if err := loadPairs(db, "SELECT pkg_id, interface_id FROM package_interfaces", &r.pkgInterfaces); err != nil {
		return nil, err
	}
	if err := loadPairs(db, "SELECT pkg_id, func_id FROM package_functions", &r.pkgFunctions); err != nil {
		return nil, err
	}
	if err := loadPairs(db, "SELECT pkg_id, extvar_id FROM package_extvars", &r.pkgExtVars); err != nil {
		return nil, err
	}
	if err := loadPairs(db, "SELECT file_id, struct_id FROM file_structs", &r.fileStructs); err != nil {
		return nil, err
	}
	if err := loadPairs(db, "SELECT file_id, interface_id FROM file_interfaces", &r.fileInterfaces); err != nil {
		return nil, err
	}
	if err := loadPairs(db, "SELECT file_id, func_id FROM file_functions", &r.fileFunctions); err != nil {
		return nil, err
	}
	if err := loadPairs(db, "SELECT file_id, extvar_id FROM file_extvars", &r.fileExtVars); err != nil {
		return nil, err
	}
	if err := loadPairs(db, "SELECT file_id, pkg_path FROM file_imports_pkg", &r.fileImpPkg); err != nil {
		return nil, err
	}
	if err := loadPairs(db, "SELECT file_id, dep_path FROM file_imports_dep", &r.fileImpDep); err != nil {
		return nil, err
	}
	if err := loadSingles(db, "SELECT struct_id, func_id FROM struct_constructor", &r.structConstr); err != nil {
		return nil, err
	}
	if err := loadPairs(db, "SELECT struct_id, interface_id FROM struct_implements", &r.structImpls); err != nil {
		return nil, err
	}
	if err := loadPairs(db, "SELECT struct_id, pkg_path FROM struct_uses_pkg", &r.structUsesPkg); err != nil {
		return nil, err
	}
	if err := loadPairs(db, "SELECT struct_id, dep_path FROM struct_uses_dep", &r.structUsesDep); err != nil {
		return nil, err
	}
	if err := loadPairs(db, "SELECT interface_id, pkg_path FROM interface_uses_pkg", &r.ifaceUsesPkg); err != nil {
		return nil, err
	}
	if err := loadPairs(db, "SELECT interface_id, dep_path FROM interface_uses_dep", &r.ifaceUsesDep); err != nil {
		return nil, err
	}
	if err := loadSingles(db, "SELECT func_id, struct_id FROM method_from", &r.methodFrom); err != nil {
		return nil, err
	}
	if err := loadPairs(db, "SELECT caller_id, callee_id FROM func_calls", &r.funcCalls); err != nil {
		return nil, err
	}
	if err := loadPairs(db, "SELECT func_id, struct_id FROM func_uses_struct", &r.funcUsesStruct); err != nil {
		return nil, err
	}
	if err := loadPairs(db, "SELECT func_id, interface_id FROM func_uses_interface", &r.funcUsesIface); err != nil {
		return nil, err
	}
	if err := loadPairs(db, "SELECT func_id, extvar_id FROM func_uses_extvar", &r.funcUsesExtVar); err != nil {
		return nil, err
	}
	if err := loadPairs(db, "SELECT func_id, dep_path FROM func_uses_dep", &r.funcUsesDep); err != nil {
		return nil, err
	}
	if err := loadPairs(db, "SELECT func_id, pkg_path FROM func_uses_pkg", &r.funcUsesPkg); err != nil {
		return nil, err
	}

	return r, nil
}

func loadPairs(db *sql.DB, query string, dst *map[string][]string) error {
	*dst = make(map[string][]string)
	rows, err := db.Query(query)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var a, b string
		if err := rows.Scan(&a, &b); err != nil {
			return err
		}
		(*dst)[a] = append((*dst)[a], b)
	}
	return rows.Err()
}

func loadSingles(db *sql.DB, query string, dst *map[string]string) error {
	*dst = make(map[string]string)
	rows, err := db.Query(query)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var a, b string
		if err := rows.Scan(&a, &b); err != nil {
			return err
		}
		(*dst)[a] = b
	}
	return rows.Err()
}

func assembleRelations(topo *domain.Topology, r *relationMap) {
	// Package ← Files / Elements
	for pkgID, pkg := range topo.Packages {
		spid := string(pkgID)

		var files []domain.FilePath
		for _, f := range r.pkgFiles[spid] {
			files = append(files, domain.FilePath(f))
		}
		var structs []domain.StructID
		for _, s := range r.pkgStructs[spid] {
			structs = append(structs, domain.StructID(s))
		}
		var ifaces []domain.InterfaceID
		for _, i := range r.pkgInterfaces[spid] {
			ifaces = append(ifaces, domain.InterfaceID(i))
		}
		var funcs []domain.FunctionID
		for _, f := range r.pkgFunctions[spid] {
			funcs = append(funcs, domain.FunctionID(f))
		}
		var evars []domain.ExternalVarID
		for _, e := range r.pkgExtVars[spid] {
			evars = append(evars, domain.ExternalVarID(e))
		}
		topo.Packages[pkgID] = domain.Package{
			Path:         pkg.Path,
			Files:        files,
			Structs:      structs,
			Interfaces:   ifaces,
			Functions:    funcs,
			ExternalVars: evars,
		}
	}

	// File ← Elements / Imports
	for fileID, f := range topo.Files {
		sfid := string(fileID)

		var structs []domain.StructID
		for _, s := range r.fileStructs[sfid] {
			structs = append(structs, domain.StructID(s))
		}
		var ifaces []domain.InterfaceID
		for _, i := range r.fileInterfaces[sfid] {
			ifaces = append(ifaces, domain.InterfaceID(i))
		}
		var funcs []domain.FunctionID
		for _, fn := range r.fileFunctions[sfid] {
			funcs = append(funcs, domain.FunctionID(fn))
		}
		var evars []domain.ExternalVarID
		for _, e := range r.fileExtVars[sfid] {
			evars = append(evars, domain.ExternalVarID(e))
		}
		var pkgs []domain.PackagePath
		for _, p := range r.fileImpPkg[sfid] {
			pkgs = append(pkgs, domain.PackagePath(p))
		}
		var deps []domain.Dependancy
		for _, d := range r.fileImpDep[sfid] {
			deps = append(deps, domain.Dependancy{PackagePath: domain.DependancyPath(d)})
		}
		topo.Files[fileID] = domain.File{
			Path:                 f.Path,
			Name:                 f.Name,
			Description:          f.Description,
			FromPackage:          f.FromPackage,
			Structs:              structs,
			Interfaces:           ifaces,
			Functions:            funcs,
			ExternalVars:         evars,
			PackagesImported:     pkgs,
			DependanciesImported: deps,
		}
	}

	// Struct → Constructor + Implements
	structImpls := r.structImpls
	ifaceImplBy := make(map[string][]string)
	for sid, iids := range structImpls {
		for _, iid := range iids {
			ifaceImplBy[iid] = append(ifaceImplBy[iid], sid)
		}
	}

	for structID, s := range topo.Struct {
		ssid := string(structID)
		s.Methods = collectMethods(topo, structID)
		if cid, ok := r.structConstr[ssid]; ok {
			c := domain.FunctionID(cid)
			s.Constructor = &c
		}
		if iids, ok := structImpls[ssid]; ok && len(iids) > 0 {
			first := domain.InterfaceID(iids[0])
			s.Implements = &first
		}
		for _, p := range r.structUsesPkg[ssid] {
			s.PackagesUsed = append(s.PackagesUsed, domain.PackagePath(p))
		}
		for _, d := range r.structUsesDep[ssid] {
			s.DependanciesUsed = append(s.DependanciesUsed, domain.DependancyPath(d))
		}
		topo.Struct[structID] = s
	}

	// Interface → ImplementedBy + Uses
	for ifaceID, iface := range topo.Interfaces {
		sifid := string(ifaceID)
		iface.ImplementedBy = make([]domain.StructID, 0, len(ifaceImplBy[sifid]))
		for _, sid := range ifaceImplBy[sifid] {
			iface.ImplementedBy = append(iface.ImplementedBy, domain.StructID(sid))
		}
		for _, p := range r.ifaceUsesPkg[sifid] {
			iface.PackagesUsed = append(iface.PackagesUsed, domain.PackagePath(p))
		}
		for _, d := range r.ifaceUsesDep[sifid] {
			iface.DependanciesUsed = append(iface.DependanciesUsed, domain.DependancyPath(d))
		}
		topo.Interfaces[ifaceID] = iface
	}

	// Function → MethodFrom + Uses
	for funcID, f := range topo.Functions {
		sfid := string(funcID)

		if sid, ok := r.methodFrom[sfid]; ok {
			s := domain.StructID(sid)
			f.MethodFrom = &s
		}

		var fUsed []domain.FunctionID
		for _, callee := range r.funcCalls[sfid] {
			fUsed = append(fUsed, domain.FunctionID(callee))
		}
		var sUsed []domain.StructID
		for _, s := range r.funcUsesStruct[sfid] {
			sUsed = append(sUsed, domain.StructID(s))
		}
		var iUsed []domain.InterfaceID
		for _, i := range r.funcUsesIface[sfid] {
			iUsed = append(iUsed, domain.InterfaceID(i))
		}
		var eUsed []domain.ExternalVarID
		for _, e := range r.funcUsesExtVar[sfid] {
			eUsed = append(eUsed, domain.ExternalVarID(e))
		}
		var dUsed []domain.DependancyPath
		for _, d := range r.funcUsesDep[sfid] {
			dUsed = append(dUsed, domain.DependancyPath(d))
		}
		var pUsed []domain.PackagePath
		for _, p := range r.funcUsesPkg[sfid] {
			pUsed = append(pUsed, domain.PackagePath(p))
		}

		f.FunctionsUsed = fUsed
		f.StructsUsed = sUsed
		f.InterfacesUsed = iUsed
		f.ExternalVarsUsed = eUsed
		f.DependanciesUsed = dUsed
		f.PackagesUsed = pUsed

		topo.Functions[funcID] = f
	}
}

func collectMethods(topo *domain.Topology, structID domain.StructID) []domain.FunctionID {
	var methods []domain.FunctionID
	for id, f := range topo.Functions {
		if f.MethodFrom != nil && *f.MethodFrom == structID {
			methods = append(methods, id)
		}
	}
	return methods
}

// ─────────────────────────────────── helpers ────────────────────────────────────

func execMany(tx *sql.Tx, query string, iter func(yield func(...any) bool)) error {
	stmt, err := tx.Prepare(query)
	if err != nil {
		return err
	}
	defer stmt.Close()
	iter(func(args ...any) bool {
		_, err = stmt.Exec(args...)
		return err == nil
	})
	return err
}

func toJSON(v interface{}) string {
	if v == nil {
		return "[]"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func fromJSON[T any](s string) T {
	var v T
	if s != "" {
		json.Unmarshal([]byte(s), &v)
	}
	return v
}
