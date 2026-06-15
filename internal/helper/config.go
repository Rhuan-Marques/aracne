package helper

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"aracne/internal/topology/domain"
)

type ScanMode string

const (
	ScanModeDefault ScanMode = "default"
	ScanModeHard    ScanMode = "hard"
	ScanModeAll     ScanMode = "all"
)

type ReadToolMode string
type EditToolMode string
type OtherToolMode string
type GrepToolMode string

const (
	ReadModeNative   ReadToolMode = "native"
	ReadModeMCP      ReadToolMode = "mcp"
	ReadModeTerminal ReadToolMode = "terminal"

	EditModeNative   EditToolMode = "native"
	EditModeMCP      EditToolMode = "mcp"
	EditModeTerminal EditToolMode = "terminal"

	OtherModeMCP      OtherToolMode = "mcp"
	OtherModeTerminal OtherToolMode = "terminal"

	GrepModeNative   GrepToolMode = "native"
	GrepModeMCP      GrepToolMode = "mcp"
	GrepModeTerminal GrepToolMode = "terminal"
)

type ToolModes struct {
	Read  ReadToolMode  `json:"read"`
	Edit  EditToolMode  `json:"edit"`
	Other OtherToolMode `json:"other"`
	Grep  GrepToolMode  `json:"grep"`
}

type ScannerConfig struct {
	UpdateFrequency int `json:"update_frequency"`
}

type Config struct {
	ScanMode             ScanMode                     `json:"scan_mode"`
	ToolModes            ToolModes                    `json:"tool_modes"`
	NeedDescription      []domain.ResourceKind        `json:"need_description"`
	DescribeTargets      []domain.ResourceKind        `json:"describe_targets,omitempty"`
	DescriptionBatchSize int                          `json:"description_batch_size"`
	ReadSplit            map[domain.ResourceKind]bool `json:"read_split,omitempty"`
	MaxFileSize          int64                        `json:"max_file_size,omitempty"`
	Scanner              ScannerConfig                `json:"scanner"`
}

const DefaultDescriptionBatchSize = 5

func (c *Config) EffectiveMaxFileSize() int64 {
	if c.MaxFileSize <= 0 {
		return 512 * 1024
	}
	return c.MaxFileSize
}

func ConfigPath(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), "config.json")
}

func DefaultToolModes() ToolModes {
	return ToolModes{Read: ReadModeMCP, Edit: EditModeNative, Other: OtherModeMCP, Grep: GrepModeNative}
}

func DefaultNeedDescription() []domain.ResourceKind {
	return []domain.ResourceKind{
		domain.ResourceFunction,
		domain.ResourceMethod,
		domain.ResourceType,
		domain.ResourceInterface,
		domain.ResourceFile,
	}
}

func DefaultDescribeTargets() []domain.ResourceKind {
	return DefaultNeedDescription()
}

func DefaultConfig() *Config {
	return &Config{ScanMode: ScanModeDefault, ToolModes: DefaultToolModes(), NeedDescription: DefaultNeedDescription(), DescriptionBatchSize: DefaultDescriptionBatchSize, MaxFileSize: 512 * 1024, Scanner: ScannerConfig{UpdateFrequency: 200}}
}

func LoadConfig(path string) *Config {
	cfg := DefaultConfig()
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg
	}
	var loaded Config
	if err := json.Unmarshal(data, &loaded); err != nil {
		return cfg
	}
	if loaded.ScanMode == ScanModeDefault || loaded.ScanMode == ScanModeHard || loaded.ScanMode == ScanModeAll {
		cfg.ScanMode = loaded.ScanMode
	}
	if loaded.ToolModes.Read == ReadModeNative || loaded.ToolModes.Read == ReadModeMCP || loaded.ToolModes.Read == ReadModeTerminal {
		cfg.ToolModes.Read = loaded.ToolModes.Read
	}
	if loaded.ToolModes.Edit == EditModeNative || loaded.ToolModes.Edit == EditModeMCP || loaded.ToolModes.Edit == EditModeTerminal {
		cfg.ToolModes.Edit = loaded.ToolModes.Edit
	}
	if loaded.ToolModes.Other == OtherModeMCP || loaded.ToolModes.Other == OtherModeTerminal {
		cfg.ToolModes.Other = loaded.ToolModes.Other
	}
	if loaded.ToolModes.Grep == GrepModeNative || loaded.ToolModes.Grep == GrepModeMCP || loaded.ToolModes.Grep == GrepModeTerminal {
		cfg.ToolModes.Grep = loaded.ToolModes.Grep
	}
	if loaded.NeedDescription != nil {
		if targets, err := NormalizeDescribeTargets(loaded.NeedDescription); err == nil {
			cfg.NeedDescription = targets
		}
	} else if loaded.DescribeTargets != nil {
		if targets, err := NormalizeDescribeTargets(loaded.DescribeTargets); err == nil {
			cfg.NeedDescription = targets
		}
	}
	if loaded.DescribeTargets != nil {
		if targets, err := NormalizeDescribeTargets(loaded.DescribeTargets); err == nil {
			cfg.DescribeTargets = targets
		}
	}
	if loaded.MaxFileSize > 0 {
		cfg.MaxFileSize = loaded.MaxFileSize
	}
	if loaded.DescriptionBatchSize > 0 {
		cfg.DescriptionBatchSize = loaded.DescriptionBatchSize
	}
	if loaded.ReadSplit != nil {
		cfg.ReadSplit = loaded.ReadSplit
	}
	if loaded.Scanner.UpdateFrequency > 0 {
		cfg.Scanner.UpdateFrequency = loaded.Scanner.UpdateFrequency
	}
	return cfg
}

func ParseDescribeTargets(value string) ([]domain.ResourceKind, error) {
	if strings.TrimSpace(value) == "" {
		return nil, fmt.Errorf("describe targets cannot be empty")
	}
	parts := strings.Split(value, ",")
	targets := make([]domain.ResourceKind, 0, len(parts))
	for _, part := range parts {
		kind, err := ParseDescribeTarget(part)
		if err != nil {
			return nil, err
		}
		targets = append(targets, kind)
	}
	return dedupeDescribeTargets(targets), nil
}

func NormalizeDescribeTargets(targets []domain.ResourceKind) ([]domain.ResourceKind, error) {
	normalized := make([]domain.ResourceKind, 0, len(targets))
	for _, target := range targets {
		kind, err := ParseDescribeTarget(string(target))
		if err != nil {
			return nil, err
		}
		normalized = append(normalized, kind)
	}
	return dedupeDescribeTargets(normalized), nil
}

func ParseDescribeTarget(value string) (domain.ResourceKind, error) {
	s := strings.ToLower(strings.TrimSpace(value))
	s = strings.ReplaceAll(s, "-", "_")
	s = strings.ReplaceAll(s, " ", "_")
	s = strings.TrimSuffix(s, "s")

	switch s {
	case "package":
		return domain.ResourcePackage, nil
	case "file":
		return domain.ResourceFile, nil
	case "function":
		return domain.ResourceFunction, nil
	case "method":
		return domain.ResourceMethod, nil
	case "type", "struct", "class", "classe":
		return domain.ResourceType, nil
	case "named_type":
		return domain.ResourceNamedType, nil
	case "interface":
		return domain.ResourceInterface, nil
	case "variable", "external_var", "externalvar":
		return domain.ResourceVariable, nil
	case "dependency", "dependencie":
		return domain.ResourceDependency, nil
	default:
		return "", fmt.Errorf("invalid describe target %q", value)
	}
}

func DescribeTargetSet(targets []domain.ResourceKind) map[domain.ResourceKind]bool {
	if targets == nil {
		targets = DefaultDescribeTargets()
	}
	set := make(map[domain.ResourceKind]bool, len(targets))
	for _, target := range targets {
		set[target] = true
	}
	return set
}

func ShouldDescribeKind(kind domain.ResourceKind, targets []domain.ResourceKind) bool {
	if targets == nil {
		targets = DefaultDescribeTargets()
	}
	return DescribeTargetSet(targets)[kind]
}

func FormatDescribeTargets(targets []domain.ResourceKind) string {
	if targets == nil {
		targets = DefaultDescribeTargets()
	}
	values := make([]string, 0, len(targets))
	for _, target := range targets {
		values = append(values, string(target))
	}
	return strings.Join(values, ", ")
}

func dedupeDescribeTargets(targets []domain.ResourceKind) []domain.ResourceKind {
	seen := make(map[domain.ResourceKind]bool, len(targets))
	result := make([]domain.ResourceKind, 0, len(targets))
	for _, target := range targets {
		if seen[target] {
			continue
		}
		seen[target] = true
		result = append(result, target)
	}
	return result
}

func SaveConfig(cfg *Config, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func EnsureConfig(path string) *Config {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		cfg := DefaultConfig()
		if saveErr := SaveConfig(cfg, path); saveErr != nil {
			return cfg
		}
		return cfg
	}
	return LoadConfig(path)
}
