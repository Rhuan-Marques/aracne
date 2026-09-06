package progress

import (
	"bytes"
	"strings"
	"testing"
)

// A zero Reporter is the one every package-level bar starts as, and the one a programmatic
// caller never turns on. It has to be silent, or a read-path scan writes carriage returns into
// somebody's output.
func TestZeroReporterDrawsNothing(t *testing.T) {
	var buf bytes.Buffer
	var p Reporter
	p.SetOutput(&buf)

	p.StartPhase("parsing go", 3)
	p.Step()
	p.EndPhase()

	if buf.Len() != 0 {
		t.Fatalf("disabled reporter drew %q", buf.String())
	}
}

// A nil *Reporter is the "nothing to draw" caller. Every method has to survive it, because the
// alternative is every call site guarding the bar it does not have.
func TestNilReporterIsInert(t *testing.T) {
	var p *Reporter
	p.SetEnabled(true)
	p.SetOutput(&bytes.Buffer{})
	p.StartPhase("describing", 2)
	p.Step()
	p.Add(5)
	p.EndPhase()
}

func TestEnabledReporterDrawsAndFinishesTheLine(t *testing.T) {
	var buf bytes.Buffer
	var p Reporter
	p.SetOutput(&buf)
	p.SetEnabled(true)

	p.StartPhase("describing", 4)
	p.EndPhase()

	out := buf.String()
	if !strings.Contains(out, "describing") {
		t.Fatalf("bar is missing its label: %q", out)
	}
	if !strings.Contains(out, "0/4") {
		t.Fatalf("bar did not open empty: %q", out)
	}
	// EndPhase draws the bar full whatever it was told, so a phase that lost track of a few
	// items does not leave a bar frozen at 80% above the summary that follows it.
	if !strings.Contains(out, "4/4 (100%)") {
		t.Fatalf("bar did not close full: %q", out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Fatalf("bar did not drop to a new line: %q", out)
	}
	if !strings.HasPrefix(out, "\r") {
		t.Fatalf("bar did not draw in place: %q", out)
	}
}

// Add is the batch-shaped stepper: one result covering several items moves the bar by all of
// them, and reporting more than the announced total pins at full rather than printing 7/5.
func TestAddCountsGroupsAndClampsAtTheTotal(t *testing.T) {
	var buf bytes.Buffer
	var p Reporter
	p.SetOutput(&buf)
	p.SetEnabled(true)

	p.StartPhase("describing", 5)
	p.Add(3)
	if got := p.Done(); got != 3 {
		t.Fatalf("done = %d after Add(3), want 3", got)
	}
	p.Add(9)
	if got := p.Done(); got != 5 {
		t.Fatalf("done = %d after overshooting, want it clamped to 5", got)
	}
	p.EndPhase()
	if strings.Contains(buf.String(), "14/5") {
		t.Fatalf("bar printed an impossible count: %q", buf.String())
	}
}

// A phase with no work draws nothing: there is no progress to report, and the steps that
// follow have nothing to advance.
func TestEmptyPhaseDrawsNothing(t *testing.T) {
	var buf bytes.Buffer
	var p Reporter
	p.SetOutput(&buf)
	p.SetEnabled(true)

	p.StartPhase("describing", 0)
	p.Step()
	p.EndPhase()

	if buf.Len() != 0 {
		t.Fatalf("empty phase drew %q", buf.String())
	}
}
