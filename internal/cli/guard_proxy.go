package cli

import (
	"os"
	"strings"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/llm/languages/universaltools"
	"github.com/Rhuan-Marques/aracne/internal/shellcmd"
	"github.com/Rhuan-Marques/aracne/internal/topology"
)

const (
	// proxyReadTimeout bounds the work a denial is allowed to do. A denial that hangs is far
	// worse than a terse one: the agent is stalled behind the hook with nothing to show for it.
	proxyReadTimeout = 5 * time.Second
)

// proxyRead answers a denied file read with the content it asked for, instead of a pointer to
// the tool that would have supplied it.
//
// WHY. In the compact-blocked-20260830c run, 24 of 27 agents opened identically: shell, shell
// DENIED, a call spent looking up the aracne tool's schema, and only then the read. Two calls
// per run bought nothing -- the model already knew what it wanted, and the guard already knew
// which file. Denials plus tool lookups were 20% of every tool call in the run.
//
// So the denial does the read. Same single call the `cat` would have been, and the model gets
// the file WITH its context block, which is the thing the topology was there to add.
//
// WHAT THE FOLLOW-UP RUN ADDED. The first version threw away the LINE WINDOW and read the
// whole file, which over-served by 5.8x on average. It now reads the resources spanning the
// window (see universaltools.ReadWindow) and, either way, refuses to answer at all when the
// answer is disproportionate to the request -- see proxyBudget.
//
// Every guard rail here exists because this runs inside a PreToolUse hook, on the agent's
// critical path, on every denied read:
//   - reads only (never edit/write: those must go through the tools that re-sync the topology),
//   - exactly one path, so a glob or a pipeline falls back rather than guessing,
//   - the path is resolved against the PROJECT ROOT, never the hook's own cwd (see below),
//   - the file must exist and be tracked, so this never invents an answer,
//   - a size budget and a hard timeout, either of which returns "" and restores the pointer.
//
// THE PATH IS NOT RESOLVED WHERE THIS PROCESS IS STANDING. A hook runs in the session
// directory; the agent's Bash shell has a working directory of its own that persists across
// calls and that this process cannot see -- which is the entire reason guardDBPath exists. This
// used to os.Stat the operand as typed and hand the same relative string to ReadIDs, whose
// candidate list tries the process cwd first. With the agent in a subdirectory, `cat util.go`
// was answered with the contents of a DIFFERENT util.go at the repository root, under a message
// saying "Reading it for you" and naming no path. Every other resolver in the guard already
// goes through existingReadFiles, and now so does this one.
//
// It returns "" whenever it cannot be certain, and "" means the caller keeps today's behaviour.
func proxyRead(command, dbPath string) string {
	target := soleReadTarget(command, projectRoot(dbPath))
	if target.path == "" {
		return ""
	}
	info, err := os.Stat(target.path)
	if err != nil || info.IsDir() {
		return ""
	}
	done := make(chan string, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- ""
			}
		}()
		if _, err := os.Stat(dbPath); err != nil {
			done <- ""
			return
		}
		mgr := topology.New()
		if err := mgr.Load(dbPath); err != nil {
			done <- ""
			return
		}
		cfg := helper.LoadConfig(helper.ConfigPath(dbPath))
		budget := proxyBudget(cfg, target, info.Size())
		// NO LAZY FILL ON THIS PATH. NewRead attaches a descriptions filler, which is awaited
		// inline for up to descriptions.lazy.timeout_seconds (45s by default) -- far longer than
		// proxyReadTimeout, so on a repository whose descriptions are not yet written (a fresh
		// install, exactly when the filler is doing the most work) the proxy reliably gave up
		// and the model got a bare pointer instead of the file: the two-turns-for-one-question
		// failure this function exists to end, arriving intermittently. A denial is not the
		// place to pay for description generation; the next ordinary read still fills them.
		//
		// The registry is passed so a file changed since it was indexed is re-parsed before
		// the answer is cut from it, rather than answered from spans that no longer fit.
		rd := universaltools.NewRead(mgr, cfg, false, NewScannerRegistry()).WithFiller(nil)
		// read.kinds gates this surface like every other. The proxy is an ADDITION to a
		// denial message, so a kind the project does not allow simply yields no proxy answer
		// -- the denial still goes out, just without an aracne read attached to it.
		opt := universaltools.ReadIDsOptions{}

		var out string
		// A window is a narrower question than the file, so ask it first. An empty result
		// means nothing is declared across those lines -- imports, a data table -- and the
		// whole-file read is then the only honest answer.
		if target.hasWindow {
			if w, werr := rd.ReadWindow(target.path, target.from, target.to, opt); werr == nil {
				out = w
			}
		}
		if strings.TrimSpace(out) == "" {
			var rerr error
			out, rerr = rd.ReadIDs([]string{target.path}, opt)
			if rerr != nil {
				done <- ""
				return
			}
		}
		if strings.TrimSpace(out) == "" || (budget >= 0 && len(out) > budget) {
			done <- ""
			return
		}
		done <- out
	}()

	select {
	case out := <-done:
		return out
	case <-time.After(proxyReadTimeout):
		return ""
	}
}

// proxyBudget is the largest answer worth sending for this request. A negative return means
// the project has switched the ceiling off.
//
// Without it the proxy answers every question with the same thing -- the whole file -- and a
// 22-line `awk` window came back at 20,763 bytes. terminal.max_overserve keeps it proportional
// so a big honest read still goes through; helper.OverserveBudget floors and caps it.
func proxyBudget(cfg *helper.Config, t readTarget, fileSize int64) int {
	want := int(fileSize)
	if t.hasWindow && t.totalLines > 0 {
		lines := t.to - t.from + 1
		if lines < 1 {
			lines = 1
		}
		if lines > t.totalLines {
			lines = t.totalLines
		}
		want = int(fileSize) * lines / t.totalLines
	}
	return cfg.OverserveBudget(want, helper.OverserveReadFree)
}

// readTarget is the single file a denied command reads, plus the line window it asked for.
type readTarget struct {
	path       string
	hasWindow  bool
	from, to   int
	totalLines int // of the file on disk; 0 when it could not be counted
}

// soleReadTarget returns the single file a shell command reads and the window it wants, or a
// zero value when the command is anything more complicated than that. The operand is resolved
// against root -- the project the topology indexes -- and the returned path is absolute.
//
// Deliberately strict. A pipeline, a glob, two operands or an unrecognized reader all return
// nothing and cost the model the ordinary one-line denial it would have had anyway. The only
// case worth answering is the one with an unambiguous answer.
//
// The window comes from shellcmd -- the same parser `arac cmd` and the interception hook use.
// It used to come from a second, thinner set of regexes kept here, and that copy knew `sed`,
// `awk` and `head` but not `tail`: a denied `tail -n 20 f` therefore reported no window at
// all, proxyBudget measured against the whole file, and the proxy answered a twenty-line
// request with the file. That is the 5.8x over-serve proxyBudget exists to prevent, still
// live for one reader in four. One parser cannot have three of the four cases right.
func soleReadTarget(command, root string) readTarget {
	if root == "" {
		return readTarget{}
	}
	segments := splitCommandSegments(command)
	if len(segments) != 1 {
		return readTarget{}
	}
	argv := segmentArgv(segments[0])
	if len(argv) == 0 {
		return readTarget{}
	}
	req := shellcmd.Parse(argv)
	if req.Kind != shellcmd.KindRead || len(req.Operands) != 1 {
		return readTarget{}
	}
	operand := strings.Trim(req.Operands[0], `'"`)
	// The guard sees the command BEFORE the shell expands it, which shellcmd's callers on
	// the other side of the hook do not: `arac cmd` is handed argv a real shell already
	// split and globbed. So an unexpanded glob or a variable reaches here as one operand
	// standing for an unknown number of files, and there is no single answer to proxy.
	//
	// `~` ONLY WHERE THE SHELL EXPANDS IT, which is the first character. Anywhere else it is
	// an ordinary filename character, and on Windows an ordinary one in every path under a
	// shortened directory: `C:\Users\RUNNER~1\...` is what an 8.3 name looks like, and
	// `PROGRA~1` is the other one everybody has. Treating those as unexpanded globs refused
	// to proxy any read under them -- every windowed read on such a machine fell through to
	// the real command, silently.
	if strings.ContainsAny(operand, "*?[]$`") || strings.HasPrefix(operand, "~") {
		return readTarget{}
	}
	// The same resolver every other guard path uses: absolute as it stands, relative against
	// the project root, regular files only. An operand it cannot place is not proxied at all --
	// a bare pointer is a poor answer, and the wrong file's contents is a worse one.
	resolved := existingReadFiles([]string{operand}, root)
	if len(resolved) != 1 {
		return readTarget{}
	}
	t := readTarget{path: resolved[0]}
	if req.Window.Mode == shellcmd.WholeFile {
		return t
	}
	total := countFileLines(t.path)
	if total == 0 {
		return t
	}
	from, to, ok := resolveWindow(req.Window, 1, total)
	if !ok {
		return t
	}
	t.from, t.to, t.hasWindow, t.totalLines = from, to, true, total
	return t
}

// proxyLineCountLimit bounds the file countFileLines is willing to read. This runs on the
// hook's critical path and OUTSIDE proxyReadTimeout -- soleReadTarget is called before the
// goroutine that timeout guards -- so a pathological file must not be able to stall the agent
// here. Past the limit the window is simply dropped and the budget falls back to file size,
// which is the conservative direction.
const proxyLineCountLimit = 4 << 20

// countFileLines returns the file's line count, or 0 when it cannot be read. Used only to turn
// a line window into a byte estimate, so an approximate answer is fine and a failure just
// disables the window handling for this call.
func countFileLines(path string) int {
	if info, err := os.Stat(path); err != nil || info.Size() > proxyLineCountLimit {
		return 0
	}
	b, err := os.ReadFile(path)
	if err != nil || len(b) == 0 {
		return 0
	}
	n := strings.Count(string(b), "\n")
	if !strings.HasSuffix(string(b), "\n") {
		n++
	}
	return n
}
