package pyscanner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	"github.com/Rhuan-Marques/aracne/internal/topology/python"
)

// Records a Python import statement with the name, optional alias, module path, and relative import level.
type pyImport struct {
	Name   string `json:"name"`
	Alias  string `json:"alias"`
	Module string `json:"module,omitempty"`
	Level  int    `json:"level,omitempty"`
}

// Captures Python class metadata including name, docstring, base classes, decorators, methods, class variables, and abstractness/protocol flags.
type pyClass struct {
	Name       string   `json:"name"`
	Docstring  string   `json:"docstring"`
	Bases      []string `json:"bases"`
	Decorators []string `json:"decorators"`
	Methods    []pyFunc `json:"methods"`
	ClassVars  []pyVar  `json:"class_vars"`
	// NestedClasses holds classes defined in this class's body (e.g. Outer.Inner),
	// so they can be extracted as their own qualified resources.
	NestedClasses []pyClass `json:"nested_classes"`
	// Metaclass is the name in `class C(metaclass=Meta)` (empty when absent),
	// recorded so a uses_class edge to the metaclass can be emitted.
	Metaclass          string `json:"metaclass"`
	IsABC              bool   `json:"is_abc"`
	IsProtocol         bool   `json:"is_protocol"`
	HasAbstractMethods bool   `json:"has_abstract_methods"`
	Lineno             int    `json:"lineno"`
	EndLineno          int    `json:"end_lineno"`
	BodyLineno         int    `json:"body_lineno"`
	DocStart           int    `json:"doc_start"`
	DocEnd             int    `json:"doc_end"`
}

// Represents a function or method call within a Python function body with the called function name, object/method context, and line number.
type pyBodyCall struct {
	Func       string `json:"func"`
	ObjectName string `json:"object_name"`
	MethodName string `json:"method_name"`
	LineNo     int    `json:"lineno"`
	// What the call passes. ArgC is -1 when a starred argument hides the real count.
	// Recorded so a later scan can ask whether the call still fits its callee rather than
	// whether the callee merely changed. Absent on the synthesized operator and decorator
	// pseudo-calls, which have no argument list; those record no shape and stay unjudged.
	ArgC     int      `json:"argc"`
	ArgTypes []string `json:"argtypes"`
	Starred  bool     `json:"starred"`
	KwNames  []string `json:"kw_names"`
}

// Struct capturing an assignment statement in Python source: variable name, inferred value type, and line number.
// OpLeft/OpDunder are set for `x = a <op> b` assignments (the left operand name
// and the operator's dunder, e.g. __add__) so the resolver can type x as the
// class produced by the operand's operator dunder.
type pyBodyAssign struct {
	Name      string `json:"name"`
	ValueType string `json:"value_type"`
	LineNo    int    `json:"lineno"`
	OpLeft    string `json:"op_left"`
	OpDunder  string `json:"op_dunder"`
}

// Represents a Python function or method with signature, docstring, decorators, async/property/abstract flags, parameters, return values, and body calls/assignments.
type pyFunc struct {
	Name       string         `json:"name"`
	Docstring  string         `json:"docstring"`
	Decorators []string       `json:"decorators"`
	IsAsync    bool           `json:"is_async"`
	IsProperty bool           `json:"is_property"`
	IsAbstract bool           `json:"is_abstract"`
	Params     []pyVarDef     `json:"params"`
	Results    []pyVarDef     `json:"results"`
	Lineno     int            `json:"lineno"`
	EndLineno  int            `json:"end_lineno"`
	Parent     *string        `json:"parent"`
	BodyCalls  []pyBodyCall   `json:"body_calls"`
	BodyAssign []pyBodyAssign `json:"body_assignments"`
	BodyLineno int            `json:"body_lineno"`
	DocStart   int            `json:"doc_start"`
	DocEnd     int            `json:"doc_end"`
}

// Represents a Python variable definition with its name and type annotation.
// TypeRefs holds the inner class names extracted from a composite/generic
// annotation (e.g. Optional[Alpha] -> [Optional, Alpha]) so each can resolve to
// its own uses_class edge.
type pyVarDef struct {
	Name     string   `json:"name"`
	Typing   string   `json:"typing"`
	TypeRefs []string `json:"type_refs"`
	Optional bool     `json:"optional"`
	Variadic bool     `json:"variadic"`
	KeyOnly  bool     `json:"key_only"`
}

// Represents a Python variable with its name, type annotation, value, and source location.
// TypeRefs holds the inner type names when the variable is a type alias
// (PEP 695 `type X = ...` or an old-style `X = list[Y]`), enabling transitive
// resolution of annotations that name the alias.
type pyVar struct {
	Name      string   `json:"name"`
	Typing    string   `json:"typing"`
	Value     string   `json:"value"`
	TypeRefs  []string `json:"type_refs"`
	Lineno    int      `json:"lineno"`
	EndLineno int      `json:"end_lineno"`
}

// Aggregates all top-level Python file contents: docstring, imports, import map, functions, classes, and module-level variables.
type pyFileResult struct {
	Docstring string            `json:"docstring"`
	Imports   []pyImport        `json:"imports"`
	ImportMap map[string]string `json:"import_map"`
	Functions []pyFunc          `json:"functions"`
	Classes   []pyClass         `json:"classes"`
	Variables []pyVar           `json:"variables"`
}

// Parses a Python file by executing a script via python3/python interpreter and deserializes the resulting JSON AST.
func parsePythonFile(filePath string) (*pyFileResult, error) {
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return nil, fmt.Errorf("resolve path: %w", err)
	}

	dir := filepath.Dir(absPath)

	pythonExes := []string{"python3", "python"}
	var cmdErr error
	for _, exe := range pythonExes {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		cmd := exec.CommandContext(ctx, exe, "-c", pythonParseScript, absPath, dir)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		runErr := cmd.Run()
		cancel()
		if runErr == nil {
			// Decode only stdout: anything the interpreter writes to stderr
			// (deprecation/syntax warnings, site noise) must not corrupt the
			// JSON payload.
			var result pyFileResult
			if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
				return nil, fmt.Errorf("parse json: %w\nstderr: %s", err, stderr.String())
			}
			return &result, nil
		}
		if ctx.Err() == context.DeadlineExceeded {
			cmdErr = fmt.Errorf("%s parse timed out after 30s for %s", exe, absPath)
			continue
		}
		cmdErr = fmt.Errorf("%s parse failed: %s: %w", exe, stderr.String(), runErr)
	}
	return nil, cmdErr
}

// Holds parsed Python file contents: module path, imports, classes, functions, and variables with cross-file symbol resolution maps.
type ParseResult struct {
	FileID          string
	FileDescription string
	PkgPath         python.PackagePath
	// ModulePath is the module-qualified ID prefix for this file's resources
	// (root base name + relative path, extension stripped, "/"-separated), e.g.
	// "proj/pkg/shapes". All functions/classes/vars defined here are keyed under it.
	ModulePath      string
	ModuleRoot      string
	InternalImports []python.PackagePath
	// InternalImportRecords keeps the structured internal imports (name, module,
	// level) so module->module import edges can be resolved against the topology.
	InternalImportRecords []pyImport
	ExternalImports       []python.PythonDependancy
	Classes               []python.PythonClass
	Functions             []FunctionParse
	ExternalVars          []python.PythonExternalVar
	ImportMap             map[string]string
	// ImportTargets maps each internal-import alias to the module it resolves to,
	// used to build cross-file symbol IDs (alias -> target module path + symbol).
	ImportTargets map[string]pyImportTarget
	// StarImports holds the module-path prefixes of `from .m import *` imports,
	// so bare names defined in those modules resolve to a symbol in this file.
	StarImports  []string
	ClassVarRefs []ClassVarRef
	// MetaclassRefs records each class's `metaclass=Meta` reference, resolved to a
	// uses_class edge after every class is registered.
	MetaclassRefs []MetaclassRef
	// TypeAliases maps a module-level type-alias name to the inner type names it
	// expands to (PEP 695 `type X = ...` and old-style `X = list[Y]`), so an
	// annotation naming the alias resolves transitively to the aliased classes.
	TypeAliases map[string][]string
}

// Holds parsed function metadata including signature, AST body, and call/assignment analyses
type FunctionParse struct {
	Function    python.PythonFunction
	Body        *pyFunc
	BodyCalls   []pyBodyCall
	BodyAssigns []pyBodyAssign
}

// Represents a reference to a value in a class variable for later resolution
type ClassVarRef struct {
	ClassID  python.ClassID
	RefValue string
}

// Represents a class's metaclass reference (`class C(metaclass=Meta)`), resolved
// to a uses_class edge once all classes are registered.
type MetaclassRef struct {
	ClassID python.ClassID
	Name    string
}

// Parses a Python file and extracts classes, functions, variables, and imports into a ParseResult
func ParseFile(filePath string, pkgPath python.PackagePath, moduleRoot string) (*ParseResult, error) {
	raw, err := parsePythonFile(filePath)
	if err != nil {
		return nil, err
	}

	modulePath := pyModulePath(moduleRoot, filePath)

	pr := &ParseResult{
		FileID:          filePath,
		FileDescription: raw.Docstring,
		PkgPath:         pkgPath,
		ModulePath:      modulePath,
		ModuleRoot:      moduleRoot,
		ImportMap:       raw.ImportMap,
		ImportTargets:   make(map[string]pyImportTarget),
		TypeAliases:     make(map[string][]string),
	}

	for _, imp := range raw.Imports {
		// Relative imports (level > 0, e.g. `from . import x`) are internal by
		// definition, regardless of how their dotted name resolves.
		if imp.Level > 0 || isInternal(imp.Name, moduleRoot) || (imp.Module != "" && isInternal(imp.Module, moduleRoot)) {
			pr.InternalImports = append(pr.InternalImports, python.PackagePath(imp.Name))
			pr.InternalImportRecords = append(pr.InternalImportRecords, imp)
			if tgt, ok := resolveInternalImport(imp, filePath, moduleRoot); ok {
				if imp.Alias == "*" || imp.Name == "*" {
					// `from .m import *`: the alias is not a usable name; record
					// the source module so bare names defined there resolve here.
					if tgt.ModulePath != "" {
						pr.StarImports = append(pr.StarImports, tgt.ModulePath)
					}
				} else {
					alias := imp.Alias
					if alias == "" {
						alias = imp.Name
					}
					pr.ImportTargets[alias] = tgt
				}
			}
		} else {
			pr.ExternalImports = append(pr.ExternalImports, python.PythonDependancy{
				PackagePath: python.DependancyPath(imp.Name),
			})
		}
	}

	for _, cls := range raw.Classes {
		pr.addClassTree(cls, filePath, modulePath, modulePath)
	}

	for _, fn := range raw.Functions {
		f := convertFunction(fn, filePath, modulePath, nil, pr.ImportMap, pr.ImportTargets)
		pr.Functions = append(pr.Functions, FunctionParse{Function: f, Body: &fn, BodyCalls: fn.BodyCalls, BodyAssigns: fn.BodyAssign})
	}

	for _, v := range raw.Variables {
		id := python.ExternalVarID(modulePath + "." + v.Name)
		if len(v.TypeRefs) > 0 {
			pr.TypeAliases[v.Name] = v.TypeRefs
		}
		var val *any
		if v.Value != "" && v.Value != "None" {
			var x any = v.Value
			val = &x
		}
		pr.ExternalVars = append(pr.ExternalVars, python.PythonExternalVar{
			ID:          id,
			Name:        v.Name,
			Description: "",
			Typing:      v.Typing,
			Value:       val,
			Location: domain.Location{
				Path:     filePath,
				StartsAt: v.Lineno,
				EndsAt:   v.EndLineno,
			},
		})
	}

	return pr, nil
}

// addClassTree registers a class and everything it owns: its methods, class-var
// references, member resources (Enum/NamedTuple/TypedDict fields), a synthesized
// dataclass constructor, its metaclass reference, and — recursively — any nested
// classes (Outer.Inner). idPrefix is the ID namespace the class is keyed under
// (the module path for a top-level class, the parent class's ID for a nested
// one); modulePath stays the real module path for type resolution.
func (pr *ParseResult) addClassTree(cls pyClass, filePath, modulePath, idPrefix string) {
	c := convertClass(cls, filePath, idPrefix)
	pr.Classes = append(pr.Classes, c)

	for _, cv := range cls.ClassVars {
		if isResolvableRef(cv.Value) {
			pr.ClassVarRefs = append(pr.ClassVarRefs, ClassVarRef{
				ClassID:  c.ID,
				RefValue: cv.Value,
			})
		}
	}

	if cls.Metaclass != "" {
		pr.MetaclassRefs = append(pr.MetaclassRefs, MetaclassRef{
			ClassID: c.ID,
			Name:    cls.Metaclass,
		})
	}

	hasInit := false
	for _, method := range cls.Methods {
		if method.Name == "__init__" {
			hasInit = true
		}
		f := convertFunction(method, filePath, modulePath, &c.ID, pr.ImportMap, pr.ImportTargets)
		pr.Functions = append(pr.Functions, FunctionParse{Function: f, Body: &method, BodyCalls: method.BodyCalls, BodyAssigns: method.BodyAssign})
	}

	// Enum members and class-based NamedTuple/TypedDict fields are modeled as
	// member variable resources keyed under the class (e.g. Color.RED, PointNT.x).
	if isMemberFieldClass(cls.Bases) {
		for _, cv := range cls.ClassVars {
			pr.ExternalVars = append(pr.ExternalVars, python.PythonExternalVar{
				ID:     python.ExternalVarID(string(c.ID) + "." + cv.Name),
				Name:   cv.Name,
				Typing: cv.Typing,
				Location: domain.Location{
					Path:     filePath,
					StartsAt: cv.Lineno,
					EndsAt:   cv.EndLineno,
				},
			})
		}
	}

	// A @dataclass with no explicit __init__ gets a synthesized constructor so
	// instantiation resolves to a real constructor resource.
	if !hasInit && isDataclass(cls.Decorators) {
		var input []python.VariableDefinition
		for _, cv := range cls.ClassVars {
			input = append(input, python.VariableDefinition{
				Name:     cv.Name,
				Typing:   cv.Typing,
				TypingID: canonicalTypeID(cv.Typing, modulePath, pr.ImportMap, pr.ImportTargets),
			})
		}
		ctorID := python.FunctionID(string(c.ID) + ".__init__")
		ctor := python.PythonFunction{
			ID:          ctorID,
			Name:        "__init__",
			Input:       input,
			Loc:         domain.Location{Path: filePath, StartsAt: cls.Lineno, EndsAt: cls.EndLineno},
			Connections: make(map[python.ConnectionKind][]string),
			MethodFrom:  &c.ID,
		}
		pr.Functions = append(pr.Functions, FunctionParse{Function: ctor})
	}

	for _, nested := range cls.NestedClasses {
		pr.addClassTree(nested, filePath, modulePath, string(c.ID))
	}
}

// isMemberFieldClass reports whether a class's bases mark it as an Enum,
// NamedTuple, or TypedDict — kinds whose class-level assignments are modeled as
// member field resources rather than ordinary class attributes.
func isMemberFieldClass(bases []string) bool {
	for _, b := range bases {
		name := b
		if i := strings.LastIndex(b, "."); i >= 0 {
			name = b[i+1:]
		}
		switch name {
		case "Enum", "IntEnum", "StrEnum", "Flag", "IntFlag", "NamedTuple", "TypedDict":
			return true
		}
	}
	return false
}

// isDataclass reports whether any decorator is the @dataclass decorator.
func isDataclass(decorators []string) bool {
	for _, d := range decorators {
		name := d
		if i := strings.LastIndex(d, "."); i >= 0 {
			name = d[i+1:]
		}
		if name == "dataclass" {
			return true
		}
	}
	return false
}

// Converts a parsed Python class into a PythonClass topology resource with metadata.
func convertClass(cls pyClass, filePath string, modulePath string) python.PythonClass {
	id := python.ClassID(modulePath + "." + cls.Name)
	return python.PythonClass{
		ID:                 id,
		Name:               cls.Name,
		Description:        cls.Docstring,
		Bases:              cls.Bases,
		Params:             nil,
		Loc:                domain.Location{Path: filePath, StartsAt: cls.Lineno, EndsAt: cls.EndLineno},
		Connections:        make(map[python.ConnectionKind][]string),
		IsABC:              cls.IsABC,
		IsProtocol:         cls.IsProtocol,
		HasAbstractMethods: cls.HasAbstractMethods,
		BodyLine:           cls.BodyLineno,
		DocStart:           cls.DocStart,
		DocEnd:             cls.DocEnd,
	}
}

// Converts a parsed Python function into a PythonFunction topology resource, resolving type hints and handling methods.
func convertFunction(fn pyFunc, filePath string, modulePath string, classID *python.ClassID, importMap map[string]string, importTargets map[string]pyImportTarget) python.PythonFunction {
	var input []python.VariableDefinition
	for _, p := range fn.Params {
		if p.Name == "self" || p.Name == "cls" {
			continue
		}
		input = append(input, python.VariableDefinition{
			Name: p.Name, Typing: p.Typing,
			TypingID: canonicalTypeID(p.Typing, modulePath, importMap, importTargets),
			Optional: p.Optional, Variadic: p.Variadic, KeyOnly: p.KeyOnly,
		})
	}

	var output []python.VariableDefinition
	for _, r := range fn.Results {
		output = append(output, python.VariableDefinition{Name: r.Name, Typing: r.Typing, TypingID: canonicalTypeID(r.Typing, modulePath, importMap, importTargets)})
	}

	var id python.FunctionID
	if classID != nil {
		// classID already carries the module prefix; do not double it.
		id = python.FunctionID(string(*classID) + "." + fn.Name)
	} else {
		id = python.FunctionID(modulePath + "." + fn.Name)
	}

	return python.PythonFunction{
		ID:          id,
		Name:        fn.Name,
		Description: fn.Docstring,
		Decorators:  fn.Decorators,
		Input:       input,
		Output:      output,
		Loc:         domain.Location{Path: filePath, StartsAt: fn.Lineno, EndsAt: fn.EndLineno},
		Connections: make(map[python.ConnectionKind][]string),
		MethodFrom:  classID,
		IsAsync:     fn.IsAsync,
		BodyLine:    fn.BodyLineno,
		DocStart:    fn.DocStart,
		DocEnd:      fn.DocEnd,
	}
}

// isInternal reports whether an import names first-party code.
//
// It used to compare the import's first segment to filepath.Base(moduleRoot) — i.e. to
// assume the checkout directory IS the top-level package. That is false for src layouts
// and for any checkout not named after its package, so first-party imports were recorded
// as third-party dependencies and their edges never drawn. See importroots.go.
func isInternal(importPath, moduleRoot string) bool {
	return rootsFor(moduleRoot).isInternalImport(importPath)
}

// pyModulePath returns the module-qualified ID namespace for a source file: the file's
// path relative to the project root, extension stripped, "/"-separated. Mirrors
// jsModulePath so Python and JS share one file-first ID scheme (e.g. root "/x/proj",
// file "/x/proj/pkg/shapes.py" -> "pkg/shapes").
//
// The project-root BASE NAME used to be prefixed here ("proj/pkg/shapes"). It was removed
// in id-scheme 2: the directory a checkout happens to live in appears nowhere in the
// source, so no reader — human or model — could construct an ID, and every lookup in
// Python/JS fell through to an ambiguous name scan. Dropping it makes the ID a repo-
// relative path, which is exactly what an import statement or a traceback shows.
// The importable dotted form ("pkg.shapes.Class.method") still resolves: see
// internal/topology/idresolve, whose suffix tier is separator- and prefix-insensitive.
func pyModulePath(root, file string) string {
	rel, err := filepath.Rel(root, file)
	if err != nil {
		rel = filepath.Base(file)
	}
	rel = strings.ReplaceAll(rel, "\\", "/")
	rel = strings.TrimSuffix(rel, filepath.Ext(rel))
	if rel == "." || rel == "" {
		return ""
	}
	return rel
}

// pyRefValueRE matches a bare identifier or dotted path that could name a
// resolvable resource (class/function/variable).
var pyRefValueRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`)

// pyNonRefValues are tokens the AST script emits for literals/collections that
// look like identifiers but never name a topology resource.
var pyNonRefValues = map[string]bool{
	"None": true, "True": true, "False": true,
	"list": true, "tuple": true, "dict": true, "set": true,
	"str": true, "expr": true,
}

// isResolvableRef reports whether a class-var value is worth attempting to
// resolve to a class/function/variable reference. Literal values (numbers,
// quoted strings, collapsed collection tokens) are skipped so they no longer
// generate spurious reference edges.
func isResolvableRef(value string) bool {
	if value == "" || pyNonRefValues[value] {
		return false
	}
	return pyRefValueRE.MatchString(value)
}
