package helper

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoadConfigRead_UTF8BOM pins ST-8: a config saved with a UTF-8 byte order mark -- Notepad
// and several Windows editors do this -- failed to decode, and EnsureConfig replaced it with
// defaults, silently resetting the project's mode and ignore rules.
func TestLoadConfigRead_UTF8BOM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	body := `{"mode":"mcp","scan":{"ignore":["gen/"]}}`
	if err := os.WriteFile(path, []byte("\xef\xbb\xbf"+body+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, ok, readErr := LoadConfigRead(path)
	if !ok || readErr != nil {
		t.Fatalf("a BOM-prefixed valid config is valid: ok=%v err=%v", ok, readErr)
	}
	if cfg.EffectiveMode() != ModeMCP || len(cfg.Scan.Ignore) != 1 || cfg.Scan.Ignore[0] != "gen/" {
		t.Fatalf("its keys must be read, got mode=%q ignore=%v", cfg.EffectiveMode(), cfg.Scan.Ignore)
	}

	cfg = EnsureConfig(path)
	if cfg.EffectiveMode() != ModeMCP {
		t.Fatalf("EnsureConfig must keep a BOM-prefixed config, got mode %q", cfg.EffectiveMode())
	}
	if data, _ := os.ReadFile(path); string(data) != "\xef\xbb\xbf"+body+"\n" {
		t.Fatalf("EnsureConfig must not rewrite a config it could read, got %q", data)
	}
}

// TestEnsureConfig_InvalidStillReplaced pins the documented behaviour ST-8 must not widen: a
// file that is not JSON is still overwritten with defaults.
func TestEnsureConfig_InvalidStillReplaced(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("\xef\xbb\xbfnot json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := LoadConfigRead(path); ok {
		t.Fatal("a BOM does not make garbage valid")
	}
	EnsureConfig(path)
	if _, ok, _ := LoadConfigRead(path); !ok {
		t.Fatal("an invalid config is replaced with defaults, as configuration.md documents")
	}
}
