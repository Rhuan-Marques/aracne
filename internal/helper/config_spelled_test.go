package helper

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Validate is where a typo in an enum key is reported -- EffectiveMode's contract says so, and
// falls back silently on the strength of it. It used to judge the values normalizeConfig had
// already coerced, so a misspelled mode ran as cli with nothing said, while the file on disk
// still showed the intended value.
func TestValidateReportsEnumTyposAfterLoading(t *testing.T) {
	for _, tc := range []struct{ raw, key string }{
		{`{"mode":"intercept_lineranges"}`, "mode"},
		{`{"contract_verbosity":"hgih"}`, "contract_verbosity"},
		{`{"read":{"context_filter":"ful"}}`, "read.context_filter"},
	} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(tc.raw), 0o644); err != nil {
			t.Fatal(err)
		}
		cfg, ok := LoadConfigStrict(path)
		if !ok {
			t.Fatalf("%s: did not load as the current schema", tc.raw)
		}
		err := cfg.Validate()
		if err == nil || !strings.HasPrefix(err.Error(), tc.key+":") {
			t.Errorf("%s: Validate() = %v, want an error naming %s", tc.raw, err, tc.key)
		}
	}
}

// A value a caller SETS after loading is judged as set. `arac init` loads the config, writes
// the wizard's answer over the mode and validates: the typo it has just corrected is gone.
func TestValidateJudgesAValueSetAfterLoading(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"mode":"intercept_lineranges","contract_verbosity":"hgih"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, _ := LoadConfigStrict(path)
	cfg.Mode = ModeInterceptLineRanges
	cfg.ContractVerbosity = ContractVerbosityHigh
	if err := cfg.Validate(); err != nil {
		t.Errorf("both typos were overwritten with valid values, Validate() = %v", err)
	}

	// Well-formed and absent values still pass, loaded or constructed.
	for _, raw := range []string{`{"mode":"mcp"}`, `{"scan":{"ignore":[]}}`, `{"mode":" CLI "}`} {
		p := filepath.Join(t.TempDir(), "config.json")
		os.WriteFile(p, []byte(raw), 0o644)
		c, _ := LoadConfigStrict(p)
		if err := c.Validate(); err != nil {
			t.Errorf("%s: Validate() = %v", raw, err)
		}
	}
	if err := DefaultConfig().Validate(); err != nil {
		t.Errorf("DefaultConfig: %v", err)
	}
}
