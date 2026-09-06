// Package progress draws a single-line, in-place progress bar for a long run.
//
// It is its own package because two unrelated runs want the same bar. `arac scan` parses a
// known number of files per phase; `arac descriptions generate` describes a known number of
// resources per wave. Both are phase-shaped -- a total is known before the work starts, items
// complete one at a time, and at most one bar is ever live -- so both drive one Reporter
// rather than growing a second renderer that drifts from the first.
package progress

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// barWidth is the drawn width of the bar itself, in cells, excluding the label and the
// counter around it.
const barWidth = 24

// renderInterval throttles redraws. A wave of fast completions would otherwise repaint the
// same cells hundreds of times a second, which reads as flicker rather than as progress.
// Phase start and end force a draw through it, so the bar is always correct when it matters.
const renderInterval = 60 * time.Millisecond

// Reporter is one bar.
//
// The zero value is a DISABLED reporter, and every method on it is a no-op until
// SetEnabled(true). That default is what keeps escape codes out of output nobody is watching:
// a programmatic scan on the read path, a test, a piped run. A nil *Reporter is equally inert,
// so a caller with nothing to draw can pass nil instead of building one.
type Reporter struct {
	mu      sync.Mutex
	out     io.Writer // nil means os.Stderr; set by SetOutput, which only tests call
	enabled bool
	label   string
	total   int
	done    int
	last    time.Time
	onLine  bool // a bar is drawn and needs a trailing newline to finish
}

// SetEnabled turns the bar on or off. Call it once, before the work starts, with the
// resolution of whatever `--progress` flag and config the command exposes.
func (p *Reporter) SetEnabled(on bool) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.enabled = on
	p.mu.Unlock()
}

// SetOutput redirects the bar away from stderr. It exists for tests, which need to read what
// was drawn; production callers leave it alone and get stderr, so the bar stays out of the
// stdout a command's real output goes to.
func (p *Reporter) SetOutput(w io.Writer) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.out = w
	p.mu.Unlock()
}

// StartPhase begins a new bar for label covering total items. A total of zero or less starts
// nothing: a phase with no work has no progress to report, and a bar stuck at 0/0 is worse
// than no bar.
func (p *Reporter) StartPhase(label string, total int) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.enabled || total <= 0 {
		return
	}
	p.label = label
	p.total = total
	p.done = 0
	p.last = time.Time{}
	p.render(true)
}

// Step advances the active bar by one completed item.
func (p *Reporter) Step() { p.Add(1) }

// Add advances the active bar by n completed items, for work that finishes in groups -- a
// batch of descriptions comes back as one result covering several resources, and reporting it
// as one step would leave the bar reading a fraction of the truth.
func (p *Reporter) Add(n int) {
	if p == nil || n <= 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.enabled || p.total <= 0 {
		return
	}
	p.done += n
	// A caller that reports more items than it announced is reporting a total it got wrong,
	// and "7/5 (100%)" advertises the bug in the counter rather than in the caller. The bar
	// pins at full instead; the run's own summary is where an accounting mismatch belongs.
	if p.done > p.total {
		p.done = p.total
	}
	p.render(false)
}

// Done reports how many items the active phase has completed. It is the number the bar is
// drawing, so a caller that wants to say the same thing in its own words -- a test, a summary
// line -- reads it here instead of keeping a second count that can disagree with the bar.
func (p *Reporter) Done() int {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.done
}

// EndPhase finishes the active bar (drawing it full) and drops to a new line, so the completed
// phase stays visible and whatever the command prints next is not written over it.
func (p *Reporter) EndPhase() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.enabled || p.total <= 0 {
		return
	}
	p.done = p.total
	p.render(true)
	if p.onLine {
		fmt.Fprintln(p.writer())
		p.onLine = false
	}
	p.total = 0
}

// render draws the current bar in place. The caller must hold p.mu.
func (p *Reporter) render(force bool) {
	if !force && time.Since(p.last) < renderInterval {
		return
	}
	p.last = time.Now()

	frac := 1.0
	if p.total > 0 {
		frac = float64(p.done) / float64(p.total)
	}
	frac = min(frac, 1)
	filled := min(int(frac*barWidth), barWidth)
	bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)
	fmt.Fprintf(p.writer(), "\r  %-18s [%s] %d/%d (%3.0f%%)", p.label, bar, p.done, p.total, frac*100)
	p.onLine = true
}

// writer is where the bar is drawn. The caller must hold p.mu.
func (p *Reporter) writer() io.Writer {
	if p.out != nil {
		return p.out
	}
	return os.Stderr
}
