package scanner

import "github.com/Rhuan-Marques/aracne/internal/progress"

// scanProgress is the scan's progress bar: a single-line, in-place bar on stderr, phase
// oriented because each ParallelParse call is a phase with a known total and phases run
// sequentially (languages, and the Go scanner's parse/analyze passes, are serial), so at most
// one bar is ever active.
//
// It is disabled by default and enabled once per `arac scan` run via SetProgressEnabled --
// programmatic/read scans leave it off. The renderer itself lives in internal/progress, which
// `arac descriptions generate` draws its wave bar with too.
var scanProgress progress.Reporter

// SetProgressEnabled turns the scan progress bar on or off. Call it once, before scanning,
// with the resolution of the `--progress` flag / scan.progress config.
func SetProgressEnabled(on bool) { scanProgress.SetEnabled(on) }

// progressStartPhase begins a new bar for label covering total items.
func progressStartPhase(label string, total int) { scanProgress.StartPhase(label, total) }

// progressStep advances the active bar by one completed item.
func progressStep() { scanProgress.Step() }

// progressEndPhase finishes the active bar and drops to a new line so the completed phase
// stays visible and later output isn't clobbered.
func progressEndPhase() { scanProgress.EndPhase() }
