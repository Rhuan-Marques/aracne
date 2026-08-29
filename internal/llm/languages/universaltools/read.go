package universaltools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"aracne/internal/helper"
	"aracne/internal/llm/languages/gotools"
	"aracne/internal/llm/languages/javatools"
	"aracne/internal/llm/languages/jstools"
	"aracne/internal/llm/languages/pythontools"
	"aracne/internal/llm/languages/readunit"
	"aracne/internal/llm/languages/renderstate"
	"aracne/internal/llm/languages/rusttools"
	"aracne/internal/llm/tools"
	"aracne/internal/toolspec"
	"aracne/internal/topology"
	"aracne/internal/topology/domain"
	"aracne/internal/topology/golang"
	"aracne/internal/topology/java"
	"aracne/internal/topology/javascript"
	"aracne/internal/topology/python"
	"aracne/internal/topology/rust"
	"aracne/internal/topology/scanner"
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
}

// NewRead builds the read tool. nativeReadAvailable comes from the agent's blocked_tools; reg
// may be nil, which disables the single-file self-heal described on healStale.
func NewRead(mgr *topology.TopologyManager, cfg *helper.Config, nativeReadAvailable bool, reg *scanner.Registry) *Read {
	if cfg == nil {
		cfg = helper.LoadConfig(helper.ConfigPath(mgr.DbPath()))
	}
	return &Read{mgr: mgr, nativeReadAvailable: nativeReadAvailable, cfg: cfg, reg: reg}
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
	return []tools.Parameter{
		{Name: "ids", Type: "array", Items: "string", Description: desc, Required: true},
	}
}

// readArgs accepts the documented list and also a bare string, because models emit a scalar
// often enough that rejecting it would just buy a correction turn.
func readArgs(args json.RawMessage) ([]string, error) {
	var params struct {
		IDs json.RawMessage `json:"ids"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if len(params.IDs) == 0 {
		return nil, fmt.Errorf("missing required argument: ids")
	}
	var list []string
	if err := json.Unmarshal(params.IDs, &list); err == nil {
		return list, nil
	}
	var single string
	if err := json.Unmarshal(params.IDs, &single); err == nil {
		return []string{single}, nil
	}
	return nil, fmt.Errorf("invalid arguments: ids must be a string or an array of strings")
}

// ReadIDsOptions lets a caller override what the tool reads. The CLI uses it: `arac read` is
// the human surface, so read.kinds -- which exists to narrow what a MODEL is offered -- must
// not lock a person out of their own topology.
type ReadIDsOptions struct {
	// Kinds overrides read.kinds. Nil uses the configured set.
	Kinds []domain.ResourceKind
	// ForcedKind narrows ID resolution to a single kind (the CLI's --kind).
	ForcedKind domain.ResourceKind
}

// Run is the tool entry point: parse the id list, then hand off to ReadIDs.
func (r *Read) Run(args json.RawMessage) (string, error) {
	ids, err := readArgs(args)
	if err != nil {
		return "", err
	}
	return r.ReadIDs(ids, ReadIDsOptions{})
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

	cfg := helper.LoadConfig(helper.ConfigPath(r.mgr.DbPath()))
	kinds := opt.Kinds
	if kinds == nil {
		kinds = cfg.EffectiveReadKinds()
	}
	allowed := kindSet(kinds)
	filter := topology.WithContextFilter(cfg.EffectiveContextFilter())

	topo, topoErr := r.mgr.ReadAll()

	var units []readunit.Unit
	var problems []string
	for _, id := range ids {
		u, note, err := r.unitFor(topo, topoErr, id, allowed, kinds, filter, opt.ForcedKind)
		switch {
		case err != nil:
			problems = append(problems, fmt.Sprintf("- %s: %v", id, err))
		case note != "":
			problems = append(problems, fmt.Sprintf("- %s: %s", id, note))
		default:
			u.Label = displayPath(topo, u.Path)
			units = append(units, u)
		}
	}

	out := readunit.Render(units, readunit.Options{IncludeIncoming: cfg.EffectiveIncludeIncoming()})

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
	}
	if out == "" {
		return "", fmt.Errorf("no readable resources for the given ids")
	}
	return out, nil
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
	kinds []domain.ResourceKind, filter topology.TopologyOption, forcedKind domain.ResourceKind) (readunit.Unit, string, error) {

	if topoErr == nil {
		match := func(res domain.Resource) bool {
			if forcedKind != "" {
				return res.Kind == forcedKind
			}
			return true
		}
		target, note, resolveErr := resolveReadTargetWith(r.mgr, id, match)
		if note != "" {
			return readunit.Unit{}, note, nil
		}
		if resolveErr == nil {
			if !allowed[target.res.Kind] {
				return readunit.Unit{}, "", fmt.Errorf(
					"resolves to a %s, which read.kinds does not allow (allowed: %s)",
					target.res.Kind, kindNames(kinds))
			}
			u, buildNote, buildErr := r.buildUnit(topo, target, filter)
			if se, stale := topology.AsStaleIndex(buildErr); stale {
				return r.healStale(se, id, match, allowed, filter, buildErr)
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
	allowed map[domain.ResourceKind]bool, filter topology.TopologyOption, original error) (readunit.Unit, string, error) {

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
	return r.buildUnit(topo, target, filter)
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
	health, err := r.mgr.IndexHealth("")
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
func (r *Read) buildUnit(topo *domain.Topology, t readTarget, filter topology.TopologyOption) (readunit.Unit, string, error) {
	if t.res.Kind == domain.ResourceFile {
		return r.fileUnit(topo, t)
	}

	switch t.res.Kind {
	case domain.ResourceFunction, domain.ResourceMethod:
		switch t.res.Language {
		case "go":
			ctx, err := golang.NewGoManager(r.mgr).ReadFunction(t.id, filter)
			return wrap(gotools.FunctionUnit, ctx, err)
		case "python":
			ctx, err := python.NewPythonManager(r.mgr).ReadFunction(t.id, filter)
			return wrap(pythontools.FunctionUnit, ctx, err)
		case "javascript", "typescript":
			ctx, err := javascript.NewJavaScriptManager(r.mgr).ReadFunction(t.id, filter)
			return wrap(jstools.FunctionUnit, ctx, err)
		case "rust":
			ctx, err := rust.NewRustManager(r.mgr).ReadFunction(t.id, filter)
			return wrap(rusttools.FunctionUnit, ctx, err)
		case "java":
			ctx, err := java.NewJavaManager(r.mgr).ReadFunction(t.id, filter)
			return wrap(javatools.FunctionUnit, ctx, err)
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
	return r.genericUnit(topo, t)
}

// wrap adapts a (context, error) manager call into a Unit, keeping the dispatch table above to
// two lines per case.
func wrap[T any](build func(T) readunit.Unit, ctx T, err error) (readunit.Unit, string, error) {
	if err != nil {
		return readunit.Unit{}, "", err
	}
	return build(ctx), "", nil
}

// fileUnit builds a whole-file read with no language-specific code.
//
// Covers names every declaration in the file, which is what keeps them out of the context
// section; Neighbors is what those declarations reach outside the file, which is what the
// context section should have been showing all along.
func (r *Read) fileUnit(topo *domain.Topology, t readTarget) (readunit.Unit, string, error) {
	entry, err := r.mgr.Cut(domain.Location{Path: t.id})
	if err != nil {
		return readunit.Unit{}, "", err
	}
	members := domain.FileMembers(topo, t.id)
	neighbors := domain.OutgoingNeighbors(topo, members, map[string]bool{t.id: true})

	u := readunit.Unit{
		ID:     t.id,
		Kind:   domain.ResourceFile,
		Path:   t.id,
		Line:   0,
		Fence:  t.res.Language,
		Body:   entry.Cut,
		Covers: members,
	}
	u.Context = neighborContext(neighbors)
	return u, "", nil
}

// genericUnit is the language-neutral fallback: the resource's own cut plus its outgoing
// neighbours straight off the graph.
func (r *Read) genericUnit(topo *domain.Topology, t readTarget) (readunit.Unit, string, error) {
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
	u.Context = neighborContext(domain.OutgoingNeighbors(topo, []string{t.id}, nil))
	return u, "", nil
}

// rawFileUnit reads a path that is not in the topology at all.
func (r *Read) rawFileUnit(topo *domain.Topology, id string) (readunit.Unit, error) {
	root := ""
	if topo != nil {
		root = topo.Root
	}
	maxSize := helper.LoadConfig(helper.ConfigPath(r.mgr.DbPath())).EffectiveMaxFileSize()
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
// in full.
func neighborContext(neighbors []domain.Resource) func(*strings.Builder, *renderstate.State) {
	if len(neighbors) == 0 {
		return nil
	}
	return func(b *strings.Builder, st *renderstate.State) {
		g := st.Guard(b)
		for _, n := range neighbors {
			if !st.Renderable(n.ID) || !g.More() {
				continue
			}
			description := n.Description
			if description == "" {
				description = "no description"
			}
			fmt.Fprintf(b, "## %s (%s): %s\n", n.ID, n.Kind, description)
		}
	}
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
	if err != nil || strings.HasPrefix(rel, "..") {
		return path
	}
	return filepath.ToSlash(rel)
}
