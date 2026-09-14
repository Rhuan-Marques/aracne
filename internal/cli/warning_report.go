package cli

import (
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
func driftWarningReport(dbPath string, warnings []domain.TopologyWarning) string {
	summary := formatDriftWarnings(warnings)
	if summary == "" {
		return ""
	}
	reads := warnread.Section(dbPath, NewScannerRegistry(), warnings, warnread.HookBudget)
	if reads == "" {
		return summary
	}
	return summary + "\n\n" + reads
}
