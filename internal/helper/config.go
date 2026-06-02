package helper

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type ScanMode string

const (
	ScanModeDefault ScanMode = "default"
	ScanModeHard    ScanMode = "hard"
	ScanModeAll     ScanMode = "all"
)

type CliFunctionMode string

const (
	CliModeMCP      CliFunctionMode = "mcp"
	CliModeTerminal CliFunctionMode = "terminal"
)

type Config struct {
	ScanMode        ScanMode        `json:"scan_mode"`
	CliFunctionMode CliFunctionMode `json:"cli_function_mode"`
}

func ConfigPath(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), "config.json")
}

func LoadConfig(path string) *Config {
	cfg := &Config{ScanMode: ScanModeDefault, CliFunctionMode: CliModeMCP}
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
	if loaded.CliFunctionMode == CliModeMCP || loaded.CliFunctionMode == CliModeTerminal {
		cfg.CliFunctionMode = loaded.CliFunctionMode
	}
	return cfg
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
		cfg := &Config{ScanMode: ScanModeDefault, CliFunctionMode: CliModeMCP}
		if saveErr := SaveConfig(cfg, path); saveErr != nil {
			return cfg
		}
		return cfg
	}
	return LoadConfig(path)
}
