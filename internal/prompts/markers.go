package prompts

import "github.com/Rhuan-Marques/aracne/internal/helper"

// ModeMarkers is the sentence that must appear in exactly one mode's contract. Each is the
// capability that mode is FOR, so a marker in the wrong contract is a promise the project
// cannot keep.
//
// It lives here, next to the generators, because it is the one thing a phrasing pass has to
// keep in step with the prose. It was duplicated -- once in internal/cli, once in the e2e
// suite -- and a rewrite of the CLI contract updated one copy and not the other, so `arac
// setup` wrote the right file while a test insisted it had written the wrong one. Every suite
// that pins a mode to its contract reads THIS map.
var ModeMarkers = map[string]string{
	helper.ModeMCP:                 "arrive as MCP tools",
	helper.ModeCLI:                 "Reach for it the moment you want to know",
	helper.ModeInterceptID:         "## Resource IDs",
	helper.ModeInterceptLineRanges: "## Line ranges",
}
