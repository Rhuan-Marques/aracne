package pyscanner

import (
	"runtime"
	"testing"
	"time"
)

// TestParseTimeoutIsUnchangedOffWindows pins the half of the Windows timeout fix that is
// meant to be invisible everywhere else. The bound was raised because a loaded Windows
// runner spends longer STARTING an interpreter than Linux or macOS spend parsing with one;
// if that raise ever leaks onto the other two it would mean a hung parse there is held for
// minutes instead of seconds, which is a regression and not a fix.
func TestParseTimeoutIsUnchangedOffWindows(t *testing.T) {
	t.Setenv("ARACNE_PYTHON_PARSE_TIMEOUT", "")
	got := pythonParseTimeout()
	want := 30 * time.Second
	if runtime.GOOS == "windows" {
		want = 3 * time.Minute
	}
	if got != want {
		t.Errorf("parse timeout on %s = %s, want %s", runtime.GOOS, got, want)
	}
}

// TestParseTimeoutOverride: the override is the escape hatch for a machine no default
// anticipated -- including a Linux one, which is why it is not compiled out off Windows.
func TestParseTimeoutOverride(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  string
		want time.Duration
	}{
		{"a duration is taken", "90s", 90 * time.Second},
		{"whitespace is trimmed", "  2m  ", 2 * time.Minute},
		// A bad or non-positive value falls back rather than disabling the bound: an
		// unparseable env var must not turn a guard against a hang into no guard at all.
		{"garbage falls back", "soon", 0},
		{"zero falls back", "0s", 0},
		{"negative falls back", "-5s", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ARACNE_PYTHON_PARSE_TIMEOUT", tc.env)
			want := tc.want
			if want == 0 {
				want = 30 * time.Second
				if runtime.GOOS == "windows" {
					want = 3 * time.Minute
				}
			}
			if got := pythonParseTimeout(); got != want {
				t.Errorf("ARACNE_PYTHON_PARSE_TIMEOUT=%q gave %s, want %s", tc.env, got, want)
			}
		})
	}
}
