package universaltools

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/lazydesc"
	"github.com/Rhuan-Marques/aracne/internal/llm/languages/gotools"
	"github.com/Rhuan-Marques/aracne/internal/llm/languages/javatools"
	"github.com/Rhuan-Marques/aracne/internal/llm/languages/jstools"
	"github.com/Rhuan-Marques/aracne/internal/llm/languages/pythontools"
	"github.com/Rhuan-Marques/aracne/internal/llm/languages/readunit"
	"github.com/Rhuan-Marques/aracne/internal/llm/languages/renderstate"
	"github.com/Rhuan-Marques/aracne/internal/llm/languages/rusttools"
	"github.com/Rhuan-Marques/aracne/internal/llm/tools"
	"github.com/Rhuan-Marques/aracne/internal/toolspec"
	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	"github.com/Rhuan-Marques/aracne/internal/topology/golang"
	"github.com/Rhuan-Marques/aracne/internal/topology/java"
	"github.com/Rhuan-Marques/aracne/internal/topology/javascript"
	"github.com/Rhuan-Marques/aracne/internal/topology/python"
	"github.com/Rhuan-Marques/aracne/internal/topology/rust"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner"
)

// Read is the single read tool.
//
// It replaced read / read_function / read_struct / read_interface / read_named_type /
// read_file / read_package / read_dependency. Eight tools asked the model to classify a
// resource before reading it -- something idresolve already does from the ID alone -- and
// benchmarking showed models routinely picking the wrong one and paying a correction turn.
// Which kinds are readable is now read.kinds, a project-wide setting.
//
// It takes a LIST of ids, because the other half of the turn cost was one lookup per turn:
// every call re-sends the whole transcript, so three ids in one call is far cheaper than three
// calls. Results are grouped by declaring file and share a single context section.
type Read struct {
	mgr *topology.TopologyManager
	// nativeReadAvailable reports whether the harness kept its own read tool for this agent.
	// It decides this tool's NAME and whether the description advertises file paths.
	nativeReadAvailable bool
	cfg                 *helper.Config
	// reg lets a read recover from a stale index by re-parsing the ONE file whose recorded
	// span no longer fits it, instead of handing the model a dead end. Optional: a nil
	// registry only costs the self-heal, never correctness.
	reg *scanner.Registry
	// lazy fills in the descriptions this response is about to show and does not have. A nil
	// Filler is the switched-off state and every call on it is a no-op, so there is no
	// second code path for descriptions.lazy being false.
	lazy *lazydesc.Filler
}

// NewRead builds the read tool. nativeReadAvailable comes from the agent's blocked_tools; reg
// may be nil, which disables the single-file self-heal described on healStale.
func NewRead(mgr *topology.TopologyManager, cfg *helper.Config, nativeReadAvailable bool, reg *scanner.Registry) *Read {
	if cfg == nil {
		cfg = helper.LoadConfig(helper.ConfigPath(mgr.DbPath()))
	}
	return &Read{
		mgr: mgr, nativeReadAvailable: nativeReadAvailable, cfg: cfg, reg: reg,
		lazy: lazydesc.New(mgr, cfg, ""),
	}
}

// WithFiller replaces the lazy-description filler, which is how a test installs a generator
// that does not need an API key. Returns the receiver so it can be chained onto NewRead.
func (r *Read) WithFiller(f *lazydesc.Filler) *Read {
	r.lazy = f
	return r
}

// Name is "read" when the harness's own read is blocked, "read_resource" otherwise. Two tools
// called "read" in one session is a coin flip even though the MCP prefix distinguishes them,
// and the loser is usually the one that knows the topology.
func (r *Read) Name() string { return toolspec.ResolveReadToolName(r.nativeReadAvailable) }

// readsFiles reports whether this tool should advertise file paths: only when "file" is a
// permitted kind AND the harness has no native read of its own to do that job better.
func (r *Read) readsFiles() bool {
	if r.nativeReadAvailable {
		return false
	}
	for _, k := range r.cfg.EffectiveReadKinds() {
		if k == domain.ResourceFile {
			return true
		}
	}
	return false
}

// Description tells the model the one thing that actually changes its behaviour: prefer a
// symbol over a file, and batch.
func (r *Read) Description() string {
	var b strings.Builder
	b.WriteString("Read one or more resources by ID and get their source plus the context they connect to. ")
	b.WriteString("Prefer resources -- a function, method, struct/class or interface -- over whole files: ")
	b.WriteString("a symbol carries its neighbours and their descriptions, which usually answers the question ")
	b.WriteString("for a fraction of a file's tokens. ")
	if r.readsFiles() {
		b.WriteString("A file path works too, for a config, an unsupported language, or when you genuinely need the whole file. ")
	}
	b.WriteString("Pass every ID you need in ONE call: results are grouped by file and share a single context section, ")
	b.WriteString("so one batched call costs far less than one call per ID. ")
	b.WriteString("IDs are forgiving -- a unique trailing part is enough, and a miss returns the nearest candidates.")
	return b.String()
}

// Parameters is a single list. There is deliberately no start_line/end_line: walking a file in
// ranges was the most expensive habit benchmarking found, costing a turn per window.
func (r *Read) Parameters() []tools.Parameter {
	desc := "Resource IDs to read (functions, methods, structs/classes, interfaces). Duplicates are ignored."
	if r.readsFiles() {
		desc = "Resource IDs or file paths to read. Prefer resource IDs over file paths. Duplicates are ignored."
	}
	params := []tools.Parameter{
		{Name: "ids", Type: "array", Items: "string", Description: desc, Required: true},
	}
	// Only worth advertising when file reads are abridged; otherwise it is a no-op knob and
	// every token spent describing it is wasted on every call.
	if r.readsFiles() && r.cfg.EffectiveFileMode() == helper.FileModeSkeleton {
		params = append(params, tools.Parameter{
			Name: "full", Type: "boolean", Required: false,
			Description: "Return whole file bodies verbatim instead of signatures with large " +
				"bodies elided. Use when you need exact text to edit. No effect on non-file ids.",
		})
	}
	return params
}

// readArgs accepts the documented list and also a bare string, because models emit a scalar
// often enough that rejecting it would just buy a correction turn.
func readArgs(args json.RawMessage) ([]string, bool, error) {
	var params struct {
		IDs  json.RawMessage `json:"ids"`
		Full bool            `json:"full"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, false, fmt.Errorf("invalid arguments: %w", err)
	}
	if len(params.IDs) == 0 {
		return nil, false, fmt.Errorf("missing required argument: ids")
	}
	var list []string
	if err := json.Unmarshal(params.IDs, &list); err == nil {
		return list, params.Full, nil
	}
	var single string
	if err := json.Unmarshal(params.IDs, &single); err == nil {
		return []string{single}, params.Full, nil
	}
	return nil, false, fmt.Errorf("invalid arguments: ids must be a string or an array of strings")
}

// ReadIDsOptions lets a caller override what the tool reads.
//
// Kinds is an override no PRODUCT surface uses any more. read.kinds is the project's answer to
// "what may be read here", and every entrance -- the MCP tool, `arac read`, an intercepted
// shell read, the denial proxy -- narrows through the configured set. It stays for tests, and
// for a future caller that genuinely reads outside the project's own policy.
type ReadIDsOptions struct {
	// Kinds overrides read.kinds. Nil uses the configured set, which is what every caller
	// in the product passes.
	Kinds []domain.ResourceKind
	// ForcedKind narrows ID resolution to a single kind (the CLI's --kind).
	ForcedKind domain.ResourceKind
	// ForceFullFile returns whole file bodies verbatim even under read.file_mode "skeleton".
	ForceFullFile bool
}

// Run is the tool entry point: parse the id list, then hand off to ReadIDs.
//
// An all-unresolved read comes back as an ordinary answer, not a failed tool call. A model
// that mistyped every id it asked for is owed the "did you mean" candidates and the
// index-health note -- that report IS the useful reply, and turning it into a tool error would
// spend a turn teaching the model nothing.
func (r *Read) Run(args json.RawMessage) (string, error) {
	ids, full, err := readArgs(args)
	if err != nil {
		return "", err
	}
	out, err := r.ReadIDs(ids, ReadIDsOptions{ForceFullFile: full})
	var unresolved *UnresolvedError
	if errors.As(err, &unresolved) {
		return unresolved.Report, nil
	}
	return out, err
}

// UnresolvedError is a read in which NOTHING resolved: every id was a typo, a path that is not
// there, or -- since read.kinds gates every entrance -- a kind this project does not allow.
//
// The report is the same text a partially successful batch prints under "# UNRESOLVED:",
// carried as an error rather than as output so the surfaces that must FAIL can. `arac read`
// exits non-zero on it and an intercepted shell read refuses on it, instead of printing an
// explanation and calling that success.
type UnresolvedError struct {
	// Report is the reader-facing text: one line per id, plus the index-health note when the
	// database is the likelier culprit than the ids.
	Report string
}

func (e *UnresolvedError) Error() string { return strings.TrimSpace(e.Report) }

// ReadKinds is the project's read.kinds, folded the way the gate folds it -- a method reads as
// a function.
//
// Exported because one read entrance never reaches the gate inside ReadIDs: a WINDOWED shell
// read (`head -20 pkg.Thing`) renders a slice of a file rather than a unit built from an id,
// so it applies the same gate itself. See cli.resolveReadOperand.
func (r *Read) ReadKinds() map[domain.ResourceKind]bool {
	return kindSet(r.cfgOrLoad().EffectiveReadKinds())
}

// KindOf is the kind an id resolves to, or "" when it resolves to nothing. An operand that
// resolves to nothing is not a refusal -- it is the passthrough case, and the caller decides
// that -- so the two answers are deliberately different.
func (r *Read) KindOf(id string) domain.ResourceKind {
	target, _, err := resolveReadTargetWith(r.mgr, id, func(domain.Resource) bool { return true })
	if err != nil {
		return ""
	}
	return target.res.Kind
}

// Suggestion is what the resolver has to say about an id that names nothing uniquely: the
// resources an ambiguous id matched, or the ranked "did you mean" list for a near miss. It is ""
// when the id resolves, and when nothing resembles it -- the case a caller should hand back to
// the shell unchanged.
func (r *Read) Suggestion(id string) string {
	_, note, err := resolveReadTargetWith(r.mgr, id, func(domain.Resource) bool { return true })
	if note != "" {
		return strings.TrimSpace(note)
	}
	var ce *candidatesError
	if errors.As(err, &ce) {
		return ce.Error()
	}
	return ""
}

// KindRefusal is the error every entrance gives for a kind read.kinds does not allow, so the
// tool, the CLI and the shell all say the same thing about the same setting.
func KindRefusal(kind domain.ResourceKind, kinds []domain.ResourceKind) error {
	return fmt.Errorf("resolves to a %s, which read.kinds does not allow (allowed: %s)",
		kind, kindNames(kinds))
}

// ReadIDs resolves every id, renders the bodies grouped by declaring file, then ONE shared
// context section for the whole batch.
//
// Bodies before context is load-bearing: by the time a neighbour is considered, the render
// state already knows every resource shown as source anywhere in the response -- including the
// ones a whole-file read pulled in implicitly -- so none of them can be listed again.
func (r *Read) ReadIDs(rawIDs []string, opt ReadIDsOptions) (string, error) {
	ids := normalizeIDs(rawIDs)
	if len(ids) == 0 {
		return "", fmt.Errorf("missing required argument: ids")
	}

	// The config the tool was CONSTRUCTED with, not a fresh read off disk. Every caller
	// passes one and this re-read discarded it -- harmless today because they all load from
	// the same path, but it made the injected config silently inert and cost a JSON parse on
	// every read. cfgOrLoad is the same accessor the sibling read paths use.
	cfg := r.cfgOrLoad()
	kinds := opt.Kinds
	if kinds == nil {
		kinds = cfg.EffectiveReadKinds()
	}
	allowed := kindSet(kinds)
	filter := topology.WithContextFilter(cfg.EffectiveContextFilter())

	topo, topoErr := r.mgr.ReadAll()

	// One ledger for the whole batch, shared with Render below. Bodies are assembled here,
	// before Render runs, so an enclosing type inlined by the first method of a struct is
	// only recognized as "already shown" by the second if both phases consult the same
	// state. Without it, five methods of one struct emitted the struct five times.
	st := renderstate.New()

	// Resolved once for the batch. The per-call override exists because a model that has seen
	// only a skeleton and now needs exact bytes to build an `edit` old_string must be able to
	// ask for them without an admin changing the project config.
	fileMode := cfg.EffectiveFileMode()
	if opt.ForceFullFile {
		fileMode = helper.FileModeFull
	}
	// Skeleton has its own threshold, deliberately not context_filter.small_function_threshold.
	// Sharing them meant a project that (reasonably) set the context knob to 40 lines got a
	// skeleton that elided almost nothing -- 2 of 39 reads, and a whole-file read still 1.00x
	// the bytes on disk. See helper.DefaultSkeletonThreshold.
	skeletonThreshold := cfg.EffectiveSkeletonThreshold()
	// A symbol body over this many lines is abridged the same way. Disabled when the caller
	// asked for exact bytes, since that request is usually about building an `edit` old_string.
	maxSymbolLines := cfg.EffectiveMaxSymbolLines()
	if opt.ForceFullFile {
		maxSymbolLines = 0
	}

	build := func(topo *domain.Topology, topoErr error, st *renderstate.State) ([]readunit.Unit, []string) {
		var units []readunit.Unit
		var problems []string
		for _, id := range ids {
			u, note, err := r.unitFor(topo, topoErr, id, allowed, kinds, filter, opt.ForcedKind, st, fileMode, skeletonThreshold)
			switch {
			case err != nil:
				problems = append(problems, fmt.Sprintf("- %s: %v", id, err))
			case note != "":
				problems = append(problems, fmt.Sprintf("- %s: %s", id, note))
			default:
				u.Label = displayPath(topo, u.Path)
				u.Body = abridgeSymbolBody(u, maxSymbolLines)
				units = append(units, u)
			}
		}
		return units, problems
	}

	units, problems := build(topo, topoErr, st)

	// The neighbours this response is about to name are only known once the units exist, so
	// the fill happens between building them and rendering them -- and a fill that writes
	// anything invalidates the units, which were assembled against the topology as it was a
	// moment ago. Rebuilding is the cheap half of a step whose other half was a provider
	// call, and it is the only way the render can see prose that did not exist when the
	// bodies were cut. The state has to be fresh too: replaying the old ledger would suppress
	// every enclosing type it already recorded as inlined.
	if r.lazy.FillForRead(topo, unitIDs(units)) {
		if refreshed, err := r.mgr.ReadAll(); err == nil {
			topo, topoErr = refreshed, nil
			st = renderstate.New()
			units, problems = build(topo, topoErr, st)
		}
	}

	out := readunit.Render(units, readunit.Options{
		IncludeIncoming: cfg.EffectiveIncludeIncoming(),
		State:           st,
		Locate:          locator(topo, cfg),
	})

	// The over-serve ceiling. This tool stands in for the harness's own read, and past a few
	// multiples of the source it delivered, the context block is no longer what makes it the
	// cheaper one. The fallback is the same source without aracne's sections -- rebuilt
	// against a fresh ledger, since the render above has already spent this one.
	if !cfg.WithinOverserve(out, bodyBytes(units), helper.OverserveReadFree) {
		plainState := renderstate.New()
		plain, _ := build(topo, topoErr, plainState)
		if bare := readunit.Render(withoutTopology(plain), readunit.Options{
			State:  plainState,
			Locate: locator(topo, cfg),
		}); bare != "" {
			out = bare
		}
	}

	// A bad id in a batch must not throw away the good ones: the whole point of batching is
	// that one call answers several questions, and failing all of them over one typo would
	// cost exactly the extra turn this tool exists to save.
	if len(problems) > 0 {
		if out != "" {
			out += "\n"
		}
		out += "# UNRESOLVED:\n" + strings.Join(problems, "\n") + "\n"
		// Attribute the miss. Without this a stale database and a typo produce the same
		// output, and the model has no way to tell which recovery is the cheap one.
		if note := r.indexHealthNote(); note != "" {
			out += note + "\n"
		}
		// Nothing came back at all: the report is the whole answer, so hand it up as a
		// failure. Callers that would rather narrate than fail unwrap it -- see Run.
		if len(units) == 0 {
			return "", &UnresolvedError{Report: out}
		}
	}
	if out == "" {
		return "", fmt.Errorf("no readable resources for the given ids")
	}
	return out, nil
}

// bodyBytes is the source a read delivers, and the denominator the over-serve ceiling divides
// by. Everything else in the answer -- the group headers, the import block, the context and
// used-by sections -- is what aracne added to it.
func bodyBytes(units []readunit.Unit) int {
	total := 0
	for _, u := range units {
		total += len(u.Body)
	}
	return total
}

// withoutTopology is the units with their topology sections dropped: the source and its
// imports, and nothing aracne put around them.
func withoutTopology(units []readunit.Unit) []readunit.Unit {
	out := make([]readunit.Unit, len(units))
	copy(out, units)
	for i := range out {
		out[i].Context = nil
		out[i].Incoming = nil
	}
	return out
}

// unitIDs is the canonical ids a response will show as source: the seeds a read fill plans
// from, and the set it must never describe (their bodies are the answer, not their prose).
func unitIDs(units []readunit.Unit) []string {
	out := make([]string, 0, len(units))
	for _, u := range units {
		if u.ID != "" {
			out = append(out, u.ID)
		}
		// A whole-file read covers every declaration inside it. Those declarations are
		// printed as source, so like the file itself they need no description of their own --
		// but what they REACH is exactly what the file's context section lists, and none of
		// it is reachable from the file node alone.
		out = append(out, u.Covers...)
	}
	return out
}

// locator returns the function that names a resource by span, or nil under identification_mode
// "id" -- where nil makes the whole rewrite a no-op and every existing surface is untouched.
func locator(topo *domain.Topology, cfg *helper.Config) func(string) string {
	if topo == nil || cfg == nil || !cfg.LineRangeIdentification() {
		return nil
	}
	return func(id string) string { return SpanOf(topo, id) }
}

// SpanOf renders a resource's location as "path:start-end", relative to the topology root, or
// "" when the id is unknown or carries no usable span.
//
// The empty return is load-bearing: it is what tells the context rewriter to leave a line
// alone, which is how section headings survive a pass that only wants to touch resource ids.
func SpanOf(topo *domain.Topology, id string) string {
	if topo == nil || id == "" {
		return ""
	}
	res, ok := topo.Resources[id]
	if !ok {
		if res, ok = topo.Resources[helper.NormalizeResourceID(id)]; !ok {
			return ""
		}
	}
	loc := res.Location
	if loc.Path == "" {
		return ""
	}
	path := displayPath(topo, loc.Path)
	if res.Kind == domain.ResourceFile || loc.StartsAt < 1 || loc.EndsAt < loc.StartsAt {
		return path
	}
	return fmt.Sprintf("%s:%d-%d", path, loc.StartsAt, loc.EndsAt)
}

// normalizeIDs canonicalizes and de-duplicates the input, preserving first-seen order so the
// output follows the order the model asked in.
func normalizeIDs(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, raw := range in {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		id = helper.NormalizeResourceID(id)
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// kindSet turns the configured kinds into a lookup, folding method into function: a method is
// resolved and read exactly as a function is, as it was under read_function.
func kindSet(kinds []domain.ResourceKind) map[domain.ResourceKind]bool {
	set := make(map[domain.ResourceKind]bool, len(kinds)+1)
	for _, k := range kinds {
		set[k] = true
		if k == domain.ResourceFunction {
			set[domain.ResourceMethod] = true
		}
	}
	return set
}

// kindNames lists the configured kinds for an error message.
func kindNames(kinds []domain.ResourceKind) string {
	names := make([]string, 0, len(kinds))
	for _, k := range kinds {
		names = append(names, string(k))
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// unitFor resolves one id and builds its Unit. The middle return is a soft note (an ambiguity
// list, say) that belongs in the output without being an error.
func (r *Read) unitFor(topo *domain.Topology, topoErr error, id string, allowed map[domain.ResourceKind]bool,
	kinds []domain.ResourceKind, filter topology.TopologyOption, forcedKind domain.ResourceKind,
	st *renderstate.State, fileMode string, smallThreshold int) (readunit.Unit, string, error) {

	if topoErr == nil {
		match := func(res domain.Resource) bool {
			if forcedKind != "" {
				return res.Kind == forcedKind
			}
			return true
		}

		// `src/app.rs:build_app` and `src/app.rs:95-115` are both shapes a model invents when
		// it wants part of a file, and both missed in the benchmark run. The first is a
		// scoped symbol lookup and is served; the second is a line window and is answered
		// with the id that covers it -- see nodesSpanning for why it is not served directly.
		lookup := id
		if base, kind, from, to, symbol := splitFileSuffix(id); kind != suffixNone {
			switch kind {
			case suffixRange:
				return readunit.Unit{}, "", rangeRedirect(topo, id, base, from, to)
			case suffixSymbol:
				lookup = symbol
				inner := match
				// Scope during resolution, never after: idresolve counts its matches AFTER
				// the filter, so a name that is ambiguous repo-wide but unique in this file
				// resolves cleanly here and would come back "ambiguous" if filtered later.
				match = func(res domain.Resource) bool { return inner(res) && declaredIn(base, res) }
			}
		}

		target, note, resolveErr := resolveReadTargetWith(r.mgr, lookup, match)
		if note != "" {
			return readunit.Unit{}, note, nil
		}
		if resolveErr == nil {
			if !allowed[target.res.Kind] {
				return readunit.Unit{}, "", KindRefusal(target.res.Kind, kinds)
			}
			u, buildNote, buildErr := r.buildUnit(topo, target, filter, st, fileMode, smallThreshold)
			if se, stale := topology.AsStaleIndex(buildErr); stale {
				// `lookup`, not `id`: for the `path/to/file.go:Symbol` form they differ, and
				// re-resolving the composite string fails every time -- so the self-heal
				// re-parsed the file and then handed back the original error anyway.
				return r.healStale(se, lookup, match, allowed, filter, buildErr, st, fileMode, smallThreshold)
			}
			return u, buildNote, buildErr
		}
		// Hold the resolver's error: it carries the ranked "did you mean" candidates, and a
		// bare "not found" would cost the model a whole exploration turn to recover what the
		// resolver already worked out. It is only surfaced if the raw-file fallback also
		// fails, so a legitimate file read is never pre-empted by a fuzzy near-miss.
		if !allowed[domain.ResourceFile] {
			return readunit.Unit{}, "", resolveErr
		}
		if u, err := r.rawFileUnit(topo, id); err == nil {
			return u, "", nil
		}
		return readunit.Unit{}, "", resolveErr
	}

	// No topology at all (not scanned yet). Reading a path off disk is still a legitimate
	// answer -- configs, lockfiles, unsupported languages -- when files are readable.
	if !allowed[domain.ResourceFile] {
		return readunit.Unit{}, "", fmt.Errorf("not found in topology")
	}
	u, err := r.rawFileUnit(topo, id)
	if err != nil {
		return readunit.Unit{}, "", err
	}
	return u, "", nil
}

// healStale recovers a read whose target is in the graph but whose recorded span no longer
// fits the file: it re-parses that ONE file, re-resolves the id against the refreshed graph,
// and builds the unit again.
//
// WHY THIS EXISTS. A cold index -- a database restored beside source it was not built from, a
// checkout or branch switch, an edit made outside aracne -- leaves every symbol in the touched
// file unreadable, and the failure looked exactly like a wrong ID. A benchmarked flask
// instance lost all three of its seeds to it: `Config.from_file` resolved fine, the CLASS
// around it was indexed nine lines past the end of the file, and the model spent its turns
// grepping instead. Re-indexing one file is cheap and is precisely what an edit to that file
// would have done anyway -- which is how the same instance recovered, by accident, on the run
// where it happened to edit first.
//
// Bounded on purpose: one file, one retry, no recursion. If it fails again the caller gets the
// second error, which still says "not indexed".
func (r *Read) healStale(se *topology.StaleIndexError, id string, match func(domain.Resource) bool,
	allowed map[domain.ResourceKind]bool, filter topology.TopologyOption, original error,
	st *renderstate.State, fileMode string, smallThreshold int) (readunit.Unit, string, error) {

	if r.reg == nil || se.Path == "" {
		return readunit.Unit{}, "", original
	}
	// Under the per-file lock, so a re-index racing a concurrent edit of the same file cannot
	// interleave with it.
	if _, err := r.mgr.WithFileLock(se.Path, func(bool) (string, error) {
		_, updErr := r.mgr.UpdateFile(se.Path, r.reg)
		return "", updErr
	}); err != nil {
		return readunit.Unit{}, "", original
	}

	topo, err := r.mgr.ReadAll()
	if err != nil {
		return readunit.Unit{}, "", original
	}
	// Re-resolve rather than reusing the old target: a re-parse can move a resource, and a
	// rename or deletion in the same edit can retire its ID entirely.
	target, note, resolveErr := resolveReadTargetWith(r.mgr, id, match)
	if note != "" {
		return readunit.Unit{}, note, nil
	}
	if resolveErr != nil || !allowed[target.res.Kind] {
		return readunit.Unit{}, "", original
	}
	return r.buildUnit(topo, target, filter, st, fileMode, smallThreshold)
}

// indexHealthNote is the line appended under "# UNRESOLVED:" when the database has drifted
// from the source tree.
//
// A miss with a current index means the ID is wrong; a miss with a stale one may mean the
// symbol simply was never indexed. Saying which is the difference between a correction turn
// and an exploration turn -- and it is the difference between a benchmark measuring the tool
// and a benchmark measuring its fixtures. Computed only when something already failed, so the
// tree walk never lands on the happy path.
func (r *Read) indexHealthNote() string {
	health, err := r.mgr.IndexHealth("", r.reg)
	if err != nil || !health.Stale() {
		return ""
	}
	files := health.Files()
	if len(files) > 5 {
		files = append(files[:5:5], fmt.Sprintf("… and %d more", len(health.Files())-5))
	}
	return fmt.Sprintf(
		"NOTE: the index is STALE — %s since the last scan (%s). A miss above may mean "+
			"\"not indexed\" rather than \"does not exist\"; re-scan with `arac scan` and retry.",
		health.Summary(), strings.Join(files, ", "))
}

// buildUnit dispatches on the RESOURCE's language and kind. Files are language-neutral: the
// body is the source and the context is a generic graph walk, so no per-language file
// formatter is involved.
func (r *Read) buildUnit(topo *domain.Topology, t readTarget, filter topology.TopologyOption, st *renderstate.State,
	fileMode string, smallThreshold int) (readunit.Unit, string, error) {
	// The language-neutral readers below take the RESOLVED filter rather than the option,
	// because they render their context themselves instead of going through a manager.
	ctxFilter := r.cfgOrLoad().EffectiveContextFilter()
	if t.res.Kind == domain.ResourceFile {
		return r.fileUnit(topo, t, ctxFilter, fileMode, smallThreshold)
	}

	switch t.res.Kind {
	case domain.ResourceFunction, domain.ResourceMethod:
		switch t.res.Language {
		case "go":
			ctx, err := golang.NewGoManager(r.mgr).ReadFunction(t.id, filter)
			return wrapStateful(gotools.FunctionUnit, ctx, err, st)
		case "python":
			ctx, err := python.NewPythonManager(r.mgr).ReadFunction(t.id, filter)
			return wrapStateful(pythontools.FunctionUnit, ctx, err, st)
		case "javascript", "typescript":
			ctx, err := javascript.NewJavaScriptManager(r.mgr).ReadFunction(t.id, filter)
			return wrapStateful(jstools.FunctionUnit, ctx, err, st)
		case "rust":
			ctx, err := rust.NewRustManager(r.mgr).ReadFunction(t.id, filter)
			return wrapStateful(rusttools.FunctionUnit, ctx, err, st)
		case "java":
			ctx, err := java.NewJavaManager(r.mgr).ReadFunction(t.id, filter)
			return wrapStateful(javatools.FunctionUnit, ctx, err, st)
		}
	case domain.ResourceStruct:
		// A Python ABC/Protocol is a class, so it lands here rather than under interface --
		// which is correct: its source and members are what the model wants either way.
		switch t.res.Language {
		case "go":
			ctx, err := golang.NewGoManager(r.mgr).ReadStruct(t.id, filter)
			return wrap(gotools.StructUnit, ctx, err)
		case "python":
			ctx, err := python.NewPythonManager(r.mgr).ReadClass(t.id, filter)
			return wrap(pythontools.ClassUnit, ctx, err)
		case "javascript", "typescript":
			ctx, err := javascript.NewJavaScriptManager(r.mgr).ReadClass(t.id, filter)
			return wrap(jstools.ClassUnit, ctx, err)
		case "rust":
			ctx, err := rust.NewRustManager(r.mgr).ReadStruct(t.id, filter)
			return wrap(rusttools.StructUnit, ctx, err)
		case "java":
			ctx, err := java.NewJavaManager(r.mgr).ReadStruct(t.id, filter)
			return wrap(javatools.StructUnit, ctx, err)
		}
	case domain.ResourceInterface:
		switch t.res.Language {
		case "go":
			ctx, err := golang.NewGoManager(r.mgr).ReadInterface(t.id, filter)
			return wrap(gotools.InterfaceUnit, ctx, err)
		case "typescript", "javascript":
			ctx, err := javascript.NewJavaScriptManager(r.mgr).ReadInterface(t.id, filter)
			return wrap(jstools.InterfaceUnit, ctx, err)
		case "rust":
			ctx, err := rust.NewRustManager(r.mgr).ReadInterface(t.id, filter)
			return wrap(rusttools.InterfaceUnit, ctx, err)
		case "java":
			ctx, err := java.NewJavaManager(r.mgr).ReadInterface(t.id, filter)
			return wrap(javatools.InterfaceUnit, ctx, err)
		}
	case domain.ResourceNamedType:
		switch t.res.Language {
		case "go":
			ctx, err := golang.NewGoManager(r.mgr).ReadNamedType(t.id)
			return wrap(gotools.NamedTypeUnit, ctx, err)
		case "typescript", "javascript":
			ctx, err := javascript.NewJavaScriptManager(r.mgr).ReadNamedType(t.id)
			return wrap(jstools.NamedTypeUnit, ctx, err)
		case "rust":
			ctx, err := rust.NewRustManager(r.mgr).ReadNamedType(t.id)
			return wrap(rusttools.NamedTypeUnit, ctx, err)
		}
	case domain.ResourcePackage:
		if t.res.Language == "go" {
			ctx, err := golang.NewGoManager(r.mgr).ReadPackage(t.id)
			return wrap(gotools.PackageUnit, ctx, err)
		}
	case domain.ResourceDependency:
		switch t.res.Language {
		case "go":
			ctx, err := golang.NewGoManager(r.mgr).ReadDependency(t.id)
			return wrap(gotools.DependencyUnit, ctx, err)
		case "python":
			ctx, err := python.NewPythonManager(r.mgr).ReadDependency(t.id)
			return wrap(pythontools.DependencyUnit, ctx, err)
		case "javascript", "typescript":
			ctx, err := javascript.NewJavaScriptManager(r.mgr).ReadDependency(t.id)
			return wrap(jstools.DependencyUnit, ctx, err)
		case "rust":
			ctx, err := rust.NewRustManager(r.mgr).ReadDependency(t.id)
			return wrap(rusttools.DependencyUnit, ctx, err)
		case "java":
			ctx, err := java.NewJavaManager(r.mgr).ReadDependency(t.id)
			return wrap(javatools.DependencyUnit, ctx, err)
		}
	}

	// Variables, and any kind a language has no enriched reader for, still read: source cut
	// plus a generic neighbour walk. A resource the model can name should never be a dead end.
	return r.genericUnit(topo, t, ctxFilter)
}

// wrap adapts a (context, error) manager call into a Unit, keeping the dispatch table above to
// two lines per case.
func wrap[T any](build func(T) readunit.Unit, ctx T, err error) (readunit.Unit, string, error) {
	if err != nil {
		return readunit.Unit{}, "", err
	}
	return build(ctx), "", nil
}

// wrapStateful is wrap for the builders that need the batch's shared render state, so an
// enclosing type inlined into one member's body is not inlined again into the next one's.
func wrapStateful[T any](build func(T, *renderstate.State) readunit.Unit, ctx T, err error, st *renderstate.State) (readunit.Unit, string, error) {
	if err != nil {
		return readunit.Unit{}, "", err
	}
	return build(ctx, st), "", nil
}

// fileUnit builds a whole-file read with no language-specific code.
//
// Covers names every declaration in the file, which is what keeps them out of the context
// section; Neighbors is what those declarations reach outside the file, which is what the
// context section should have been showing all along.
func (r *Read) fileUnit(topo *domain.Topology, t readTarget, filter domain.ContextFilter,
	fileMode string, smallThreshold int) (readunit.Unit, string, error) {
	var body string
	if fileMode == helper.FileModeSkeleton {
		// The locator is passed so the skeleton's markers speak the identification mode's
		// vocabulary: a span under intercept_line_ranges, where a resource id is the one
		// token the mode exists not to hand back.
		sk, skErr := skeletonBody(topo, r.mgr, t.id, smallThreshold, locator(topo, r.cfgOrLoad()))
		if skErr != nil {
			return readunit.Unit{}, "", skErr
		}
		body = sk
	} else {
		entry, err := r.mgr.Cut(domain.Location{Path: t.id})
		if err != nil {
			return readunit.Unit{}, "", err
		}
		body = entry.Cut
	}
	members := domain.FileMembers(topo, t.id)
	neighbors := domain.OutgoingNeighbors(topo, members, map[string]bool{t.id: true})

	u := readunit.Unit{
		ID:     t.id,
		Kind:   domain.ResourceFile,
		Path:   t.id,
		Line:   0,
		Fence:  t.res.Language,
		Body:   body,
		Covers: members,
	}
	u.Context = neighborContext(neighbors, filter)
	return u, "", nil
}

// genericUnit is the language-neutral fallback: the resource's own cut plus its outgoing
// neighbours straight off the graph.
func (r *Read) genericUnit(topo *domain.Topology, t readTarget, filter domain.ContextFilter) (readunit.Unit, string, error) {
	entry, err := r.mgr.Cut(t.res.Location)
	if err != nil {
		return readunit.Unit{}, "", err
	}
	u := readunit.Unit{
		ID:    t.id,
		Kind:  t.res.Kind,
		Path:  t.res.Location.Path,
		Line:  t.res.Location.StartsAt,
		Fence: t.res.Language,
		Body:  entry.Cut,
	}
	u.Context = neighborContext(domain.OutgoingNeighbors(topo, []string{t.id}, nil), filter)
	return u, "", nil
}

// rawFileUnit reads a path that is not in the topology at all.
func (r *Read) rawFileUnit(topo *domain.Topology, id string) (readunit.Unit, error) {
	root := ""
	if topo != nil {
		root = topo.Root
	}
	maxSize := r.cfgOrLoad().EffectiveMaxFileSize()
	for _, cand := range readPathCandidates(id, root) {
		info, err := os.Stat(cand)
		if err != nil || info.IsDir() {
			continue
		}
		content, err := helper.ReadRawFile(cand, maxSize)
		if err != nil {
			return readunit.Unit{}, err
		}
		// ReadRawFile prefixes the basename; the group header already carries the path.
		if _, rest, ok := strings.Cut(content, "\n"); ok {
			content = rest
		}
		return readunit.Unit{ID: cand, Kind: domain.ResourceFile, Path: cand, Body: content}, nil
	}
	return readunit.Unit{}, fmt.Errorf("not found in topology and not a readable file")
}

// readPathCandidates returns the input followed by alternative path forms: its absolute form,
// and (for a relative input) its form joined onto the topology root.
func readPathCandidates(id, root string) []string {
	candidates := []string{id}
	seen := map[string]bool{id: true}
	add := func(p string) {
		if p != "" && !seen[p] {
			seen[p] = true
			candidates = append(candidates, p)
		}
	}
	if abs, err := filepath.Abs(id); err == nil {
		add(abs)
	}
	if root != "" && !filepath.IsAbs(id) {
		add(filepath.Join(root, id))
	}
	return candidates
}

// neighborContext renders a flat neighbour list, skipping whatever the response already shows
// in full and whatever read.context_filter says not to render.
//
// THE FILTER HAS TO BE APPLIED HERE TOO. ReadIDs resolves read.context_filter and threads it
// into the per-language managers, which honour it -- but the two language-neutral readers,
// fileUnit and genericUnit, build their context through this function and used to take no
// filter at all. So the project-wide dial had no effect on the two shapes a terminal-surface
// project sees most: an intercepted `cat file.go`, and any variable or unsupported-language
// resource. A project that set "off" to stop paying for context blocks kept paying for them
// on exactly the reads it was trimming.
func neighborContext(neighbors []domain.Resource, filter domain.ContextFilter) func(*strings.Builder, *renderstate.State) {
	if len(neighbors) == 0 {
		return nil
	}
	return func(b *strings.Builder, st *renderstate.State) {
		g := st.Guard(b)
		for _, n := range neighbors {
			if !st.Renderable(n.ID) || !g.More() {
				continue
			}
			hasDescription := strings.TrimSpace(n.Description) != ""
			if filter.For(n.Kind, locSpan(n.Location), hasDescription) == domain.VisibilityHidden {
				continue
			}
			description := n.Description
			if description == "" {
				description = "no description"
			}
			fmt.Fprintf(b, "## %s (%s): %s\n", n.ID, n.Kind, domain.RenderDescription(n.Kind, description))
		}
	}
}

// locSpan is a resource's inclusive line count, or 0 when it has no usable span -- which is
// what ContextFilter.For reads as "unknown".
func locSpan(loc domain.Location) int {
	if loc.StartsAt < 1 || loc.EndsAt < loc.StartsAt {
		return 0
	}
	return loc.EndsAt - loc.StartsAt + 1
}

// displayPath renders a file ID for the group header: relative to the topology root when it
// sits underneath it, absolute otherwise.
//
// File IDs are absolute paths, and repeating a long absolute prefix above every group is pure
// overhead -- on small resources the header rivalled the source it introduced. The relative
// form still resolves if the model passes it back, because idresolve tolerates a missing root
// prefix.
func displayPath(topo *domain.Topology, path string) string {
	if topo == nil || topo.Root == "" || path == "" {
		return path
	}
	rel, err := filepath.Rel(topo.Root, path)
	if err != nil || !domain.RelInside(rel) {
		return path
	}
	return filepath.ToSlash(rel)
}
