package topogrep

import "strconv"

// RawBytes is what a plain grep would have printed for this result: the textual match rows and
// the context around them, in the shape FormatResult prints them, and none of the topology's
// additions -- no resource headers, no node rows, no trailers.
//
// It is what the over-serve ceiling measures an answer against. Node rows are excluded because
// they ARE the addition being measured: a search that answers one matching line with thirty
// declaration lines has over-served by thirty rows, not by none.
func RawBytes(res *Result, opt Options) int {
	if res == nil {
		return 0
	}
	// A file list and a count are already grep's own shape, and both are built from textual
	// matches only. There is nothing in them to measure.
	if opt.Mode == OutputFiles || opt.Mode == OutputCount {
		return len(FormatResult(res, opt))
	}

	// The SAME two shape questions FormatResult asks, or the denominator describes a row the
	// caller never sees: `-H` puts the path back on a single-file search, and without `-n` a
	// plain grep prints no line number at all.
	bare := opt.Terse && res.RootIsFile && !opt.WithFilename
	numbered := !opt.Terse || opt.LineNumbers
	counted := map[string]map[int]bool{}
	total := 0
	row := func(path string, line int, text string) {
		if counted[path] == nil {
			counted[path] = map[int]bool{}
		}
		if counted[path][line] {
			return
		}
		counted[path][line] = true
		// `text` plus its newline, the `line:` grep prints for -n, and the `path:` it prints
		// when it searched more than one file.
		total += len(text) + 1
		if numbered {
			total += len(strconv.Itoa(line)) + 1
		}
		if !bare {
			total += len(path) + 1
		}
	}

	for _, m := range res.Matches {
		if m.NodeHit {
			continue
		}
		ctx := res.Context[m.Path]
		for line := m.Line - opt.Before; line < m.Line; line++ {
			if text, ok := ctx[line]; ok {
				row(m.Path, line, text)
			}
		}
		row(m.Path, m.Line, m.Text)
		for line := m.Line + 1; line <= m.Line+opt.After; line++ {
			if text, ok := ctx[line]; ok {
				row(m.Path, line, text)
			}
		}
	}
	for _, path := range res.BinaryFiles {
		total += len("Binary file  matches\n") + len(path)
	}
	return total
}

// HasTextualMatch reports whether any row came from a line of source.
//
// It is what exempts a search from the over-serve ceiling. An answer built entirely from node
// names and stored descriptions is the one thing a plain grep could not have produced at all,
// so there is nothing for it to be disproportionate TO -- measuring it against zero raw bytes
// would delete the feature exactly where it is the whole answer. That answer is bounded
// already, by NodeHitBudget.
func HasTextualMatch(res *Result) bool {
	if res == nil {
		return false
	}
	for _, m := range res.Matches {
		if !m.NodeHit {
			return true
		}
	}
	return len(res.BinaryFiles) > 0
}

// WithoutTopology is the same result with every row a plain grep could not have produced taken
// out, so FormatResult renders what grep would have: textual matches, no annotation.
//
// It is the fallback for the surfaces that have no real command to run instead -- the search
// tool and `arac grep`. The truncation trailer survives, because an answer quietly shorter than
// the search found is the one failure the caller cannot see.
func WithoutTopology(res *Result) *Result {
	if res == nil {
		return nil
	}
	out := *res
	out.Matches = make([]Match, 0, len(res.Matches))
	for _, m := range res.Matches {
		if m.NodeHit {
			continue
		}
		m.ResourceID, m.Description = "", ""
		out.Matches = append(out.Matches, m)
	}
	// Annotation is decided from this count, and zero turns it off: the headers are what the
	// caller is being spared.
	out.DistinctResources = 0
	out.TitleWithheld, out.DescriptionWithheld = 0, 0
	return &out
}
