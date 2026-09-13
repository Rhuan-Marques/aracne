package universaltools

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/llm/languages/readunit"
	"github.com/Rhuan-Marques/aracne/internal/llm/languages/renderstate"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// A slice read answers "these lines of this file" the way the shell command that asked for it
// meant it, instead of substituting a question aracne would rather answer.
//
// WHY THIS IS NOT ReadWindow. ReadWindow (universal_read.go) maps a line range to the
// RESOURCES covering it and reads those whole. That is the right answer for a guard denial --
// the model asked the wrong question and gets pointed at the right one -- and the wrong answer
// for interception, where the command still has to mean what it says. `tail -2` must return
// two lines. Returning the 200-line function they sit in is not a tail.
//
// So the body here is built from three kinds of bytes and nothing else:
//
//   - the requested lines, cut verbatim from the file;
//   - the SIGNATURE lines of any declaration the window opens inside, also verbatim, so the
//     model knows what it is looking at instead of a floating fragment;
//   - elision markers, which are unmistakably not source.
//
// That is the same invariant skeleton.go documents and `edit` depends on: every byte is either
// copied from the file or is a marker no language reads as a comment. A model must never be
// able to build an `edit` old_string out of text aracne composed.

// How much of a window's own size the CONTEXT block may add, and the floor below which the
// ratio stops applying.
//
// The floor matters as much as the ratio: a three-line `tail` is exactly the case where one
// neighbour's description is most likely to answer the next question, and a strict percentage
// of three lines would leave room for nothing at all.
const (
	sliceContextPercent = 60
	sliceContextFloor   = 600
)

// ReadSlice renders lines [from, to] of a file, framed by the declarations they sit inside and
// followed by a CONTEXT section restricted to what those lines actually mention.
//
// It reuses the ordinary read pipeline rather than a parallel one: the enclosing declaration's
// Unit supplies the import block, the group header and the neighbour walk, and only the Body
// is replaced. A second renderer would drift from the first within one release.
func (r *Read) ReadSlice(path string, from, to int) (string, error) {
	topo, err := r.mgr.ReadAll()
	if err != nil {
		return "", err
	}
	fileID, ok := r.resolveFileID(topo, path)
	if !ok {
		return "", fmt.Errorf("%s is not in the topology", path)
	}
	// The requested lines are cut from disk and always right; the FRAME around them is not.
	// nodesCovering reads recorded spans, so a file changed outside the session framed its
	// window with the wrong declaration. Re-index it first. See ReadIDs' freshness note.
	if r.freshen([]string{fileID}) {
		if topo, err = r.mgr.ReadAll(); err != nil {
			return "", err
		}
	}
	total := fileLineCount(fileID)
	if total == 0 {
		return "", fmt.Errorf("%s could not be read", path)
	}
	from, to = clampWindow(from, to, total)

	entry, err := r.mgr.Cut(domain.Location{Path: fileID, StartsAt: from, EndsAt: to})
	if err != nil {
		return "", err
	}

	covering := nodesCovering(topo, fileID, from, to)
	// The anchor's context may inline neighbour source from other files; those have to be
	// current too.
	if r.freshen(touchedFiles(topo, []readunit.Unit{{Covers: coveringIDs(covering)}})) {
		if topo, err = r.mgr.ReadAll(); err != nil {
			return "", err
		}
		covering = nodesCovering(topo, fileID, from, to)
	}

	// THE PROMISE intercept_line_ranges MODE MAKES. Every context entry now advertises a span, so a
	// model that reads exactly that span must get exactly what the resource read would have
	// given it -- imports, enclosing type, full context block. When the window contains every
	// declaration it touches and cuts none of them, it IS a resource read, so serve it as one
	// rather than as a framed slice with restricted context.
	//
	// A window that slices a declaration keeps the framed form: there the restriction is
	// right, because the caller asked for part of something and the context of the whole
	// would answer a question they did not ask.
	//
	// The promotion runs through the ordinary gate, so read.kinds still decides: a window over
	// a kind the project does not allow is not promoted, and falls to the framed form below.
	// It is not refused here -- the caller asked for lines of a FILE, and cli.resolveReadOperand
	// has already applied the gate to whatever the operand named.
	if ids := promotable(covering, from, to); len(ids) > 0 {
		if out, err := r.ReadIDs(ids, ReadIDsOptions{}); err == nil &&
			strings.TrimSpace(out) != "" {
			return out, nil
		}
	}

	body := sliceBody(r, covering, entry.Cut, from, to, locator(topo, r.cfgOrLoad()))

	// A window's context section is restricted to the resources the window MENTIONS
	// (RestrictTo below), so the fill is restricted to the same set: describing what the
	// enclosing declaration reaches but this window never names would be paying for lines
	// that are about to be filtered out. The body is unaffected by descriptions, so it is
	// computed once and kept across the refresh.
	mentioned := mentionedResourceIDs(topo, body)
	if r.lazy.FillForReadIn(topo, coveringIDs(covering), mentioned) {
		if refreshed, rErr := r.mgr.ReadAll(); rErr == nil {
			topo = refreshed
			covering = nodesCovering(topo, fileID, from, to)
		}
	}

	unit := r.sliceUnit(topo, fileID, covering, body)
	if r.cfgOrLoad().ContextOff() {
		unit.Context = nil // context_filter "off": the lines asked for, nothing around them
	}
	unit.Label = displayPath(topo, unit.Path)
	unit.Imports = dropImportsAlreadyShown(unit.Imports, body)
	unit.Deps = dropImportsAlreadyShown(unit.Deps, body)

	st := renderstate.New()
	st.RestrictTo(mentioned)
	// Bound the context against the WINDOW, not against the response.
	//
	// renderstate's default budget (24 KB) is sized for a resource read, where the body is a
	// whole declaration. A window can be two lines, and a two-line window carrying twelve
	// neighbours has inverted the request: the annotation is the answer and the answer is a
	// footnote. Measured across twelve 40-line windows the context ran 16.4% of what the plain
	// command would have printed, so this ceiling does not bind the ordinary case -- it exists
	// to stop the tail, where one window sat at 1.86x almost entirely on context.
	//
	// Truncation is not silent: renderstate.Trailer names how many neighbours were withheld,
	// and each is still one `arac read` away.
	if budget := len(body) * sliceContextPercent / 100; budget < st.MaxBytes {
		st.MaxBytes = max(budget, sliceContextFloor)
	}
	cfg := r.cfg
	if cfg == nil {
		cfg = helper.LoadConfig(helper.ConfigPath(r.mgr.DbPath()))
	}
	out := readunit.Render([]readunit.Unit{unit}, readunit.Options{
		IncludeIncoming: cfg.EffectiveIncludeIncoming(),
		State:           st,
		Locate:          locator(topo, cfg),
	})
	if strings.TrimSpace(out) == "" {
		return "", fmt.Errorf("nothing to render for %s:%d-%d", path, from, to)
	}
	return out, nil
}

// BodyBounds returns the file and the line range of a resource's BODY -- its span minus the
// signature lines that introduce it.
//
// This is what lets `head -20 app.Flask` mean "the first 20 lines of the class", the way
// `head -20 app.py` means "the first 20 lines of the file". Counting the signature against the
// caller's N would make a one-line `head` return nothing but the declaration it asked to look
// past, and the signature is shown regardless as framing.
func (r *Read) BodyBounds(id string) (path string, from, to int, err error) {
	target, _, rerr := resolveReadTargetWith(r.mgr, id, func(domain.Resource) bool { return true })
	if rerr != nil {
		return "", 0, 0, rerr
	}
	// The bounds ARE a recorded span, so a file changed outside the session windowed
	// `head -20 <id>` against lines that no longer hold the resource. Re-index and re-resolve.
	if r.freshen([]string{target.res.Location.Path}) {
		if target, _, rerr = resolveReadTargetWith(r.mgr, id, func(domain.Resource) bool { return true }); rerr != nil {
			return "", 0, 0, rerr
		}
	}
	loc := target.res.Location
	if loc.Path == "" || loc.StartsAt < 1 || loc.EndsAt < loc.StartsAt {
		return "", 0, 0, fmt.Errorf("resource %q has no source location", id)
	}
	// A file node carries the whole file and has no signature to skip.
	if target.res.Kind == domain.ResourceFile {
		return loc.Path, 1, fileLineCount(loc.Path), nil
	}
	entry, cutErr := r.mgr.Cut(loc)
	if cutErr != nil {
		return "", 0, 0, cutErr
	}
	head, _ := splitSignature(entry.Cut)
	bodyStart := loc.StartsAt + countLines(head)
	if bodyStart > loc.EndsAt {
		// A one-line declaration is all signature; the body IS the line.
		bodyStart = loc.StartsAt
	}
	return loc.Path, bodyStart, loc.EndsAt, nil
}

// sliceBody assembles the three permitted kinds of bytes in source order.
//
// Every file line is written at most once. A frame's signature is shown only up to the line
// before the window -- a multi-line signature the window opens inside used to be printed whole
// and then again as the window's own first lines -- and a line an enclosing frame's signature
// already showed is not shown again. Each marker counts the lines between the last line
// written and the NEXT one, so a nested frame's signature is never also counted as hidden
// inside its parent.
func sliceBody(r *Read, covering []domain.Resource, cut string, from, to int, loc func(string) string) string {
	var b strings.Builder
	last := 0    // the last file line written above the window
	openID := "" // the innermost frame written so far: the owner of the lines after `last`
	for _, res := range covering {
		if res.Location.StartsAt >= from {
			continue // starts inside the window: its own first line is already shown
		}
		entry, err := r.mgr.Cut(res.Location)
		if err != nil {
			continue // one unreadable frame must not cost the model its window
		}
		head, _ := splitSignature(entry.Cut)
		for k, text := range strings.Split(strings.TrimRight(head, "\n"), "\n") {
			n := res.Location.StartsAt + k
			if n >= from {
				break // the window prints it
			}
			if n <= last {
				continue // an enclosing frame's signature already printed it
			}
			if hidden := n - last - 1; hidden > 0 && openID != "" {
				b.WriteString(marker(openID, hidden, loc))
			}
			b.WriteString(text)
			b.WriteString("\n")
			last = n
		}
		openID = res.ID
	}
	if hidden := from - last - 1; hidden > 0 && openID != "" {
		b.WriteString(marker(openID, hidden, loc))
	}
	b.WriteString(strings.TrimRight(cut, "\n"))
	b.WriteString("\n")
	for _, res := range covering {
		if hidden := res.Location.EndsAt - to; hidden > 0 {
			b.WriteString(marker(res.ID, hidden, loc))
			break // one trailing marker is enough; nested nodes end together
		}
	}
	return b.String()
}

// marker is the window's own elision line, deliberately much shorter than
// renderstate.ElisionMarker.
//
// MEASURED. Over twelve 40-line windows of a real repository the shared marker accounted for
// 10.8% of the bytes the plain `sed` would have printed -- the single largest piece of the
// overhead after the requested lines themselves. It costs that much because it is built for a
// different job: it names the id TWICE and tells the reader to "read %q for its source",
// which is the right instruction when a marker stands in for a resource the response chose
// not to inline, and redundant here. A caller looking at lines 95-135 of a file it named
// already knows where the rest is; widening the range is one edit to a command it just typed.
//
// What survives is the part that is load-bearing: the leading U+22EF, which opens no comment
// in any language aracne scans, so this can never be mistaken for source and pasted into an
// `edit` old_string.
// The id is shortened to its last segment. A Rust or Java id is most of a line on its own
// (`clap::parse::parser::Parser::possible_long_flag_subcommand` is 56 characters), and the
// full path buys nothing a reader cannot see: the declaration's own signature sits directly
// above the marker, and the contract already tells the model a unique trailing part resolves.
func marker(id string, lines int, loc func(string) string) string {
	// Under intercept_line_ranges identification the marker names the span, because that is a command
	// the reader can run. Naming the id there would hand back the one token this mode exists
	// to stop advertising.
	if loc != nil {
		if span := loc(id); span != "" {
			return fmt.Sprintf("⋯ +%d lines — whole declaration at %s ⋯\n", lines, span)
		}
	}
	return fmt.Sprintf("⋯ +%d lines of %s ⋯\n", lines, lastIDSegment(id))
}

// cfgOrLoad returns the tool's config, loading it if the caller never supplied one.
func (r *Read) cfgOrLoad() *helper.Config {
	if r.cfg != nil {
		return r.cfg
	}
	return helper.LoadConfig(helper.ConfigPath(r.mgr.DbPath()))
}

// sliceUnit picks the Unit whose imports, group header and neighbour walk frame this window,
// and swaps in the windowed body.
//
// The anchor is the INNERMOST covering declaration, because its neighbours are the closest
// thing to the window's own neighbours. With nothing declared across the window -- a license
// header, an import block, a data table -- the file node is the honest anchor.
func (r *Read) sliceUnit(topo *domain.Topology, fileID string, covering []domain.Resource, body string) readunit.Unit {
	// The anchor's Unit is built against a THROWAWAY state: its builder consults the state to
	// decide whether to inline an enclosing type into the body, and that body is discarded
	// here. Letting it write into the real ledger would leave a "shown above" back-reference
	// pointing at source this response never showed.
	scratch := renderstate.New()
	filter := filterOption(r.mgr)

	if len(covering) > 0 {
		inner := covering[len(covering)-1]
		if u, _, err := r.buildUnit(topo, readTarget{id: inner.ID, res: inner}, filter, scratch,
			helper.FileModeFull, 0); err == nil {
			u.Body = body
			u.Path = fileID
			u.Line = inner.Location.StartsAt
			u.Covers = coveringIDs(covering)
			return u
		}
	}
	// File anchor: no per-language context, just the neighbours of what the file declares.
	members := domain.FileMembers(topo, fileID)
	u := readunit.Unit{
		ID:     fileID,
		Kind:   domain.ResourceFile,
		Path:   fileID,
		Line:   1,
		Body:   body,
		Covers: append(coveringIDs(covering), members...),
	}
	u.Context = neighborContext(domain.OutgoingNeighbors(topo, members, map[string]bool{fileID: true}),
		r.cfgOrLoad().EffectiveContextFilter())
	return u
}

// whollyContained returns the covering declarations when the window is EXACTLY them, and nil
// otherwise.
//
// Two conditions, and the second one is the whole point. Every covering declaration must fit
// inside the window -- otherwise the caller asked for part of something, and promoting would
// hand back the whole of it. And the window must hold nothing BUT those declarations: every
// line of it has to belong to one.
//
// THE SECOND CONDITION WAS MISSING, and it cost the caller lines they had asked for. A Go
// file's package clause and import block belong to no declaration, so `head -12 f.go` -- a
// window whose first eight lines are exactly that preamble -- satisfied "every covering
// declaration fits" on the strength of one interface at lines 9-11, promoted to a read of
// that interface, and returned three lines where twelve were asked for. Nothing marked the
// other nine as missing, because as far as the promoted read was concerned they were never
// requested.
func whollyContained(covering []domain.Resource, from, to int) []string {
	if len(covering) == 0 {
		return nil
	}
	sorted := append([]domain.Resource(nil), covering...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Location.StartsAt != sorted[j].Location.StartsAt {
			return sorted[i].Location.StartsAt < sorted[j].Location.StartsAt
		}
		return sorted[i].Location.EndsAt > sorted[j].Location.EndsAt
	})
	out := make([]string, 0, len(sorted))
	// next is the first line of the window not yet accounted for by a declaration.
	next := from
	for _, res := range sorted {
		if res.Location.StartsAt < from || res.Location.EndsAt > to {
			return nil // the window cuts this declaration
		}
		if res.Location.StartsAt > next {
			return nil // a line of the window belongs to no declaration
		}
		if res.Location.EndsAt >= next {
			next = res.Location.EndsAt + 1
		}
		out = append(out, res.ID)
	}
	if next <= to {
		return nil // the window runs past the last declaration
	}
	return out
}

// promotable returns the declarations a window should be served as a resource read of, or nil
// when it must stay a framed slice.
//
// whollyContained is the general rule. The second case is the one it cannot see: a window that
// is EXACTLY one declaration's span, where that declaration sits inside another -- a method in
// a Python, JS or Java class. The class covers the window without fitting in it, so the window
// looked like a slice of the class. But that span is precisely what intercept_line_ranges
// advertises for the method, in every context entry and marker that names it; reading it is
// how a model in that mode reads the method, and the mode promises it gets the method's
// resource read, enclosing type included. The enclosing declarations are set aside and the
// window is judged on what lies inside it.
func promotable(covering []domain.Resource, from, to int) []string {
	if ids := whollyContained(covering, from, to); len(ids) > 0 {
		return ids
	}
	for _, res := range covering {
		if res.Location.StartsAt != from || res.Location.EndsAt != to {
			continue
		}
		var inside []domain.Resource
		for _, c := range covering {
			if c.Location.StartsAt >= from && c.Location.EndsAt <= to {
				inside = append(inside, c)
			}
		}
		return whollyContained(inside, from, to)
	}
	return nil
}

// coveringIDs lists the ids rendered (whole or in part) by this body, so the context section
// never lists something the reader is already looking at.
func coveringIDs(covering []domain.Resource) []string {
	out := make([]string, 0, len(covering))
	for _, res := range covering {
		out = append(out, res.ID)
	}
	return out
}

// nodesCovering returns every declaration in a file overlapping [start, end], outermost first.
//
// Distinct from nodesSpanning, which caps at six and sorts by id: that shape is right for an
// error message listing candidates and wrong here, where the order IS the nesting (a class
// before its method) and a dropped entry is a missing frame.
func nodesCovering(topo *domain.Topology, fileID string, start, end int) []domain.Resource {
	var hits []domain.Resource
	for _, res := range topo.Resources {
		if res.Kind == domain.ResourceFile || !declaredIn(fileID, res) {
			continue
		}
		if res.Location.StartsAt <= end && res.Location.EndsAt >= start {
			hits = append(hits, res)
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		a, b := hits[i].Location, hits[j].Location
		if a.StartsAt != b.StartsAt {
			return a.StartsAt < b.StartsAt
		}
		if a.EndsAt != b.EndsAt {
			return a.EndsAt > b.EndsAt // the wider span encloses the narrower one
		}
		return hits[i].ID < hits[j].ID
	})
	return hits
}

// mentionedResourceIDs is the CONTEXT restriction: the resources whose names actually appear
// in the bytes being shown.
//
// The window is a slice of a declaration, but the declaration's neighbours are the neighbours
// of the WHOLE thing. Rendering all of them answers a question nobody asked -- `tail -2` of a
// 200-line function would return the context of all 200 lines, which is both the largest part
// of the response and the least relevant. Matching on identifier tokens is deliberately
// language-neutral: it needs no per-language reference index, and it is the same test a reader
// applies -- "is this name on my screen?".
func mentionedResourceIDs(topo *domain.Topology, body string) map[string]bool {
	tokens := identifierTokens(body)
	if len(tokens) == 0 {
		return map[string]bool{}
	}
	allowed := make(map[string]bool)
	for id, res := range topo.Resources {
		if res.Name != "" && tokens[res.Name] {
			allowed[id] = true
			continue
		}
		if tokens[lastIDSegment(id)] {
			allowed[id] = true
		}
	}
	return allowed
}

// identifierTokens splits text into the identifier-shaped runs a reader would recognize as
// names. Everything else -- punctuation, string delimiters, digits leading a token -- is a
// separator.
func identifierTokens(text string) map[string]bool {
	out := make(map[string]bool)
	field := func(r rune) bool {
		return !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9')
	}
	for _, tok := range strings.FieldsFunc(text, field) {
		if tok != "" {
			out[tok] = true
		}
	}
	return out
}

// lastIDSegment returns the final name in a resource ID across every scheme aracne mints:
// Go's `pkg.(Recv).Method`, Python's dotted module path, Rust's `crate::mod::Type`, Java's
// `Owner.name(sig)`.
func lastIDSegment(id string) string {
	// A Java id carries its signature at the END (`com.Foo.bar(int,int)`); a Go or Python
	// method id carries its receiver in the MIDDLE (`pkg.(Circle).Area`). Cutting at the FIRST
	// '(' handled the Java case and destroyed the other: it left `pkg.`, which then split to
	// the empty string, and the marker read `⋯ +1 lines of  ⋯`. Anchoring the strip to a
	// trailing ')' keeps both.
	if strings.HasSuffix(id, ")") {
		if i := strings.LastIndexByte(id, '('); i > 0 {
			id = id[:i]
		}
	}
	for _, sep := range []string{"::", ".", "/", "#", "$"} {
		if i := strings.LastIndex(id, sep); i >= 0 {
			id = id[i+len(sep):]
		}
	}
	return strings.Trim(id, "()")
}

// dropImportsAlreadyShown removes import tokens whose text is already inside the window, so a
// `head -20` covering a file's own import block does not get it printed twice.
func dropImportsAlreadyShown(imports []string, body string) []string {
	if len(imports) == 0 {
		return imports
	}
	out := imports[:0:0]
	for _, imp := range imports {
		if imp != "" && !strings.Contains(body, strings.Trim(imp, `"`)) {
			out = append(out, imp)
		}
	}
	return out
}

// resolveFileID maps a path as typed onto the file node's id (an absolute path), so a relative
// operand from an agent's shell still finds its file.
func (r *Read) resolveFileID(topo *domain.Topology, path string) (string, bool) {
	root := ""
	if topo != nil {
		root = topo.Root
	}
	for _, cand := range readPathCandidates(helper.NormalizeResourceID(path), root) {
		if _, ok := topo.Resources[cand]; ok {
			return cand, true
		}
		// Abs of a candidate, not only of the input: a topology root that is itself relative
		// makes the joined form relative too. The canonical spellings are already in the
		// candidate list; see helper.PathCandidates.
		if abs, err := filepath.Abs(cand); err == nil {
			if _, ok := topo.Resources[abs]; ok {
				return abs, true
			}
		}
	}
	return "", false
}

// clampWindow keeps a requested range inside the file. A command that asks for more lines than
// exist is not an error -- `head -1000` on a 40-line file prints 40 -- so neither is this.
func clampWindow(from, to, total int) (int, int) {
	if from < 1 {
		from = 1
	}
	if to > total {
		to = total
	}
	if to < from {
		to = from
	}
	return from, to
}

// fileLineCount counts the lines on disk, or 0 when the file cannot be read.
//
// Deliberately os.ReadFile and not helper.ReadRawFile: the latter prepends the basename as a
// header line, which would put every window off by one.
func fileLineCount(path string) int {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return 0
	}
	n := strings.Count(string(data), "\n")
	if data[len(data)-1] != '\n' {
		n++
	}
	return n
}
