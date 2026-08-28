package universaltools

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"aracne/internal/helper"
	"aracne/internal/llm/languages/gotools"
	"aracne/internal/llm/languages/javatools"
	"aracne/internal/llm/languages/jstools"
	"aracne/internal/llm/languages/pythontools"
	"aracne/internal/llm/languages/rusttools"
	"aracne/internal/llm/tools"
	"aracne/internal/topology"
	"aracne/internal/topology/domain"
	"aracne/internal/topology/golang"
	"aracne/internal/topology/idresolve"
	"aracne/internal/topology/java"
	"aracne/internal/topology/javascript"
	"aracne/internal/topology/python"
	"aracne/internal/topology/rust"
)

// LLM tool that reads a function or method's source code with topology context.
type UniversalReadFunction struct{ mgr *topology.TopologyManager }

// LLM tool that reads a struct/class with topology context, delegating to language-specific handlers.
type UniversalReadStruct struct{ mgr *topology.TopologyManager }

// LLM tool that reads an interface, protocol, or abstract class with topology context.
type UniversalReadInterface struct{ mgr *topology.TopologyManager }

// LLM tool that reads file contents with optional line range filtering and topology context.
type UniversalReadFile struct{ mgr *topology.TopologyManager }

// LLM tool that reads a package and its language-specific topology context.
type UniversalReadPackage struct{ mgr *topology.TopologyManager }

// LLM tool that reads dependencies and formats language-specific topology context.
type UniversalReadDependency struct{ mgr *topology.TopologyManager }

// LLM tool that reads a Go named type or TypeScript type definition with topology context.
type UniversalReadNamedType struct{ mgr *topology.TopologyManager }

// Holds a resolved resource ID and its domain.Resource metadata for read operations.
type readTarget struct {
	id  string
	res domain.Resource
}

// Creates a new UniversalReadFunction tool instance with the given topology manager.
func NewReadFunction(mgr *topology.TopologyManager) *UniversalReadFunction {
	return &UniversalReadFunction{mgr: mgr}
}

// Constructs a UniversalReadStruct tool for LLM access to struct/class sources and topology context.
func NewReadStruct(mgr *topology.TopologyManager) *UniversalReadStruct {
	return &UniversalReadStruct{mgr: mgr}
}

// Creates a new UniversalReadInterface tool instance with the given topology manager.
func NewReadInterface(mgr *topology.TopologyManager) *UniversalReadInterface {
	return &UniversalReadInterface{mgr: mgr}
}

// Creates a new UniversalReadFile tool instance with the given topology manager.
func NewReadFile(mgr *topology.TopologyManager) *UniversalReadFile {
	return &UniversalReadFile{mgr: mgr}
}

// Constructs a UniversalReadPackage tool for LLM access to package sources and topology context.
func NewReadPackage(mgr *topology.TopologyManager) *UniversalReadPackage {
	return &UniversalReadPackage{mgr: mgr}
}

// Creates a new UniversalReadDependency tool instance with the given topology manager.
func NewReadDependency(mgr *topology.TopologyManager) *UniversalReadDependency {
	return &UniversalReadDependency{mgr: mgr}
}

// Constructs a UniversalReadNamedType tool for LLM access to named type sources and topology context.
func NewReadNamedType(mgr *topology.TopologyManager) *UniversalReadNamedType {
	return &UniversalReadNamedType{mgr: mgr}
}

// Returns the tool name "read_function" for the LLM interface.
func (r *UniversalReadFunction) Name() string { return "read_function" }

// Returns the tool description for reading functions or methods with topology context.
func (r *UniversalReadFunction) Description() string {
	return "Read a function or method source and language-specific topology context."
}

// Returns parameter schema describing the function name or resource ID argument.
func (r *UniversalReadFunction) Parameters() []tools.Parameter {
	return nameParam("The function name or resource ID to read")
}

// Resolves a function/method by name and returns its source with language-specific topology context.
func (r *UniversalReadFunction) Run(args json.RawMessage) (string, error) {
	name, err := readNameArg(args)
	if err != nil {
		return "", err
	}
	target, message, err := resolveReadTarget(r.mgr, name, domain.ResourceFunction, domain.ResourceMethod)
	if err != nil || message != "" {
		return message, err
	}
	filter := filterOption(r.mgr)
	switch target.res.Language {
	case "go":
		ctx, err := golang.NewGoManager(r.mgr).ReadFunction(target.id, filter)
		if err != nil {
			return "", fmt.Errorf("read function: %w", err)
		}
		return gotools.FormatGoFunctionContext(ctx), nil
	case "python":
		ctx, err := python.NewPythonManager(r.mgr).ReadFunction(target.id, filter)
		if err != nil {
			return "", fmt.Errorf("read function: %w", err)
		}
		return pythontools.FormatPythonFunctionContext(ctx), nil
	case "javascript", "typescript":
		ctx, err := javascript.NewJavaScriptManager(r.mgr).ReadFunction(target.id, filter)
		if err != nil {
			return "", fmt.Errorf("read function: %w", err)
		}
		return jstools.FormatJavaScriptFunctionContext(ctx), nil
	case "rust":
		ctx, err := rust.NewRustManager(r.mgr).ReadFunction(target.id, filter)
		if err != nil {
			return "", fmt.Errorf("read function: %w", err)
		}
		return rusttools.FormatRustFunctionContext(ctx), nil
	case "java":
		ctx, err := java.NewJavaManager(r.mgr).ReadFunction(target.id, filter)
		if err != nil {
			return "", fmt.Errorf("read function: %w", err)
		}
		return javatools.FormatJavaFunctionContext(ctx), nil
	default:
		return "", unsupportedLanguage("read_function", target)
	}
}

// Returns the tool name "read_struct".
func (r *UniversalReadStruct) Name() string { return "read_struct" }

// Returns the description for the read_struct tool.
func (r *UniversalReadStruct) Description() string {
	return "Read a type/class source and language-specific topology context."
}

// Returns parameter schema for read_struct tool: the type/class name or resource ID to read.
func (r *UniversalReadStruct) Parameters() []tools.Parameter {
	return nameParam("The type/class name or resource ID to read")
}

// Executes read_struct tool by resolving target name, delegating to language-specific managers (Go/Python/JS/TS), and formatting output.
func (r *UniversalReadStruct) Run(args json.RawMessage) (string, error) {
	name, err := readNameArg(args)
	if err != nil {
		return "", err
	}
	target, message, err := resolveReadTarget(r.mgr, name, domain.ResourceStruct)
	if err != nil || message != "" {
		return message, err
	}
	filter := filterOption(r.mgr)
	switch target.res.Language {
	case "go":
		ctx, err := golang.NewGoManager(r.mgr).ReadStruct(target.id, filter)
		if err != nil {
			return "", fmt.Errorf("read struct: %w", err)
		}
		return gotools.FormatGoStructContext(ctx), nil
	case "python":
		ctx, err := python.NewPythonManager(r.mgr).ReadClass(target.id, filter)
		if err != nil {
			return "", fmt.Errorf("read class: %w", err)
		}
		return pythontools.FormatPythonClassContext(ctx), nil
	case "javascript", "typescript":
		ctx, err := javascript.NewJavaScriptManager(r.mgr).ReadClass(target.id, filter)
		if err != nil {
			return "", fmt.Errorf("read class: %w", err)
		}
		return jstools.FormatJavaScriptClassContext(ctx), nil
	case "rust":
		ctx, err := rust.NewRustManager(r.mgr).ReadStruct(target.id, filter)
		if err != nil {
			return "", fmt.Errorf("read struct: %w", err)
		}
		return rusttools.FormatRustStructContext(ctx), nil
	case "java":
		ctx, err := java.NewJavaManager(r.mgr).ReadStruct(target.id, filter)
		if err != nil {
			return "", fmt.Errorf("read struct: %w", err)
		}
		return javatools.FormatJavaStructContext(ctx), nil
	default:
		return "", unsupportedLanguage("read_struct", target)
	}
}

// Returns the tool name "read_interface".
func (r *UniversalReadInterface) Name() string { return "read_interface" }

// Returns the description for the read_interface tool: "Read an interface, protocol, or abstract class context."
func (r *UniversalReadInterface) Description() string {
	return "Read an interface, protocol, or abstract class context."
}

// Returns parameters for read_interface: the interface/protocol name or resource ID to read.
func (r *UniversalReadInterface) Parameters() []tools.Parameter {
	return nameParam("The interface/protocol name or resource ID to read")
}

// Executes read_interface: resolves an interface/protocol/abstract-class by name/ID and returns formatted context for Go, Python, or TypeScript.
func (r *UniversalReadInterface) Run(args json.RawMessage) (string, error) {
	name, err := readNameArg(args)
	if err != nil {
		return "", err
	}
	target, message, err := resolveReadTargetWith(r.mgr, name, func(res domain.Resource) bool {
		if res.Kind == domain.ResourceInterface {
			return true
		}
		return res.Language == "python" && res.Kind == domain.ResourceStruct && (boolProp(res, "is_abc") || boolProp(res, "is_protocol"))
	})
	if err != nil || message != "" {
		return message, err
	}
	filter := filterOption(r.mgr)
	switch target.res.Language {
	case "go":
		ctx, err := golang.NewGoManager(r.mgr).ReadInterface(target.id, filter)
		if err != nil {
			return "", fmt.Errorf("read interface: %w", err)
		}
		return gotools.FormatGoInterfaceContext(ctx), nil
	case "python":
		return pythonInterfaceSummary(r.mgr, target.id)
	case "typescript":
		ctx, err := javascript.NewJavaScriptManager(r.mgr).ReadInterface(target.id, filter)
		if err != nil {
			return "", fmt.Errorf("read interface: %w", err)
		}
		return jstools.FormatJavaScriptInterfaceContext(ctx), nil
	case "rust":
		ctx, err := rust.NewRustManager(r.mgr).ReadInterface(target.id, filter)
		if err != nil {
			return "", fmt.Errorf("read interface: %w", err)
		}
		return rusttools.FormatRustInterfaceContext(ctx), nil
	case "java":
		ctx, err := java.NewJavaManager(r.mgr).ReadInterface(target.id, filter)
		if err != nil {
			return "", fmt.Errorf("read interface: %w", err)
		}
		return javatools.FormatJavaInterfaceContext(ctx), nil
	default:
		return "", unsupportedLanguage("read_interface", target)
	}
}

// Returns the tool name "read_file" for the universal file reader.
func (r *UniversalReadFile) Name() string { return "read_file" }

// Returns the help text for read_file tool describing its ability to read file source with topology context and optional line range filtering.
func (r *UniversalReadFile) Description() string {
	return "Read a file's source plus its topology context. Pass start_line/end_line for a raw line range instead."
}

// Returns the parameter schema for read_file tool: required file path, optional 1-indexed start_line and end_line for line range filtering.
func (r *UniversalReadFile) Parameters() []tools.Parameter {
	return []tools.Parameter{
		{Name: "name", Type: "string", Description: "The file path or name to read", Required: true},
		{Name: "start_line", Type: "integer", Description: "Optional 1-indexed first line to read. When set (with or without end_line), only the raw line range is returned, without topology context.", Required: false},
		{Name: "end_line", Type: "integer", Description: "Optional 1-indexed last line to read, inclusive. Defaults to the end of the file when only start_line is given.", Required: false},
	}
}

// Reads a file or line range by resolving its target and formatting content with language-specific topology context.
func (r *UniversalReadFile) Run(args json.RawMessage) (string, error) {
	name, startLine, endLine, err := readFileArgs(args)
	if err != nil {
		return "", err
	}
	target, message, err := resolveReadTarget(r.mgr, name, domain.ResourceFile)
	if startLine > 0 || endLine > 0 {
		path := name
		if err == nil && message == "" {
			path = target.id
		}
		content, last, rerr := helper.ReadFileRange(path, startLine, endLine)
		if rerr != nil {
			return "", rerr
		}
		first := startLine
		if first <= 0 {
			first = 1
		}
		return fmt.Sprintf("%s (lines %d-%d)\n%s", filepath.Base(path), first, last, content), nil
	}
	if err == nil && message == "" {
		switch target.res.Language {
		case "go":
			ctx, err := golang.NewGoManager(r.mgr).ReadFile(target.id)
			if err != nil {
				return "", fmt.Errorf("read file: %w", err)
			}
			return gotools.FormatGoFileContext(ctx), nil
		case "python":
			ctx, err := python.NewPythonManager(r.mgr).ReadModule(target.id)
			if err != nil {
				return "", fmt.Errorf("read module: %w", err)
			}
			return pythontools.FormatPythonModuleContext(ctx), nil
		case "javascript", "typescript":
			ctx, err := javascript.NewJavaScriptManager(r.mgr).ReadModule(target.id)
			if err != nil {
				return "", fmt.Errorf("read module: %w", err)
			}
			return jstools.FormatJavaScriptModuleContext(ctx), nil
		case "rust":
			ctx, err := rust.NewRustManager(r.mgr).ReadModule(target.id)
			if err != nil {
				return "", fmt.Errorf("read module: %w", err)
			}
			return rusttools.FormatRustModuleContext(ctx), nil
		case "java":
			ctx, err := java.NewJavaManager(r.mgr).ReadModule(target.id)
			if err != nil {
				return "", fmt.Errorf("read module: %w", err)
			}
			return javatools.FormatJavaModuleContext(ctx), nil
		}
	}
	return readRawFileContent(name)
}

// Reads raw file content up to the configured maximum file size.
func readRawFileContent(path string) (string, error) {
	maxSize := helper.LoadConfig(helper.ConfigPath(".aracne/topology.db")).EffectiveMaxFileSize()
	return helper.ReadRawFile(path, maxSize)
}

// Returns the tool name "read_package".
func (r *UniversalReadPackage) Name() string { return "read_package" }

// Returns description: "Read a package and language-specific topology context."
func (r *UniversalReadPackage) Description() string {
	return "Read a package and language-specific topology context."
}

// Returns parameter schema for read_package tool: the package name or resource ID to read.
func (r *UniversalReadPackage) Parameters() []tools.Parameter {
	return nameParam("The package name or resource ID to read")
}

// Executes read_package: resolves the target package and returns formatted topology context for Go packages only.
func (r *UniversalReadPackage) Run(args json.RawMessage) (string, error) {
	name, err := readNameArg(args)
	if err != nil {
		return "", err
	}
	target, message, err := resolveReadTarget(r.mgr, name, domain.ResourcePackage)
	if err != nil || message != "" {
		return message, err
	}
	switch target.res.Language {
	case "go":
		ctx, err := golang.NewGoManager(r.mgr).ReadPackage(target.id)
		if err != nil {
			return "", fmt.Errorf("read package: %w", err)
		}
		return gotools.FormatGoPackageContext(ctx), nil
	default:
		// Python and JS/TS have no package resource; the module (file) is the
		// unit there — use read_file instead.
		return "", unsupportedLanguage("read_package", target)
	}
}

// Returns the name "read_dependency" for the UniversalReadDependency tool.
func (r *UniversalReadDependency) Name() string { return "read_dependency" }

// Returns the description string for the read_dependency tool.
func (r *UniversalReadDependency) Description() string {
	return "Read a dependency and language-specific reverse usage context."
}

// Returns the parameter schema for read_dependency tool: a single required string parameter for the dependency name or resource ID.
func (r *UniversalReadDependency) Parameters() []tools.Parameter {
	return nameParam("The dependency name or resource ID to read")
}

// Executes read_dependency tool by resolving a dependency name to its target, then delegates to the appropriate language-specific manager to format dependency context.
func (r *UniversalReadDependency) Run(args json.RawMessage) (string, error) {
	name, err := readNameArg(args)
	if err != nil {
		return "", err
	}
	target, message, err := resolveReadTarget(r.mgr, name, domain.ResourceDependency)
	if err != nil || message != "" {
		return message, err
	}
	switch target.res.Language {
	case "go":
		ctx, err := golang.NewGoManager(r.mgr).ReadDependency(target.id)
		if err != nil {
			return "", fmt.Errorf("read dependency: %w", err)
		}
		return gotools.FormatGoDependencyContext(ctx), nil
	case "python":
		ctx, err := python.NewPythonManager(r.mgr).ReadDependency(target.id)
		if err != nil {
			return "", fmt.Errorf("read dependency: %w", err)
		}
		return pythontools.FormatPythonDependencyContext(ctx), nil
	case "javascript", "typescript":
		ctx, err := javascript.NewJavaScriptManager(r.mgr).ReadDependency(target.id)
		if err != nil {
			return "", fmt.Errorf("read dependency: %w", err)
		}
		return jstools.FormatJavaScriptDependencyContext(ctx), nil
	case "rust":
		ctx, err := rust.NewRustManager(r.mgr).ReadDependency(target.id)
		if err != nil {
			return "", fmt.Errorf("read dependency: %w", err)
		}
		return rusttools.FormatRustDependencyContext(ctx), nil
	case "java":
		ctx, err := java.NewJavaManager(r.mgr).ReadDependency(target.id)
		if err != nil {
			return "", fmt.Errorf("read dependency: %w", err)
		}
		return javatools.FormatJavaDependencyContext(ctx), nil
	default:
		return "", unsupportedLanguage("read_dependency", target)
	}
}

// Returns the tool name "read_named_type".
func (r *UniversalReadNamedType) Name() string { return "read_named_type" }

// Returns the description for the read_named_type tool: "Read a Go named type source and topology context."
func (r *UniversalReadNamedType) Description() string {
	return "Read a Go named type source and topology context."
}

// Returns parameter schema requesting a named type name or resource ID to read.
func (r *UniversalReadNamedType) Parameters() []tools.Parameter {
	return nameParam("The named type name or resource ID to read")
}

// Reads a named type from topology and formats context for Go or TypeScript languages.
func (r *UniversalReadNamedType) Run(args json.RawMessage) (string, error) {
	name, err := readNameArg(args)
	if err != nil {
		return "", err
	}
	target, message, err := resolveReadTarget(r.mgr, name, domain.ResourceNamedType)
	if err != nil || message != "" {
		return message, err
	}
	switch target.res.Language {
	case "go":
		ctx, err := golang.NewGoManager(r.mgr).ReadNamedType(target.id)
		if err != nil {
			return "", fmt.Errorf("read named type: %w", err)
		}
		return gotools.FormatGoNamedTypeContext(ctx), nil
	case "typescript":
		ctx, err := javascript.NewJavaScriptManager(r.mgr).ReadNamedType(target.id)
		if err != nil {
			return "", fmt.Errorf("read named type: %w", err)
		}
		return jstools.FormatJavaScriptNamedTypeContext(ctx), nil
	case "rust":
		ctx, err := rust.NewRustManager(r.mgr).ReadNamedType(target.id)
		if err != nil {
			return "", fmt.Errorf("read named type: %w", err)
		}
		return rusttools.FormatRustNamedTypeContext(ctx), nil
	default:
		return "", unsupportedLanguage("read_named_type", target)
	}
}

// Creates a required string parameter named "name" for use in tool definitions.
func nameParam(description string) []tools.Parameter {
	return []tools.Parameter{{Name: "name", Type: "string", Description: description, Required: true}}
}

// filterOption builds the context-block visibility option from the project
// config so neighbor rendering honors read.context_filter.
func filterOption(mgr *topology.TopologyManager) topology.TopologyOption {
	cfg := helper.LoadConfig(helper.ConfigPath(mgr.DbPath()))
	return topology.WithContextFilter(cfg.EffectiveContextFilter())
}

// Parses JSON arguments to extract a required "name" field.
func readNameArg(args json.RawMessage) (string, error) {
	var params struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if params.Name == "" {
		return "", fmt.Errorf("missing required argument: name")
	}
	return params.Name, nil
}

// Parses file read arguments (name, start_line, end_line) from JSON, validating that name is required.
func readFileArgs(args json.RawMessage) (string, int, int, error) {
	var params struct {
		Name      string `json:"name"`
		StartLine int    `json:"start_line"`
		EndLine   int    `json:"end_line"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", 0, 0, fmt.Errorf("invalid arguments: %w", err)
	}
	if params.Name == "" {
		return "", 0, 0, fmt.Errorf("missing required argument: name")
	}
	return params.Name, params.StartLine, params.EndLine, nil
}

// Resolves a resource name to a readTarget by filtering topology resources by kind.
func resolveReadTarget(mgr *topology.TopologyManager, name string, kinds ...domain.ResourceKind) (readTarget, string, error) {
	kindSet := make(map[domain.ResourceKind]bool, len(kinds))
	for _, kind := range kinds {
		kindSet[kind] = true
	}
	return resolveReadTargetWith(mgr, name, func(res domain.Resource) bool { return kindSet[res.Kind] })
}

// Resolves a resource name to a readTarget using a custom match predicate, with fallback language handling.
func resolveReadTargetWith(mgr *topology.TopologyManager, name string, matches func(domain.Resource) bool) (readTarget, string, error) {
	topo, err := mgr.ReadAll()
	if err != nil {
		return readTarget{}, "", fmt.Errorf("read topology: %w", err)
	}
	fallbackLanguage := topo.Language
	if fallbackLanguage == "multi" {
		fallbackLanguage = ""
	}
	candidates := make([]readTarget, 0)
	tryID := func(id string) bool {
		res, ok := topo.Resources[id]
		if !ok || !matches(res) {
			return false
		}
		if res.Language == "" {
			res.Language = fallbackLanguage
		}
		candidates = append(candidates, readTarget{id: id, res: res})
		return true
	}
	id := helper.NormalizeResourceID(name)
	if tryID(id) {
		return candidates[0], "", nil
	}
	if abs, absErr := filepath.Abs(id); absErr == nil && abs != id && tryID(abs) {
		return candidates[0], "", nil
	}
	// Beyond an exact ID, hand the query to the shared resolver: it absorbs a wrong root
	// prefix and the wrong separator convention (the Python/JS "worktree/src/flask/app.X"
	// vs "flask.app.X" problem), consults the alias table for IDs minted under a previous
	// id-scheme, and — on a miss — returns ranked suggestions instead of a dead end. A
	// wrong guess should cost the model a correction, not a whole extra exploration turn.
	res := idresolve.Resolve(topo, name, idresolve.Options{
		Filter: matches,
		Alias:  func(old string) (string, bool) { return helper.ResolveAlias(mgr.DbPath(), old) },
	})
	switch {
	case res.Found():
		target := res.Resource
		if target.Language == "" {
			target.Language = fallbackLanguage
		}
		return readTarget{id: res.ID, res: target}, "", nil
	case res.Tier == idresolve.TierAmbiguous:
		for _, c := range res.Candidates {
			r := topo.Resources[c.ID]
			if r.Language == "" {
				r.Language = fallbackLanguage
			}
			candidates = append(candidates, readTarget{id: c.ID, res: r})
		}
		sort.Slice(candidates, func(i, j int) bool { return candidates[i].id < candidates[j].id })
		return readTarget{}, ambiguousTargets(name, candidates), nil
	}
	if hint := idresolve.FormatCandidates(name, res.Candidates); hint != "" {
		return readTarget{}, "", fmt.Errorf("resource %q not found in topology. %s", name, hint)
	}
	return readTarget{}, "", fmt.Errorf("resource %q not found in topology", name)
}

// Formats an error message listing multiple resource candidates matching a name query.
func ambiguousTargets(name string, candidates []readTarget) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Multiple resources matching %q found:\n", name))
	for _, target := range candidates {
		b.WriteString(fmt.Sprintf("- %s (%s, %s)\n", target.id, target.res.Language, target.res.Kind))
	}
	return b.String()
}

// Returns an error indicating that a tool does not support a given language for a resource.
func unsupportedLanguage(tool string, target readTarget) error {
	return fmt.Errorf("%s does not support language %q for resource %s", tool, target.res.Language, target.id)
}

// Extracts a boolean property value from a resource, defaulting to false if missing or not a bool.
func boolProp(res domain.Resource, key string) bool {
	value, ok := res.Properties[key]
	if !ok || value == nil {
		return false
	}
	b, ok := value.(bool)
	return ok && b
}

// Generates a formatted summary of a Python class including name, type (ABC or Protocol), bases, and location.
func pythonInterfaceSummary(mgr *topology.TopologyManager, id string) (string, error) {
	topo, err := mgr.ReadAll()
	if err != nil {
		return "", fmt.Errorf("read topology: %w", err)
	}
	pt := python.FromGeneric(topo)
	class, ok := pt.Classes[python.ClassID(id)]
	if !ok {
		return "", fmt.Errorf("class %s not found in topology", id)
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Class: %s\n", class.Name))
	b.WriteString(fmt.Sprintf("Description: %s\n", class.Description))
	if class.IsABC {
		b.WriteString("Type: Abstract Base Class (ABC)\n")
	}
	if class.IsProtocol {
		b.WriteString("Type: Protocol\n")
	}
	b.WriteString(fmt.Sprintf("Location: %s:%d\n", class.Loc.Path, class.Loc.StartsAt))
	if len(class.Bases) > 0 {
		b.WriteString(fmt.Sprintf("Base classes: %s\n", strings.Join(class.Bases, ", ")))
	}
	return b.String(), nil
}
