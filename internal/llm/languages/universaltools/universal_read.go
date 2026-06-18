package universaltools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"aracne/internal/helper"
	"aracne/internal/llm/languages/gotools"
	"aracne/internal/llm/languages/jstools"
	"aracne/internal/llm/languages/pythontools"
	"aracne/internal/llm/tools"
	"aracne/internal/topology"
	"aracne/internal/topology/domain"
	"aracne/internal/topology/golang"
	"aracne/internal/topology/javascript"
	"aracne/internal/topology/python"
)

type UniversalReadFunction struct{ mgr *topology.TopologyManager }
type UniversalReadStruct struct{ mgr *topology.TopologyManager }
type UniversalReadInterface struct{ mgr *topology.TopologyManager }
type UniversalReadFile struct{ mgr *topology.TopologyManager }
type UniversalReadPackage struct{ mgr *topology.TopologyManager }
type UniversalReadDependency struct{ mgr *topology.TopologyManager }
type UniversalReadNamedType struct{ mgr *topology.TopologyManager }

type readTarget struct {
	id  string
	res domain.Resource
}

func NewReadFunction(mgr *topology.TopologyManager) *UniversalReadFunction {
	return &UniversalReadFunction{mgr: mgr}
}
func NewReadStruct(mgr *topology.TopologyManager) *UniversalReadStruct {
	return &UniversalReadStruct{mgr: mgr}
}
func NewReadInterface(mgr *topology.TopologyManager) *UniversalReadInterface {
	return &UniversalReadInterface{mgr: mgr}
}
func NewReadFile(mgr *topology.TopologyManager) *UniversalReadFile {
	return &UniversalReadFile{mgr: mgr}
}
func NewReadPackage(mgr *topology.TopologyManager) *UniversalReadPackage {
	return &UniversalReadPackage{mgr: mgr}
}
func NewReadDependency(mgr *topology.TopologyManager) *UniversalReadDependency {
	return &UniversalReadDependency{mgr: mgr}
}
func NewReadNamedType(mgr *topology.TopologyManager) *UniversalReadNamedType {
	return &UniversalReadNamedType{mgr: mgr}
}

func (r *UniversalReadFunction) Name() string { return "read_function" }
func (r *UniversalReadFunction) Description() string {
	return "Read a function or method source and language-specific topology context."
}
func (r *UniversalReadFunction) Parameters() []tools.Parameter {
	return nameParam("The function name or resource ID to read")
}
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
	default:
		return "", unsupportedLanguage("read_function", target)
	}
}

func (r *UniversalReadStruct) Name() string { return "read_struct" }
func (r *UniversalReadStruct) Description() string {
	return "Read a type/class source and language-specific topology context."
}
func (r *UniversalReadStruct) Parameters() []tools.Parameter {
	return nameParam("The type/class name or resource ID to read")
}
func (r *UniversalReadStruct) Run(args json.RawMessage) (string, error) {
	name, err := readNameArg(args)
	if err != nil {
		return "", err
	}
	target, message, err := resolveReadTarget(r.mgr, name, domain.ResourceType)
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
	default:
		return "", unsupportedLanguage("read_struct", target)
	}
}

func (r *UniversalReadInterface) Name() string { return "read_interface" }
func (r *UniversalReadInterface) Description() string {
	return "Read an interface, protocol, or abstract class context."
}
func (r *UniversalReadInterface) Parameters() []tools.Parameter {
	return nameParam("The interface/protocol name or resource ID to read")
}
func (r *UniversalReadInterface) Run(args json.RawMessage) (string, error) {
	name, err := readNameArg(args)
	if err != nil {
		return "", err
	}
	target, message, err := resolveReadTargetWith(r.mgr, name, func(res domain.Resource) bool {
		if res.Kind == domain.ResourceInterface {
			return true
		}
		return res.Language == "python" && res.Kind == domain.ResourceType && (boolProp(res, "is_abc") || boolProp(res, "is_protocol"))
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
	default:
		return "", unsupportedLanguage("read_interface", target)
	}
}

func (r *UniversalReadFile) Name() string { return "read_file" }
func (r *UniversalReadFile) Description() string {
	return "Read a file/module source and language-specific topology context. Optionally pass start_line/end_line to read only a specific line range (raw lines, no context)."
}
func (r *UniversalReadFile) Parameters() []tools.Parameter {
	return []tools.Parameter{
		{Name: "name", Type: "string", Description: "The file path or name to read", Required: true},
		{Name: "start_line", Type: "integer", Description: "Optional 1-indexed first line to read. When set (with or without end_line), only the raw line range is returned, without topology context.", Required: false},
		{Name: "end_line", Type: "integer", Description: "Optional 1-indexed last line to read, inclusive. Defaults to the end of the file when only start_line is given.", Required: false},
	}
}
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
		}
	}
	return readRawFileContent(name)
}

func readRawFileContent(path string) (string, error) {
	maxSize := helper.LoadConfig(helper.ConfigPath(".aracne/topology.db")).EffectiveMaxFileSize()
	if info, err := os.Stat(path); err == nil && info.Size() > maxSize {
		return "", fmt.Errorf("file %q is %d bytes, exceeding the configured read.max_file_size of %d bytes", path, info.Size(), maxSize)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("file %q not found in topology and cannot be read as file: %w", path, err)
	}
	if isBinary(data) {
		return "", fmt.Errorf("binary file %q cannot be displayed as text", path)
	}
	return fmt.Sprintf("%s\n%s", filepath.Base(path), string(data)), nil
}

func isBinary(data []byte) bool {
	n := len(data)
	if n > 512 {
		n = 512
	}
	return bytes.IndexByte(data[:n], 0) >= 0
}

func (r *UniversalReadPackage) Name() string { return "read_package" }
func (r *UniversalReadPackage) Description() string {
	return "Read a package and language-specific topology context."
}
func (r *UniversalReadPackage) Parameters() []tools.Parameter {
	return nameParam("The package name or resource ID to read")
}
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
	case "python":
		ctx, err := python.NewPythonManager(r.mgr).ReadPackage(target.id)
		if err != nil {
			return "", fmt.Errorf("read package: %w", err)
		}
		return pythontools.FormatPythonPackageContext(ctx), nil
	case "javascript", "typescript":
		ctx, err := javascript.NewJavaScriptManager(r.mgr).ReadPackage(target.id)
		if err != nil {
			return "", fmt.Errorf("read package: %w", err)
		}
		return jstools.FormatJavaScriptPackageContext(ctx), nil
	default:
		return "", unsupportedLanguage("read_package", target)
	}
}

func (r *UniversalReadDependency) Name() string { return "read_dependency" }
func (r *UniversalReadDependency) Description() string {
	return "Read a dependency and language-specific reverse usage context."
}
func (r *UniversalReadDependency) Parameters() []tools.Parameter {
	return nameParam("The dependency name or resource ID to read")
}
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
	default:
		return "", unsupportedLanguage("read_dependency", target)
	}
}

func (r *UniversalReadNamedType) Name() string { return "read_named_type" }
func (r *UniversalReadNamedType) Description() string {
	return "Read a Go named type source and topology context."
}
func (r *UniversalReadNamedType) Parameters() []tools.Parameter {
	return nameParam("The named type name or resource ID to read")
}
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
	default:
		return "", unsupportedLanguage("read_named_type", target)
	}
}

func nameParam(description string) []tools.Parameter {
	return []tools.Parameter{{Name: "name", Type: "string", Description: description, Required: true}}
}

// filterOption builds the context-block visibility option from the project
// config so neighbor rendering honors read.context_filter.
func filterOption(mgr *topology.TopologyManager) topology.TopologyOption {
	cfg := helper.LoadConfig(helper.ConfigPath(mgr.DbPath()))
	return topology.WithContextFilter(cfg.EffectiveContextFilter())
}

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

func resolveReadTarget(mgr *topology.TopologyManager, name string, kinds ...domain.ResourceKind) (readTarget, string, error) {
	kindSet := make(map[domain.ResourceKind]bool, len(kinds))
	for _, kind := range kinds {
		kindSet[kind] = true
	}
	return resolveReadTargetWith(mgr, name, func(res domain.Resource) bool { return kindSet[res.Kind] })
}

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
	for id, res := range topo.Resources {
		if !matches(res) {
			continue
		}
		if res.Name == name || id == name {
			if res.Language == "" {
				res.Language = fallbackLanguage
			}
			candidates = append(candidates, readTarget{id: id, res: res})
		}
	}
	if len(candidates) == 0 {
		return readTarget{}, "", fmt.Errorf("resource %q not found in topology", name)
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].id < candidates[j].id })
	if len(candidates) > 1 {
		return readTarget{}, ambiguousTargets(name, candidates), nil
	}
	return candidates[0], "", nil
}

func ambiguousTargets(name string, candidates []readTarget) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Multiple resources matching %q found:\n", name))
	for _, target := range candidates {
		b.WriteString(fmt.Sprintf("- %s (%s, %s)\n", target.id, target.res.Language, target.res.Kind))
	}
	return b.String()
}

func unsupportedLanguage(tool string, target readTarget) error {
	return fmt.Errorf("%s does not support language %q for resource %s", tool, target.res.Language, target.id)
}

func boolProp(res domain.Resource, key string) bool {
	value, ok := res.Properties[key]
	if !ok || value == nil {
		return false
	}
	b, ok := value.(bool)
	return ok && b
}

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
