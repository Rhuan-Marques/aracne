package shellcmd

import "strings"

// Downstream consumers: what a command on the RIGHT of a pipe does to the bytes it receives.
//
// WHY THIS IS ITS OWN CLASSIFICATION. When aracne answers the producer of a pipeline, the
// consumer no longer sees the real command's output -- it sees aracne's. Whether that is
// honest depends entirely on what the consumer does with the lines:
//
//	grep … | grep -v _test | head -30    SELECTS and caps a list of matches, in order. aracne
//	                                     returns a list of matches too, so both still mean
//	                                     "the matches, minus the test ones, first 30".
//	grep … | sort -u                     REORDERS them. aracne's output is structured -- a
//	                                     `# path:a-b` annotation followed by its matches --
//	                                     and sorting separates the two.
//	grep … | wc -l                       COUNTS lines. aracne's answer interleaves `# path:a-b`
//	                                     annotation lines with the match lines, so the count is
//	                                     of a different thing -- and it comes back as a bare
//	                                     number with nothing to reveal the substitution.
//	grep … | xargs sed -i …              EXECUTES the lines. Substituting the input silently
//	                                     changes what gets edited.
//
// So the rule is not "does it read stdin" but "does it preserve line-for-line semantics such
// that filtering aracne's answer means the same as filtering the real one". Order-preserving
// selectors and cappers do. Anything that reorders, rewrites, aggregates, counts, executes or
// writes does not.
//
// This lives in shellcmd rather than in the guard because BOTH the guard's blocked_tools
// classification and the interception rewrite have to agree on which segments of a pipeline are
// aracne's to answer. Two copies of this judgement would drift on the first change to either.

// ConsumerClass is what a downstream pipeline stage does to the lines it is given.
type ConsumerClass int

const (
	// ConsumerOpaque is the safe default: this stage's meaning depends on bytes aracne did not
	// promise to reproduce, so the producer upstream of it must run for real.
	ConsumerOpaque ConsumerClass = iota
	// ConsumerCapper truncates a list without changing the lines that survive (head, tail).
	ConsumerCapper
	// ConsumerLineFilter keeps or drops whole lines IN ORDER (grep and friends). Selecting from
	// aracne's matches answers the same question selecting from the real ones did. Reordering
	// does not, which is why sort/uniq are opaque.
	ConsumerLineFilter
)

// consumerClasses maps a command word to what it does to piped lines.
//
// Deliberately small and deliberately a allow-list. The cost of wrongly calling something a
// filter is a silently different answer; the cost of wrongly calling it opaque is one command
// aracne does not serve. Those are not symmetric.
var consumerClasses = map[string]ConsumerClass{
	// Cappers. `head`/`tail` on a list of matches still means "the first/last few of them",
	// and they preserve order.
	"head": ConsumerCapper,
	"tail": ConsumerCapper,

	// Line SELECTORS. Each keeps or drops whole lines and emits them in the order received.
	// That is the property that matters: aracne's search output is structured -- a
	// `# path:a-b` annotation followed by the matches it covers -- and any stage that
	// REORDERS the lines separates an annotation from what it annotates.
	"grep":  ConsumerLineFilter,
	"egrep": ConsumerLineFilter,
	"fgrep": ConsumerLineFilter,
	"rg":    ConsumerLineFilter,
	"cat":   ConsumerLineFilter, // `| cat` is a no-op pass-along

	// Everything else is ConsumerOpaque by omission. The ones worth naming, because they look
	// harmless and are not:
	//   sort, uniq  REORDER or collapse lines, breaking annotation-to-match adjacency
	//   cut, tr     rewrite the line's bytes, so a `path:line:` prefix may not survive
	//   sed, awk    arbitrary per-line programs; `sed -n 5p` means a different 5th line
	//   wc          counts lines, and aracne's annotation lines are counted too
	//   xargs       executes the lines
	//   tee         writes them somewhere aracne's answer should not go
	//   jq          parses them as JSON, which aracne's framing is not
}

// ClassifyConsumer reports what a downstream pipeline stage does to the lines it receives.
// argv is the stage's command word followed by its arguments, with any leading environment
// assignments already stripped.
//
// IT READS THE FLAGS, NOT ONLY THE WORD, and that is the half it used to be missing. The name
// answers half the question: `grep -v _test` selects lines and `grep -c _test` COUNTS them --
// and a count is exactly the "count of a different thing, returned as a bare number with
// nothing to reveal the substitution" this file's header refuses `wc -l` for. Classified on the
// word alone, `| grep -c` was waved through as a line filter, and so were `grep -l`,
// `grep -o`, `grep -q` and `head -c`. Measured on a two-match fixture, `grep … | grep -c util`
// answered 2 for the real pipeline and 3 for the intercepted one.
func ClassifyConsumer(argv []string) ConsumerClass {
	if len(argv) == 0 {
		return ConsumerOpaque
	}
	name := Base(argv[0])
	class, ok := consumerClasses[name]
	if !ok {
		return ConsumerOpaque
	}
	if consumerFlagsChangeOutput(name, argv[1:]) {
		return ConsumerOpaque
	}
	return class
}

// The flags that stop a stage from being a line-for-line selector, per family. Each changes
// either the UNIT of the stage's output (a count, a file list, a match fragment, an exit
// status, NUL separators) or the unit of its input (bytes rather than lines) -- which is the
// same objection this file makes to `wc`, spelled as an option instead of as a command.
//
// Short and long are separate because only a short cluster is scanned letter by letter.
var (
	grepOutputShort = map[byte]bool{
		'c': true, 'l': true, 'L': true, 'o': true, 'q': true, 'm': true, 'Z': true,
	}
	grepOutputLong = map[string]bool{
		"--count": true, "--files-with-matches": true, "--files-without-match": true,
		"--only-matching": true, "--quiet": true, "--silent": true, "--max-count": true,
		"--null": true,
	}
	// `head -c` / `tail -c` count BYTES, so they can truncate mid-line -- and mid-annotation.
	// `tail -f` follows a stream aracne answered once and will not extend.
	pagerOutputShort = map[byte]bool{'c': true, 'f': true, 'F': true, 'q': true, 'z': true}
	pagerOutputLong  = map[string]bool{
		"--bytes": true, "--follow": true, "--quiet": true, "--silent": true,
		"--zero-terminated": true,
	}
)

// consumerFlagsChangeOutput reports whether any argument is one of the flags above.
//
// Over-refusing is the safe direction here and the asymmetry is the same one the allow-list
// itself rests on: a flag wrongly called opaque costs one command aracne does not serve, and a
// flag wrongly waved through costs a silently different answer.
func consumerFlagsChangeOutput(name string, args []string) bool {
	short, long := grepOutputShort, grepOutputLong
	switch name {
	case "head", "tail":
		short, long = pagerOutputShort, pagerOutputLong
	case "cat":
		// `cat` has no flag that changes the unit of a line: -n, -A and friends decorate the
		// lines they are given, and a decorated copy of aracne's answer is still aracne's
		// answer -- which is what the caller of a `| cat` pass-along asked for.
		return false
	}
	endOfFlags := false
	for _, a := range args {
		switch {
		case endOfFlags, a == "-", !strings.HasPrefix(a, "-"):
			continue
		case a == "--":
			endOfFlags = true
		case strings.HasPrefix(a, "--"):
			// `--max-count=5` is the same flag as `--max-count 5`.
			flag, _, _ := strings.Cut(a, "=")
			if long[flag] {
				return true
			}
		default:
			// A short cluster, possibly ending in a glued value (`-m5`, `-c40`). A value's
			// digits are in no table, so scanning the whole token is safe.
			for i := 1; i < len(a); i++ {
				if short[a[i]] {
					return true
				}
			}
		}
	}
	return false
}

// ConsumerPreservesLines reports whether a stage can be handed aracne's answer instead of the
// real command's without changing what the pipeline as a whole means.
func ConsumerPreservesLines(argv []string) bool {
	return ClassifyConsumer(argv) != ConsumerOpaque
}
