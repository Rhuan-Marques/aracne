package cli

import (
	"encoding/json"
	"io"
	"os"
	"strings"

	"aracne/internal/helper"
	"aracne/internal/shellcmd"
)

// Interception is how aracne reaches an agent that has no MCP tools: it rewrites the shell
// read the model already typed into the same read, served from the topology.
//
// WHY A REWRITE AND NOT A DENIAL. The guard's older answer to a read it wanted to serve was to
// DENY the command and put the content in the refusal (guard_proxy.go). That worked, and it
// reads as a refusal: in one benchmark run 24 of 27 agents opened with shell, DENIED, a tool
// lookup, then the read -- two calls spent on a question the model had already asked
// correctly. A PreToolUse hook may instead return `updatedInput`, which rewrites the tool's
// arguments before it runs. So the command runs, its stdout is aracne's answer, and there is
// nothing to recover from.
//
// WHAT THIS FUNCTION IS AND IS NOT RESPONSIBLE FOR. It answers one question: "is this a single,
// self-contained shell read or search that `arac cmd` models?" Whether the target is in scope
// -- indexed, resolvable, worth enriching -- is decided ONCE, inside `arac cmd`, which has the
// topology open anyway. Splitting that decision across both would let the hook rewrite
// something the verb then passes through, and the two would drift apart on the first change to
// either.
//
// The one scope test kept here is cheap and worth its cost: a read of an EXISTING file the
// topology does not know is left completely untouched, so an agent reading CHANGELOG.md never
// sees its command rewritten to something that could only pass it through.

// interceptCommand returns the command to run in place of the one the model typed, or false to
// leave the call exactly as it is.
//
// It rewrites SEGMENT BY SEGMENT rather than all-or-nothing. Measured over the 462 real shell
// commands the control arm of fair-20260901a issued, an all-or-nothing rule reached 59 of them:
// 256 were refused purely for being compound, and the two shapes aracne is actually good at --
// a whole-file read (0.21x the bytes of `cat`) and a search (0.62x the bytes of `grep`) -- were
// mostly hiding inside them, as `cd d && cat f`, `ls x && sed -n …` and `grep … | head -30`.
// Splicing a prefix into the one segment that is a read leaves every other byte of the line,
// including its quoting and its redirects, exactly as the model wrote it.
func interceptCommand(command, dbPath string, cfg *helper.Config) (string, bool) {
	if !cfg.InterceptShell() {
		return "", false
	}
	original := strings.TrimSpace(command)
	if original == "" {
		return "", false
	}
	// A heredoc body is data, and a newline may hide a second command from the offset
	// arithmetic below. Neither is worth the risk of splicing into.
	if strings.Contains(original, "<<") || strings.ContainsAny(original, "\n") {
		return "", false
	}
	// `arac cmd -- head f` is itself a command containing `head`. Without this the hook would
	// rewrite its own rewrite, forever. The `--` matters too: it stops commandFields scanning
	// past `arac` to the read command word behind it.
	if isAracCommand(map[string]interface{}{"command": original}) {
		return "", false
	}
	if operatesOutsideProject(original, projectRoot(dbPath)) {
		return "", false
	}
	arac, err := os.Executable()
	if err != nil || arac == "" {
		return "", false
	}

	segments := splitCommandSegments(original)
	var at []int
	for i := range segments {
		if off, ok := interceptableSegment(segments, i, original, dbPath, cfg); ok {
			at = append(at, off)
		}
	}
	if len(at) == 0 {
		return "", false
	}
	prefix := quoteForShell(arac) + " cmd -- "
	// Back to front, so an earlier offset is still valid after a later splice.
	out := original
	for i := len(at) - 1; i >= 0; i-- {
		out = out[:at[i]] + prefix + out[at[i]:]
	}
	return out, true
}

// interceptableSegment reports whether one segment of a command may be rewritten, and the byte
// offset in the original string where the `arac cmd --` prefix belongs.
func interceptableSegment(segments []commandSegment, i int, original, dbPath string, cfg *helper.Config) (int, bool) {
	seg := segments[i]
	// A segment reading piped stdin has no file to look up, and one redirecting stdout would
	// send aracne's answer to that file instead of to the model.
	if seg.pipedInto || seg.redirectsOut {
		return 0, false
	}
	argv := segmentArgv(seg)
	if len(argv) == 0 {
		return 0, false
	}
	// The command word must be the segment's FIRST word. commandFields deliberately looks past
	// env assignments and wrappers (`sudo`, `rtk proxy`) to classify what really runs;
	// prefixing those would put `arac cmd --` in front of the wrapper instead of the command.
	rawFields := strings.Fields(seg.text)
	if len(rawFields) == 0 || !strings.EqualFold(shellcmd.Base(rawFields[0]), shellcmd.Base(argv[0])) {
		return 0, false
	}

	req := shellcmd.Parse(argv)
	switch req.Kind {
	case shellcmd.KindRead:
		// Only the intercepting modes rewrite a read. In ModeMCP the read capability is a
		// tool and in ModeAracneRead it is `arac read`; rewriting the model's `cat` on top of
		// either would be a second answer to a question that already has one.
		if !cfg.InterceptReads() {
			return 0, false
		}
	case shellcmd.KindGrep:
		// Every mode. See Config.InterceptGrep.
	default:
		return 0, false
	}
	if !pipesOnlyIntoCappers(segments, i, req.Kind) {
		return 0, false
	}
	if namesOnlyUnindexedFiles(req.Operands, dbPath) {
		return 0, false
	}

	// Splice before the first non-blank byte, so the separator and any spacing survive.
	off := seg.start
	for off < seg.end && off < len(original) && (original[off] == ' ' || original[off] == '\t') {
		off++
	}
	return off, true
}

// pipesOnlyIntoCappers decides whether a segment that FEEDS a pipe may be rewritten.
//
// The distinction is what the consumer does to the bytes. `grep … | head -30` caps a list of
// matches, and aracne returns a list of matches too, so capping it means the same thing --
// this is the single most common search shape in a real transcript. `cat f | grep x` does not:
// the consumer would search aracne's RENDERING, whose elided bodies are not in the file's text,
// and quietly return a different answer.
//
// So only a search may be rewritten upstream of a pipe, and only into pure line cappers. A read
// producer is left alone: `cat f | head -30` means "the first 30 lines of f", which is a window
// -- and the window shape is the one that costs MORE than the plain command anyway, so nothing
// is lost by leaving it.
func pipesOnlyIntoCappers(segments []commandSegment, i int, kind shellcmd.Kind) bool {
	if i+1 >= len(segments) || !segments[i+1].pipedInto {
		return true // feeds no pipe
	}
	if kind != shellcmd.KindGrep {
		return false
	}
	for j := i + 1; j < len(segments) && segments[j].pipedInto; j++ {
		switch commandWord(segments[j].text) {
		case "head", "tail":
		default:
			return false
		}
	}
	return true
}

// namesOnlyUnindexedFiles reports whether every operand is a file that exists on disk and has
// no topology nodes.
//
// This is the one scope test the hook keeps, and it is the same trade guard_untracked.go makes
// for denials: for a file the topology does not model, aracne would answer with the identical
// raw bytes the command prints. Rewriting buys nothing and costs the model a command line it
// did not write. An operand that is NOT a file on disk is left alone here on purpose -- it may
// be a resource ID, which only the topology can settle, and `arac cmd` settles it.
func namesOnlyUnindexedFiles(operands []string, dbPath string) bool {
	// existingReadFiles is the same resolver the denial path uses, and it tries each token
	// against the project root as well as the process cwd. That matters here: a hook runs in
	// the SESSION directory while the agent's shell may have cd'd elsewhere, so resolving a
	// relative operand against the hook's own cwd alone finds nothing and would judge every
	// relative read "not a file". Where it still cannot tell, this returns false and `arac
	// cmd` -- which runs in the agent's actual shell -- settles it correctly.
	existing := existingReadFiles(operands, projectRoot(dbPath))
	if len(existing) == 0 || len(existing) != len(operands) {
		return false // nothing to judge, or a mix that includes a possible resource ID
	}
	tracked, ok := trackedFiles(existing, dbPath)
	if !ok {
		return false // could not consult the topology: behave as before and let the verb decide
	}
	return len(tracked) == 0
}

// segmentArgv turns a scanned segment back into argv. splitCommandSegments drops quote
// characters and substitutes a control byte for the spaces they held together, so putting
// those spaces back yields exactly the tokens a shell would have produced -- which is what
// shellcmd.Parse expects, and what makes `sed -n '120,160p' f` classify.
func segmentArgv(seg commandSegment) []string {
	fields := commandFields(seg.text)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		out = append(out, strings.ReplaceAll(f, string(quotedSpace), " "))
	}
	return out
}

// quoteForShell wraps a path in single quotes when it contains anything a shell would split
// or expand. The aracne binary's own path is not usually exotic, but a Windows-style install
// under "Program Files" is, and an unquoted one would silently become two arguments.
func quoteForShell(p string) string {
	if !strings.ContainsAny(p, " \t\"'$`\\&;|<>()*?[]{}!#~") {
		return p
	}
	return "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
}

// emitPreToolRewrite answers a PreToolUse event by replacing the command the tool will run.
//
// The whole original tool_input is copied and only `command` swapped, so `description`,
// `timeout` and `run_in_background` survive. Claude Code validates updatedInput against the
// tool's own schema and falls back to the original input if it does not fit, which makes a
// malformed rewrite a no-op rather than a broken call.
func emitPreToolRewrite(output io.Writer, toolInput map[string]interface{}, command string) {
	updated := make(map[string]interface{}, len(toolInput)+1)
	for k, v := range toolInput {
		updated[k] = v
	}
	updated["command"] = command
	json.NewEncoder(output).Encode(map[string]interface{}{
		"hookSpecificOutput": map[string]interface{}{
			"hookEventName": "PreToolUse",
			"updatedInput":  updated,
		},
	})
}
