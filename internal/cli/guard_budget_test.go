package cli

import (
	"testing"
	"time"
)

// THE HOOK'S TIMEOUT HAS TO COVER THE GUARD'S OWN WORST CASE, or the harness kills the hook
// mid-scan -- and on the PostToolUse side that discards the warning report, the channel the
// contract says has no substitute.
//
// It was hard-coded at 30 while driftCheck ran two independently-bounded stages back to back
// for a worst case of 40, and the PreToolUse path reached ~31 (a 20s pre-tool scan, then
// trackedFiles at 3, the untracked check at 3 and the read proxy at 5). driftCheck now shares
// one deadline; this pins the arithmetic so neither number can move without the other.
func TestGuardHookTimeoutCoversTheGuardsOwnBudgets(t *testing.T) {
	hook := time.Duration(GuardHookTimeoutSeconds) * time.Second

	// PostToolUse: one shared budget across the staleness probe and the scan it gates.
	if driftCheckBudget > hook {
		t.Errorf("the post-tool drift check may spend %s inside a hook allowed %s",
			driftCheckBudget, hook)
	}
	// PreToolUse: the freshness scan, then the checks interception and a denial make.
	preTool := guardScanTimeout + untrackedCheckTimeout + untrackedCheckTimeout + proxyReadTimeout
	if preTool > hook {
		t.Errorf("the pre-tool path may spend %s inside a hook allowed %s", preTool, hook)
	}
}
