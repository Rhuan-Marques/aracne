package tests_test

import (
	"strings"
	"testing"
)

// The four modes, through the real binary.
//
// Everything else about the mode lives in unit tests that ask a Config what it thinks. This
// file asks the SHIPPED BINARY what it does, because the failure the modes exist to prevent is
// exactly the kind a unit test agrees with: a predicate that returns the right boolean while
// the surface it is supposed to govern goes on behaving the old way. Each test below runs the
// same command in every mode and asserts on the bytes that come back.

// modeProject is terminalProject in a chosen mode.
func modeProject(t *testing.T, mode string) string {
	t.Helper()
	root := terminalProject(t)
	setMode(t, root, mode)
	return root
}

const (
	modeMCP         = "mcp"
	modeAracneRead  = "cli"
	modeInterceptID = "intercept_id"
	modeLineRange   = "intercept_line_ranges"
)

var everyMode = []string{modeMCP, modeAracneRead, modeInterceptID, modeLineRange}

// A read is enriched in the two intercepting modes and passed through byte-for-byte in the
// other two. The passthrough half is the one worth testing hardest: it is what makes the guard
// safe to leave installed in a project that did not ask for interception.
func TestReadEnrichmentFollowsTheMode(t *testing.T) {
	for _, tc := range []struct {
		mode       string
		wantFramed bool
	}{
		{modeMCP, false},
		{modeAracneRead, false},
		{modeInterceptID, true},
		{modeLineRange, true},
	} {
		root := modeProject(t, tc.mode)
		got := aracCmd(t, root, "tail", "-4", "pkg/shapes.go")

		// The enclosing signature is the marker: a framed answer carries it, a passthrough
		// cannot, because `tail -4` never printed that line.
		framed := strings.Contains(got, "func Total(shapes []Shape) float64 {")
		if framed != tc.wantFramed {
			t.Errorf("mode %q: read was framed = %v, want %v\n%s", tc.mode, framed, tc.wantFramed, got)
		}

		if !tc.wantFramed {
			native, ok := nativeOut(t, root, "tail", "-4", "pkg/shapes.go")
			if !ok {
				t.Skip("tail is unavailable")
			}
			if got != native {
				t.Errorf("mode %q: passthrough is not byte-identical to the real command\n got: %q\nwant: %q",
					tc.mode, got, native)
			}
		}
	}
}

// Search is answered by the topology in every mode. It is the one capability with no competing
// surface: no read tool and no `arac read` answers "which declaration is DESCRIBED as X", so
// there is never a mode in which handing a search back to the real binary is the better answer.
func TestSearchIsAnsweredInEveryMode(t *testing.T) {
	for _, mode := range everyMode {
		root := modeProject(t, mode)
		got := aracCmd(t, root, "grep", "round", "pkg")

		// "round" appears only in Circle's doc comment, which a plain grep finds -- and the
		// topology annotates with the declaration it belongs to. The annotation is the proof
		// the search went through aracne rather than the real binary.
		if !strings.Contains(got, "#") {
			t.Errorf("mode %q: search returned no topology annotation\n%s", mode, got)
		}
	}
}

// The addressing vocabulary is the difference between the two intercepting modes, and it has to
// show up in the SEARCH HEADER, which is where the model actually reads it.
func TestSearchHeadersUseTheModesVocabulary(t *testing.T) {
	idRoot := modeProject(t, modeInterceptID)
	idOut := aracCmd(t, idRoot, "grep", "round", "pkg")
	if !strings.Contains(idOut, "demo/pkg.Circle") {
		t.Errorf("intercept_id: search header does not name the resource ID\n%s", idOut)
	}
	if strings.Contains(idOut, "shapes.go:13-") {
		t.Errorf("intercept_id: search header names a span instead of an ID\n%s", idOut)
	}

	lrRoot := modeProject(t, modeLineRange)
	lrOut := aracCmd(t, lrRoot, "grep", "round", "pkg")
	if !strings.Contains(lrOut, "pkg/shapes.go:") || !strings.Contains(lrOut, "-") {
		t.Errorf("intercept_line_ranges: search header does not name a span\n%s", lrOut)
	}
	if strings.Contains(lrOut, "demo/pkg.Circle —") {
		t.Errorf("intercept_line_ranges: search header still names a resource ID\n%s", lrOut)
	}
}

// A context entry carries the span in ModeInterceptLineRanges and only there. This is the regression the
// rework closes: ModeMCP used to print spans its own `read` tool could not accept, and
// ModeCLI would have printed them with nothing at all able to read one.
func TestContextEntriesCarrySpansOnlyInLineRangeMode(t *testing.T) {
	for _, tc := range []struct {
		mode      string
		wantSpans bool
	}{
		{modeMCP, false},
		{modeAracneRead, false},
		{modeInterceptID, false},
		{modeLineRange, true},
	} {
		root := modeProject(t, tc.mode)
		got := mustRun(t, root, "read", "demo/pkg.Describe")

		if !strings.Contains(got, "# CONTEXT:") {
			t.Fatalf("mode %q: `arac read` returned no context to inspect\n%s", tc.mode, got)
		}
		// Total is the neighbour Describe calls; its entry is where a span would appear.
		hasSpan := strings.Contains(got, "pkg/shapes.go:") && strings.Contains(got, "demo/pkg.Total (")
		if hasSpan != tc.wantSpans {
			t.Errorf("mode %q: context entries carry spans = %v, want %v\n%s",
				tc.mode, hasSpan, tc.wantSpans, got)
		}
	}
}

// `arac read` is the surface ModeCLI's contract points at, so it has to work there --
// and it stays available in every other mode, because it is the CLI a person uses too.
func TestAracReadWorksInEveryMode(t *testing.T) {
	for _, mode := range everyMode {
		root := modeProject(t, mode)
		got := mustRun(t, root, "read", "demo/pkg.Describe")
		if !strings.Contains(got, "func Describe(s Shape) string {") {
			t.Errorf("mode %q: `arac read` did not return the declaration\n%s", mode, got)
		}
	}
}

// A resource ID stands where a path does wherever aracne is answering the command. It is
// ADVERTISED only by ModeInterceptID's contract, but refusing to ACCEPT one in the other
// intercepting mode would refuse a question aracne can answer in favour of a `cat` that fails.
func TestResourceIDOperandsWorkInBothInterceptingModes(t *testing.T) {
	for _, mode := range []string{modeInterceptID, modeLineRange} {
		root := modeProject(t, mode)
		got := aracCmd(t, root, "head", "-3", "demo/pkg.Describe")
		if !strings.Contains(got, "func Describe(s Shape) string {") {
			t.Errorf("mode %q: a resource ID did not resolve as a read operand\n%s", mode, got)
		}
	}
}

// Only ModeMCP has MCP tools, and `arac serve` has to SAY so rather than start a server that
// lists nothing. An empty registry is the failure that looks like a working connection: the
// harness attaches, offers no tools, and nothing anywhere reports a problem.
func TestServeRefusesOutsideMCPMode(t *testing.T) {
	for _, mode := range []string{modeAracneRead, modeInterceptID, modeLineRange} {
		root := modeProject(t, mode)
		out, err := runLtp(t, root, "serve", "--tool-profile", "main")
		if err == nil {
			t.Errorf("mode %q: `arac serve` started a server with no tools", mode)
		}
		if !strings.Contains(out, "serves no MCP tools") {
			t.Errorf("mode %q: refusal does not explain itself:\n%s", mode, out)
		}
	}
}

// `arac init` has to rewrite the contract when the mode changes, and the contract it writes has
// to be the one for the mode the project is actually in.
func TestInitWritesTheContractForTheMode(t *testing.T) {
	for _, tc := range []struct {
		mode   string
		marker string
	}{
		{modeAracneRead, "Prefer it over reading whole files or line ranges"},
		{modeInterceptID, "## Resource IDs"},
		{modeLineRange, "## Line ranges"},
	} {
		root := modeProject(t, tc.mode)
		mustRun(t, root, "setup", "-y", "--claude")
		body := readFile(t, root, "CLAUDE.md")
		if !strings.Contains(body, tc.marker) {
			t.Errorf("mode %q: CLAUDE.md is not that mode's contract (want %q)\n%s",
				tc.mode, tc.marker, body)
		}
	}
}
