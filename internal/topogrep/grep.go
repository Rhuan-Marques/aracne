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
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"unicode/utf8"

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
	// FromLine and ToLine confine the search to an inclusive span of lines -- the body of one
	// resource, for `grep pat pkg.Func`. Zero leaves that side unbounded. A line outside the
	// span is not a match, not context and not a declaration this search reports.
	//
	// THE SPAN IS APPLIED DURING THE SCAN, NOT TO A FINISHED RESULT. Trimming the result kept
	// only rows that had already survived the head limit, so a resource at the bottom of a file
	// with more than 200 earlier matches lost every one of its own: the answer was empty with
	// exit 1, `-c` on the same operand said 2, and the trim reset Truncated, so nothing said a
	// cap had been involved. Scanned inside the span, the cap and its trailer are measured
	// against the resource's own matches.
	FromLine int
	ToLine   int
	Before   int // context lines before each hit
	After    int // context lines after each hit
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
	// Plain returns the rows a real grep would have printed and nothing else: textual matches
	// only, in path and line order, with no resource header, no node row, no trailer and NO
	// HEAD LIMIT.
	//
	// It is what an intercepted search must render when a PIPELINE reads it rather than a
	// model. Everything Plain switches off is an addition addressed to a reader, and each one
	// changes what a downstream stage computes:
	//
	//   - the cap turns 281 matches into 200 and says so on a line the next stage filters out
	//     or `head` discards, so the pipeline silently loses a third of its answer;
	//   - the `#` header and the node rows are lines the real command never emitted, so they
	//     are counted by a counter and consume a capper's budget;
	//   - the tiered ranking reorders the matches, so `| head -N` keeps a different N.
	//
	// The caller that sets it is `arac cmd --piped`, which the guard splices only for a segment
	// whose every consumer keeps whole lines in order -- and that promise is only true of rows
	// the real command would have printed. See cli.serveGrep.
	//
	// What it delivers is the same SET of rows, each byte-identical, in path and line order --
	// which is why the walk prunes nothing under it but what the caller excluded (see
	// shouldSkipDir): the dependency and build trees an enriched search skips are trees the
	// real command searches.
	// Not the same sequence: `grep -r` walks in readdir order, which it does not define and
	// which differs between filesystems, while this walk is lexical. A counter, a line filter
	// and anything reading a row's fields therefore agree exactly; a `| head -N` gets N real
	// matches in a deterministic order rather than the N the local filesystem happened to
	// enumerate first.
	Plain bool
	// NoIgnore searches the trees .gitignore excludes, as `rg --no-ignore` does. The
	// pruning it switches off is the ignore hierarchy only: the version control metadata
	// in prunedDirs is skipped either way, being no part of any search.
	NoIgnore bool
	// git is the ignore hierarchy for the root being walked, installed by walkSearch.
	// It is not a caller's to set: it depends on which root is in hand.
	git *gitIgnores
	// LineRange names each annotated hit by "path:start-end" instead of by resource id.
	// See helper.IdentifyLineRange: the id is aracne's vocabulary, the span is the shell's,
	// and a search result is precisely where the caller decides what to read next.
	LineRange bool
	// DescriptionKinds limits which resource kinds may match on their stored
	// description. nil means the caller did not choose and gets
	// DefaultDescriptionKinds(); an explicit empty slice disables description
	// matching entirely. Titles are never gated -- a name match is precise.
	DescriptionKinds []domain.ResourceKind
	// GrepFilters applies the filename filters the way GNU grep does, for the shell surface
	// standing in for it. NameFilters -- its --include and --exclude, in command-line order --
	// replace Globs and ExcludeGlobs: the LAST filter whose glob matches decides, and a file no
	// filter matches is searched unless the first filter is an --include. A walked file is
	// matched by its base name. An operand is held to them too, a file to NameFilters and a
	// directory to ExcludeDirs, matched against the operand and every suffix of it that starts
	// after a slash, as grep matches it.
	GrepFilters bool
	NameFilters []NameFilter
	// FollowLinks is grep's -R: a symbolic link met during the walk is followed. Without it
	// only a link named as a root is, which is -r's rule and ripgrep's.
	FollowLinks bool
	// Only, when set, is the exact list of files the real tool would search, spelled the way it
	// prints them -- ripgrep's, from `rg --files`, which alone knows its .gitignore, .ignore,
	// hidden-file and glob rules. The walk visits nothing else and prints each file under that
	// spelling; Globs, ExcludeGlobs and Type are the tool's to apply, so the caller leaves them
	// empty. Binary files follow ripgrep's rules too: one met in the walk is skipped, and one
	// named that matches makes the search ErrUnmodelled (see searchFile).
	Only []string
	// CountZeros is a tool that prints `path:0` for every file it searched -- grep does, rg does
	// not. The count of a single named file is printed whenever it is set; the zero rows of a
	// wider search only under Plain, which promises the real command's rows.
	CountZeros bool
	// UnicodeWords is a real tool that counts non-ASCII letters as word characters -- GNU grep
	// in a UTF-8 locale, ripgrep always -- where Go's `\b` and `\w` know only ASCII. For a
	// pattern with a word test (-w, `\b`, `\B`, `\w`, `\W`), a line where the test could land
	// next to a non-ASCII byte is one the two may disagree about -- `grep -w Radius` does not
	// match `éRadius`, and Go's `\b` does -- so the search is ErrUnmodelled. A pattern with no
	// word test, and a line of plain ASCII, are answered as before.
	UnicodeWords bool

	// implicitRoot is a search that named no path at all: rows are relative to the working
	// directory with no `./`, the way `grep -r pat` prints them.
	implicitRoot bool
	// only is Only keyed by canonical path, and onlyDirs every directory that leads to one.
	only     map[string]string
	onlyDirs map[string]bool
	// wordTest is the pattern with its word tests loosened, for UnicodeWords; see
	// loosenWordTests. words reports that the pattern has a word test at all.
	wordTest *regexp.Regexp
	words    bool
}

// NameFilter is one --include (Exclude false) or --exclude (Exclude true). See
// Options.NameFilters.
type NameFilter struct {
	Glob    string
	Exclude bool
}

// ErrUnmodelled means the search reached something whose real output this package does not
// reproduce -- a duplicate operand under Plain, a binary or mis-encoded file whose rows the real
// tool would suppress, a symbolic-link loop, a word test beside a non-ASCII letter -- and gave up
// rather than guess. The shell surface answers it by running the real command.
var ErrUnmodelled = errors.New("the real command's answer is not modelled here")

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
	// Searched is every file the search opened, match or not: grep -c prints `path:0` for
	// the rest. See Options.CountZeros.
	Searched []string

	// rootOf is the index of the root each displayed path was reached through. The real
	// command answers its operands in the order it was given them, so paths sort by it first.
	rootOf map[string]int
}

// pathLess orders two displayed paths: by the operand they came from, then by name.
func (r *Result) pathLess(a, b string) bool {
	if ra, rb := r.rootOf[a], r.rootOf[b]; ra != rb {
		return ra < rb
	}
	return a < b
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
		opt.implicitRoot = len(opt.Roots) == 0
		opt.Root = "."
	}
	if opt.Only != nil {
		opt.only, opt.onlyDirs = map[string]string{}, map[string]bool{}
		for _, spelled := range opt.Only {
			key := canonicalPath(spelled)
			opt.only[key] = spelled
			for dir := filepath.Dir(key); !opt.onlyDirs[dir]; dir = filepath.Dir(dir) {
				opt.onlyDirs[dir] = true
			}
		}
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
	if opt.UnicodeWords {
		var loose string
		if loose, opt.words = loosenWordTests(pattern); opt.words {
			// A loosened pattern that does not compile leaves wordTest nil, and then every
			// non-ASCII line is treated as one the tools may disagree about.
			opt.wordTest, _ = regexp.Compile(loose)
		}
	}
	exts, err := typeExtensions(opt.Type)
	if err != nil {
		return nil, err
	}

	index := buildResourceIndex(topo)
	aliasResourceIndexForRoots(index, searchRoots(opt))
	tiers := matchNodes(re, topo, descriptionKindSet(opt.DescriptionKinds))
	out := &Result{
		Counts:     map[string]int{},
		Limit:      effectiveLimit(opt),
		Context:    map[string]map[int]string{},
		RootIsFile: rootIsSingleFile(opt),
		rootOf:     map[string]int{},
	}

	// binaryCounts is kept apart from `all` on purpose: a binary file's matches are real
	// and belong in Counts, but its LINES may never be rendered, so they must not sit in
	// the list the head limit slices and the formatter prints.
	binaryCounts := map[string]int{}
	var all []Match
	if err := walkSearch(opt, exts, func(path, display string, root int, named bool) error {
		found, err := searchFile(path, display, named, re, index, tiers, opt)
		if err != nil {
			return err
		}
		if !found.opened {
			return nil
		}
		out.Searched = append(out.Searched, found.display)
		out.rootOf[found.display] = root
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
	sort.Slice(out.BinaryFiles, func(i, j int) bool { return out.pathLess(out.BinaryFiles[i], out.BinaryFiles[j]) })
	// Priority first, then the familiar path/line order within a tier. Tier is a
	// property of the resource, so one resource's rows never straddle two tiers and
	// FormatResult's one-header-per-run grouping still holds.
	sort.Slice(all, func(i, j int) bool {
		if all[i].MatchedOn != all[j].MatchedOn {
			return all[i].MatchedOn < all[j].MatchedOn
		}
		if all[i].Path != all[j].Path {
			return out.pathLess(all[i].Path, all[j].Path)
		}
		return all[i].Line < all[j].Line
	})

	if opt.Plain {
		// A node row exists because a NAME or a stored DESCRIPTION matched, so the real command
		// never printed it. Dropped here rather than at render time so Total, Counts and Files
		// describe the same rows the caller receives.
		all = dropNodeHits(all)
	}
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
	sort.Slice(out.Files, func(i, j int) bool { return out.pathLess(out.Files[i], out.Files[j]) })

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

// dropNodeHits removes every row a node earned on its title or its description, leaving the
// textual matches a plain grep would have found. See Options.Plain.
func dropNodeHits(matches []Match) []Match {
	kept := matches[:0]
	for _, m := range matches {
		if m.NodeHit {
			continue
		}
		kept = append(kept, m)
	}
	return kept
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
	// A cap the reader can act on is a feature; a cap a PIPELINE cannot see is data loss it
	// has no way to notice. See Options.Plain.
	case opt.Plain:
		return 0
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
// IT NO LONGER NAMES WHAT THE WALK PRUNED. That note existed because aracne pruned trees of
// its own choosing -- dependency and build directories off a hardcoded list, plus the
// scanner's scan.ignore -- and a search that skipped them silently could read as "nothing
// uses this". Now that the only rule is the project's own .gitignore, the note explains a
// decision the project already made and the reader already knows, on every result forever.
// ripgrep prunes the same trees and says nothing; so does this.
func FormatResult(res *Result, opt Options) string {
	return formatBody(res, opt)
}

// formatBody renders a result for the requested mode, without the skipped-directory note.
//
// In content mode the resource annotation is emitted ONCE per resource, as a header above
// its matches, rather than as two extra lines under every matching line. A resource with
// forty hits used to repeat its description forty times.
func formatBody(res *Result, opt Options) string {
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
	// otherwise degrade to plain grep, which is what a broad sweep wanted anyway. Never under
	// Plain, where every added line is a line the pipeline counts.
	annotate := !opt.Plain && res.DistinctResources > 0 && res.DistinctResources <= AnnotateLimit &&
		annotationFits(res.Matches)

	// Context is read in FILE order or it is not read at all: a `-B2` window printed above a
	// match that sits earlier in the file than the previous one is a puzzle, not a context.
	// The tiered ranking has already done its job by this point -- it decided WHICH matches
	// survived the head limit -- so re-ordering what is left costs the ranking nothing.
	rows := res.Matches
	if opt.Before > 0 || opt.After > 0 || opt.Plain {
		rows = append([]Match(nil), rows...)
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].Path != rows[j].Path {
				return res.pathLess(rows[i].Path, rows[j].Path)
			}
			return rows[i].Line < rows[j].Line
		})
	}

	var b strings.Builder
	printed := map[string]map[int]bool{}
	// written is the header in force -- the last one printed -- and pending the one owed to the
	// next row that prints. A header is written only when a row follows it: a node row whose
	// line an earlier match already printed as context used to leave its header standing alone,
	// naming a resource with nothing under it.
	written, pending := "", ""
	lastPath, lastLine := "", 0
	first := true
	// headed marks a resource header as the last thing written. A header already separates
	// the rows under it from the rows above, so it stands in for grep's `--`.
	headed := false
	contextual := opt.Before > 0 || opt.After > 0

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
		if pending != "" {
			if !first {
				b.WriteByte('\n')
			}
			first = false
			b.WriteString(pending)
			written, pending, headed = pending, "", true
		}
		if !first {
			b.WriteByte('\n')
			// grep separates non-contiguous groups with `--`, and only when context was asked
			// for; without it a jump from line 40 to line 900 reads as one block. The test is
			// adjacency to the row printed LAST, which is grep's own rule. Asked per match, of
			// the match's window, it could not see a window an earlier match had half printed.
			if contextual && !headed && (path != lastPath || line != lastLine+1) {
				b.WriteString("--\n")
			}
		}
		first, headed = false, false
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

	for i, m := range rows {
		header := rowHeader(m, opt, annotate)
		// A row printed bare still reads as part of the last header above it. Once one has been
		// written, a bare row gets the header a line outside every declaration gets -- its file --
		// so a line is never attributed to a resource it is not in.
		if header == "" && written != "" {
			header = "# " + m.Path
		}
		pending = ""
		if header != written {
			pending = header
		}

		ctx := res.Context[m.Path]
		for line := m.Line - opt.Before; line < m.Line; line++ {
			if text, ok := ctx[line]; ok {
				emit(m.Path, line, text, false)
			}
		}
		emit(m.Path, m.Line, m.Text, true)
		// The after-window stops short of the next row in the same file. That row is printed
		// next, and its own window reaches further than this one, so whatever lies between it
		// and the end of this window is printed after it -- in file order. Printing the whole
		// window here is what put `33-` and `34-` above the match on line 32.
		end := m.Line + opt.After
		if i+1 < len(rows) && rows[i+1].Path == m.Path && rows[i+1].Line <= end {
			end = rows[i+1].Line - 1
		}
		for line := m.Line + 1; line <= end; line++ {
			if text, ok := ctx[line]; ok {
				emit(m.Path, line, text, false)
			}
		}
	}

	// A binary file's content is never rendered. Saying so is grep's own wording, and it
	// is the only honest row: the file matched, and its bytes are not for a terminal.
	// Under Plain a binary match gets no row: GNU grep (3.5 and later) reports it on STDERR,
	// so the rows a pipeline reads from the real command hold nothing for it. It is still
	// counted, and still listed by -l -- which is what those modes print.
	binaries := res.BinaryFiles
	if opt.Plain {
		binaries = nil
	}
	for _, path := range binaries {
		if !first {
			b.WriteByte('\n')
		}
		first = false
		fmt.Fprintf(&b, "Binary file %s matches", path)
	}

	// The three trailers are prose for a reader. Under Plain there is no reader, and a
	// sentence appended to a pipeline is one more line for it to count, filter or keep.
	if opt.Plain {
		return b.String()
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
	// One line: a stored newline would end the header and let the rest pose as structure.
	if d := strings.Join(strings.Fields(m.Description), " "); d != "" {
		header += " — " + d
	}
	return header
}

// formatCounts renders count mode.
//
// `grep -c pat file` prints a bare number and `grep -rc pat dir` prints one path:count per
// file. That is not a cosmetic difference: a bare count is the whole reason a caller reaches
// for -c instead of piping to `wc -l`, and prefixing the path moves the field they read.
//
// `-H` puts the path back on a single file's count, as it does on its rows. And grep prints a
// count for every file it searched, `path:0` included: a single named file always gets one, and
// a wider search gets the zero rows under Plain, where `grep -c X *.go | grep ':0$'` -- "which
// files never mention X" -- reads them. The annotated answer leaves them out as noise.
func formatCounts(res *Result, opt Options) string {
	bare := opt.Terse && res.RootIsFile && !opt.WithFilename
	paths := make([]string, 0, len(res.Counts))
	for p := range res.Counts {
		paths = append(paths, p)
	}
	if opt.CountZeros && (opt.Plain || res.RootIsFile) {
		for _, p := range res.Searched {
			if _, counted := res.Counts[p]; !counted {
				paths = append(paths, p)
			}
		}
	}
	if len(paths) == 0 {
		if bare && opt.CountZeros {
			return "0"
		}
		return noMatches(opt)
	}
	sort.Slice(paths, func(i, j int) bool { return res.pathLess(paths[i], paths[j]) })
	var b strings.Builder
	for i, p := range paths {
		if i > 0 {
			b.WriteByte('\n')
		}
		if bare {
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

// walkSearch visits every candidate file under each search root, handing each one over with
// the spelling the result prints it under and the index of the root it was reached through.
//
// A file reached through two roots is visited once: `grep pat . src` would otherwise report
// every hit under src twice, and the caps would be measured against a doubled total. That is the
// annotated answer's choice, not the real command's -- grep and rg both search such a file twice
// -- so under Plain, which promises the real command's rows, a second visit is ErrUnmodelled.
func walkSearch(opt Options, exts map[string]bool, visit func(path, display string, root int, named bool) error) error {
	seen := map[string]bool{}
	for i, root := range searchRoots(opt) {
		// One matcher per root, anchored at the repository that encloses it, so a root
		// given as `../other-repo` is judged by ITS .gitignore and not by this one's.
		opt := opt
		opt.git = newGitIgnores(root)
		if !opt.implicitRoot {
			opt.git.exempt(root)
		}
		once := func(path string) error {
			key := canonicalPath(path)
			if seen[key] {
				if opt.Plain {
					return ErrUnmodelled
				}
				return nil
			}
			seen[key] = true
			return visit(path, rowPath(root, path, opt), i, path == root)
		}
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
		// A file named outright is searched whatever aracne's OWN filters say. `grep pat
		// vendor/x.go` asked for that file; answering "no matches" because a filter would have
		// skipped it during a walk answers a different question. The caller's filters are the
		// caller's: see namedFileWanted.
		if !namedFileWanted(root, opt) {
			return nil
		}
		return visit(root)
	}
	if namedDirExcluded(root, opt) {
		return nil
	}
	// A root that is a symbolic link to a directory is walked: the link was named, and grep -r
	// and rg both follow one named on the command line. WalkDir does not follow its root, so it
	// is handed the link with a trailing separator, which the OS resolves -- and the paths below
	// keep the link's name, as the real commands print them.
	start := root
	if li, lerr := os.Lstat(root); lerr == nil && li.Mode()&os.ModeSymlink != 0 {
		start = root + string(filepath.Separator)
	}
	return walkTree(start, opt, exts, visit)
}

// walkTree walks one directory. Without FollowLinks a symbolic link met on the way is skipped,
// which is grep -r's rule (and ripgrep's). With it -- grep -R -- every link is followed, a
// linked directory walked the same way.
//
// Following links can loop, and grep -R names each loop in a warning and walks on, while a
// dangling link is an error that sets its exit status to 2. Neither is modelled: both are
// ErrUnmodelled, and the real command answers.
func walkTree(start string, opt Options, exts map[string]bool, visit func(path string) error) error {
	// Every directory entered, by logical path, so a directory can be checked against the ones
	// above it. Only kept when links are followed; without that there is no way back up.
	entered := map[string]os.FileInfo{}
	var walk func(start string) error
	walk = func(start string) error {
		return filepath.WalkDir(start, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if opt.FollowLinks && d.Type()&os.ModeSymlink != 0 {
				target, statErr := os.Stat(path)
				if statErr != nil {
					return ErrUnmodelled
				}
				if target.IsDir() {
					if shouldSkipDir(path, d.Name(), opt) {
						return nil
					}
					return walk(path + string(filepath.Separator))
				}
				if !target.Mode().IsRegular() || !wantFile(path, d.Name(), exts, opt) {
					return nil
				}
				return visit(path)
			}
			if d.IsDir() {
				// The search root is never pruned by its own name: searching inside
				// ~/.dotfiles must work.
				if path != start {
					if shouldSkipDir(path, d.Name(), opt) {
						return filepath.SkipDir
					}
				}
				if opt.FollowLinks {
					info, infoErr := d.Info()
					if infoErr != nil {
						return filepath.SkipDir
					}
					here := filepath.Clean(path)
					for up := filepath.Dir(here); up != here; here, up = up, filepath.Dir(up) {
						above, ok := entered[up]
						if !ok {
							break
						}
						if os.SameFile(above, info) {
							return ErrUnmodelled
						}
					}
					entered[filepath.Clean(path)] = info
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
	return walk(start)
}

// rowPath spells a file -- or a pruned directory -- the walk reached from root, the way the
// result prints it.
//
// On the shell surface that is the real command's spelling, and the real command echoes the
// operand it was given: `grep -rn x ./go` prints `./go/f.go`, `grep x go/../go/f.go b.go` prints
// the first path exactly as typed, and `grep -r x` with no operand at all prints `f.go` -- the
// `./` there was never the caller's. Each root keeps its own spelling: one `./go` among the
// operands used to put a `./` on the rows of every other. grep drops a directory operand's
// trailing slashes before joining; ripgrep keeps them, and its spelling comes from its own list
// (see Options.Only). `arac grep` and the MCP tool address rows by path and line and imitate
// nothing, so they get the cleaned relative path.
func rowPath(root, path string, opt Options) string {
	if opt.only != nil {
		if spelled, ok := opt.only[canonicalPath(path)]; ok {
			return spelled
		}
	}
	if !opt.Terse || opt.implicitRoot {
		return displayPath(path, false)
	}
	if path == root {
		return filepath.ToSlash(root)
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil {
		return displayPath(path, false)
	}
	// Both separators, spelled out. It was `/`+string(filepath.Separator), which on Unix is
	// the cutset "//" -- a duplicate character, and a cutset that drops the backslash on the
	// one platform where it is the separator.
	return filepath.ToSlash(strings.TrimRight(root, `/\`) + "/" + rel)
}

// namedFileWanted reports whether a file named as an operand is searched. aracne's own filters
// never skip one; the caller's do, the way the real command applies them. ripgrep's list says
// whether it searches the file. grep holds it to --include and --exclude, matched against the
// operand and each suffix of it -- `--exclude=*_test.go` skips a named `pkg/a_test.go` too.
func namedFileWanted(root string, opt Options) bool {
	if opt.only != nil {
		_, ok := opt.only[canonicalPath(root)]
		return ok
	}
	if !opt.GrepFilters || len(opt.NameFilters) == 0 {
		return true
	}
	return admitted(opt.NameFilters, func(glob string) bool { return matchesNameSuffix(glob, root) })
}

// namedDirExcluded is grep's --exclude-dir held against a directory operand, matched as a named
// file is. A search that named no directory is never excluded: grep does not skip the `.` it
// walks for a bare `grep -r`.
func namedDirExcluded(root string, opt Options) bool {
	if !opt.GrepFilters || opt.implicitRoot {
		return false
	}
	for _, ex := range opt.ExcludeDirs {
		if matchesNameSuffix(ex, root) {
			return true
		}
	}
	return false
}

// admitted is GNU grep's rule for a run of --include and --exclude: the last filter whose glob
// matches decides, and a file none matches is searched unless the first filter was an
// --include. `--exclude='*_test.go' --include='*.go'` therefore searches a_test.go (the include
// came last) and README.md (nothing matched, and the first filter excluded).
func admitted(filters []NameFilter, matches func(glob string) bool) bool {
	for i := len(filters) - 1; i >= 0; i-- {
		if matches(filters[i].Glob) {
			return !filters[i].Exclude
		}
	}
	return filters[0].Exclude
}

// matchesNameSuffix is how grep matches an operand against a filter: the whole name, or any
// trailing part of it that starts right after a slash.
func matchesNameSuffix(glob, name string) bool {
	name = filepath.ToSlash(name)
	if globMatch(glob, name) {
		return true
	}
	for i := 0; i < len(name); i++ {
		if name[i] == '/' && (i+1 == len(name) || name[i+1] != '/') && globMatch(glob, name[i+1:]) {
			return true
		}
	}
	return false
}

// globMatch is fnmatch as grep calls it, bar one spelling filepath.Match lacks: `[!...]`
// negates a bracket the way `[^...]` does.
func globMatch(glob, name string) bool {
	ok, err := filepath.Match(strings.ReplaceAll(glob, "[!", "[^"), name)
	return err == nil && ok
}

// prunedDirs are the only directories pruned by name: version-control metadata and aracne's
// own store. Nothing in them is source, no search is ever about them, and ripgrep skips all
// four as hidden -- so this stays a strict subset of what rg prunes, which is the budget the
// rest of the walk is held to (see gitignore.go).
//
// IT IS A LIST AND NOT A DOT-PREFIX RULE. Skipping every name beginning with "." was one
// line and cost the search `.github`, which is where an agent looks for CI configuration --
// `grep -rn runs-on .` came back empty and read as "this project has no CI".
//
// The dependency and build trees that used to sit here -- node_modules, vendor, .venv, the
// cache and output directories -- are gone. They are pruned now exactly when .gitignore
// excludes them, which is the same answer for a project that ignores them and the right one
// for a project that does not: a vendored Go repository greps its own vendor/ tree.
var prunedDirs = map[string]bool{".git": true, ".hg": true, ".svn": true, ".aracne": true}

// shouldSkipDir reports whether to prune a directory.
//
// Beyond the always-noise set it applies the caller's own --exclude-dir and the .gitignore
// hierarchy, and it says nothing about either. A search that prunes what the project itself
// ignores is doing what every other search in a repository does, and a line of output
// explaining it on every result is a line the reader did not ask for.
//
// UNDER Plain ONLY THE CALLER'S OWN EXCLUSIONS APPLY. A plain search is what an intercepted
// command renders when a pipeline reads it, and its promise is the rows the real command would
// have printed; the real `grep -r` descends into every one of these, so pruning them there
// returned a smaller set -- `grep -rn X . | head` lost every ignored hit with nothing to show
// for it.
func shouldSkipDir(path, name string, opt Options) bool {
	if name == "." {
		return false
	}
	// Nothing below it is on the real tool's list: a directory it never enters, quietly.
	if opt.only != nil && !opt.onlyDirs[canonicalPath(path)] {
		return true
	}
	for _, ex := range opt.ExcludeDirs {
		if ok, err := filepath.Match(ex, name); err == nil && ok {
			return true // the caller asked for this; they know
		}
	}
	if opt.Plain {
		return false
	}
	if prunedDirs[name] {
		return true
	}
	return !opt.NoIgnore && opt.git.Match(path, true)
}

// wantFile applies the glob and type filters, the exclusions and the ignore rules.
func wantFile(path, name string, exts map[string]bool, opt Options) bool {
	if opt.only != nil {
		if _, listed := opt.only[canonicalPath(path)]; !listed {
			return false
		}
	}
	// The ignore hierarchy is the project's rule, not the caller's, and a plain search makes
	// no additions or omissions of its own; see shouldSkipDir.
	if !opt.Plain && !opt.NoIgnore && opt.git.Match(path, false) {
		return false
	}
	// grep matches --include and --exclude against the base name, in order; see GrepFilters.
	if opt.GrepFilters && len(opt.NameFilters) > 0 {
		return admitted(opt.NameFilters, func(glob string) bool { return globMatch(glob, name) }) &&
			(len(exts) == 0 || exts[strings.ToLower(filepath.Ext(name))])
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
		"js":         {".js", ".jsx", ".mjs", ".cjs", ".vue"},
		"javascript": {".js", ".jsx", ".mjs", ".cjs", ".vue"},
		"ts":         {".ts", ".tsx", ".mts", ".cts"},
		"typescript": {".ts", ".tsx", ".mts", ".cts"},
		"rust":       {".rs"},
		"rs":         {".rs"},
		"java":       {".java"},
		"c":          {".c", ".h"},
		"cpp":        {".cc", ".cpp", ".cxx", ".hpp", ".hh", ".h", ".hxx", ".inl"},
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
	// opened is a file the search could read, and so one grep -c prints a count for.
	opened bool
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

// farSniffBytes is how far past binarySniffBytes a strict search looks for a NUL. It covers the
// first buffer grep and ripgrep read before deciding a file is binary; see searchFile.
const farSniffBytes = 256 * 1024

// holdsNUL reports whether the file's first n bytes hold a NUL, restoring the file offset.
func holdsNUL(f *os.File, n int) bool {
	defer f.Seek(0, 0)
	buf, _ := io.ReadAll(io.LimitReader(f, int64(n)))
	return bytes.IndexByte(buf, 0) >= 0
}

// searchFile scans one file.
//
// On a scanner error it returns the matches found SO FAR rather than discarding them. The
// previous `return nil, nil` meant one over-long line (a minified bundle, a generated
// table) silently erased every hit in that file -- a search that looked successful and
// simply lied.
//
// A STRICT search -- Plain, or one whose files ripgrep chose (Options.Only) -- promises what the
// real tool would print, and some files' output depends on rules this package does not
// reproduce. It returns ErrUnmodelled for a file that matches and holds such bytes, and the real
// command answers instead:
//
//   - a NUL past the first binarySniffBytes: grep decides "binary" from a first buffer of its own
//     size, and on meeting a NUL later stops printing mid-file; ripgrep does the same with its
//     own buffer and a warning. Where the rows stop is their buffer arithmetic.
//   - under Plain, a line that is not valid UTF-8: grep in a UTF-8 locale suppresses every such
//     row it would print (and `.` never matches its bad bytes), where Go reads them as U+FFFD.
//   - a binary file ripgrep was named: it describes one with a message grep does not print. One
//     it met in a walk it skips outright -- its first buffer holds the NUL, so it reports
//     nothing, not even the matches above it -- and so does this search.
func searchFile(path, display string, named bool, re *regexp.Regexp, index map[string][]resourceLocation, tiers map[string]MatchSource, opt Options) (fileResult, error) {
	out := fileResult{display: display}
	f, err := os.Open(path)
	if err != nil {
		return out, nil
	}
	defer f.Close()
	out.opened = true
	strict := opt.Plain || opt.only != nil

	if looksBinary(f) {
		if opt.only != nil && !named {
			return out, nil
		}
		out.binary = true
		out.count = countBinaryMatches(f, re, opt)
		if opt.only != nil && out.count > 0 {
			return out, ErrUnmodelled
		}
		return out, nil
	}
	lateNUL := strict && holdsNUL(f, farSniffBytes)
	badEncoding := false

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
	for line, idxs := range wanted {
		// A declaration outside the span is never captured, so it is never owed either.
		if inSpan(opt, line) {
			pendingDeclarations += len(idxs)
		}
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
	if opt.Plain {
		scanner.Split(scanRawLines)
	}
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		// The span ends the scan outright: nothing past it is a match, context or a declaration
		// this search reports. Lines before it are skipped before anything reads them, so the
		// before-window of the span's first match cannot reach above the resource either.
		if opt.ToLine > 0 && lineNo > opt.ToLine {
			break
		}
		if lineNo < opt.FromLine {
			continue
		}
		// A trailing CR is a line terminator artifact, not content: it is dropped from what
		// is matched AND from what is reported, so a CRLF checkout does not lose every `$`
		// anchored pattern. Leading whitespace is the opposite -- it IS content, it is what
		// a Python block is made of, and it is what an edit has to reproduce, so it stays.
		//
		// Not under Plain. A pipeline reads the real command's bytes, and grep and rg both keep
		// the CR: `retry$` does not match a CRLF line, and the row they print ends in one. See
		// scanRawLines.
		line := scanner.Text()
		if !opt.Plain {
			line = strings.TrimRight(line, "\r")
		}
		if strict {
			lateNUL = lateNUL || strings.IndexByte(line, 0) >= 0
			badEncoding = badEncoding || (opt.Plain && !utf8.ValidString(line))
		}

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

		// Once -m has its N matches, a line inside the after-window the last one opened is
		// CONTEXT, whatever it holds: `grep -m1 -A3` over two adjacent matches prints the second
		// as `32-`, not `32:`. Reading it as a match counted past N and re-opened the window, so
		// the answer ran on past where grep stops. The window stored the line just above.
		if wordTestUnsure(line, opt) {
			return out, ErrUnmodelled
		}
		if (opt.PerFileLimit == 0 || hits < opt.PerFileLimit) && re.MatchString(line) {
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
	if hits > 0 && (lateNUL || badEncoding) {
		return out, ErrUnmodelled
	}

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

// loosenWordTests rewrites an RE2 pattern so it matches wherever the pattern could match under
// EITHER reading of a word character -- Go's ASCII one or the Unicode one grep and rg use -- and
// reports whether it had a word test at all. `\b` and `\B` are dropped, and `\w` and `\W` also
// accept any non-ASCII character. A `\w` inside a bracket, where widening a negated class would
// narrow it, loosens to the empty pattern, which matches everywhere. Where the loosened pattern
// matches near a non-ASCII byte, the real tool's answer for that line is not one this package can
// vouch for; see Options.UnicodeWords.
func loosenWordTests(pattern string) (string, bool) {
	var b strings.Builder
	found, inClass, anywhere := false, false, false
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch {
		case c == '\\' && i+1 < len(pattern):
			i++
			switch d := pattern[i]; d {
			case 'b', 'B':
				found = true
			case 'w', 'W':
				found, anywhere = true, anywhere || inClass
				b.WriteString(`(?:\` + string(d) + `|[^\x00-\x7F])`)
			default:
				b.WriteByte('\\')
				b.WriteByte(d)
			}
		case c == '[' && !inClass:
			inClass = true
			b.WriteByte(c)
			// A `]` first in a class (after any `^`) is a literal, not its end.
			if i+1 < len(pattern) && pattern[i+1] == '^' {
				i++
				b.WriteByte('^')
			}
			if i+1 < len(pattern) && pattern[i+1] == ']' {
				i++
				b.WriteByte(']')
			}
		case c == '[' && inClass && i+1 < len(pattern) && pattern[i+1] == ':':
			// A POSIX class, `[:alpha:]`, copied whole so its `]` does not end the class.
			end := strings.Index(pattern[i+2:], ":]")
			if end < 0 {
				b.WriteByte(c)
				continue
			}
			b.WriteString(pattern[i : i+2+end+2])
			i += 2 + end + 1
		case c == ']' && inClass:
			inClass = false
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	if anywhere {
		return "", true
	}
	return b.String(), found
}

// wordTestUnsure reports whether the pattern's word test could decide this line differently for
// the real tool: the loosened pattern matches it with a non-ASCII byte inside the match or next
// to either end of it.
func wordTestUnsure(line string, opt Options) bool {
	if !opt.words {
		return false
	}
	ascii := true
	for i := 0; i < len(line); i++ {
		if line[i] >= utf8.RuneSelf {
			ascii = false
			break
		}
	}
	if ascii {
		return false
	}
	if opt.wordTest == nil {
		return true
	}
	for _, loc := range opt.wordTest.FindAllStringIndex(line, -1) {
		for i := max(0, loc[0]-1); i < min(len(line), loc[1]+1); i++ {
			if line[i] >= utf8.RuneSelf {
				return true
			}
		}
	}
	return false
}

// scanRawLines splits at '\n' and nothing else. bufio.ScanLines also drops a CR before the
// newline, which is the CRLF line Plain has to keep.
func scanRawLines(data []byte, atEOF bool) (int, []byte, error) {
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		return i + 1, data[:i], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// scanBinaryLines splits at a newline or a NUL, the line ends grep reads in a binary file.
func scanBinaryLines(data []byte, atEOF bool) (int, []byte, error) {
	if i := bytes.IndexAny(data, "\n\x00"); i >= 0 {
		return i + 1, data[:i], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// countBinaryMatches counts a binary file's matching lines without keeping any of them.
// The count is real -- `grep -c` reports it -- and the bytes never leave this function.
//
// A "line" is what grep counts in a binary file: once it has seen a NUL it reads every NUL as a
// line end too, so `-c` over one counts the matching runs between newlines AND NULs.
func countBinaryMatches(f *os.File, re *regexp.Regexp, opt Options) int {
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineBytes)
	scanner.Split(scanBinaryLines)
	n, lineNo := 0, 0
	for scanner.Scan() {
		lineNo++
		// The span bounds a count as it bounds the rows; see Options.FromLine.
		if opt.ToLine > 0 && lineNo > opt.ToLine {
			break
		}
		if lineNo < opt.FromLine {
			continue
		}
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

// inSpan reports whether a line lies inside Options.FromLine..ToLine, either side of which
// may be unbounded.
func inSpan(opt Options, line int) bool {
	return line >= opt.FromLine && (opt.ToLine <= 0 || line <= opt.ToLine)
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

// aliasResourceIndexForRoots registers the index under the spelling the WALK will produce, for
// a root that reaches the project through a symlinked directory.
//
// The index is keyed by the resource paths the scan stored, and a scan resolves its root before
// it walks (helper.CanonicalPath). The walk here does not: a root keeps the spelling the caller
// typed, because that is what the rows must print. So `arac grep pat .` inside a linked
// checkout -- or anywhere under macOS's /var, which is a symlink to /private/var -- looked up
// every file under a name the index does not hold, found no resource, and answered with a plain
// grep: no headers, no descriptions, no node rows, and no sign that anything was missing.
//
// Aliasing rather than canonicalizing the root keeps both halves: the walk and the rows stay in
// the caller's spelling, and the lookup finds the resources anyway. It costs one EvalSymlinks
// per root, and a pass over the index only for a root that actually resolves elsewhere.
func aliasResourceIndexForRoots(index map[string][]resourceLocation, roots []string) {
	if len(index) == 0 {
		return
	}
	for _, root := range roots {
		abs := canonicalPath(root)
		resolved, err := filepath.EvalSymlinks(abs)
		if err != nil || resolved == "" {
			continue
		}
		if resolved = canonicalPath(resolved); resolved == abs {
			continue
		}
		type alias struct {
			key string
			val []resourceLocation
		}
		var add []alias
		prefix := resolved + string(filepath.Separator)
		for key, val := range index {
			switch {
			case key == resolved:
				add = append(add, alias{abs, val})
			case strings.HasPrefix(key, prefix):
				add = append(add, alias{filepath.Join(abs, key[len(prefix):]), val})
			}
		}
		for _, a := range add {
			if _, exists := index[a.key]; !exists {
				index[a.key] = a.val
			}
		}
	}
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

func displayPath(path string, dotPrefix bool) string {
	out := filepath.ToSlash(path)
	if rel, err := filepath.Rel(".", path); err == nil && domain.RelInside(rel) {
		out = filepath.ToSlash(rel)
	}
	if dotPrefix && !filepath.IsAbs(out) && !strings.HasPrefix(out, "./") {
		out = "./" + out
	}
	return out
}
