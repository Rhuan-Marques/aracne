package cli

import (
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/llm/warnread"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// driftWarningReport is the text a warning report comes back as: the summary formatDriftWarnings
// has always rendered, plus -- when features.warning_reads is on -- the full read of the code
// those warnings name.
//
// One funnel, because there are four entrances to the same report (`arac edit`, `arac write`,
// `arac update-file`, and the guard's post-tool drift check) and a warning has to read
// identically whichever produced the change. The expansion is best-effort: everything below
// returns "" rather than failing, so a project that turns the feature on never gets a WORSE
// report than one that leaves it off.
//
// reserve is the bytes the caller prints in the same message before the report -- see
// warnread.Options.Reserve. It is spent on the fallback summary as well as the expansion: the
// budget exists because Claude Code cuts a long hook message to a preview, and a plain list of
// eighty warnings is exactly as long as it looks.
func driftWarningReport(dbPath string, warnings []domain.TopologyWarning, reserve int) string {
	summary := formatDriftWarnings(warnings)
	if summary == "" {
		return ""
	}
	// THE EXPANSION REPLACES THE SUMMARY, it no longer sits under it. The summary's whole
	// content -- which warning, on which two ids -- is now written on the offending line
	// itself, and printing both would say everything twice in the one report that is emitted
	// on the agent's critical path. The summary is the FALLBACK: the feature off, the hook
	// budget spent, or nothing readable, and the reader still gets what it always got.
	reads, shown := warnread.SectionPage(dbPath, NewScannerRegistry(), warnings, warnread.Options{
		Budget:   warnread.HookBudget,
		Headline: warnread.HeadlinePush,
		Reserve:  reserve,
	})
	if reads != "" {
		requeueUnshown(dbPath, warnings, shown)
		return reads
	}
	cfg := helper.LoadConfig(helper.ConfigPath(dbPath))
	if !cfg.WarningReadsEnabled() {
		return summary
	}
	fitted, shown := fitSummary(cfg, warnings, warnread.Budgeted(cfg, reserve))
	requeueUnshown(dbPath, warnings, shown)
	return fitted
}

// requeueUnshown puts back every TRANSIENT the page left out.
//
// A transient exists only in the pending queue, and the report drained it to show it (see
// unreportedWarnings). A stored warning the budget left off the page is still in the table, where
// `arac warnings list --read` finds it; a transient left off was about to exist nowhere, while
// the note under the page counted it as "left". Back in the queue it is on the next page of
// `--read`, or arrives with the next post-edit report, whichever comes first -- and it is still
// reported once, because both of those drain exactly what they show.
func requeueUnshown(dbPath string, all, shown []domain.TopologyWarning) {
	seen := make(map[string]bool, len(shown))
	for _, w := range shown {
		seen[w.ID] = true
	}
	var back []domain.TopologyWarning
	for _, w := range all {
		if w.Transient && !seen[w.ID] {
			back = append(back, w)
		}
	}
	helper.RequeueTransients(dbPath, back)
}

// fitSummary is the fallback listing cut to the byte budget: the longest prefix of the sorted
// warnings whose lines, plus the note naming how many were left out, fit in limit (0 means no
// limit). A listing is linear -- each warning is one line -- so unlike the expansion it needs
// no search: lines are added until the next one, with the note it would need, would not fit.
//
// THE HEADER AND THE NOTE ARE ALWAYS KEPT, even when a budget that small admits no line at all.
// A report that says "N warnings, see the list" is still a true and actionable report; an
// empty one says the edit broke nothing.
//
// It also returns the warnings the listing shows, for requeueUnshown. formatDriftWarnings writes
// exactly one line per warning, in the order given, which is what makes row i warning i.
func fitSummary(cfg *helper.Config, warnings []domain.TopologyWarning, limit int) (string, []domain.TopologyWarning) {
	ordered := append([]domain.TopologyWarning(nil), warnings...)
	domain.SortWarnings(ordered)
	full := formatDriftWarnings(ordered)
	if limit == 0 || len(full) <= limit {
		return full, ordered
	}
	lines := strings.Split(full, "\n")
	header, rows := lines[0], lines[1:]
	kept := header
	for i, row := range rows {
		next := kept + "\n" + row
		if len(next)+len("\n")+len(warnread.MoreNote(cfg, len(rows)-i-1)) > limit {
			return kept + "\n" + warnread.MoreNote(cfg, len(rows)-i), ordered[:i]
		}
		kept = next
	}
	return kept, ordered
}
