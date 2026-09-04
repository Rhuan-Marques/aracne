package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/llm/languages/universaltools"
	"github.com/Rhuan-Marques/aracne/internal/toolspec"
	"github.com/Rhuan-Marques/aracne/internal/topology"
)

const (
	// proxyReadTimeout bounds the work a denial is allowed to do. A denial that hangs is far
	// worse than a terse one: the agent is stalled behind the hook with nothing to show for it.
	proxyReadTimeout = 5 * time.Second
	// proxyMaxBytes is the absolute ceiling on what a denial reason may carry. It was 100KB,
	// which in practice caught nothing: the worst over-serve measured in
	// compact-blocked-after-bs-20260830c was 59,177 bytes and sailed under it.
	proxyMaxBytes = 32 * 1024
	// proxyMinBudget is what the answer is always allowed, however small the request. A
	// twenty-line `sed` window legitimately expands to the whole function around it plus a
	// context block, and refusing that would put us back to the bare pointer for the very
	// case the proxy exists to serve.
	proxyMinBudget = 12 * 1024
	// proxyOverServeFactor bounds the answer against what was actually asked for. Past this
	// the proxy is no longer a cheaper read -- it is a way to spend the context window on one
	// refused `cat` -- so it hands back "" and the model gets the ordinary one-line denial.
	proxyOverServeFactor = 4
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
//   - the file must exist and be tracked, so this never invents an answer,
//   - a size budget and a hard timeout, either of which returns "" and restores the pointer.
//
// It returns "" whenever it cannot be certain, and "" means the caller keeps today's behaviour.
func proxyRead(command, dbPath string) string {
	target := soleReadTarget(command)
	if target.path == "" {
		return ""
	}
	info, err := os.Stat(target.path)
	if err != nil || info.IsDir() {
		return ""
	}
	budget := proxyBudget(target, info.Size())

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
		rd := universaltools.NewRead(mgr, cfg, false, nil)
		opt := universaltools.ReadIDsOptions{Kinds: helper.AllReadKinds()}

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
		if strings.TrimSpace(out) == "" || len(out) > budget {
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

// proxyBudget is the largest answer worth sending for this request.
//
// Without it the proxy answers every question with the same thing -- the whole file -- and a
// 22-line `awk` window came back at 20,763 bytes. The budget is proportional so a big honest
// read still goes through, floored so a small window still gets its enclosing function plus
// context, and capped absolutely so nothing gets to blow the context window.
func proxyBudget(t readTarget, fileSize int64) int {
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
	budget := want * proxyOverServeFactor
	if budget < proxyMinBudget {
		budget = proxyMinBudget
	}
	if budget > proxyMaxBytes {
		budget = proxyMaxBytes
	}
	return budget
}

// readTarget is the single file a denied command reads, plus the line window it asked for.
type readTarget struct {
	path       string
	hasWindow  bool
	from, to   int
	totalLines int // of the file on disk; 0 when it could not be counted
}

var (
	// `sed -n 120,160p` and `sed -n '120,160p'` (quotes are already stripped by the splitter).
	reSedRange = regexp.MustCompile(`(?:^|[\s;])(\d+)\s*,\s*(\d+)\s*p`)
	// `sed -n 120p`.
	reSedSingle = regexp.MustCompile(`(?:^|[\s;])(\d+)\s*p(?:[\s;]|$)`)
	// `awk 'NR>=120 && NR<=160'` in its common spellings.
	reAwkRange = regexp.MustCompile(`NR\s*>=?\s*(\d+).*?NR\s*<=?\s*(\d+)`)
	// `head -40`, `head -n 40`.
	reHead = regexp.MustCompile(`^-n?(\d+)$`)
)

// soleReadTarget returns the single file a shell command reads and the window it wants, or a
// zero value when the command is anything more complicated than that.
//
// Deliberately strict. A pipeline, a glob, two operands or an unrecognized reader all return
// nothing and cost the model the ordinary one-line denial it would have had anyway. The only
// case worth answering is the one with an unambiguous answer.
func soleReadTarget(command string) readTarget {
	segments := splitCommandSegments(command)
	if len(segments) != 1 {
		return readTarget{}
	}
	fields := commandFields(segments[0].text)
	if len(fields) < 2 {
		return readTarget{}
	}
	if key, ok := toolspec.ShellCommandKeyForArgs(fields[0], fields[1:], segments[0].redirectsOut); !ok || key != "read" {
		return readTarget{}
	}
	var operands []string
	for _, a := range fields[1:] {
		if strings.HasPrefix(a, "-") || strings.ContainsAny(a, "*?[]") {
			continue
		}
		// sed/awk take a script operand before the filename; only a path can be the target.
		if !strings.ContainsAny(a, "/\\") && !strings.Contains(a, ".") {
			continue
		}
		operands = append(operands, a)
	}
	if len(operands) != 1 {
		return readTarget{}
	}
	t := readTarget{path: filepath.Clean(strings.Trim(operands[0], `'"`))}
	t.from, t.to, t.hasWindow = readWindow(commandBase(fields[0]), fields[1:], t.path)
	if t.hasWindow {
		t.totalLines = countFileLines(t.path)
		if t.totalLines == 0 {
			t.hasWindow = false
		}
	}
	return t
}

// readWindow extracts the line range a read command asked for. The target path is excluded
// from the scanned text so a filename containing digits cannot be mistaken for a range.
func readWindow(name string, args []string, path string) (from, to int, ok bool) {
	var parts []string
	for _, a := range args {
		if strings.Trim(a, `'"`) == path {
			continue
		}
		parts = append(parts, a)
	}
	joined := strings.Join(parts, " ")
	// The splitter substitutes a control character for spaces inside quoted runs; put them
	// back so `'NR>=955 && NR<=995'` matches as one expression.
	joined = strings.ReplaceAll(joined, string(quotedSpace), " ")

	switch name {
	case "sed":
		if m := reSedRange.FindStringSubmatch(joined); m != nil {
			return atoi(m[1]), atoi(m[2]), true
		}
		if m := reSedSingle.FindStringSubmatch(joined); m != nil {
			n := atoi(m[1])
			return n, n, true
		}
	case "awk", "gawk", "mawk":
		if m := reAwkRange.FindStringSubmatch(joined); m != nil {
			return atoi(m[1]), atoi(m[2]), true
		}
	case "head":
		for i, a := range parts {
			// `head -40` and `head -n40` carry the count in the flag itself.
			if m := reHead.FindStringSubmatch(a); m != nil {
				return 1, atoi(m[1]), true
			}
			// `head -n 40` puts it in the next token.
			if (a == "-n" || a == "--lines") && i+1 < len(parts) {
				if n := atoi(parts[i+1]); n > 0 {
					return 1, n, true
				}
			}
		}
		// A bare `head file` is the default 10 lines.
		return 1, 10, true
	}
	return 0, 0, false
}

// commandBase strips a path and any extension off a command word, so `/usr/bin/sed` and
// `sed.exe` both classify as `sed`. toolspec has the same helper unexported; duplicating four
// lines is cheaper than widening that package's surface for one caller.
func commandBase(tok string) string {
	tok = strings.ToLower(tok)
	if i := strings.LastIndexAny(tok, `/\`); i >= 0 {
		tok = tok[i+1:]
	}
	return strings.TrimSuffix(tok, ".exe")
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
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
