// Package topogrep is content search that annotates hits with the topology resource
// they fall inside.
//
// SIZING MATTERS HERE. This is the highest-volume tool aracne exposes, and the shipped
// agent contract tells the model to use it instead of its native grep. Measured against
// `grep -rn` on cli/cli, the previous implementation emitted 2.26x the bytes for
// `'func '`, 2.58x for `'err'` (3.1 MB from a single call) and 1.84x for `'Options'` —
// while offering none of a real grep's ways to ask for less: no glob, no file-type
// filter, no case-insensitivity, no output modes, no result cap, and no truncation at
// all. Being both bigger AND less capable than the tool it replaces is why the aracne arm
// spent more context than the baseline on discovery.
//
// Two rules follow, and the code below enforces them:
//
//   - Never return unbounded output. Every result is capped and says so.
//   - Pay for topology annotation ONCE per resource, not once per matching line.
//
// The search itself spans three tiers, in priority order: a node whose TITLE matches,
// a node whose stored DESCRIPTION matches, then a matching LINE. The description tier
// is the one a plain grep cannot reach at all -- that prose lives only in the topology
// DB -- so `retry` finds a function documented as "retries on 5xx" whose code never
// says "retry". A node is reported once, never twice: a title match outranks a
// description match, and a node whose body already produced a line hit is promoted to
// its tier rather than given a second row of its own.
package topogrep

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// OutputMode selects how much of a hit is rendered.
type OutputMode string

const (
	// OutputContent renders matching lines (the default).
	OutputContent OutputMode = "content"
	// OutputFiles renders only the paths that contain a match — the cheapest way to
	// scope a follow-up search.
	OutputFiles OutputMode = "files_with_matches"
	// OutputCount renders per-file match counts.
	OutputCount OutputMode = "count"
)

// DefaultHeadLimit bounds `content` output when the caller does not choose. A search that
// returns thousands of lines has not answered a question, it has moved the cost from the
// search to the reader.
const DefaultHeadLimit = 200

// AnnotateLimit is the number of DISTINCT resources above which content output drops the
// topology annotation and renders as plain grep.
//
// Annotation earns its bytes only at triage scale. With five hits, knowing what each
// enclosing function does is exactly the information that saves a `read`. With two
// thousand hits spread over two thousand functions it is one header per match — the same
// per-line overhead that made this tool 2.1x the size of `grep -rn`, just moved onto its
// own line. Above the limit the caller is told how to get annotation back.
const AnnotateLimit = 30

// AnnotateOverheadBudget caps annotation at a fraction of the matched content.
//
// A resource count alone is the wrong test: 25 resources across 500 matching lines is 25
// headers of useful context, while 25 resources across 30 matching lines is nearly one
// header per line — the very shape that made this tool cost more than the grep it
// replaces. Measuring the headers against the content they annotate catches both.
const AnnotateOverheadBudget = 0.25

// AnnotateFreeBytes is the content size below which annotation is always kept.
//
// The ratio test alone punishes exactly the case annotation is FOR: one hit with one
// header is a poor ratio and a trivial absolute cost. Below this floor the overhead cannot
// matter; above it, annotation has to justify itself proportionally.
//
// LOWERED FROM 4096 AFTER MEASUREMENT. Four kilobytes is not "cannot matter": on the clap
// fixture, `grep -rn 'pub struct' src/` returns 730 bytes from a plain grep and came back at
// 2,211 -- 3.03x -- of which 1,481 bytes were headers. The matched ROWS were 1.00x native, so
// the entire blow-up was annotation waved through by this floor. A narrow search is precisely
// where a header per resource approaches a header per line, which is the shape
// AnnotateOverheadBudget exists to catch; the floor was letting it past unmeasured.
//
// 512 keeps the case the floor was written for -- a handful of hits with a handful of headers
// is still free -- while making anything larger earn its annotation proportionally.
const AnnotateFreeBytes = 512

// MatchSource says why a row is in the result, and its values are ordered by
// priority: a node named after the query outranks one merely described by it,
// which outranks a raw line hit. Ranking is therefore one extra sort key.
type MatchSource int

const (
	// MatchTitle: the node's ID or Name matched the pattern.
	MatchTitle MatchSource = iota
	// MatchDescription: the node's stored description matched. Descriptions live
	// only in the topology DB, so this is the one tier a plain grep cannot reach.
	MatchDescription
	// MatchContent: a source line matched. The default.
	MatchContent
)

// NodeHitBudget caps node rows -- rows that exist only because a title or a
// description matched -- at a fraction of the head limit, per tier.
//
// Prose is long and common words match a lot of it. Without a budget, one
// ordinary query ("value", "the") would fill the entire head limit with node
// rows and push out every line hit the caller actually grepped for, which is the
// opposite of ranking. Promoted line matches are NOT capped by this: those are
// real hits that would have been returned anyway.
const NodeHitBudget = 0.25

// MinNodeHits floors the budget so a small head_limit still shows some node
// rows; MaxNodeHits applies when the head limit is unlimited.
const (
	MinNodeHits = 5
	MaxNodeHits = 50
)

// maxLineBytes is the per-line ceiling. The previous 64 KB default silently dropped every
// match already found in a file the moment one long line appeared (minified JS, generated
// tables, vendored bundles) — see searchFile.
const maxLineBytes = 8 * 1024 * 1024

// Match is a single hit.
type Match struct {
	Path       string `json:"path"`
	Line       int    `json:"line"`
	Text       string `json:"text"`
	ResourceID string `json:"resource_id,omitempty"`
	// ResourceStart and ResourceEnd are the enclosing resource's span. Carried so the
	// renderer can name a hit by the lines the caller can read rather than by an id.
	ResourceStart int    `json:"resource_start,omitempty"`
	ResourceEnd   int    `json:"resource_end,omitempty"`
	Description   string `json:"description,omitempty"`
	// MatchedOn is why this row ranked where it did. It is a property of the
	// enclosing resource, so every row of one resource shares it -- which is what
	// keeps a resource's matches contiguous after the tiered sort.
	MatchedOn MatchSource `json:"matched_on"`
	// NodeHit marks a row that is the node's declaration line rather than a
	// matching line: the node surfaced because its title or description matched
	// and its body contained no hit at all.
	NodeHit bool `json:"node_hit,omitempty"`
}

// Options configures one search.
type Options struct {
	Pattern string
	// Root is the single path to walk. Roots supersedes it when both are set.
	Root string
	// Roots are the paths to walk, for the `grep pat a.go b.go` shape a shell glob
	// produces. One search over several roots, so the caps and the accounting below
	// still describe the whole answer rather than one arbitrary part of it.
	Roots []string
	// Globs are filename patterns, e.g. "*.go" or "**/*_test.go". A file is searched when
	// it matches ANY of them, which is what grep's repeatable --include means: two
	// --include flags widen the search, they do not narrow it to the last one.
	Globs []string
	// ExcludeGlobs and ExcludeDirs are grep's --exclude and --exclude-dir: a file or
	// directory matching one is not searched, whatever Globs says.
	ExcludeGlobs []string
	ExcludeDirs  []string
	Type         string // language shorthand, e.g. "go", "py", "ts"
	IgnoreCase   bool
	Mode         OutputMode
	HeadLimit    int // 0 uses DefaultHeadLimit; negative means unlimited
	// PerFileLimit is grep's -m: at most this many matches from EACH file. It is not
	// HeadLimit under another name -- `grep -rm 1 foo .` asks for one hit per file, an
	// index of the whole tree, and answering it with one hit in total is a different and
	// much smaller answer. 0 means no per-file cap.
	PerFileLimit int
	Before       int // context lines before each hit
	After        int // context lines after each hit
	// WithFilename and LineNumbers are grep's `-H` and `-n`, and they are honoured ONLY under
	// Terse -- the intercepted shell surface, the one caller that has flags to honour. Every
	// other caller (the MCP tool, `arac grep`) addresses rows by path and line unconditionally
	// and leaves both zero, so a zero value can never silently strip a column from them.
	WithFilename bool
	LineNumbers  bool
	// Terse renders the way the real grep does: nothing at all when there are no matches,
	// a bare number for a single file's count, and cap advice spelled in shell flags.
	//
	// It exists because the same renderer serves two callers with opposite contracts. An
	// MCP tool result of "" reads as a broken tool, so the tool surface wants prose. A
	// shell caller pipes stdout into `$(...)`, and prose there becomes a filename, a count
	// or a match -- so for the intercepted shell, saying nothing is the only honest answer.
	Terse bool
	// Ignore applies the project's scan.ignore rules. Build it with
	// domain.BuildIgnoreMatcher(root, cfg.Scan.Ignore); nil disables the check.
	Ignore *domain.IgnoreMatcher
	// LineRange names each annotated hit by "path:start-end" instead of by resource id.
	// See helper.IdentifyLineRange: the id is aracne's vocabulary, the span is the shell's,
	// and a search result is precisely where the caller decides what to read next.
	LineRange bool
	// DescriptionKinds limits which resource kinds may match on their stored
	// description. nil means the caller did not choose and gets
	// DefaultDescriptionKinds(); an explicit empty slice disables description
	// matching entirely. Titles are never gated -- a name match is precise.
	DescriptionKinds []domain.ResourceKind
}

// Result carries the hits plus enough accounting to tell the caller what it did not see.
type Result struct {
	Matches []Match
	// Files, in path order, that contained at least one match.
	Files []string
	// Counts maps a path to its match count (all matches, not just the rendered ones).
	Counts map[string]int
	// Total is how many matches existed before the head limit was applied.
	Total int
	// Truncated reports that Total exceeded the limit.
	Truncated bool
	// Limit is the limit actually applied (for rendering the trailer).
	Limit int
	// DistinctResources counts the distinct resources among the rendered LINE
	// matches; it decides whether annotation is worth its bytes (see
	// AnnotateLimit). Node rows are excluded: they are always annotated, because
	// for them the header is the answer.
	DistinctResources int
	// TitleWithheld and DescriptionWithheld count the node rows dropped by the
	// per-tier budget, so the trailer can say what was not shown.
	TitleWithheld       int
	DescriptionWithheld int
	// Context holds the context lines -A/-B/-C asked for, keyed path -> line -> text.
	//
	// A MAP, not a slice hanging off each match, and that is the whole fix: two matches
	// three lines apart used to carry overlapping windows that were rendered twice, and
	// each window was printed under a single line number for all of its lines. Keyed by
	// absolute line number, a line can only be stored once and can only be numbered
	// correctly. Lines that matched are never in here -- they are rendered as matches.
	Context map[string]map[int]string
	// BinaryFiles are the paths that matched but hold NUL bytes. Their matches are counted
	// (Counts and Files include them) and their CONTENT is never rendered: dumping a
	// binary into a terminal is what `grep` refuses to do, and dumping it into a model's
	// context window is worse.
	BinaryFiles []string
	// RootIsFile records that the caller named exactly one file. `grep -c pat file` prints
	// a bare number and `grep -rc pat dir` prints path:count; the difference is this.
	RootIsFile bool
}

// Found reports whether the search has anything to show, in the mode it was run in.
//
// Not `len(Matches) > 0`: a file list and a count are built from textual matches only, so
// a result carrying nothing but node rows has found something to PRINT in content mode and
// nothing to report in the other two. The exit status callers branch on comes from here.
func (r *Result) Found(mode OutputMode) bool {
	if r == nil {
		return false
	}
	if mode == OutputContent {
		return len(r.Matches) > 0 || len(r.BinaryFiles) > 0
	}
	return len(r.Files) > 0
}

type resourceLocation struct {
	id          string
	description string
	kind        domain.ResourceKind
	startsAt    int
	endsAt      int
}

// Search is the back-compatible entry point: content mode, default cap, no filters.
func Search(pattern, root string, topo *domain.Topology) ([]Match, error) {
	res, err := SearchWith(Options{Pattern: pattern, Root: root}, topo)
	if err != nil {
		return nil, err
	}
	return res.Matches, nil
}

// SearchWith runs a configured search.
func SearchWith(opt Options, topo *domain.Topology) (*Result, error) {
	if opt.Pattern == "" {
		return nil, fmt.Errorf("pattern is required")
	}
	if opt.Root == "" {
		opt.Root = "."
	}
	if opt.Mode == "" {
		opt.Mode = OutputContent
	}
	pattern := opt.Pattern
	if opt.IgnoreCase && !strings.HasPrefix(pattern, "(?i)") {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("compile pattern: %w", err)
	}
	exts, err := typeExtensions(opt.Type)
	if err != nil {
		return nil, err
	}

	index := buildResourceIndex(topo)
	tiers := matchNodes(re, topo, descriptionKindSet(opt.DescriptionKinds))
	out := &Result{
		Counts:     map[string]int{},
		Limit:      effectiveLimit(opt),
		Context:    map[string]map[int]string{},
		RootIsFile: rootIsSingleFile(opt),
	}

	// binaryCounts is kept apart from `all` on purpose: a binary file's matches are real
	// and belong in Counts, but its LINES may never be rendered, so they must not sit in
	// the list the head limit slices and the formatter prints.
	binaryCounts := map[string]int{}
	var all []Match
	if err := walkSearch(opt, exts, func(path string) error {
		found, err := searchFile(path, re, index, tiers, opt)
		if err != nil {
			return err
		}
		if found.binary {
			if found.count > 0 {
				binaryCounts[found.display] = found.count
				out.BinaryFiles = append(out.BinaryFiles, found.display)
			}
			return nil
		}
		all = append(all, found.matches...)
		if len(found.context) > 0 {
			out.Context[found.display] = found.context
		}
		return nil
	}); err != nil {
		return nil, err
	}
	sort.Strings(out.BinaryFiles)

	// Priority first, then the familiar path/line order within a tier. Tier is a
	// property of the resource, so one resource's rows never straddle two tiers and
	// FormatResult's one-header-per-run grouping still holds.
	sort.Slice(all, func(i, j int) bool {
		if all[i].MatchedOn != all[j].MatchedOn {
			return all[i].MatchedOn < all[j].MatchedOn
		}
		if all[i].Path != all[j].Path {
			return all[i].Path < all[j].Path
		}
		return all[i].Line < all[j].Line
	})

	all, out.TitleWithheld, out.DescriptionWithheld = capNodeHits(all, out.Limit)

	// Accounting sits AFTER the node-hit budget, so Files and Counts describe rows the
	// caller can actually get, and BEFORE the head limit, so they stay the honest
	// pre-truncation numbers the trailer reports against.
	out.Matches = all
	out.Total = len(all)
	// Counts and Files describe TEXTUAL matches, and node rows are excluded from both.
	//
	// A node row exists because the pattern matched a name or a stored description, not
	// because the file holds the string. In content mode that row arrives under a header
	// saying exactly which node it is, and it is the tool's whole point. In a bare file
	// list or a count there is no header and no way to tell: `grep -rl demo/pkg .` would
	// name files that do not contain the string, and `grep -rc` would answer "5" about a
	// file where the real count is 0. A count has to be a count.
	for _, m := range all {
		if m.NodeHit {
			continue
		}
		out.Counts[m.Path]++
	}
	for path, n := range binaryCounts {
		out.Counts[path] += n
		// Counted into Total as well as Counts. Total is what the truncation trailer reports
		// against ("showing N of M"), and leaving binary hits out of it under-reported M
		// while `grep -c` over the same search reported them.
		out.Total += n
	}
	for path := range out.Counts {
		out.Files = append(out.Files, path)
	}
	sort.Strings(out.Files)

	// Cap only what is rendered; Total and Counts keep the honest numbers so the trailer
	// can say how much was withheld.
	if out.Limit > 0 && len(out.Matches) > out.Limit {
		out.Matches = out.Matches[:out.Limit]
		out.Truncated = true
	}
	distinct := map[string]bool{}
	for _, m := range out.Matches {
		if m.ResourceID != "" && !m.NodeHit {
			distinct[m.ResourceID] = true
		}
	}
	out.DistinctResources = len(distinct)
	return out, nil
}

// DefaultDescriptionKinds is the set of resource kinds whose description may match
// when the caller does not choose. It mirrors helper.DefaultGrepDescriptionKinds;
// topogrep deliberately does not import the config package, so the default is
// restated here and the two are kept in step by TestDefaultDescriptionKindsMatchConfig.
func DefaultDescriptionKinds() []domain.ResourceKind {
	return []domain.ResourceKind{
		domain.ResourceFunction,
		domain.ResourceMethod,
		domain.ResourceStruct,
		domain.ResourceInterface,
	}
}

// descriptionKindSet resolves Options.DescriptionKinds. nil means "the caller did not
// choose" and gets the defaults; an explicit empty slice means "never match on a
// description" and must not be back-filled into the defaults.
func descriptionKindSet(kinds []domain.ResourceKind) map[domain.ResourceKind]bool {
	if kinds == nil {
		kinds = DefaultDescriptionKinds()
	}
	if len(kinds) == 0 {
		return nil
	}
	set := make(map[domain.ResourceKind]bool, len(kinds))
	for _, kind := range kinds {
		set[kind] = true
	}
	return set
}

// matchNodes decides once, for the whole topology, which nodes the pattern matches on
// their title or on their stored description.
//
// This is the half of the search a plain grep cannot do: a description exists only in
// the topology DB, so `retry` can find a function documented as "retries on 5xx" whose
// code never says "retry". Title wins over description, so a node is never reported
// twice for the same query.
func matchNodes(re *regexp.Regexp, topo *domain.Topology, kinds map[domain.ResourceKind]bool) map[string]MatchSource {
	if topo == nil || len(topo.Resources) == 0 {
		return nil
	}
	tiers := make(map[string]MatchSource)
	for id, res := range topo.Resources {
		switch {
		case re.MatchString(res.Name) || matchesIDTail(re, id):
			tiers[id] = MatchTitle
		case res.Description != "" && kinds[res.Kind] && re.MatchString(res.Description):
			tiers[id] = MatchDescription
		}
	}
	if len(tiers) == 0 {
		return nil
	}
	return tiers
}

// matchesIDTail reports whether the pattern matches a SUFFIX of a resource id.
//
// Matching anywhere in the id promoted every node declared under a matching path: searching
// for `helper` made every resource whose id contains `internal/helper/...` a title hit, which
// re-ranked all their line matches above everything else and filled the node-row budget with
// declarations that matched only their own directory name.
//
// A suffix is the honest test. A pattern naming a declaration -- `RunGuard`,
// `internal/cli.RunGuard` -- ends where the id ends; a path fragment in the middle does not.
func matchesIDTail(re *regexp.Regexp, id string) bool {
	for _, loc := range re.FindAllStringIndex(id, -1) {
		if loc[1] == len(id) {
			return true
		}
	}
	return false
}

// capNodeHits bounds the rows that exist only because a node's title or description
// matched, per tier, and reports how many it dropped. See NodeHitBudget for why.
func capNodeHits(matches []Match, limit int) ([]Match, int, int) {
	budget := nodeHitBudget(limit)
	kept := matches[:0]
	shown := map[MatchSource]int{}
	titleWithheld, descriptionWithheld := 0, 0
	for _, m := range matches {
		if m.NodeHit {
			shown[m.MatchedOn]++
			if shown[m.MatchedOn] > budget {
				if m.MatchedOn == MatchTitle {
					titleWithheld++
				} else {
					descriptionWithheld++
				}
				continue
			}
		}
		kept = append(kept, m)
	}
	return kept, titleWithheld, descriptionWithheld
}

func nodeHitBudget(limit int) int {
	if limit <= 0 {
		return MaxNodeHits
	}
	if budget := int(float64(limit) * NodeHitBudget); budget > MinNodeHits {
		return budget
	}
	return MinNodeHits
}

func effectiveLimit(opt Options) int {
	switch {
	case opt.Mode != OutputContent:
		return 0 // file lists and counts are already one line per file
	case opt.HeadLimit < 0:
		return 0
	case opt.HeadLimit == 0:
		return DefaultHeadLimit
	default:
		return opt.HeadLimit
	}
}

// Format renders content-mode matches with the default options.
func Format(matches []Match) string {
	distinct := map[string]bool{}
	for _, m := range matches {
		if m.ResourceID != "" && !m.NodeHit {
			distinct[m.ResourceID] = true
		}
	}
	return FormatResult(&Result{
		Matches: matches, Total: len(matches), DistinctResources: len(distinct),
	}, Options{Mode: OutputContent})
}

// FormatResult renders a result for the requested mode.
//
// In content mode the resource annotation is emitted ONCE per resource, as a header above
// its matches, rather than as two extra lines under every matching line. A resource with
// forty hits used to repeat its description forty times.
func FormatResult(res *Result, opt Options) string {
	if res == nil {
		return ""
	}
	switch opt.Mode {
	case OutputFiles:
		if len(res.Files) == 0 {
			return noMatches(opt)
		}
		return strings.Join(res.Files, "\n")
	case OutputCount:
		return formatCounts(res, opt)
	}

	if len(res.Matches) == 0 && len(res.BinaryFiles) == 0 {
		return noMatches(opt)
	}
	// Annotate only when the result set is small enough to triage by reading the
	// descriptions AND the headers stay a small fraction of the content they describe;
	// otherwise degrade to plain grep, which is what a broad sweep wanted anyway.
	annotate := res.DistinctResources > 0 && res.DistinctResources <= AnnotateLimit &&
		annotationFits(res.Matches)

	// Context is read in FILE order or it is not read at all: a `-B2` window printed above a
	// match that sits earlier in the file than the previous one is a puzzle, not a context.
	// The tiered ranking has already done its job by this point -- it decided WHICH matches
	// survived the head limit -- so re-ordering what is left costs the ranking nothing.
	rows := res.Matches
	if opt.Before > 0 || opt.After > 0 {
		rows = append([]Match(nil), rows...)
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].Path != rows[j].Path {
				return rows[i].Path < rows[j].Path
			}
			return rows[i].Line < rows[j].Line
		})
	}

	var b strings.Builder
	printed := map[string]map[int]bool{}
	lastHeader := ""
	lastPath, lastLine := "", 0
	first := true

	// grep names the file only when it searched more than one, so one named file prints
	// `line:text` and `line-text`. That is the same distinction formatCounts makes for `-c`,
	// and for the same reason: it moves the field the caller reads. `grep -n pat f | cut -d:
	// -f1` is a list of line numbers to the shell, and a list of one repeated path to anyone
	// who prefixed it. The MCP surface is not Terse and keeps the path, where a row has to
	// stand on its own.
	bare := opt.Terse && res.RootIsFile && !opt.WithFilename
	// grep prints a line number only for `-n`. aracne printed one always, which inserts a
	// field in the middle of every row: `grep -r pat dir | cut -d: -f2-` is the match text to
	// the real command and `line:text` here. The resource header above the run still carries
	// the span either way, which is where that information actually earns its place.
	numbered := !opt.Terse || opt.LineNumbers

	emit := func(path string, line int, text string, isMatch bool) {
		if printed[path] == nil {
			printed[path] = map[int]bool{}
		}
		if printed[path][line] {
			return
		}
		printed[path][line] = true
		if !first {
			b.WriteByte('\n')
		}
		first = false
		switch {
		case bare && !numbered:
			fmt.Fprintf(&b, "%s", text)
		case bare && isMatch:
			fmt.Fprintf(&b, "%d:%s", line, text)
		case bare:
			fmt.Fprintf(&b, "%d-%s", line, text)
		case !numbered && isMatch:
			fmt.Fprintf(&b, "%s:%s", path, text)
		case !numbered:
			fmt.Fprintf(&b, "%s-%s", path, text)
		case isMatch:
			fmt.Fprintf(&b, "%s:%d:%s", path, line, text)
		default:
			fmt.Fprintf(&b, "%s-%d-%s", path, line, text)
		}
		lastPath, lastLine = path, line
	}

	for _, m := range rows {
		header := rowHeader(m, opt, annotate)
		if header != "" && header != lastHeader {
			if !first {
				b.WriteByte('\n')
			}
			first = false
			b.WriteString(header)
		}
		lastHeader = header

		// grep separates non-contiguous context groups with `--`, and only when context was
		// asked for. Without it a jump from line 40 to line 900 reads as one block. A header
		// already separates this run from the previous one, so it stands in for the `--`.
		if (opt.Before > 0 || opt.After > 0) && !first && header == "" &&
			(m.Path != lastPath || m.Line-opt.Before > lastLine+1) {
			b.WriteString("\n--")
		}

		ctx := res.Context[m.Path]
		for line := m.Line - opt.Before; line < m.Line; line++ {
			if text, ok := ctx[line]; ok {
				emit(m.Path, line, text, false)
			}
		}
		emit(m.Path, m.Line, m.Text, true)
		for line := m.Line + 1; line <= m.Line+opt.After; line++ {
			if text, ok := ctx[line]; ok {
				emit(m.Path, line, text, false)
			}
		}
	}

	// A binary file's content is never rendered. Saying so is grep's own wording, and it
	// is the only honest row: the file matched, and its bytes are not for a terminal.
	for _, path := range res.BinaryFiles {
		if !first {
			b.WriteByte('\n')
		}
		first = false
		fmt.Fprintf(&b, "Binary file %s matches", path)
	}

	if !annotate && res.DistinctResources > AnnotateLimit {
		fmt.Fprintf(&b, "\n… %d distinct resources matched, so topology annotation is "+
			"omitted. %s", res.DistinctResources, narrowAdvice(opt))
	}
	if withheld := res.TitleWithheld + res.DescriptionWithheld; withheld > 0 {
		fmt.Fprintf(&b, "\n… %d more node(s) matched on name or description but were not "+
			"shown. %s", withheld, narrowPatternAdvice(opt))
	}
	if res.Truncated {
		fmt.Fprintf(&b, "\n… %d more match(es) not shown (showing %d of %d). %s",
			res.Total-len(res.Matches), len(res.Matches), res.Total, narrowAdvice(opt))
	}
	return b.String()
}

// rowHeader is the annotation line a row is printed under, or "" for a row printed bare.
//
// The empty-id case is why this is a function. A line outside every declaration used to
// print with no header at all, which put it visually under the PREVIOUS resource's
// header -- attributing a line to a function it is not in. Naming the file instead costs
// one short line and says the true thing.
func rowHeader(m Match, opt Options, annotate bool) string {
	// A node row is annotated whatever the gate says: it is in the result BECAUSE of its
	// title or description, so without the header it renders as an unexplained
	// declaration line.
	if !annotate && !m.NodeHit {
		return ""
	}
	if m.ResourceID == "" {
		return "# " + m.Path
	}
	header := "# " + resourceLabel(m, opt)
	if m.Description != "" {
		header += " — " + m.Description
	}
	return header
}

// formatCounts renders count mode.
//
// `grep -c pat file` prints a bare number and `grep -rc pat dir` prints one path:count per
// file. That is not a cosmetic difference: a bare count is the whole reason a caller reaches
// for -c instead of piping to `wc -l`, and prefixing the path moves the field they read.
func formatCounts(res *Result, opt Options) string {
	if len(res.Counts) == 0 {
		if opt.Terse && res.RootIsFile {
			return "0"
		}
		return noMatches(opt)
	}
	paths := make([]string, 0, len(res.Counts))
	for p := range res.Counts {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var b strings.Builder
	for i, p := range paths {
		if i > 0 {
			b.WriteByte('\n')
		}
		if opt.Terse && res.RootIsFile {
			fmt.Fprintf(&b, "%d", res.Counts[p])
			continue
		}
		fmt.Fprintf(&b, "%s:%d", p, res.Counts[p])
	}
	return b.String()
}

// narrowAdvice tells the caller how to ask for less, in the vocabulary of the surface they
// are on. `head_limit` is a parameter of the MCP tool; a shell caller cannot type it, and
// advice you cannot follow is worse than none.
func narrowAdvice(opt Options) string {
	if opt.Terse {
		return "Narrow with --include=<glob>, a path argument, or -m N to cap each file."
	}
	return "Narrow with glob/type/path (or use output_mode=files_with_matches) to get " +
		"resource IDs and descriptions back, or raise head_limit."
}

// narrowPatternAdvice is the same idea for the node-row budget, which no shell flag reaches.
func narrowPatternAdvice(opt Options) string {
	if opt.Terse {
		return "Narrow the pattern."
	}
	return "Narrow the pattern, or raise head_limit."
}

// annotationFits reports whether the per-resource headers would stay within
// AnnotateOverheadBudget of the matched content.
func annotationFits(matches []Match) bool {
	content, header := 0, 0
	seen := map[string]bool{}
	for _, m := range matches {
		// Node rows are annotated unconditionally, so they neither earn nor owe
		// anything in the budget that decides annotation for the line matches.
		if m.NodeHit {
			continue
		}
		content += len(m.Path) + len(m.Text) + 8 // + "path:line:"
		if m.ResourceID == "" || seen[m.ResourceID] {
			continue
		}
		seen[m.ResourceID] = true
		header += len(m.ResourceID) + len(m.Description) + 6 // + "# " and the separator
	}
	if content == 0 {
		return false
	}
	if content+header <= AnnotateFreeBytes {
		return true
	}
	return float64(header)/float64(content) <= AnnotateOverheadBudget
}

// noMatches is an explicit answer -- except on the shell surface, where it must not be.
//
// Returning "" made a successful search with zero hits indistinguishable from a broken TOOL,
// which is why the prose exists. It is exactly wrong for an intercepted shell command: the
// caller there is a pipeline, and `files=$(grep -rl foo .)` turns that sentence into a
// filename. Real grep prints nothing and exits 1, and Terse says to do the same.
func noMatches(opt Options) string {
	if opt.Terse {
		return ""
	}
	if opt.Pattern == "" {
		return "no matches"
	}
	return fmt.Sprintf("no matches for %q", opt.Pattern)
}

// searchRoots is the paths a search walks. Roots wins when set; Root is the one-path form
// every caller but the shell still uses.
func searchRoots(opt Options) []string {
	if len(opt.Roots) > 0 {
		return opt.Roots
	}
	if opt.Root == "" {
		return []string{"."}
	}
	return []string{opt.Root}
}

// rootIsSingleFile reports that the caller named exactly one file rather than a tree. It is
// the difference between `grep -c pat file`, which prints a bare number, and `grep -rc pat
// dir`, which prints one path:count per file.
func rootIsSingleFile(opt Options) bool {
	roots := searchRoots(opt)
	if len(roots) != 1 {
		return false
	}
	info, err := os.Stat(roots[0])
	return err == nil && !info.IsDir()
}

// walkSearch visits every candidate file under each search root.
//
// A file reached through two roots is visited once: `grep pat . src` would otherwise report
// every hit under src twice, and the caps would be measured against a doubled total.
func walkSearch(opt Options, exts map[string]bool, visit func(path string) error) error {
	seen := map[string]bool{}
	once := func(path string) error {
		key := canonicalPath(path)
		if seen[key] {
			return nil
		}
		seen[key] = true
		return visit(path)
	}
	for _, root := range searchRoots(opt) {
		if err := walkOneRoot(root, opt, exts, once); err != nil {
			return err
		}
	}
	return nil
}

// walkOneRoot visits the candidate files under a single root.
func walkOneRoot(root string, opt Options, exts map[string]bool, visit func(path string) error) error {
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("stat path: %w", err)
	}
	if !info.IsDir() {
		// A file named outright is searched whatever the filters say. `grep pat vendor/x.go`
		// asked for that file; answering "no matches" because a filter would have skipped it
		// during a walk answers a different question.
		return visit(root)
	}
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			// The search root is never pruned by its own name: searching inside
			// ~/.dotfiles must work.
			if path != root && shouldSkipDir(path, d.Name(), opt) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeType != 0 {
			return nil
		}
		if !wantFile(path, d.Name(), exts, opt) {
			return nil
		}
		return visit(path)
	})
}

// prunedDirs are the directories no search descends into: version-control metadata,
// aracne's own store, and the dependency and build trees whose size is the reason nobody
// greps them on purpose.
//
// IT IS A LIST AND NOT A DOT-PREFIX RULE. Skipping every name beginning with "." was one
// line and cost the search `.github`, which is where an agent looks for CI configuration --
// `grep -rn runs-on .` came back empty and read as "this project has no CI". Whatever else
// a given project wants skipped is what scan.ignore is for.
var prunedDirs = map[string]bool{
	".git": true, ".hg": true, ".svn": true, ".aracne": true,
	"node_modules": true, "vendor": true,
	".venv": true, "venv": true, ".tox": true,
	".mypy_cache": true, ".pytest_cache": true, ".ruff_cache": true,
	".gradle": true, ".terraform": true, ".next": true, ".nuxt": true,
}

// shouldSkipDir prunes a directory. Beyond the always-noise set it applies the caller's own
// --exclude-dir and the project's scan.ignore rules, which were previously not wired in
// here at all -- so a search happily descended into build output the scanner itself had
// been told to skip.
func shouldSkipDir(path, name string, opt Options) bool {
	if name == "." {
		return false
	}
	if prunedDirs[name] {
		return true
	}
	for _, ex := range opt.ExcludeDirs {
		if ok, err := filepath.Match(ex, name); err == nil && ok {
			return true
		}
	}
	return opt.Ignore.MatchDir(path)
}

// wantFile applies the glob and type filters, the exclusions and the ignore rules.
func wantFile(path, name string, exts map[string]bool, opt Options) bool {
	if opt.Ignore.Match(path) {
		return false
	}
	for _, ex := range opt.ExcludeGlobs {
		if matchGlob(ex, path, name) {
			return false
		}
	}
	if len(exts) > 0 && !exts[strings.ToLower(filepath.Ext(name))] {
		return false
	}
	if len(opt.Globs) == 0 {
		return true
	}
	// ANY glob, not the last one. `--include=*.go --include=*.md` searches both, and
	// keeping only the last silently dropped every Go match behind a successful exit status.
	for _, g := range opt.Globs {
		if matchGlob(g, path, name) {
			return true
		}
	}
	return false
}

// matchGlob supports a bare filename pattern ("*.go"), a "**/"-prefixed pattern
// ("**/*_test.go", equivalent to the bare form), and a path pattern ("src/*.ts").
func matchGlob(glob, path, name string) bool {
	g := strings.TrimPrefix(glob, "**/")
	if !strings.ContainsAny(g, "/\\") {
		ok, err := filepath.Match(g, name)
		return err == nil && ok
	}
	slashed := filepath.ToSlash(path)
	if ok, err := filepath.Match(g, slashed); err == nil && ok {
		return true
	}
	// Also try matching the trailing segments so "src/*.ts" hits "./a/src/x.ts".
	segs := strings.Split(slashed, "/")
	want := len(strings.Split(g, "/"))
	if want < len(segs) {
		tail := strings.Join(segs[len(segs)-want:], "/")
		ok, err := filepath.Match(g, tail)
		return err == nil && ok
	}
	return false
}

// typeExtensions maps a language shorthand to the extensions it covers.
func typeExtensions(t string) (map[string]bool, error) {
	if t == "" {
		return nil, nil
	}
	table := map[string][]string{
		"go":         {".go"},
		"py":         {".py", ".pyi"},
		"python":     {".py", ".pyi"},
		"js":         {".js", ".jsx", ".mjs", ".cjs"},
		"javascript": {".js", ".jsx", ".mjs", ".cjs"},
		"ts":         {".ts", ".tsx", ".mts", ".cts"},
		"typescript": {".ts", ".tsx", ".mts", ".cts"},
		"rust":       {".rs"},
		"rs":         {".rs"},
		"java":       {".java"},
		"c":          {".c", ".h"},
		"cpp":        {".cc", ".cpp", ".cxx", ".hpp", ".hh"},
		"md":         {".md", ".markdown"},
		"json":       {".json"},
		"yaml":       {".yaml", ".yml"},
		"toml":       {".toml"},
		"sh":         {".sh", ".bash"},
	}
	exts, ok := table[strings.ToLower(t)]
	if !ok {
		known := make([]string, 0, len(table))
		for k := range table {
			known = append(known, k)
		}
		sort.Strings(known)
		return nil, fmt.Errorf("unknown type %q; known types: %s", t, strings.Join(known, ", "))
	}
	set := make(map[string]bool, len(exts))
	for _, e := range exts {
		set[e] = true
	}
	return set, nil
}

// fileResult is one file's contribution to a search.
type fileResult struct {
	display string
	matches []Match
	// context holds the -A/-B/-C lines by absolute line number, so a line can be stored
	// only once and can only be numbered correctly. Matching lines are never in here.
	context map[int]string
	// binary marks a file holding NUL bytes. Its matches are COUNTED and its lines are
	// never rendered, which is what grep does and for the same reason.
	binary bool
	count  int
}

// binarySniffBytes is how much of a file is inspected for NUL before deciding it is not
// text. grep uses the first buffer it reads; a few kilobytes catches every real binary
// format's header without reading a large file twice.
const binarySniffBytes = 8 * 1024

// looksBinary reports whether the opened file holds a NUL byte in its first bytes. The file
// offset is restored, so the caller can scan it from the top either way.
func looksBinary(f *os.File) bool {
	buf := make([]byte, binarySniffBytes)
	n, _ := f.Read(buf)
	defer f.Seek(0, 0)
	for i := 0; i < n; i++ {
		if buf[i] == 0 {
			return true
		}
	}
	return false
}

// searchFile scans one file.
//
// On a scanner error it returns the matches found SO FAR rather than discarding them. The
// previous `return nil, nil` meant one over-long line (a minified bundle, a generated
// table) silently erased every hit in that file -- a search that looked successful and
// simply lied.
func searchFile(path string, re *regexp.Regexp, index map[string][]resourceLocation, tiers map[string]MatchSource, opt Options) (fileResult, error) {
	out := fileResult{display: displayPath(path)}
	f, err := os.Open(path)
	if err != nil {
		return out, nil
	}
	defer f.Close()

	if looksBinary(f) {
		out.binary = true
		out.count = countBinaryMatches(f, re, opt)
		return out, nil
	}

	canonical := canonicalPath(path)
	resources := index[canonical]

	// Declaration lines to capture for nodes this file owns whose title or description
	// matched. Reading them here is free -- the scan is already walking every line --
	// and it means a node row can never escape the glob/type/path/ignore filters the
	// caller asked for, because it is only produced for a file the walk visited.
	wanted := declarationLines(resources, tiers)
	declared := map[int]string{} // index into resources -> declaration line text
	// How many wanted declaration lines are still ahead of the scan. The -m cap stops
	// collecting MATCHES, not node rows: a node whose declaration sits below the cut-off was
	// silently dropped, and the description tier -- the half a plain grep cannot reach -- is
	// exactly what went missing, with nothing in the trailer to say so.
	pendingDeclarations := 0
	for _, idxs := range wanted {
		pendingDeclarations += len(idxs)
	}
	capped := false
	lineHit := map[string]bool{} // resource id -> the body contained a match

	context := map[int]string{}
	matched := map[int]bool{}
	var ring []string // rolling window of the previous opt.Before lines
	pendingAfter := 0
	hits := 0

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineBytes)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		// A trailing CR is a line terminator artifact, not content: it is dropped from what
		// is matched AND from what is reported, so a CRLF checkout does not lose every `$`
		// anchored pattern. Leading whitespace is the opposite -- it IS content, it is what
		// a Python block is made of, and it is what an edit has to reproduce, so it stays.
		line := strings.TrimRight(scanner.Text(), "\r")

		if pendingAfter > 0 {
			context[lineNo] = line
			pendingAfter--
		}

		for _, i := range wanted[lineNo] {
			declared[i] = line
			pendingDeclarations--
		}
		// Past the cap the scan keeps going only for the declaration lines it still owes,
		// and stops the moment it owes none. Everything below is match collection.
		if capped {
			if pendingDeclarations <= 0 {
				break
			}
			continue
		}

		if re.MatchString(line) {
			matched[lineNo] = true
			m := Match{Path: out.display, Line: lineNo, Text: line, MatchedOn: MatchContent}
			if resource := bestResource(resources, lineNo); resource != nil {
				m.ResourceID = resource.id
				m.ResourceStart, m.ResourceEnd = resource.startsAt, resource.endsAt
				m.Description = resource.description
				// A line inside a node the pattern also named or described is that
				// node's hit: promote it rather than emitting a second row for the
				// same node further down.
				if tier, ok := tiers[resource.id]; ok {
					m.MatchedOn = tier
				}
				lineHit[resource.id] = true
			}
			// The before-window, stored by absolute line number. ring[i] is the line
			// lineNo-len(ring)+i, because the current line has not been appended yet.
			for i, c := range ring {
				context[lineNo-len(ring)+i] = c
			}
			out.matches = append(out.matches, m)
			pendingAfter = opt.After
			hits++
		}

		// grep -m stops COLLECTING once it has its N matches -- but not before it has
		// emitted the trailing context those matches already promised, and not before it has
		// captured the declaration lines of the nodes this file owes (see capped above).
		if opt.PerFileLimit > 0 && hits >= opt.PerFileLimit && pendingAfter == 0 {
			if pendingDeclarations <= 0 {
				break
			}
			capped = true
		}
		if opt.Before > 0 {
			ring = append(ring, line)
			if len(ring) > opt.Before {
				ring = ring[1:]
			}
		}
	}
	// Deliberately swallow the scanner error: whatever was found before the failure is
	// real, and returning it beats erasing a whole file's hits because one line was long.
	_ = scanner.Err()

	// A line that matched is rendered as a match, never also as its neighbour's context.
	for line := range matched {
		delete(context, line)
	}
	out.context = context

	// Only now, for nodes the pattern named or described whose body produced nothing:
	// these are exactly the nodes a content-only grep misses.
	for i, text := range declared {
		resource := &resources[i]
		if lineHit[resource.id] {
			continue
		}
		out.matches = append(out.matches, Match{
			Path:          out.display,
			Line:          declarationLine(resource),
			Text:          text,
			ResourceID:    resource.id,
			ResourceStart: resource.startsAt,
			ResourceEnd:   resource.endsAt,
			Description:   resource.description,
			MatchedOn:     tiers[resource.id],
			NodeHit:       true,
		})
	}
	return out, nil
}

// countBinaryMatches counts a binary file's matching lines without keeping any of them.
// The count is real -- `grep -c` reports it -- and the bytes never leave this function.
func countBinaryMatches(f *os.File, re *regexp.Regexp, opt Options) int {
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineBytes)
	n := 0
	for scanner.Scan() {
		if re.Match(scanner.Bytes()) {
			n++
			if opt.PerFileLimit > 0 && n >= opt.PerFileLimit {
				break
			}
		}
	}
	_ = scanner.Err()
	return n
}

// declarationLines maps a line number to the resources whose declaration starts there,
// for the matched nodes of one file. Nothing is allocated when the file owns no matched
// node, which is the common case.
func declarationLines(resources []resourceLocation, tiers map[string]MatchSource) map[int][]int {
	if len(tiers) == 0 {
		return nil
	}
	var wanted map[int][]int
	for i := range resources {
		if _, ok := tiers[resources[i].id]; !ok {
			continue
		}
		if wanted == nil {
			wanted = map[int][]int{}
		}
		line := declarationLine(&resources[i])
		wanted[line] = append(wanted[line], i)
	}
	return wanted
}

// declarationLine is where a node row points. A resource with no line span (a
// file-level resource, say) points at the top of its file.
func declarationLine(resource *resourceLocation) int {
	if resource.startsAt <= 0 {
		return 1
	}
	return resource.startsAt
}

// buildResourceIndex maps canonical file path -> resources, narrowest span first.
func buildResourceIndex(topo *domain.Topology) map[string][]resourceLocation {
	index := make(map[string][]resourceLocation)
	if topo == nil {
		return index
	}
	for id, res := range topo.Resources {
		if res.Location.Path == "" {
			continue
		}
		path := canonicalPath(res.Location.Path)
		index[path] = append(index[path], resourceLocation{
			id:          id,
			description: res.Description,
			kind:        res.Kind,
			startsAt:    res.Location.StartsAt,
			endsAt:      res.Location.EndsAt,
		})
	}
	for path := range index {
		sort.Slice(index[path], func(i, j int) bool {
			left, right := span(index[path][i]), span(index[path][j])
			if left != right {
				return left < right
			}
			return index[path][i].id < index[path][j].id
		})
	}
	return index
}

// bestResource picks the narrowest resource containing the line, falling back to the
// file-level resource.
func bestResource(resources []resourceLocation, line int) *resourceLocation {
	var fallback *resourceLocation
	for i := range resources {
		resource := &resources[i]
		if resource.kind == domain.ResourceFile {
			if fallback == nil {
				fallback = resource
			}
			continue
		}
		if resource.startsAt <= line && (resource.endsAt == 0 || line <= resource.endsAt) {
			return resource
		}
	}
	return fallback
}

// resourceLabel names a hit's enclosing resource for the header line.
//
// Under LineRange it is "path:start-end", which is directly runnable: the caller's next move is
// a read of exactly those lines, and aracne answers that read with the whole declaration. The
// id form is kept for identification_mode "id" and whenever a span is unavailable.
func resourceLabel(m Match, opt Options) string {
	if opt.LineRange && m.ResourceStart > 0 && m.ResourceEnd >= m.ResourceStart {
		return fmt.Sprintf("%s:%d-%d", m.Path, m.ResourceStart, m.ResourceEnd)
	}
	return m.ResourceID
}

func span(resource resourceLocation) int {
	if resource.startsAt == 0 || resource.endsAt == 0 {
		return 1 << 30
	}
	return resource.endsAt - resource.startsAt
}

func canonicalPath(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}
	return path
}

func displayPath(path string) string {
	if rel, err := filepath.Rel(".", path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(path)
}
