package shellcmd

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
// `word` is the segment's command word (already unwrapped of paths and quoting by Base).
func ClassifyConsumer(word string) ConsumerClass {
	if c, ok := consumerClasses[Base(word)]; ok {
		return c
	}
	return ConsumerOpaque
}

// ConsumerPreservesLines reports whether a stage can be handed aracne's answer instead of the
// real command's without changing what the pipeline as a whole means.
func ConsumerPreservesLines(word string) bool {
	return ClassifyConsumer(word) != ConsumerOpaque
}
