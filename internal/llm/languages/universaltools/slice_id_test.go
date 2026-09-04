package universaltools

import (
	"strings"
	"testing"
)

// The elision marker names a declaration by the last segment of its id, and ModeInterceptID
// depends on that name: it is the token the model feeds back as a command operand.
//
// Go and Python put the receiver in the MIDDLE of the id and Java puts a signature at the END,
// so a rule tuned for one has to be checked against the other. Cutting at the first '(' used to
// leave a Go method's marker reading `⋯ +1 lines of  ⋯` with no name at all.
func TestLastIDSegment(t *testing.T) {
	for _, tc := range []struct{ id, want string }{
		{"github.com/Rhuan-Marques/aracne/testing_ground/go/shapes.(Triangle).Perimeter", "Perimeter"},
		{"github.com/Rhuan-Marques/aracne/internal/cli.RunGuard", "RunGuard"},
		{"src/flask/app.Flask.register_blueprint", "register_blueprint"},
		{"crate::args::Args::parse", "parse"},
		{"com.example.Foo.bar(int,int)", "bar"},
		{"com.example.Foo#baz", "baz"},
		{"Plain", "Plain"},
	} {
		if got := lastIDSegment(tc.id); got != tc.want {
			t.Errorf("lastIDSegment(%q) = %q, want %q", tc.id, got, tc.want)
		}
	}
}

// The defect this pins was not in lastIDSegment's return value but in what the reader SAW:
// `⋯ +1 lines of  ⋯`, a marker with no name in it. ModeInterceptID hands that name back to the
// model as a command operand, so an empty one is a dead end rather than a cosmetic slip -- and
// a rule that is right for Java ids can be wrong for Go ones without any test noticing, which
// is how it shipped.
func TestMarkerAlwaysNamesTheDeclaration(t *testing.T) {
	ids := []string{
		"github.com/Rhuan-Marques/aracne/testing_ground/go/shapes.(Triangle).Perimeter",
		"github.com/Rhuan-Marques/aracne/internal/cli.RunGuard",
		"src/flask/app.Flask.register_blueprint",
		"crate::args::Args::parse",
		"com.example.Foo.bar(int,int)",
	}

	// id mode: the marker names the declaration, so the name must never be empty.
	for _, id := range ids {
		got := marker(id, 3, nil)
		if strings.Contains(got, "lines of  ") || strings.HasSuffix(strings.TrimSpace(got), "of ⋯") {
			t.Errorf("marker(%q) has no name in it: %q", id, got)
		}
		if seg := lastIDSegment(id); seg == "" || !strings.Contains(got, seg) {
			t.Errorf("marker(%q) = %q, want it to name %q", id, got, seg)
		}
	}

	// intercept_line_ranges mode: the marker names the span instead, and falls back to the id form when
	// the locator cannot resolve one -- so the name still has to be there.
	span := func(string) string { return "pkg/shapes.go:120-123" }
	if got := marker(ids[0], 3, span); !strings.Contains(got, "pkg/shapes.go:120-123") {
		t.Errorf("marker with a locator did not name the span: %q", got)
	}
	unresolvable := func(string) string { return "" }
	if got := marker(ids[0], 3, unresolvable); !strings.Contains(got, "Perimeter") {
		t.Errorf("marker fell back to a nameless id form: %q", got)
	}
}
