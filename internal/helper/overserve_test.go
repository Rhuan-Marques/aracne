package helper

import "testing"

// The ceiling is a ratio with two ends. Without the floor it refuses the narrow window whose
// enclosing function was the point; without the cap a large raw answer buys an enormous
// enriched one. Both ends have to hold, and the floor has to differ by surface: a read expands
// once, a search adds per row.
func TestOverserveBudgetIsProportionalFlooredAndCapped(t *testing.T) {
	cfg := DefaultConfig()

	// Proportional in the band between the two ends.
	if got, want := cfg.OverserveBudget(4000, OverserveSearchFree), 16000; got != want {
		t.Errorf("budget for 4000 raw bytes = %d, want %d", got, want)
	}
	// Floored, and by the allowance the CALLER named -- a search's additions are per-row and
	// do not get a read's one-off expansion.
	if got := cfg.OverserveBudget(100, OverserveReadFree); got != OverserveReadFree {
		t.Errorf("small read budget = %d, want the read floor %d", got, OverserveReadFree)
	}
	if got := cfg.OverserveBudget(100, OverserveSearchFree); got != OverserveSearchFree {
		t.Errorf("small search budget = %d, want the search floor %d", got, OverserveSearchFree)
	}
	// Capped absolutely, however honest the raw answer was.
	if got := cfg.OverserveBudget(1<<20, OverserveReadFree); got != OverserveMaxBytes {
		t.Errorf("budget for a megabyte = %d, want the cap %d", got, OverserveMaxBytes)
	}
}

// 0 is off, and off has to be distinguishable from "a budget of zero bytes" -- which would
// refuse every answer instead of allowing every answer.
func TestOverserveCanBeSwitchedOff(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Terminal.MaxOverserve = new(int)

	if got := cfg.OverserveBudget(100, OverserveReadFree); got >= 0 {
		t.Errorf("max_overserve 0 budgeted %d bytes, want no ceiling", got)
	}
	if !cfg.WithinOverserve(string(make([]byte, 1<<20)), 1, OverserveReadFree) {
		t.Error("max_overserve 0 still refused a megabyte")
	}
}

// A surface that could not load a config must not become the most expensive one. An absent
// config is the DEFAULT ceiling, not the missing ceiling.
func TestAbsentConfigKeepsTheDefaultCeiling(t *testing.T) {
	var cfg *Config
	if got, want := cfg.OverserveBudget(4000, OverserveSearchFree), 4000*DefaultTerminalMaxOverserve; got != want {
		t.Errorf("nil config budgeted %d, want the default ceiling %d", got, want)
	}
	if cfg.WithinOverserve(string(make([]byte, 40000)), 100, OverserveSearchFree) {
		t.Error("nil config served 40KB for a 100-byte question")
	}
}
