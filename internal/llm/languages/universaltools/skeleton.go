package universaltools

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/llm/languages/readunit"
	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// skeletonBody renders a whole-file read as the file's shape rather than its text: every
// top-level declaration in source order, each shown in full when it is short and reduced to
// its signature plus an elision marker when it is not.
//
// WHY THIS EXISTS. A benchmark run measured whole-file reads returning 1.00x the bytes on
// disk, with the "# CONTEXT:" block added on top -- so a file read through aracne cost
// strictly more than `cat` and the context block was the only thing it bought. Symbol reads
// were already compact; only the file path was not.
//
// THE CONSTRAINT THAT SHAPES EVERYTHING HERE. The `edit` tool matches old_string against the
// bytes on disk. So every byte this function emits is either copied verbatim from the file or
// is an unmistakable marker -- there is no third category. A "reconstructed" or "summarized"
// signature would be exactly the text a model would paste into old_string and get a
// confusing miss from, which is why the elision marker (renderstate.ElisionMarker) opens with
// a character that begins no comment in any language aracne scans.
func skeletonBody(topo *domain.Topology, mgr *topology.TopologyManager, fileID string,
	smallThreshold int, loc func(string) string) (string, error) {

	entry, err := mgr.Cut(domain.Location{Path: fileID})
	if err != nil {
		return "", err
	}
	members := domain.FileTopLevelMembers(topo, fileID)
	if len(members) == 0 {
		// Nothing is declared here that the topology knows about -- a config file, an
		// unsupported language. There is no skeleton to render, so the file itself is the
		// only honest answer.
		return entry.Cut, nil
	}

	lines := strings.Split(strings.TrimRight(entry.Cut, "\n"), "\n")
	elisions := elidableSpans(topo, mgr, members, len(lines), smallThreshold)

	var b strings.Builder
	for ln := 1; ln <= len(lines); {
		span, ok := elisions[ln]
		if !ok {
			// Not the start of an elided declaration: the line is emitted verbatim,
			// whatever it is. THIS IS THE PART THAT USED TO BE MISSING. The old renderer
			// walked the DECLARATIONS and emitted only those, so everything between them
			// -- the package clause, the import block, a module docstring, every doc
			// comment, the license header -- vanished with no marker to say so. Those are
			// the first lines anyone reads a file for, and a model has no way to notice
			// they were never shown.
			b.WriteString(lines[ln-1])
			b.WriteString("\n")
			ln++
			continue
		}
		for l := span.start; l <= span.headEnd; l++ {
			b.WriteString(lines[l-1])
			b.WriteString("\n")
		}
		b.WriteString(marker(span.id, span.end-span.headEnd, loc))
		ln = span.end + 1
	}
	return b.String(), nil
}

// elidedSpan is one declaration whose body the skeleton replaces with a marker.
type elidedSpan struct {
	id      string
	start   int
	headEnd int // the last line of the signature, shown verbatim
	end     int
}

// elidableSpans picks the declarations worth eliding and returns them keyed by their first
// line, so the walk above can recognize one in constant time.
//
// NESTED MEMBERS ARE DROPPED, and that is not an optimization. A Python class and its methods
// both arrive as members of the file, so the old renderer emitted the class as "signature plus
// a marker saying 13 lines of body are not shown" and then emitted those same 13 lines as the
// methods -- a marker that contradicted the answer directly beneath it, telling the reader to
// go and fetch something they already had. A declaration inside another is part of its
// parent's span and is rendered, or elided, with it.
func elidableSpans(topo *domain.Topology, mgr *topology.TopologyManager, members []string,
	totalLines, smallThreshold int) map[int]elidedSpan {

	type span struct {
		id         string
		start, end int
	}
	var spans []span
	for _, id := range members {
		res, ok := topo.Resources[id]
		if !ok {
			continue
		}
		loc := res.Location
		if loc.StartsAt < 1 || loc.EndsAt < loc.StartsAt || loc.StartsAt > totalLines {
			continue
		}
		spans = append(spans, span{id: id, start: loc.StartsAt, end: min(loc.EndsAt, totalLines)})
	}
	// Outermost first, so a nested member is recognized as nested rather than the other way
	// round.
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].start != spans[j].start {
			return spans[i].start < spans[j].start
		}
		return spans[i].end > spans[j].end
	})

	out := map[int]elidedSpan{}
	covered := 0 // the last line already claimed by an outer declaration
	for _, sp := range spans {
		if sp.start <= covered {
			continue // nested inside one already handled
		}
		covered = sp.end
		if sp.end-sp.start+1 <= smallThreshold {
			continue // short enough to show whole; the walk emits it line by line
		}
		entry, err := mgr.Cut(domain.Location{Path: topo.Resources[sp.id].Location.Path,
			StartsAt: sp.start, EndsAt: sp.end})
		if err != nil {
			continue // one unreadable member must not cost the model the whole file
		}
		head, _ := splitSignature(entry.Cut)
		headEnd := sp.start + countLines(head) - 1
		if headEnd < sp.start {
			headEnd = sp.start
		}
		if headEnd >= sp.end {
			continue // all signature: there is no body to elide
		}
		out[sp.start] = elidedSpan{id: sp.id, start: sp.start, headEnd: headEnd, end: sp.end}
	}
	return out
}

// splitSignature returns the leading lines of a declaration to show verbatim, and how many
// lines were left out.
//
// The split is heuristic because the topology stores no body-start offset: a resource carries
// only StartsAt/EndsAt. It leans toward showing MORE, because over-inclusion costs tokens
// while under-inclusion could cut a signature mid-way. Correctness does not rest on it -- the
// elision marker does, since whatever this returns is verbatim file bytes either way.
func splitSignature(cut string) (head string, elidedLines int) {
	lines := strings.Split(strings.TrimRight(cut, "\n"), "\n")
	if len(lines) == 0 {
		return "", 0
	}
	for i, line := range lines {
		t := strings.TrimRight(strings.TrimSpace(line), " \t")
		// A DOC COMMENT is not the signature, and its text is not evidence about where the
		// body starts. `// TODO:` and `/// Panics:` end in a colon, which made the head the
		// comment alone and swallowed the declaration itself into the elision -- a skeleton
		// showing a comment where a signature should be.
		if IsCommentLine(t) {
			continue
		}
		// A brace or colon at end of line opens the body in every language aracne scans.
		if strings.HasSuffix(t, "{") || strings.HasSuffix(t, ":") {
			return strings.Join(lines[:i+1], "\n"), len(lines) - i - 1
		}
		// A signature this long is no longer a signature -- stop guessing, and fall through
		// to the DECLARATION LINE alone below.
		//
		// One line, not nine, and that is deliberate: the brace is not always at end of
		// line (a trailing comment is enough to hide it), so a nine-line fallback shows
		// eight lines of BODY for every such declaration -- which is worse than one line of
		// signature, and is what the elision marker beneath it exists to stand in for.
		if i >= 8 {
			break
		}
	}
	return lines[0], len(lines) - 1
}

// IsCommentLine reports whether an already-trimmed line opens or continues a comment in any
// language aracne scans. Used to keep a declaration's doc comment out of the signature split,
// and -- by warnread -- to keep a doc comment that merely MENTIONS a missing symbol from being
// mistaken for the reference that is broken.
func IsCommentLine(trimmed string) bool {
	switch {
	case strings.HasPrefix(trimmed, "//"), strings.HasPrefix(trimmed, "/*"),
		strings.HasPrefix(trimmed, "#"):
		return true
	case trimmed == "*" || strings.HasPrefix(trimmed, "* ") || strings.HasPrefix(trimmed, "*/"):
		return true
	}
	return false
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(strings.TrimRight(s, "\n"), "\n") + 1
}

func pluralLines(n int) string {
	if n == 1 {
		return "1 line"
	}
	return itoa(n) + " lines"
}

// itoa avoids pulling strconv in for one call site in a file that otherwise only formats.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// abridgeSymbolBody caps ONE symbol's source the way skeletonBody caps a file's declarations.
//
// WHY THIS EXISTS. Every read path had a ceiling except the one the tool actively encourages.
// read.max_file_size bounds a file; file_mode "skeleton" bounds a whole-file read; nothing at
// all bounded a symbol. Reading `['into_config', 'EngineState.merge_env']` out of nushell
// returned 75,893 bytes in compact-blocked-after-bs-20260830c -- the largest single tool
// result across two benchmark runs -- because `into_config` is one enormous function and a
// symbol read hands back the whole thing.
//
// Files are left alone here: fileUnit has already applied file_mode to them, and re-abridging
// a skeleton would elide the signatures that ARE the skeleton. The marker names the id and the
// `full: true` escape hatch, so the exact bytes are always one call away.
//
// ANNOTATIONS CHANGE THE SHAPE OF THE CUT, not whether it happens. A caller that asked for a
// line to be marked asked for that line to be VISIBLE, and "signature plus a marker" is
// precisely the answer that hides it -- a warning 300 lines into a long function would be
// reported by showing a signature the warned line is not in. An annotated over-cap body keeps
// the signature plus a band around each anchor instead. An empty anns takes the original path
// byte for byte, which is every read in the product that is not a warning report.
func abridgeSymbolBody(u readunit.Unit, maxLines int, anns []readunit.Annotation, window int) string {
	if maxLines <= 0 || u.Kind == domain.ResourceFile || countLines(u.Body) <= maxLines {
		return u.Body
	}
	// A method's body is NOT one declaration: the enclosing type is inlined above it, so
	// splitting at the first opening brace would keep the struct and elide the method the
	// model actually asked for. Abridge the LAST top-level declaration instead, which is the
	// target in every rendering this package produces.
	lead, tail := splitLastDeclaration(u.Body)
	head, elided := splitSignature(tail)
	if elided <= 0 {
		return u.Body
	}
	if windowed, ok := windowTail(tail, head, anns, window, u.ID); ok {
		return lead + windowed
	}
	if !strings.HasSuffix(head, "\n") {
		head += "\n"
	}
	return lead + head + abridgedMarker(u.ID, elided)
}

// signatureOnlyBody cuts a symbol's body down to its signature, for ReadIDsOptions.SignatureOnly.
//
// Same split as abridgeSymbolBody, so a method keeps the enclosing type rendered above it: the
// render ledger has already recorded that type as shown, and a later method of it would
// back-reference a declaration that was never printed. ok is false when there is no body to
// leave out -- a one-line declaration is its own signature.
func signatureOnlyBody(u readunit.Unit) (string, bool) {
	lead, tail := splitLastDeclaration(u.Body)
	head, elided := splitSignature(tail)
	if elided <= 0 {
		return "", false
	}
	if !strings.HasSuffix(head, "\n") {
		head += "\n"
	}
	return lead + head + signatureOnlyMarker(u.ID, elided), true
}

// signatureOnlyMarker stands in for a body left out because only the signature was asked for.
// Unlike abridgedMarker, reading the id again DOES return the body -- this response chose to
// omit it, the read did not cap it -- so that is the way back it names, worded like
// renderstate.ElisionMarker so it holds under either tool name (MCP read or `arac read`).
func signatureOnlyMarker(id string, elided int) string {
	return fmt.Sprintf("⋯ body not shown (%s) — read %s for its source ⋯\n\n", pluralLines(elided), id)
}

// windowTail keeps the signature, a band of `radius` lines either side of every anchored line,
// and the declaration's closing line -- eliding the runs between them.
//
// ok is false when there is nothing to window: no anchors, radius 0, or no anchor found in
// this body. That is the caller's signal to fall back to the plain marker rather than invent a
// shape, and it is what makes an ordinary read identical to what it was before.
//
// A KEEP-MASK rather than interval arithmetic, because overlap is the common case and a mask
// merges it for free: two anchors four lines apart with radius six are one contiguous run, no
// sorting and no boundary conditions to get wrong.
func windowTail(tail, head string, anns []readunit.Annotation, radius int, id string) (string, bool) {
	if len(anns) == 0 || radius <= 0 {
		return "", false
	}
	lines := strings.Split(strings.TrimRight(tail, "\n"), "\n")
	keep := make([]bool, len(lines))

	// The signature always survives, exactly as it does on the unwindowed path.
	for i := 0; i < countLines(head) && i < len(keep); i++ {
		keep[i] = true
	}

	found := false
	for _, a := range anns {
		i := readunit.AnchorIndex(lines, a)
		if i < 0 {
			continue
		}
		found = true
		for j := max(0, i-radius); j <= min(len(lines)-1, i+radius); j++ {
			keep[j] = true
		}
	}
	if !found {
		return "", false
	}

	// The closing line. A body that stops mid-air reads as truncated output rather than as an
	// elision, and the closer costs one line. In Python there is no closer and this keeps the
	// final statement, which is just as useful and needs no per-language branch.
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			keep[i] = true
			break
		}
	}

	// Pinhole gaps. An elision marker costs a line of its own, so standing it in for one or
	// two real lines is a net loss that reads as noise.
	for i := 0; i < len(keep); i++ {
		if keep[i] {
			continue
		}
		j := i
		for j < len(keep) && !keep[j] {
			j++
		}
		if j-i <= 2 {
			for k := i; k < j; k++ {
				keep[k] = true
			}
		}
		i = j
	}

	var b strings.Builder
	elided := 0
	for i := 0; i < len(lines); i++ {
		if keep[i] {
			b.WriteString(lines[i])
			b.WriteString("\n")
			continue
		}
		j := i
		for j < len(lines) && !keep[j] {
			j++
		}
		elided += j - i
		// Indented to match the code around it, so the marker sits inside the block rather
		// than jutting out to column zero.
		b.WriteString(gapIndent(lines, i, j))
		b.WriteString(windowElision(j - i))
		b.WriteString("\n")
		i = j - 1
	}
	if elided == 0 {
		return "", false
	}
	b.WriteString("\n")
	b.WriteString(abridgedMarker(id, elided))
	return b.String(), true
}

// windowElision stands in for one run of lines a WINDOWED abridgement skipped over.
//
// Short on purpose: it can appear several times in one body, and the only thing the reader
// needs at each gap is its size. What was elided, which cap did it, and the way back to the
// exact bytes are said once, by abridgedMarker, under the whole body. It keeps the leading
// U+22EF for the reason renderstate.ElisionMarker documents -- a marker styled like a
// plausible comment invites a model to build an `edit` old_string out of text that was never
// in the file.
func windowElision(n int) string {
	return fmt.Sprintf("⋯ %s not shown ⋯", pluralLines(n))
}

// gapIndent is how far in an elision marker for lines[start:end) should sit: the DEEPER of the
// code on either side of it.
//
// Not simply the following line's indent, which reads wrong for the last gap in a body -- the
// line after it is the closing brace at column zero, so the marker standing in for fifty lines
// of function body would jut out to the margin as though it were a sibling declaration.
func gapIndent(lines []string, start, end int) string {
	before := indentAt(lines, start-1, -1)
	after := indentAt(lines, end, 1)
	if len(before) > len(after) {
		return before
	}
	return after
}

// indentAt is the leading whitespace of the first non-blank line from i, walking in step.
func indentAt(lines []string, i, step int) string {
	for ; i >= 0 && i < len(lines); i += step {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		return lines[i][:len(lines[i])-len(strings.TrimLeft(lines[i], " \t"))]
	}
	return ""
}

// abridgedMarker stands in for the body a symbol read left out over the line cap.
//
// Not renderstate.ElisionMarker: that one ends "read <id> for its source", which is the right
// instruction for source this response merely chose to put elsewhere and a dead end here --
// reading the same id again returns the same abridged body. The way back to the exact bytes is
// the `full` flag, so that is the only thing this marker names. It keeps the leading U+22EF,
// which opens no comment in any language aracne scans.
func abridgedMarker(id string, elided int) string {
	return fmt.Sprintf("⋯ %s of %s not shown (over the read.max_symbol_lines cap) — read it again "+
		"with full: true (`arac read --full` on the command line) for the exact bytes ⋯\n\n",
		pluralLines(elided), id)
}

// declStart matches the first token of a declaration in the languages aracne scans. Leading
// whitespace is allowed on purpose: Python renders a method INSIDE its class, so the
// declaration that matters is indented.
var declStart = regexp.MustCompile(`^\s*(?:pub\s+|pub\([^)]*\)\s+|async\s+|unsafe\s+|export\s+|default\s+|static\s+|final\s+|public\s+|private\s+|protected\s+|abstract\s+)*(?:func|fn|def|class|type|struct|enum|trait|impl|interface|const|var|let|function)\b`)

// splitLastDeclaration cuts a rendered body into everything before its LAST declaration and
// that declaration itself. A body with only one declaration returns ("", body), which is the
// single-symbol case and behaves exactly as no split at all.
//
// The last one, not the first, because a rendered body is not always one declaration: a method
// carries its enclosing type above it (Go, Rust) or around it (Python). Splitting at the first
// opening brace would keep the type and elide the method the model actually asked for -- the
// exact opposite of the point.
//
// An INDENTED declaration counts only when the line that structurally encloses it -- the nearest
// line above with less indentation -- opens a container (see enclosedByContainer). Column zero
// would be wrong for Python, where the method is always indented inside its class and the class
// line would win every time. But ignoring indentation outright let a local statement win: a Go
// `var x T` sixty lines into a function moved the split there, and everything above it became
// `lead`, which is printed in full and never windowed. One local `var` turned a capped method
// into its first hundred lines, and a warning report aimed at line 95 carried all 95.
func splitLastDeclaration(body string) (lead, last string) {
	lines := strings.Split(body, "\n")
	idx := 0
	for i, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if declStart.MatchString(l) && enclosedByContainer(lines, i) {
			idx = i
		}
	}
	if idx == 0 {
		return "", body
	}
	return strings.Join(lines[:idx], "\n") + "\n", strings.Join(lines[idx:], "\n")
}

// containerDecl matches a declaration whose body holds other declarations: the only thing an
// indented declaration may sit inside and still be a member rather than a local.
var containerDecl = regexp.MustCompile(`^\s*(?:pub\s+|pub\([^)]*\)\s+|unsafe\s+|export\s+|default\s+|static\s+|final\s+|public\s+|private\s+|protected\s+|abstract\s+)*(?:class|struct|enum|trait|impl|interface)\b`)

// enclosedByContainer reports whether lines[i] is at the top level of the body, or sits directly
// inside a class-like declaration. The enclosing line is the nearest non-blank line above with
// less indentation, which is a structural parent in every language aracne renders -- a
// function signature, an `if`, the `) {` closing a multi-line signature, or a class header.
// Only the last of those makes an indented `def`/`fn`/`const` a member; the rest make it a local.
func enclosedByContainer(lines []string, i int) bool {
	indent := leadingWhitespace(lines[i])
	if indent == 0 {
		return true
	}
	for j := i - 1; j >= 0; j-- {
		if strings.TrimSpace(lines[j]) == "" || leadingWhitespace(lines[j]) >= indent {
			continue
		}
		return containerDecl.MatchString(lines[j])
	}
	return true // nothing encloses it: the body itself starts indented
}

func leadingWhitespace(s string) int {
	return len(s) - len(strings.TrimLeft(s, " \t"))
}
