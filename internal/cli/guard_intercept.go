package cli

import (
	"encoding/json"
	"io"
	"os"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/shellcmd"
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
	// Each splice is an offset plus the prefix that belongs at it: a segment whose stdout a
	// pipeline consumes is answered in PLAIN mode, which is a different question from the one
	// a segment printing to the model asks. See serveGrep and topogrep.Options.Plain.
	type splice struct {
		at     int
		prefix string
	}
	var splices []splice
	plain := quoteForShell(arac) + " cmd --piped -- "
	enriched := quoteForShell(arac) + " cmd -- "
	for i := range segments {
		off, piped, ok := interceptableSegment(segments, i, original, dbPath, cfg, cfg.InterceptReads())
		if !ok {
			continue
		}
		prefix := enriched
		if piped {
			prefix = plain
		}
		splices = append(splices, splice{at: off, prefix: prefix})
	}
	if len(splices) == 0 {
		return "", false
	}
	// Back to front, so an earlier offset is still valid after a later splice.
	out := original
	for i := len(splices) - 1; i >= 0; i-- {
		out = out[:splices[i].at] + splices[i].prefix + out[splices[i].at:]
	}
	return out, true
}

// interceptableSegment reports whether one segment of a command may be rewritten, the byte
// offset in the original string where the `arac cmd` prefix belongs, and whether that segment
// feeds a pipe (which decides how the answer is rendered).
func interceptableSegment(segments []commandSegment, i int, original, dbPath string,
	cfg *helper.Config, allowReads bool) (int, bool, bool) {
	seg := segments[i]
	// A segment reading piped stdin has no file to look up, and one redirecting stdout would
	// send aracne's answer to that file instead of to the model.
	if seg.pipedInto || seg.redirectsOut {
		return 0, false, false
	}
	// A segment INSIDE a substitution, a subshell or a group is not a command whose stdout
	// the model reads: it is a value the enclosing command consumes. `X=$(cat f)`,
	// `grep foo $(cat list)` and ``echo `cat f` `` all put aracne's rendering -- fences,
	// `⋯ +N lines ⋯` markers, a `# CONTEXT:` block -- where the file's own bytes were
	// expected, and the enclosing command then runs normally on different text with nothing
	// to mark the substitution. It is the same trade the pipe rule below refuses, on a path
	// that had no rule at all.
	if seg.depth > 0 {
		return 0, false, false
	}
	argv := segmentArgv(seg)
	if len(argv) == 0 {
		return 0, false, false
	}
	// The command word must be the segment's FIRST word. commandFields deliberately looks past
	// env assignments and wrappers (`sudo`, `rtk proxy`) to classify what really runs;
	// prefixing those would put `arac cmd --` in front of the wrapper instead of the command.
	rawFields := strings.Fields(seg.text)
	if len(rawFields) == 0 || !strings.EqualFold(shellcmd.Base(rawFields[0]), shellcmd.Base(argv[0])) {
		return 0, false, false
	}

	req := shellcmd.Parse(argv)
	switch req.Kind {
	case shellcmd.KindRead:
		// Only the intercepting modes rewrite a read. In ModeMCP the read capability is a
		// tool and in ModeCLI it is `arac read`; rewriting the model's `cat` on top of
		// either would be a second answer to a question that already has one.
		//
		// allowReads is the caller's answer to that, and interceptCommand is the only caller
		// left: it passes cfg.InterceptReads(). The nudge path used to ask the same question
		// hypothetically -- "would this have been answered, had reads been intercepted?" -- and
		// no longer consults this at all; the guard decides a bash nudge from the mode and the
		// key set (see nudgeKeys).
		if !allowReads {
			return 0, false, false
		}
	case shellcmd.KindGrep:
		// Every mode. See Config.InterceptGrep.
	default:
		return 0, false, false
	}
	piped, ok := pipesIntoLinePreservingConsumers(segments, i, req.Kind)
	if !ok {
		return 0, false, false
	}
	if namesOnlyUnindexedFiles(req.Operands, dbPath) {
		return 0, false, false
	}

	// Splice before the first non-blank byte, so the separator and any spacing survive.
	off := seg.start
	for off < seg.end && off < len(original) && (original[off] == ' ' || original[off] == '\t') {
		off++
	}
	return off, piped, true
}

// pipesIntoLinePreservingConsumers decides whether a segment that FEEDS a pipe may be
// rewritten.
//
// The distinction is what the consumer does to the bytes, and it is delegated to
// shellcmd.ClassifyConsumer so the guard and the rewrite cannot disagree about which segments
// of a pipeline are aracne's to answer. `grep … | grep -v _test | head -30` filters and caps a
// list of matches, and aracne returns a list of matches too, so the pipeline still means the
// same thing. `grep … | wc -l` does not: aracne interleaves `# path:a-b` annotation lines with
// the matches, so the count is of a different thing and comes back as a bare number with
// nothing in it to reveal the substitution.
//
// The old rule here allowed head and tail only. Measured over 664 real shell commands from
// hard9-modes and navcheck-20260902a, that reached 88 of the 133 piped greps (66%); allowing
// line-preserving filters reaches 129 (97%). The 41 it was refusing were almost entirely
// `| grep -v <noise> | head -N`, which is simply how the model writes a search.
//
// A READ producer is still left alone whatever follows it. `cat f | grep x` would search
// aracne's RENDERING, whose elided bodies are not in the file's text, and quietly return
// FEWER matches than the real command -- a wrong answer with nothing to mark it as one. That
// is the opposite trade from the grep case: there, aracne's answer is the same KIND of thing
// the consumer expected; here it is not.
//
// THE WALK IS OVER STAGES, NOT OVER NEIGHBOURS, and that distinction is the whole of the fix
// below. It used to ask whether `segments[i+1].pipedInto` was set -- adjacency -- which is only
// the same question while the scanner keeps a simple command in one segment. It does not: the
// scanner cuts a new segment at every `(`, `)`, backtick and `{`, so a command carrying a
// substitution is spread over several segments with the substitution's body BETWEEN them. The
// producer's neighbour was then the body, `pipedInto` was false on it, and
// `grep -rn X $(echo .) | wc -l` was rewritten -- the exact case this function exists to refuse,
// on the path that had no rule. Measured on a two-match fixture it answered 4 where the real
// pipeline answered 2. `${VAR}` and a backtick substitution took the same route.
//
// pipelineStages walks the command list at the producer's own depth instead, so a nested body
// is skipped and the pipe bit is found wherever the scanner happened to leave it. The same
// break was live for every `2>&1` until the `&` stopped ending a command
// (splitCommandSegments.isRedirectAmpersand); a group or a subshell AROUND the producer is
// refused a step earlier, by its depth.
func pipesIntoLinePreservingConsumers(segments []commandSegment, i int, kind shellcmd.Kind) (piped, ok bool) {
	stages, feedsPipe, readable := pipelineStages(segments, i)
	if !feedsPipe {
		return false, true // feeds no pipe
	}
	if kind != shellcmd.KindGrep || !readable {
		return true, false
	}
	for _, stage := range stages {
		if !shellcmd.ConsumerPreservesLines(stage) {
			return true, false
		}
	}
	return true, true
}

// pipelineStages returns the argv of every stage the segment at i feeds its stdout into,
// whether it feeds a pipe at all, and whether every stage could be read.
//
// The scan runs at the producer's own nesting depth. A DEEPER segment is the body of a
// substitution, a subshell or a group written inside this command and is skipped -- it is a
// value the command consumes, not a stage that consumes the command. A SHALLOWER one means the
// group around this command closed, which ends the list.
//
// At the producer's own depth, what a segment IS follows from why the scanner cut the one
// before it (commandSegment.endedBy): after a `|` it is a stage of this pipeline, after a
// nesting boundary it is the same command still being written, and after a list separator the
// command list has ended. That is the fact the walk needs and the only one it cannot infer.
// Guessing it from "does this segment have a command word" -- the first attempt -- read
// `grep X $(echo .) extra.go | wc -l` as a new command starting at `extra.go`, lost the pipe,
// and handed the annotated answer to a counter.
//
// readable is false for a stage aracne cannot name -- `… | ( wc -l )` puts the real consumer
// one level down -- and the caller must treat that as an opaque consumer rather than as an
// absent one.
func pipelineStages(segments []commandSegment, i int) (stages [][]string, feedsPipe, readable bool) {
	depth := segments[i].depth
	for j := i + 1; j < len(segments); j++ {
		seg := segments[j]
		if seg.depth > depth {
			continue
		}
		if seg.depth < depth {
			break
		}
		switch segments[j-1].endedBy {
		case '|':
			feedsPipe = true
			argv := stdinReaderArgv(seg)
			if len(argv) == 0 {
				return nil, true, false
			}
			stages = append(stages, argv)
		case '(', ')', '{', '}', '`':
			// A nesting boundary inside the command this segment belongs to. Whatever follows
			// is the rest of that command -- its remaining operands, or the pipe it feeds.
			continue
		default:
			// A list separator, or the end of the string: nothing after this belongs to the
			// producer's own pipeline.
			return stages, feedsPipe, true
		}
	}
	return stages, feedsPipe, true
}

// stdinReaderArgv is the argv of the command that actually RECEIVES the pipe.
//
// Deliberately not commandFields, which looks past wrappers to classify what ultimately runs.
// That is the right answer for "what is this segment doing" and the wrong one here: in
// `… | xargs sed -i s/a/b/` the stage reading stdin is `xargs`, which EXECUTES the lines it
// is given, while commandFields reports `sed` -- a line filter -- and would wave it through.
// Only environment assignments are skipped, because `LC_ALL=C sort` is still sort reading the
// pipe.
//
// The ARGUMENTS come with it, because the flags decide as much as the word does: `grep -v` is
// a line filter and `grep -c` is a counter. See shellcmd.ClassifyConsumer.
func stdinReaderArgv(seg commandSegment) []string {
	fields := strings.Fields(withoutRedirections(seg))
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if len(out) == 0 && isEnvAssignment(f) {
			continue
		}
		out = append(out, strings.ReplaceAll(f, string(quotedSpace), " "))
	}
	return out
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
	// existingReadFiles is the same resolver the denial path uses: an ALREADY-ABSOLUTE token as
	// it stands, and a relative one joined onto the PROJECT ROOT. The hook's own working
	// directory is deliberately not consulted -- it is the session directory while the agent's
	// shell may have cd'd elsewhere, so resolving against it would answer about a different
	// file (see guardDBPath for the same problem one layer down). Where the root cannot settle
	// it either, this returns false and `arac cmd` -- which runs in the agent's actual shell --
	// decides correctly.
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

// namesAnIndexedFile reports whether some operand resolves to a file the topology knows.
//
// The POSITIVE form of namesOnlyUnindexedFiles, and the difference is the whole point.
// Interception may act on "not provably unindexed" because `arac cmd` re-checks in the
// agent's own shell and passes through when it cannot serve -- a wrong guess there costs
// nothing. The nudge has no second check: whatever it decides is printed to the model. So it
// requires evidence rather than the absence of counter-evidence.
//
// The case that made this necessary was a real one, from a smoke cell:
//
//	for f in a.tmpl b.tmpl; do echo "=== $f ==="; cat "$f"; done
//
// `cat "$f"` names a shell variable, which resolves to no path, so "are all operands
// unindexed?" answered false and the nudge fired -- recommending `arac read` for template
// files carrying zero topology nodes.
//
// A DIRECTORY IS EVIDENCE IN ITS OWN RIGHT, and leaving it out inverted the whole test for the
// commonest call there is. `existingReadFiles` keeps regular files only -- correct for a read,
// where a directory is not a target -- so a native `Grep{pattern, path: "internal/cli"}`
// resolved to no file, reported "nothing indexed", and was silently exempted from the one
// channel that teaches the annotated grep. A `Grep` with NO path was nudged and a `Grep` scoped
// to a tree full of indexed code was not. A directory inside the project is exactly the scope
// aracne can search better, so it counts.
func namesAnIndexedFile(operands []string, dbPath string) bool {
	root := projectRoot(dbPath)
	if namesProjectDir(operands, root) {
		return true
	}
	existing := existingReadFiles(operands, root)
	if len(existing) == 0 {
		return false
	}
	tracked, ok := trackedFiles(existing, dbPath)
	return ok && len(tracked) > 0
}

// segmentArgv turns a scanned segment back into argv. splitCommandSegments drops quote
// characters and substitutes a control byte for the spaces they held together, so putting
// those spaces back yields exactly the tokens a shell would have produced -- which is what
// shellcmd.Parse expects, and what makes `sed -n '120,160p' f` classify.
//
// Redirections are removed first, as the shell removes them: `cat f 2>/dev/null` is argv
// [cat f], not a two-file read, and `sed -n '1,5p' f 2>/dev/null` is a one-file window rather
// than a multi-file sed that shellcmd must refuse.
func segmentArgv(seg commandSegment) []string {
	fields := commandFields(withoutRedirections(seg))
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
