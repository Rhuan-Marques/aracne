package scanner

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// progressReporter renders a single-line, in-place progress bar to stderr while
// a scan runs. It is phase-oriented: each ParallelParse call is a phase with a
// known total, and phases run sequentially (languages, and the Go scanner's
// parse/analyze passes, are serial), so at most one bar is ever active. It is
// disabled by default and enabled once per `arac scan` run via
// SetProgressEnabled — programmatic/read scans leave it off.
type progressReporter struct {
	mu      sync.Mutex
	enabled bool
	label   string
	total   int
	done    int
	last    time.Time
	onLine  bool // a bar is currently drawn and needs a trailing newline to finish
}

var progress progressReporter

// SetProgressEnabled turns the scan progress bar on or off. Call it once, before
// scanning, with the resolution of the `--progress` flag / scan.progress config.
func SetProgressEnabled(on bool) {
	progress.mu.Lock()
	progress.enabled = on
	progress.mu.Unlock()
}

// progressStartPhase begins a new bar for label covering total items.
func progressStartPhase(label string, total int) {
	progress.mu.Lock()
	defer progress.mu.Unlock()
	if !progress.enabled || total <= 0 {
		return
	}
	progress.label = label
	progress.total = total
	progress.done = 0
	progress.last = time.Time{}
	progress.render(true)
}

// progressStep advances the active bar by one completed item.
func progressStep() {
	progress.mu.Lock()
	defer progress.mu.Unlock()
	if !progress.enabled || progress.total <= 0 {
		return
	}
	progress.done++
	progress.render(false)
}

// progressEndPhase finishes the active bar (drawing it full) and drops to a new
// line so the completed phase stays visible and later output isn't clobbered.
func progressEndPhase() {
	progress.mu.Lock()
	defer progress.mu.Unlock()
	if !progress.enabled || progress.total <= 0 {
		return
	}
	progress.done = progress.total
	progress.render(true)
	if progress.onLine {
		fmt.Fprintln(os.Stderr)
		progress.onLine = false
	}
	progress.total = 0
}

// render draws the current bar in place. Updates are throttled to ~60ms unless
// force is set (phase start/end) to avoid flicker on fast scans. The caller must
// hold p.mu.
func (p *progressReporter) render(force bool) {
	if !force && time.Since(p.last) < 60*time.Millisecond {
		return
	}
	p.last = time.Now()

	const width = 24
	frac := 1.0
	if p.total > 0 {
		frac = float64(p.done) / float64(p.total)
	}
	if frac > 1 {
		frac = 1
	}
	filled := int(frac * width)
	if filled > width {
		filled = width
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
	fmt.Fprintf(os.Stderr, "\r  %-18s [%s] %d/%d (%3.0f%%)", p.label, bar, p.done, p.total, frac*100)
	p.onLine = true
}
