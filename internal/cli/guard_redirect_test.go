package cli

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/toolspec"
)

// A redirect of a descriptor OTHER than stdout does not make a command a writer. `2>` was
// counted as one, so `sed -n '1,5p' f 2>/dev/null` classified as an edit -- refused as one under
// blocked_tools ["edit"], with advice to use `arac edit` -- and was never intercepted.
func TestStderrRedirectIsNotAnOutputRedirect(t *testing.T) {
	for _, tc := range []struct {
		command string
		want    []string
	}{
		{"sed -n 1,5p f.go 2>/dev/null", []string{"read"}},
		{"sed -n 1,5p f.go 2> /dev/null", []string{"read"}},
		{"sed -n 1,5p f.go 2>>err.log", []string{"read"}},
		{"awk 'NR<=5' f.go 2>/dev/null", []string{"read"}},
		{"sed -n 1,5p f.go 2>&1", []string{"read"}},
		// stdout, however it is spelled, still writes.
		{"sed -n 1,5p f.go > out.go", []string{"edit"}},
		{"sed -n 1,5p f.go 1>out.go", []string{"edit"}},
		{"sed -n 1,5p f.go >>out.go", []string{"edit"}},
		{"sed -n 1,5p f.go &>out.log", []string{"edit"}},
		{"echo a2>f.go; sed -n 1p x", []string{"read"}}, // `a2>` is stdout's: the word is a2
	} {
		if got := commandKeys(tc.command, true); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("commandKeys(%q) = %v, want %v", tc.command, got, tc.want)
		}
	}
	if !splitCommandSegments("echo a2>f.go")[0].redirectsOut {
		t.Error("`echo a2>f.go` redirects stdout: the 2 belongs to the word")
	}
}

// The argv the guard parses is the argv the shell would produce, redirections removed.
func TestSegmentArgvDropsRedirections(t *testing.T) {
	for command, want := range map[string][]string{
		"sed -n '1,3p' app.go 2>/dev/null": {"sed", "-n", "1,3p", "app.go"},
		"head -3 app.go 2> /dev/null":      {"head", "-3", "app.go"},
		"cat app.go 2>&1":                  {"cat", "app.go"},
		"cat < app.go":                     {"cat"},
		"cat app.go>out":                   {"cat", "app.go"},
		"grep '2>x' app.go":                {"grep", "2>x", "app.go"}, // quoted: a pattern
	} {
		if got := segmentArgv(splitCommandSegments(command)[0]); !reflect.DeepEqual(got, want) {
			t.Errorf("segmentArgv(%q) = %q, want %q", command, got, want)
		}
	}
}

// And so a read or a search carrying `2>/dev/null` is intercepted like one that does not.
func TestStderrRedirectDoesNotBlockInterception(t *testing.T) {
	root, dbPath := scannedProject(t)
	app := filepath.Join(root, "app.go")
	cfg := helper.DefaultConfig()
	cfg.Mode = helper.ModeInterceptLineRanges
	for _, command := range []string{
		"sed -n '1,3p' " + app + " 2>/dev/null",
		"cat " + app + " 2>/dev/null",
		"grep -rn Serve " + root + " 2>/dev/null",
	} {
		got, ok := interceptCommand(command, dbPath, cfg)
		if !ok || !strings.Contains(got, "cmd -- ") || !strings.HasSuffix(got, "2>/dev/null") {
			t.Errorf("%q: want a rewrite keeping its redirect, got %q (ok=%v)", command, got, ok)
		}
	}
	if _, ok := interceptCommand("cat "+app+" > /tmp/copy.go", dbPath, cfg); ok {
		t.Error("a stdout redirect must still refuse interception")
	}
}

// The user-visible half of the defect: under blocked_tools ["edit"], a read carrying a stderr
// redirect was DENIED with advice to use `arac edit`. A stdout redirect still is.
func TestBlockedEditDoesNotRefuseAStderrRedirectedRead(t *testing.T) {
	root, dbPath := scannedProject(t)
	app := filepath.Join(root, "app.go")
	blocked := map[string]bool{"edit": true, "write": true}
	bash := func(c string) map[string]interface{} { return map[string]interface{}{"command": c} }

	if d := decideGuard("Bash", bash("sed -n '1,3p' "+app+" 2>/dev/null"), blocked, true, dbPath,
		toolspec.SurfaceAracneRead); d.Deny {
		t.Errorf("a read with 2>/dev/null was refused as an edit: %s", d.Message)
	}
	if d := decideGuard("Bash", bash("sed -n '1,3p' "+app+" > "+filepath.Join(root, "out.go")), blocked, true, dbPath,
		toolspec.SurfaceAracneRead); !d.Deny {
		t.Error("writing a stream editor's output to a file is still an edit and must be refused")
	}
}
