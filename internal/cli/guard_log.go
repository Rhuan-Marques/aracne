package cli

import (
	"encoding/json"
	"os"
	"strings"
	"time"
)

// GuardLogEnv names a file the guard appends one line to per decision it makes.
//
// WHY THIS HAS TO COME FROM THE GUARD. A PreToolUse hook rewrites a command through
// `hookSpecificOutput.updatedInput`, and the harness transcript records the command the
// MODEL wrote -- verified by running a session and reading the events: the model typed
// `grep -rn X .`, aracne answered it, and the transcript shows `grep -rn X .` with aracne's
// output underneath. So nothing downstream can tell an intercepted command from one that
// ran for real by looking at what was typed, and a benchmark counting `arac cmd` in the
// transcript necessarily reports zero interceptions forever. It did.
//
// Opt-in through the environment, so a normal session pays nothing: the guard runs on the
// agent's critical path before every tool call, and an unconditional append would put a
// file write there for a number only a benchmark reads.
const GuardLogEnv = "ARACNE_GUARD_LOG"

// guardDecisionKind is what the guard did with one tool call.
type guardDecisionKind string

const (
	guardRewrote     guardDecisionKind = "rewrite"     // answered from the topology in place
	guardDenied      guardDecisionKind = "deny"        // refused, with a pointer to the surface
	guardPassedT     guardDecisionKind = "passthrough" // seen, and left to run for real
	guardNudged      guardDecisionKind = "nudge"       // PostToolUse advice
	guardDriftedScan guardDecisionKind = "drift"       // PostToolUse drift report
)

// logGuardDecision appends one event, best-effort. Never fails a tool call: this is
// measurement, and a hook that errors because a log is unwritable would break the session
// it was meant to observe.
func logGuardDecision(kind guardDecisionKind, tool, command string) {
	path := strings.TrimSpace(os.Getenv(GuardLogEnv))
	if path == "" {
		return
	}
	rec := struct {
		T        string `json:"t"`
		Tool     string `json:"tool"`
		Decision string `json:"decision"`
		Command  string `json:"command,omitempty"`
	}{
		T:        time.Now().UTC().Format(time.RFC3339Nano),
		Tool:     tool,
		Decision: string(kind),
		Command:  command,
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	// One write of one short line under O_APPEND: hooks for concurrent tool calls are
	// separate processes, and this is the only shape that keeps their lines from interleaving.
	_, _ = f.Write(append(line, '\n'))
}
