package cli

import (
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
)

// GRD-01. Interception is decided before blocked_tools, and it returns as soon as ANY segment
// was rewritten -- so `grep … ; sed -i …` came back as a rewrite and the edit ran, under a
// config that denies edits. The ordering is right for the segment interception answers (a
// rewrite beats a denial for the same capability, TestInterceptionBeatsADenialForTheSameCommand)
// and wrong for every other segment of the same command line, which nothing then checked.
func TestGRD01_ABlockedCommandChainedAfterAGrepIsStillDenied(t *testing.T) {
	root, _ := scannedProject(t)

	for _, mode := range []string{helper.ModeCLI, helper.ModeMCP} {
		cfg := blockedIn(mode, "edit", "write", "read")

		// Control: alone, each is denied.
		if reason := denyReason(t, cfg, root, "sed -i 's/a/b/' app.go"); reason == "" {
			t.Fatalf("%s: a blocked edit was not denied on its own", mode)
		}

		for _, cmd := range []string{
			"grep -rn Serve . ; sed -i 's/a/b/' app.go",
			"grep -rn Serve . && sed -i 's/a/b/' app.go",
			"grep -rn Serve . ; cat app.go",
		} {
			if reason := denyReason(t, cfg, root, cmd); reason == "" {
				t.Errorf("%s: blocked_tools was bypassed by chaining after a grep: %q", mode, cmd)
			}
		}
	}
}

// The other half of the rule: a denial must not swallow a command aracne can serve. A grep is
// rewritten even when `grep` itself is blocked, because the rewrite IS the answer.
func TestGRD01_ARewrittenGrepIsStillNotDeniedForItsOwnKey(t *testing.T) {
	root, _ := scannedProject(t)

	if reason := denyReason(t, blockedIn(helper.ModeMCP, "grep"), root, "grep -rn Serve ."); reason != "" {
		t.Errorf("a grep aracne serves was denied instead of rewritten:\n%s", reason)
	}
	if reason := denyReason(t, blockedIn(helper.ModeInterceptLineRanges, "read", "grep"), root,
		"head -20 app.go"); reason != "" {
		t.Errorf("a read aracne serves was denied instead of rewritten:\n%s", reason)
	}
}
