package cli

import (
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// The rendered distinction between a break and a maybe.
//
// WHY THIS IS WORTH A TEST OF ITS OWN: an unverified report is only useful because it does
// not claim what a stored warning claims. Render the two identically and the distinction is
// spent -- the reader treats a maybe as a break, and then goes looking in `arac warnings list`
// for a row that was never written, which is a wasted turn every time.
func TestTransientWarningsAreTaggedInTheDriftReport(t *testing.T) {
	out := formatDriftWarnings([]domain.TopologyWarning{{
		ID: "a", Kind: domain.WarnSignatureChanged,
		SourceID: "pkg.Target", TargetID: "pkg.Broken",
		Message: domain.SignatureChangedMessage("Target", "pkg.Broken"),
	}, {
		ID: "b", Kind: domain.WarnSignatureChanged,
		SourceID: "pkg.Target", TargetID: "pkg.Unreadable",
		Message:   domain.SignatureUnverifiedMessage("Target", "pkg.Unreadable"),
		Transient: true,
	}})

	lines := strings.Split(out, "\n")
	var broken, unreadable string
	for _, l := range lines {
		switch {
		case strings.Contains(l, "pkg.Broken"):
			broken = l
		case strings.Contains(l, "pkg.Unreadable"):
			unreadable = l
		}
	}
	if broken == "" || unreadable == "" {
		t.Fatalf("both warnings must be reported, got:\n%s", out)
	}
	if strings.Contains(broken, "UNVERIFIED") {
		t.Errorf("a stored warning must not be tagged unverified: %s", broken)
	}
	if !strings.Contains(unreadable, "UNVERIFIED") {
		t.Errorf("a transient warning must be tagged: %s", unreadable)
	}
	// The reader has to be able to act on it without opening the source: the caller to look
	// at, and the type the argument now has to satisfy.
	for _, want := range []string{"pkg.Unreadable", "check if caller", "still supports it"} {
		if !strings.Contains(unreadable, want) {
			t.Errorf("transient line is missing %q: %s", want, unreadable)
		}
	}
	// The endpoints are in the sentence; repeating them in a parenthetical doubled the
	// length of a report printed after every edit.
	if strings.Contains(out, "(source:") {
		t.Errorf("the endpoint suffix is back:\n%s", out)
	}
	if !strings.HasPrefix(out, "Warnings found. Check these functions:") {
		t.Errorf("unexpected header:\n%s", out)
	}
}

// A report with nothing in it must stay empty -- an "everything is fine" banner printed after
// every edit is how a channel stops being read.
func TestNoWarningsRendersNothing(t *testing.T) {
	if out := formatDriftWarnings(nil); out != "" {
		t.Errorf("empty warning set rendered %q", out)
	}
}
