package topology

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"aracne/internal/helper"
	"aracne/internal/topology/domain"
	"aracne/internal/topology/idresolve"
	"aracne/internal/topology/scanner"
)

var bugIDCounter int64

// partialIncrementalCount counts how many changed files were persisted through
// the scoped partial change-path (no full ReadDb). It is observable by tests via
// PartialIncrementalCount to prove the fast path is actually exercised.
var partialIncrementalCount int64

// PartialIncrementalCount returns the number of files persisted via the scoped
// partial change-path since process start. Intended for tests.
func PartialIncrementalCount() int64 {
	return atomic.LoadInt64(&partialIncrementalCount)
}

// Manages topology database operations and resource updates.
type TopologyManager struct {
	dbPath string
}

// Creates a new TopologyManager instance.
func New() *TopologyManager {
	return &TopologyManager{}
}

// Returns the database file path for the topology manager.
func (m *TopologyManager) DbPath() string {
	return m.dbPath
}

// RunPreToolScan runs the topology scan requested by the scan.pre_tool config
// before a tool call. PreToolScanNone (or an empty mode / nil registry) is a
// no-op. The scan root is taken from the stored topology, falling back to the
// working directory -- which matters here, because the hook runs wherever the
// agent last cd'd to. It mirrors the modes of `arac scan`: default =>
// incremental, full => re-scan all files, hard => rebuild from scratch and
// clear bugs.
func (m *TopologyManager) RunPreToolScan(reg *scanner.Registry, mode helper.PreToolScanMode) error {
	if reg == nil || mode == "" || mode == helper.PreToolScanNone {
		return nil
	}
	root := "."
	if topo, err := helper.ReadDb(m.dbPath); err == nil && topo != nil && topo.Root != "" {
		root = topo.Root
	}
	switch mode {
	case helper.PreToolScanFull:
		_, err := m.FullReScan(root, reg)
		return err
	case helper.PreToolScanHard:
		if err := m.FullScan(root, reg); err != nil {
			return err
		}
		m.DeleteAllBugs()
		return nil
	default:
		_, err := m.IncrementalScan(root, reg)
		return err
	}
}

// applyPathVisibility installs the path-visibility filter from the config next
// to the topology DB as the active filter for the upcoming scan, so paths marked
// hidden are skipped by both file discovery (indexing) and parsing (scan) in
// every mode. When no DB path is known yet it clears any previously installed
// filter so nothing is hidden.
func (m *TopologyManager) applyPathVisibility(root string) {
	if m.dbPath == "" {
		domain.SetActivePathVisibility(nil)
		domain.SetActiveIgnore(nil)
		return
	}
	cfg := helper.LoadConfig(helper.ConfigPath(m.dbPath))
	domain.SetActivePathVisibility(domain.BuildPathVisibility(root, cfg.Paths))
	domain.SetActiveIgnore(domain.BuildIgnoreMatcher(root, cfg.Scan.Ignore))
}

// Scans codebase and writes the complete topology to the database.
func (m *TopologyManager) FullScan(root string, reg *scanner.Registry) error {
	m.applyPathVisibility(root)
	topo, err := scanAllLanguages(root, reg)
	if err != nil {
		return err
	}
	if err := helper.WriteDb(topo, m.dbPath); err != nil {
		return err
	}
	helper.SyncManifest(topo, m.dbPath)
	return nil
}

// Scans codebase for changes and updates topology incrementally, handling added/modified/deleted files with optional partial-update optimization and cascading re-resolution of affected callers.
func (m *TopologyManager) IncrementalScan(root string, reg *scanner.Registry) ([]domain.TopologyWarning, error) {
	m.applyPathVisibility(root)
	if info, err := os.Stat(root); err != nil {
		return nil, fmt.Errorf("topology root %s is not accessible: %w", root, err)
	} else if !info.IsDir() {
		return nil, fmt.Errorf("topology root %s is not a directory", root)
	}
	if len(reg.DetectAll(root)) == 0 {
		return nil, fmt.Errorf("no language scanner detected for %s", root)
	}

	// Change detection only needs the manifest plus a directory walk, so gate on
	// the cheap files (db + manifest existence) here and defer the expensive graph
	// load until we know there is actually work to do.
	manifestPath := helper.ManifestPath(m.dbPath)
	if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
		return m.FullReScan(root, reg)
	}
	if _, err := os.Stat(m.dbPath); os.IsNotExist(err) {
		return m.FullReScan(root, reg)
	}

	langScanners := reg.DetectAll(root)
	var added, modified, deleted []string
	for _, ls := range langScanners {
		a, m, d, diffErr := helper.DiffScanFiles(root, ls.Name(), manifestPath)
		if diffErr != nil {
			return nil, diffErr
		}
		added = append(added, a...)
		modified = append(modified, m...)
		deleted = append(deleted, d...)
	}

	// Nothing changed: the manifest already matches the tree, so there is no
	// reason to deserialize the topology graph (the only remaining work,
	// SyncManifest, is a no-op when the diff is empty).
	if len(added) == 0 && len(modified) == 0 && len(deleted) == 0 {
		return nil, nil
	}

	// Phase 3: the scoped partial change-path. When it is safe — no deleted
	// files (deletes need a whole-graph referrer sweep) and every changed file's
	// scanner can update without loading the whole graph — persist each changed
	// file's delta directly from the DB, never reading or rewriting the full
	// graph. Correctness over coverage: anything unsafe falls through to the
	// existing full path below, unchanged.
	if handled, warnings, err := m.tryPartialIncremental(root, reg, added, modified, deleted); handled {
		return warnings, err
	}

	// A change exists; only now is it worth loading the full graph.
	topo, err := helper.ReadDb(m.dbPath)
	if err != nil {
		return m.FullReScan(root, reg)
	}
	// Fingerprint the pre-update graph so we can persist only what actually
	// changed (scoped write) instead of rewriting every row.
	beforeSigs := helper.ResourceSignatures(topo.Resources)

	// Snapshot the pre-update resources so that, once the changed files have
	// been re-registered, we can tell which resources were removed/renamed or
	// had their signature changed. A signature change of a resource invalidates
	// the body resolution of its callers in *other* files (e.g. a return-type
	// change re-points a consumer's method call), so those caller files must be
	// re-resolved too even though they were not edited on disk. This is what a
	// cold full/hard scan does implicitly by re-resolving the whole graph.
	beforeResources := make(map[string]domain.Resource, len(topo.Resources))
	beforeSigKeys := make(map[string]string, len(topo.Resources))
	for id, res := range topo.Resources {
		beforeResources[id] = res
		beforeSigKeys[id] = resourceSignatureKey(res)
	}
	// Needed to keep a re-raised signature_changed warning pointing at the
	// signature its callers were originally written against; see
	// helper.RestoreSignatureBaselines.
	beforeWarnings := cloneWarnings(topo.Warnings)

	var allWarnings []domain.TopologyWarning

	changedFiles := append(append([]string(nil), added...), modified...)

	// Phase 1: register/parse EVERY changed file into the working topology
	// first. After this pass the structural state of the graph (which symbols
	// exist, their signatures) is complete and final for this batch, so no file
	// resolved later observes a stale sibling. Body edges produced here may
	// still be stale for files processed before their dependencies, so they are
	// recomputed in phase 2.
	for _, path := range changedFiles {
		langScanner := reg.DetectFile(path)
		if langScanner == nil {
			continue
		}
		warnings, err := updateFileWithScanner(topo, langScanner, path)
		if err != nil {
			allWarnings = append(allWarnings, domain.TopologyWarning{
				ID:       "error:" + path,
				SourceID: path,
				Kind:     "",
				Message:  fmt.Sprintf("error updating %s: %v", path, err),
			})
		} else {
			allWarnings = append(allWarnings, warnings...)
		}
	}

	// Expand the re-resolve set with the files of forward-transitive callers of
	// any resource whose identity (removed/renamed) or signature changed in this
	// batch. Those callers live in files that were not necessarily edited, so
	// without this they would keep stale edges to the old symbol. The reverse
	// callers come straight from the pre-update DB (still holding the stale
	// edges), keeping this language-agnostic.
	resolveSet := make(map[string]bool, len(changedFiles))
	for _, path := range changedFiles {
		if abs, absErr := filepath.Abs(path); absErr == nil {
			resolveSet[abs] = true
		} else {
			resolveSet[path] = true
		}
	}
	// Snapshot the files that actually changed on disk BEFORE the reverse-caller
	// expansion below widens resolveSet. A reverse-caller file is re-resolved
	// precisely because it calls something whose signature moved, so it is the
	// file a signature warning must point AT, not one to skip.
	editedFiles := make(map[string]bool, len(resolveSet))
	for path := range resolveSet {
		editedFiles[path] = true
	}
	callerFiles := m.reverseCallerFiles(topo, beforeResources, beforeSigKeys, resolveSet)
	for f := range callerFiles {
		resolveSet[f] = true
	}

	// Phase 2: re-resolve every changed file AND every reverse-caller file
	// against the now-complete graph, so body edges (calls / uses_*) point at
	// the final sibling state. Re-running an already-registered file is
	// idempotent: the scanner re-parses it and recomputes its edges.
	resolvePaths := make([]string, 0, len(resolveSet))
	for f := range resolveSet {
		resolvePaths = append(resolvePaths, f)
	}
	sort.Strings(resolvePaths)
	for _, path := range resolvePaths {
		langScanner := reg.DetectFile(path)
		if langScanner == nil {
			continue
		}
		warnings, err := updateFileWithScanner(topo, langScanner, path)
		if err != nil {
			allWarnings = append(allWarnings, domain.TopologyWarning{
				ID:       "error:" + path,
				SourceID: path,
				Kind:     "",
				Message:  fmt.Sprintf("error updating %s: %v", path, err),
			})
		} else {
			allWarnings = append(allWarnings, warnings...)
		}
	}

	for _, path := range deleted {
		warnings := helper.RemoveFileResources(topo, path)
		allWarnings = append(allWarnings, warnings...)
	}

	// Files that were re-parsed are authoritative about what they reference.
	for path := range resolveSet {
		helper.ClearReferrerWarningsForFile(topo, path)
	}

	// Symbols removed from files that still exist: RemoveFileResources above
	// only covers whole-file deletion, and only goscanner did the reverse
	// lookup for the symbol case. This does it from the graph, so it works for
	// every language.
	referrerWarnings := referrerPass(topo, removedSince(beforeSigs, topo.Resources), "", resolveSet)

	// Every scanner but goscanner reports a signature change against the symbol
	// that changed and names no caller. Nothing can ever clear that shape, so fan
	// it out to the callers that now need verifying before it reaches
	// topo.Warnings and, from there, the database.
	allWarnings = helper.ExpandSignatureWarnings(topo, allWarnings, editedFiles)

	for _, w := range allWarnings {
		topo.Warnings[w.ID] = w
	}
	// Merged after, insert-if-absent, so a scanner's more specific message for
	// the same src@kind@tgt id keeps precedence over the generic one.
	mergeNewWarnings(topo, referrerWarnings)
	allWarnings = append(allWarnings, referrerWarnings...)

	helper.ResolveReferrerWarnings(topo)

	helper.CleanupOrphanedWarnings(topo)
	allWarnings = settleSignatureWarnings(topo, beforeResources, beforeWarnings, allWarnings)
	normalizeTopologyLanguages(topo)

	upserts, deletes := helper.DiffResources(beforeSigs, topo.Resources)
	if err := helper.WriteIncremental(m.dbPath, topo, upserts, deletes); err != nil {
		return allWarnings, fmt.Errorf("write topology db: %w", err)
	}

	helper.CleanupOrphanedBugs(m.dbPath, topo)
	helper.SyncManifest(topo, m.dbPath)

	return allWarnings, nil
}

// partialUpdaterFor returns the scanner for path if and only if it implements
// the scoped PartialUpdater interface; otherwise it returns nil so the caller
// falls back to the full path.
func partialUpdaterFor(reg *scanner.Registry, root, path string) scanner.PartialUpdater {
	if os.Getenv("ARAC_NO_PARTIAL") != "" {
		return nil
	}
	ls := reg.DetectFile(path)
	if ls == nil {
		return nil
	}
	pu, ok := ls.(scanner.PartialUpdater)
	if !ok {
		return nil
	}
	if !helper.IsSourceFile(root, path, ls.Name()) {
		return nil
	}
	return pu
}

// tryPartialIncremental attempts the scoped change-path. It returns handled=true
// only when it actually persisted the change without loading the full graph; on
// handled=false the caller must run the existing full path. It is safe ONLY when
// there are no deletions and every added/modified file's scanner implements
// PartialUpdater (file deletes and unknown/non-source files route to fallback).
func (m *TopologyManager) tryPartialIncremental(root string, reg *scanner.Registry, added, modified, deleted []string) (bool, []domain.TopologyWarning, error) {
	if len(deleted) != 0 {
		return false, nil, nil
	}
	changed := append(append([]string(nil), added...), modified...)
	if len(changed) == 0 {
		return false, nil, nil
	}
	// A multi-file batch may need cross-file re-resolution (a file resolved
	// early would observe a stale sibling changed later in the same batch). The
	// scoped fast path resolves each file in isolation, so route multi-file
	// batches to the full two-phase path which parses everything first, then
	// resolves once against the complete graph. The dominant single-file edit
	// stays on the fast path.
	if len(changed) > 1 {
		return false, nil, nil
	}
	updaters := make([]scanner.PartialUpdater, len(changed))
	for i, path := range changed {
		pu := partialUpdaterFor(reg, root, path)
		if pu == nil {
			return false, nil, nil
		}
		updaters[i] = pu
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false, nil, nil
	}

	var allWarnings []domain.TopologyWarning
	// UpdateFilePartial returns the scanner's whole warnings map, not a delta:
	// the partial working set is seeded with every row of the warnings table
	// (they are cheap to load and resolution needs them). Reporting that
	// verbatim hands the agent the entire backlog on every scan, so snapshot
	// what was already there and subtract it below.
	preexisting, err := helper.ReadAllWarnings(m.dbPath)
	if err != nil {
		return false, nil, nil
	}

	// Process each changed file in turn, persisting its scoped delta before the
	// next so cross-file dependencies (and warnings) see prior changes. For the
	// dominant single-file edit this is exactly one DB write.
	for i, path := range changed {
		absPath, err := filepath.Abs(path)
		if err != nil {
			return false, nil, nil
		}
		upserts, deletes, warnings, err := updaters[i].UpdateFilePartial(m.dbPath, absRoot, absPath)
		if err != nil {
			// Either the scanner asked to fall back (ErrPartialFallback) or a
			// genuine error occurred. In both cases route to the full ReadDb
			// path, which re-derives everything from scratch and is idempotent
			// with any deltas already written for earlier files in this batch.
			return false, nil, nil
		}
		// If this edit removed/renamed a symbol or changed a symbol's signature
		// that is referenced by a caller in ANOTHER file, that caller keeps a
		// stale edge unless it is re-resolved. The scoped fast path does not
		// re-resolve other files, so route to the full two-phase path (which
		// expands the batch with reverse-caller files) for correctness. The
		// common edit — adding/removing a local call with no signature/identity
		// change of a cross-file-referenced symbol — has no such target and
		// stays on the fast path.
		if m.partialNeedsCrossFileResolve(absPath, upserts, deletes) {
			return false, nil, nil
		}
		// WriteScopedResources rewrites the whole warnings table from this map, so
		// a warning this very delta orphaned would be re-inserted and outlive the
		// code it points at. The whole-graph CleanupOrphanedWarnings cannot run on
		// a working set; this drops the ones the delta is known to have
		// invalidated. Runs before the report loop below so a dropped warning is
		// not surfaced either.
		helper.CleanupOrphanedWarningsScoped(warnings, deletes)
		// The contract rule has to run here too, not only on the full path. This IS the
		// single-file edit path, so it is where an agent's fix to a caller lands; without
		// this, fixing the call would leave the warning standing until something forced a
		// full re-resolve.
		helper.ReconcileSignatureWarningsScoped(m.dbPath, warnings, upserts)
		if err := helper.WriteScopedResources(m.dbPath, upserts, deletes, warnings); err != nil {
			return true, allWarnings, fmt.Errorf("write topology db: %w", err)
		}
		atomic.AddInt64(&partialIncrementalCount, 1)
		// Surface only newly-added "changed/removed" warnings to the caller, the
		// same subset the full path reports.
		for _, w := range warnings {
			if _, wasThere := preexisting[w.ID]; wasThere {
				continue
			}
			if w.Kind == domain.WarnSignatureChanged || w.Kind == domain.WarnNodeRemoved {
				preexisting[w.ID] = w // do not repeat it for the next file in the batch
				allWarnings = append(allWarnings, w)
			}
		}
	}

	if err := helper.CleanupOrphanedBugsScoped(m.dbPath); err != nil {
		return true, allWarnings, fmt.Errorf("cleanup bugs: %w", err)
	}
	if err := helper.SyncManifestFiles(m.dbPath, changed); err != nil {
		return true, allWarnings, fmt.Errorf("sync manifest: %w", err)
	}
	return true, allWarnings, nil
}

// Rescans codebase and preserves existing resource descriptions when re-indexing.
//
// Descriptions are matched by resource ID first. When the stored database was built under
// an OLDER id-scheme, that alone would discard every description — the IDs on both sides
// describe the same code but no longer spell the same string. So a scheme change also runs
// the identity-based remap (same tiers as the descriptions sidecar: path+kind+name+parent,
// then source hash), carries bugs across, and records old -> new in `resource_alias` so
// IDs an agent already knows keep resolving.
func (m *TopologyManager) FullReScan(root string, reg *scanner.Registry) ([]domain.TopologyWarning, error) {
	m.applyPathVisibility(root)
	newTopo, err := scanAllLanguages(root, reg)
	if err != nil {
		return nil, err
	}

	oldTopo, readErr := helper.ReadDb(m.dbPath)
	var aliases map[string]string
	if readErr == nil && oldTopo != nil {
		for id, oldRes := range oldTopo.Resources {
			newRes, exists := newTopo.Resources[id]
			if !exists {
				continue
			}
			if newRes.Description == "" && oldRes.Description != "" {
				newRes.Description = oldRes.Description
				newTopo.Resources[id] = newRes
			}
		}
		// The identity fallback runs ALWAYS, not only across an id-scheme change.
		//
		// IDs drift for reasons besides a scheme bump — a renamed file, a workspace member
		// resolving differently, a scanner improvement — and ID-only matching silently
		// dropped a description every time. The tracing fixture lost a handful on every
		// single rescan for exactly this reason, while its sidecar could still match 10 of
		// 11 by identity. Matching costs one pass over the described resources; losing
		// LLM-authored text costs a regeneration.
		aliases = m.remapByIdentity(oldTopo, newTopo)
	}

	if err := helper.WriteDb(newTopo, m.dbPath); err != nil {
		return nil, err
	}

	if len(aliases) > 0 {
		// Written AFTER WriteDb: it clears `info` and rewrites resources, but leaves
		// resource_alias alone, so the mapping survives and old IDs resolve immediately.
		if _, err := helper.WriteResourceAliases(m.dbPath, aliases); err != nil {
			return nil, err
		}
		if _, err := helper.RemapBugNodes(m.dbPath, aliases); err != nil {
			return nil, err
		}
	}

	helper.CleanupOrphanedBugs(m.dbPath, newTopo)
	helper.SyncManifest(newTopo, m.dbPath)
	return nil, nil
}

// remapByIdentity matches old resources to new ones by IDENTITY rather than by ID, and
// copies descriptions across. Returns the old -> new ID mapping for the alias table, which
// keeps IDs an agent already knows resolvable after any drift.
//
// It reuses the sidecar's matcher so there is exactly one definition of "the same resource
// under a different name" in the codebase — the alternative is two rules that drift.
func (m *TopologyManager) remapByIdentity(oldTopo, newTopo *domain.Topology) map[string]string {
	// Each side is made relative to ITS OWN root. Passing the scan-root argument to both
	// silently weakens the match whenever it is not the absolute path the database stores
	// (e.g. `arac scan --all --root .`), which is the common invocation.
	oldRoot, newRoot := oldTopo.Root, newTopo.Root
	if newRoot == "" {
		newRoot = oldRoot
	}
	// Only described resources need carrying; everything else is regenerated verbatim.
	recs := helper.BuildDescriptionRecords(oldTopo, oldRoot)
	if len(recs) == 0 {
		return nil
	}
	result := helper.MatchDescriptions(recs, newTopo, newRoot)
	aliases := make(map[string]string, len(result.Matched)+len(result.Skipped))
	for _, match := range append(append([]helper.DescriptionMatch{}, result.Matched...),
		result.Skipped...) {
		if match.Record.ID != "" && match.ResourceID != "" {
			aliases[match.Record.ID] = match.ResourceID
		}
	}
	for _, match := range result.Matched {
		res, ok := newTopo.Resources[match.ResourceID]
		if !ok || res.Description != "" {
			continue
		}
		res.Description = match.Record.Description
		newTopo.Resources[match.ResourceID] = res
	}
	return aliases
}

// Sets the database path for the topology manager.
func (m *TopologyManager) Load(path string) error {
	m.dbPath = path
	return nil
}

// Exports the topology database to a file.
func (m *TopologyManager) Write(path string) error {
	if m.dbPath == "" {
		return nil
	}
	src, err := os.Open(m.dbPath)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.Create(path)
	if err != nil {
		return err
	}
	defer dst.Close()
	_, err = io.Copy(dst, src)
	return err
}

// Loads and returns the complete topology graph from the database.
func (m *TopologyManager) ReadAll() (*domain.Topology, error) {
	return helper.ReadDb(m.dbPath)
}

// Extracts a slice of source code lines from a file based on start and end line numbers.
func (m *TopologyManager) Cut(loc domain.Location) (*domain.CodeEntry, error) {
	data, err := os.ReadFile(loc.Path)
	if err != nil {
		return nil, fmt.Errorf("read file %s: %w", loc.Path, err)
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if loc.StartsAt == 0 && loc.EndsAt == 0 {
		loc.StartsAt = 1
		loc.EndsAt = len(lines)
	}
	if loc.EndsAt < loc.StartsAt {
		return nil, fmt.Errorf("EndsAt %d < StartsAt %d", loc.EndsAt, loc.StartsAt)
	}
	if loc.StartsAt < 1 {
		return nil, fmt.Errorf("StartsAt %d out of range (1-%d)", loc.StartsAt, len(lines))
	}
	// Past the end of the file is not a malformed record, it is an index describing source
	// that is no longer there. Typed, so the read path can re-index this one file and answer
	// the question rather than handing the caller a range error it cannot act on. See
	// StaleIndexError.
	if loc.StartsAt > len(lines) || loc.EndsAt > len(lines) {
		return nil, &StaleIndexError{
			Path:     loc.Path,
			StartsAt: loc.StartsAt,
			EndsAt:   loc.EndsAt,
			Lines:    len(lines),
		}
	}
	cut := strings.Join(lines[loc.StartsAt-1:loc.EndsAt], "\n")
	return &domain.CodeEntry{Location: loc, Cut: cut}, nil
}

// fileEditLocks serializes concurrent edit/write operations (and their topology
// updates) on the same absolute file path within a process, so parallel
// sub-agents editing the same file cannot lose each other's changes. Different
// files proceed concurrently. Keyed by absolute path so it is shared across all
// managers in the process.
var fileEditLocks sync.Map // map[string]*sync.Mutex

// WithFileLock runs fn while holding the per-file lock for path's absolute form,
// serializing edit/write of the same file. waited is true when another holder
// forced this call to block — a signal that the file may have changed since the
// caller last read it (used to give a clearer stale-edit error).
func (m *TopologyManager) WithFileLock(path string, fn func(waited bool) (string, error)) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	muAny, _ := fileEditLocks.LoadOrStore(absPath, &sync.Mutex{})
	mu := muAny.(*sync.Mutex)
	waited := !mu.TryLock()
	if waited {
		mu.Lock()
	}
	defer mu.Unlock()
	return fn(waited)
}

// Scans and updates topology for a file, returning any warnings and syncing the database.
func (m *TopologyManager) UpdateFile(path string, reg *scanner.Registry) ([]domain.TopologyWarning, error) {
	topo, err := helper.ReadDb(m.dbPath)
	if err != nil {
		return nil, fmt.Errorf("read topology db: %w", err)
	}
	beforeSigs := helper.ResourceSignatures(topo.Resources)
	beforeResources := make(map[string]domain.Resource, len(topo.Resources))
	for id, res := range topo.Resources {
		beforeResources[id] = res
	}
	beforeWarnings := cloneWarnings(topo.Warnings)
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	finish := func(warnings []domain.TopologyWarning) ([]domain.TopologyWarning, error) {
		for _, w := range warnings {
			topo.Warnings[w.ID] = w
		}
		helper.CleanupOrphanedWarnings(topo)
		warnings = settleSignatureWarnings(topo, beforeResources, beforeWarnings, warnings)
		upserts, deletes := helper.DiffResources(beforeSigs, topo.Resources)
		if err := helper.WriteIncremental(m.dbPath, topo, upserts, deletes); err != nil {
			return nil, fmt.Errorf("write topology db: %w", err)
		}
		helper.CleanupOrphanedBugs(m.dbPath, topo)
		// SCOPED to this one file. SyncManifest stamps EVERY file in the topology with its
		// current mtime, which is right after a scan that parsed them all and badly wrong
		// here: parsing one file would declare the whole tree freshly indexed. Anything that
		// had changed without being re-parsed -- a checkout, a restored database, a branch
		// switch -- was then invisible to every later incremental scan, because the manifest
		// said it was current. One edit froze that staleness in permanently, which is how a
		// benchmark fixture could stay wrong for the whole run.
		if _, stillIndexed := topo.Resources[absPath]; stillIndexed {
			if err := helper.SyncManifestFiles(m.dbPath, []string{absPath}); err != nil {
				return nil, fmt.Errorf("sync manifest: %w", err)
			}
		} else if err := helper.ForgetManifestFiles(m.dbPath, []string{absPath}); err != nil {
			return nil, fmt.Errorf("sync manifest: %w", err)
		}
		return warnings, nil
	}

	// Hidden paths (config) are excluded from the topology: install the filter
	// and, when this file is hidden, drop any stale resources and make the update
	// a no-op so native edits / MCP writes to hidden files never re-index them.
	m.applyPathVisibility(topo.Root)
	if domain.PathHidden(absPath) {
		return finish(helper.RemoveFileResources(topo, absPath))
	}

	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		return finish(helper.RemoveFileResources(topo, absPath))
	} else if err != nil {
		return nil, err
	}

	langScanner := reg.DetectFile(absPath)
	if langScanner == nil {
		return finish(helper.RemoveFileResources(topo, absPath))
	}
	if !helper.IsSourceFile(topo.Root, absPath, langScanner.Name()) {
		return finish(helper.RemoveFileResources(topo, absPath))
	}

	// Only goscanner writes into topo.Warnings directly; every other scanner
	// RETURNS its warnings. Dropping this return value is why an edit to a JS,
	// TS, Python, Rust or Java file reported "edit succeeded" and nothing else,
	// no matter what it broke.
	scannerWarnings, err := updateFileWithScanner(topo, langScanner, absPath)
	if err != nil {
		return nil, err
	}
	// Every scanner but goscanner reports a signature change against the symbol
	// that changed and names no caller. Nothing can ever clear that shape, so fan
	// it out to the callers that now need verifying before it is merged in.
	scannerWarnings = helper.ExpandSignatureWarnings(topo, scannerWarnings, map[string]bool{absPath: true})
	mergeNewWarnings(topo, scannerWarnings)

	// This file was just re-parsed from source, so what it references now is
	// authoritative: drop node_removed warnings attributed to its own
	// resources. Anything still broken is re-emitted by the pass below.
	helper.ClearReferrerWarningsForFile(topo, absPath)

	// Symbols that disappeared in this update get caller-attributed warnings,
	// for every language. beforeSigs' keys are the pre-update id set.
	removed := removedSince(beforeSigs, topo.Resources)
	mergeNewWarnings(topo, referrerPass(topo, removed, absPath, map[string]bool{absPath: true}))

	// A symbol that came back clears the warning about its removal.
	helper.ResolveReferrerWarnings(topo)

	warnings := addedWarnings(beforeWarnings, topo.Warnings)
	return finish(warnings)
}

// mergeNewWarnings inserts warnings that are not already present. Insert-if-
// absent rather than overwrite: goscanner emits the same src@kind@tgt id with a
// more specific message ("function X calls Y which was removed from <file>"),
// and it should win over the generic one.
func mergeNewWarnings(topo *domain.Topology, ws []domain.TopologyWarning) {
	for _, w := range ws {
		if _, exists := topo.Warnings[w.ID]; !exists {
			topo.Warnings[w.ID] = w
		}
	}
}

// removedSince returns the ids that were in the graph before this update and
// are not in it now. beforeSigs is captured before any mutation, so its keys
// are exactly the pre-update id set and no extra snapshot is needed.
func removedSince(beforeSigs map[string]string, after map[string]domain.Resource) map[string]bool {
	removed := make(map[string]bool)
	for id := range beforeSigs {
		if _, stillThere := after[id]; !stillThere {
			removed[id] = true
		}
	}
	return removed
}

// referrerPass emits caller-attributed node_removed warnings for symbols that
// disappeared in this update.
//
// Referrers living in skipPaths are skipped: those files were re-parsed from
// source in this same update, so goscanner has already emitted use_missing_node
// for them and the other scanners simply dropped the edge. Warning about them
// here would duplicate that.
func referrerPass(topo *domain.Topology, removed map[string]bool, origin string, skipPaths map[string]bool) []domain.TopologyWarning {
	if len(removed) == 0 {
		return nil
	}
	skip := func(id string) bool {
		if len(skipPaths) == 0 {
			return false
		}
		res, ok := topo.Resources[id]
		return ok && skipPaths[res.Location.Path]
	}
	return helper.ScanReferrers(topo, helper.ReferrerScan{
		Removed:    removed,
		Origin:     origin,
		ConnTypes:  helper.ReferenceConnTypes,
		SkipSource: skip,
		// Safe to strip here because ResolveReferrerWarnings runs in the same
		// update and puts the edge back if the target reappears. Without the
		// strip the graph keeps an edge to a node that no longer exists, and
		// the readers silently drop that neighbour from the CONTEXT block —
		// the agent sees an incomplete context with no signal anything is wrong.
		Strip: true,
	})
}

// Detects and scans all language parsers in a root directory, merges their topologies, and normalizes the result.
func scanAllLanguages(root string, reg *scanner.Registry) (*domain.Topology, error) {
	langScanners := reg.DetectAll(root)
	if len(langScanners) == 0 {
		return nil, fmt.Errorf("no language scanner detected for %s", root)
	}

	merged := &domain.Topology{
		Resources: make(map[string]domain.Resource),
		Warnings:  make(map[string]domain.TopologyWarning),
		Errors:    make(map[string]string),
	}
	for _, langScanner := range langScanners {
		topo, err := langScanner.Scan(root)
		if err != nil {
			return nil, fmt.Errorf("scan %s: %w", langScanner.Name(), err)
		}
		// A scanner that claimed this root and then produced no file at all is
		// always a bug, and it used to surface as a perfectly clean
		// "0 files, 0 errors". Record it so it shows in the error count.
		if countFileResources(topo) == 0 {
			merged.Errors[root+" ("+langScanner.Name()+")"] = fmt.Sprintf(
				"%s was detected for this project but indexed 0 files", langScanner.Name())
		}
		tagTopologyLanguage(topo, langScanner.Name())
		mergeTopology(merged, topo)
	}
	normalizeTopologyLanguages(merged)
	return merged, nil
}

// countFileResources returns how many file nodes a topology holds.
func countFileResources(topo *domain.Topology) int {
	n := 0
	for _, res := range topo.Resources {
		if res.Kind == domain.ResourceFile {
			n++
		}
	}
	return n
}

// Updates topology for a single file using a language-specific scanner, merges results back, and returns warnings.
func updateFileWithScanner(topo *domain.Topology, langScanner scanner.LanguageScanner, path string) ([]domain.TopologyWarning, error) {
	lang := langScanner.Name()
	subTopo := languageSubTopology(topo, lang)
	warnings, err := langScanner.UpdateFile(subTopo, path)
	if err != nil {
		return nil, err
	}
	tagTopologyLanguage(subTopo, lang)
	topo.Warnings = cloneWarnings(subTopo.Warnings)
	removeLanguageResources(topo, lang)
	mergeTopology(topo, subTopo)
	normalizeTopologyLanguages(topo)
	return warnings, nil
}

// crossFileBodyConnTypes are the edge kinds produced by body resolution that
// can re-point to a different target when a referenced symbol's signature or
// identity changes (a return-type change re-points a call; a rename moves the
// edge to the new id). They are the edges we must re-resolve in callers living
// in other files. Structural reverse edges (implemented_by/inherited_by) are
// recomputed globally by each scanner's passes, so they are not listed here.
var crossFileBodyConnTypes = []string{
	"calls",
	"uses_struct",
	"uses_class",
	"uses_interface",
	"uses_named_type",
}

// settleSignatureWarnings runs the signature_changed lifecycle that both update
// paths owe the warnings table, and returns the reportable subset of `reported`.
//
// Four steps, ordered, not interchangeable. The first three are the BASELINE rule, which
// asks whether the callee went back to the signature its callers were written against:
// restoring comes first so a warning re-raised by this update is judged against the
// signature its callers were ORIGINALLY written against rather than the one the previous
// edit left behind; stamping then records a baseline for warnings raised here for the first
// time; only with both in place can discharging get a true answer.
//
// The fourth is the CONTRACT rule, and it is authoritative wherever it can answer, because
// the first three reason about the callee alone. A callee's history is only a proxy for
// what its callers expect, and the proxy is wrong as soon as a caller changes
// independently. The contract rule compares the recorded calls against the signature they
// now face. It stays last, and stays additive, because it is silent by design on JavaScript
// and on any call it could not read -- and there the baseline rule is still the best
// available answer.
//
// The filter at the end is not cosmetic. A revert re-raises the warning before
// the discharge deletes it, so without this the command that FIXED the topology
// would print the full pile of warnings it had just cleared from the database.
func settleSignatureWarnings(
	topo *domain.Topology,
	beforeResources map[string]domain.Resource,
	beforeWarnings map[string]domain.TopologyWarning,
	reported []domain.TopologyWarning,
) []domain.TopologyWarning {
	helper.RestoreSignatureBaselines(topo, beforeWarnings)
	helper.StampSignatureBaselines(topo, beforeResources)
	helper.DischargeSignatureWarnings(topo)
	// Authoritative, and last: the three steps above reason about what the CALLEE used to
	// look like, which is only ever a proxy for what its callers expect. This one compares
	// the calls themselves against the signature they now face, so where it can answer, its
	// answer supersedes theirs.
	helper.ReconcileSignatureWarnings(topo)

	kept := reported[:0]
	for _, w := range reported {
		if w.Kind != domain.WarnSignatureChanged {
			kept = append(kept, w)
			continue
		}
		// Report the STORED warning, not the scanner's copy of it: the baseline
		// is stamped on the table, and a caller handed a copy without one would
		// be told the warning can never discharge when it can.
		stored, live := topo.Warnings[w.ID]
		if !live {
			continue
		}
		kept = append(kept, stored)
	}
	return kept
}

// resourceSignatureKey returns a fingerprint of the parts of a resource whose
// change invalidates a *caller's* body resolution: its name and its declared
// input/output types (the signature). Connections are deliberately excluded —
// a body-edge change in the resource itself does not, by itself, invalidate its
// callers. Used to detect signature changes across an incremental batch in a
// language-agnostic way (the typed Input/Output live in Properties).
func resourceSignatureKey(res domain.Resource) string {
	var b strings.Builder
	b.WriteString(res.Name)
	b.WriteByte('|')
	if res.Properties != nil {
		if in, ok := res.Properties["input"]; ok {
			j, _ := json.Marshal(in)
			b.Write(j)
		}
		b.WriteByte('|')
		if out, ok := res.Properties["output"]; ok {
			j, _ := json.Marshal(out)
			b.Write(j)
		}
		b.WriteByte('|')
		if u, ok := res.Properties["underlying"]; ok {
			j, _ := json.Marshal(u)
			b.Write(j)
		}
	}
	return b.String()
}

// reverseCallerFiles returns the set of files (absolute paths) that contain
// forward-transitive callers of any resource that was removed/renamed or had
// its signature changed during phase 1 of an incremental batch. Such callers
// keep stale body edges (to the old id, or resolved against the old signature)
// unless they are re-resolved, which is exactly what a cold full/hard scan does
// implicitly. The reverse callers are read from the pre-update resource set
// (before), which still carries the stale edges. Files already in alreadyResolving
// are skipped (they will be re-resolved anyway).
func (m *TopologyManager) reverseCallerFiles(topo *domain.Topology, before map[string]domain.Resource, beforeSigKeys map[string]string, alreadyResolving map[string]bool) map[string]bool {
	// Collect the ids whose identity or signature changed in this batch.
	changedTargets := make(map[string]bool)
	for id := range before {
		newRes, stillPresent := topo.Resources[id]
		if !stillPresent {
			// Removed or renamed: its old callers reference an id that no longer
			// exists and must be re-resolved to the new symbol.
			changedTargets[id] = true
			continue
		}
		if beforeSigKeys[id] != resourceSignatureKey(newRes) {
			changedTargets[id] = true
		}
	}
	if len(changedTargets) == 0 {
		return nil
	}

	files := make(map[string]bool)
	addCaller := func(callerID string) {
		caller, ok := before[callerID]
		if !ok {
			return
		}
		path := caller.Location.Path
		if path == "" {
			return
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			abs = path
		}
		if alreadyResolving[abs] {
			return
		}
		files[abs] = true
	}

	for targetID := range changedTargets {
		for _, connType := range crossFileBodyConnTypes {
			callers, err := helper.GetCallers(m.dbPath, targetID, connType)
			if err != nil {
				continue
			}
			for _, callerID := range callers {
				addCaller(callerID)
			}
		}
	}
	return files
}

// partialNeedsCrossFileResolve reports whether a single-file scoped update must
// be escalated to the full two-phase path because it changed (removed, renamed,
// or re-signatured) a symbol that a caller in a DIFFERENT file references. Such
// a caller keeps a stale body edge (to the old id, or resolved against the old
// signature) unless it is re-resolved, which only the full path does. absPath is
// the changed file (absolute); upserts/deletes are the scoped delta the partial
// updater produced. A miss (no cross-file caller) keeps the edit on the fast
// path, preserving the dominant single-file case.
func (m *TopologyManager) partialNeedsCrossFileResolve(absPath string, upserts []domain.Resource, deletes []string) bool {
	// Old resources of the changed file, straight from the (pre-write) DB.
	oldRes, err := helper.ReadResourcesByFile(m.dbPath, absPath)
	if err != nil || len(oldRes) == 0 {
		// No prior members (brand-new file) or DB hiccup: there is nothing whose
		// signature/identity could have changed out from under a caller.
		return false
	}

	newByID := make(map[string]domain.Resource, len(upserts))
	for _, r := range upserts {
		newByID[r.ID] = r
	}

	// Targets whose identity or signature changed in this file.
	changedTargets := make(map[string]bool)
	for _, id := range deletes {
		if _, wasInFile := oldRes[id]; wasInFile {
			changedTargets[id] = true // removed or renamed
		}
	}
	for id, old := range oldRes {
		if nr, ok := newByID[id]; ok {
			if resourceSignatureKey(old) != resourceSignatureKey(nr) {
				changedTargets[id] = true // signature changed
			}
		}
	}
	if len(changedTargets) == 0 {
		return false
	}

	// Any caller of a changed target that lives in a different file forces the
	// full path. Callers come from the pre-write DB (still holding stale edges).
	for targetID := range changedTargets {
		for _, connType := range crossFileBodyConnTypes {
			callers, cerr := helper.GetCallers(m.dbPath, targetID, connType)
			if cerr != nil {
				continue
			}
			for _, callerID := range callers {
				if _, sameFile := oldRes[callerID]; sameFile {
					continue // caller is in this file; re-resolved already
				}
				return true
			}
		}
	}
	return false
}

// Extracts a language-specific subtopology containing only resources and errors for that language.
func languageSubTopology(topo *domain.Topology, language string) *domain.Topology {
	sub := &domain.Topology{
		Root:      topo.Root,
		Language:  language,
		Languages: []string{language},
		Resources: make(map[string]domain.Resource),
		Warnings:  make(map[string]domain.TopologyWarning),
		Errors:    make(map[string]string),
	}
	for id, res := range topo.Resources {
		if resourceLanguage(res, topo.Language) == language {
			sub.Resources[id] = cloneResource(res)
		}
	}
	for id, warning := range topo.Warnings {
		sub.Warnings[id] = warning
	}
	for path, msg := range topo.Errors {
		if helper.IsSourceFile(topo.Root, path, language) {
			sub.Errors[path] = msg
		}
	}
	return sub
}

// Assigns a language tag to all untagged resources and initializes empty maps in the topology.
func tagTopologyLanguage(topo *domain.Topology, language string) {
	if topo == nil {
		return
	}
	if topo.Resources == nil {
		topo.Resources = make(map[string]domain.Resource)
	}
	if topo.Warnings == nil {
		topo.Warnings = make(map[string]domain.TopologyWarning)
	}
	if topo.Errors == nil {
		topo.Errors = make(map[string]string)
	}
	for id, res := range topo.Resources {
		if res.Language == "" {
			res.Language = language
			topo.Resources[id] = res
		}
	}
	if len(topo.Languages) == 0 && language != "" {
		topo.Languages = []string{language}
	}
	if topo.Language == "" {
		topo.Language = language
	}
}

// Merges source topology into destination, detecting resource ID collisions across languages.
func mergeTopology(dst, src *domain.Topology) {
	if src == nil {
		return
	}
	if dst.Root == "" {
		dst.Root = src.Root
	}
	if dst.Resources == nil {
		dst.Resources = make(map[string]domain.Resource)
	}
	if dst.Warnings == nil {
		dst.Warnings = make(map[string]domain.TopologyWarning)
	}
	if dst.Errors == nil {
		dst.Errors = make(map[string]string)
	}
	for id, res := range src.Resources {
		if existing, exists := dst.Resources[id]; exists && resourceLanguage(existing, dst.Language) != resourceLanguage(res, src.Language) {
			dst.Errors["resource-collision:"+id] = fmt.Sprintf("resource id %s exists in both %s and %s", id, resourceLanguage(existing, dst.Language), resourceLanguage(res, src.Language))
			continue
		}
		dst.Resources[id] = cloneResource(res)
	}
	for id, warning := range src.Warnings {
		dst.Warnings[id] = warning
	}
	for path, msg := range src.Errors {
		dst.Errors[path] = msg
	}
}

// Removes resources and errors for a specific language while preserving language-neutral dependency nodes and their incoming edges.
func removeLanguageResources(topo *domain.Topology, language string) {
	removed := make(map[string]bool)
	for id, res := range topo.Resources {
		if resourceLanguage(res, topo.Language) == language {
			// Dependency resources are language-neutral shared nodes: although a
			// dependency is tagged with whichever language happened to register it
			// first, the SAME external package can be imported from files of other
			// languages. Pruning its incoming edges here would drop a sibling
			// language's imports_dependency edge that the merging sub-topology
			// (which only carries this language's files) cannot restore. Skip it
			// from edge pruning; the merge re-adds the dependency resource.
			if res.Kind == domain.ResourceDependency {
				continue
			}
			removed[id] = true
			delete(topo.Resources, id)
		}
	}
	if len(removed) == 0 {
		return
	}
	for id, res := range topo.Resources {
		for connType, targets := range res.Connections {
			kept := targets[:0]
			for _, target := range targets {
				if !removed[target] {
					kept = append(kept, target)
				}
			}
			if len(kept) == 0 {
				delete(res.Connections, connType)
			} else {
				res.Connections[connType] = kept
			}
		}
		topo.Resources[id] = res
	}
	for path := range topo.Errors {
		if helper.IsSourceFile(topo.Root, path, language) {
			delete(topo.Errors, path)
		}
	}
}

// Populates each resource's language field, deduplicates languages, and sets topology.Language to the single language or "multi".
func normalizeTopologyLanguages(topo *domain.Topology) {
	if topo == nil {
		return
	}
	seen := make(map[string]bool)
	for id, res := range topo.Resources {
		lang := resourceLanguage(res, topo.Language)
		if lang == "" {
			continue
		}
		if res.Language == "" {
			res.Language = lang
			topo.Resources[id] = res
		}
		seen[lang] = true
	}
	languages := make([]string, 0, len(seen))
	for lang := range seen {
		languages = append(languages, lang)
	}
	sort.Strings(languages)
	topo.Languages = languages
	if len(languages) == 1 {
		topo.Language = languages[0]
	} else if len(languages) > 1 {
		topo.Language = "multi"
	}
}

// Returns a resource's language, falling back to the topology's language if unset.
func resourceLanguage(res domain.Resource, fallback string) string {
	if res.Language != "" {
		return res.Language
	}
	if fallback != "multi" {
		return fallback
	}
	return ""
}

// Creates a deep copy of a Resource with independent properties and connections maps.
func cloneResource(res domain.Resource) domain.Resource {
	clone := res
	clone.Properties = make(map[string]any, len(res.Properties))
	for key, value := range res.Properties {
		clone.Properties[key] = value
	}
	clone.Connections = make(map[string][]string, len(res.Connections))
	for kind, targets := range res.Connections {
		clone.Connections[kind] = append([]string(nil), targets...)
	}
	return clone
}

// Creates a shallow copy of the warnings map.
func cloneWarnings(warnings map[string]domain.TopologyWarning) map[string]domain.TopologyWarning {
	cloned := make(map[string]domain.TopologyWarning, len(warnings))
	for id, warning := range warnings {
		cloned[id] = warning
	}
	return cloned
}

// Returns warnings that exist in the after map but not in the before map, sorted by ID.
func addedWarnings(before, after map[string]domain.TopologyWarning) []domain.TopologyWarning {
	var added []domain.TopologyWarning
	for id, warning := range after {
		if _, exists := before[id]; !exists {
			added = append(added, warning)
		}
	}
	sort.Slice(added, func(i, j int) bool {
		return added[i].ID < added[j].ID
	})
	return added
}

// Finds resource IDs by name, optionally filtered by resource kind.
func (m *TopologyManager) FindResourcesByName(name string, kinds ...domain.ResourceKind) ([]string, error) {
	topo, err := helper.ReadDb(m.dbPath)
	if err != nil {
		return nil, err
	}
	kindSet := make(map[domain.ResourceKind]bool, len(kinds))
	for _, k := range kinds {
		kindSet[k] = true
	}
	var results []string
	for id, res := range topo.Resources {
		if res.Name != name {
			continue
		}
		if len(kindSet) > 0 && !kindSet[res.Kind] {
			continue
		}
		results = append(results, id)
	}
	return results, nil
}

// ResolveNodeID snaps a caller-supplied resource id to the canonical id the topology holds,
// or returns an error carrying ranked candidates so the caller can retry in the same turn.
//
// WHY BUG REPORTING NEEDS THIS. A bug's node_id is a model-generated FQN string, and nothing
// used to check it. An unknown id was accepted, stored, and then silently deleted by
// CleanupOrphanedBugs at the next scan that touched the graph -- so a hunter that misspelled
// one id lost that finding with no signal to anyone. Snapping also converges two hunters'
// different spellings of the same node onto one id, which is what makes duplicate detection
// work at all.
//
// It FAILS OPEN on an unreadable topology: a project that has not scanned yet can still file
// bugs. This deliberately mirrors internal/llm/tools/read.go, which resolves the same way.
//
// Kept out of CreateBug on purpose: callers that legitimately mint bugs against ids outside
// the topology (the chat workflow tests, and any synthetic fixture) must stay able to.
func (m *TopologyManager) ResolveNodeID(raw string) (string, error) {
	topo, err := m.ReadAll()
	if err != nil {
		return raw, nil
	}
	res := idresolve.Resolve(topo, raw, idresolve.Options{
		Alias: func(old string) (string, bool) {
			return helper.ResolveAlias(m.dbPath, old)
		},
	})
	if res.Found() {
		return res.ID, nil
	}
	if hint := idresolve.FormatCandidates(raw, res.Candidates); hint != "" {
		return "", fmt.Errorf("resource %q not found in topology. %s", raw, hint)
	}
	return "", fmt.Errorf("resource %q not found in topology", raw)
}

// Creates and persists a new bug record with a generated ID, linked to a topology node.
func (m *TopologyManager) CreateBug(nodeID string, description string) (*domain.KnownBug, error) {
	bug := domain.KnownBug{
		ID:          fmt.Sprintf("bug_%d_%d", time.Now().UnixNano(), atomic.AddInt64(&bugIDCounter, 1)),
		NodeID:      nodeID,
		Description: description,
		State:       domain.BugPending,
	}
	if err := helper.CreateBug(m.dbPath, bug); err != nil {
		return nil, fmt.Errorf("create bug: %w", err)
	}
	return &bug, nil
}

// Retrieves known bugs from the database filtered by node ID and state.
func (m *TopologyManager) ListBugs(nodeID string, state domain.BugState) ([]domain.KnownBug, error) {
	return helper.ReadBugs(m.dbPath, nodeID, state)
}

// Marks a bug as acknowledged in the topology database
func (m *TopologyManager) AcknowledgeBug(bugID string) error {
	return helper.UpdateBugState(m.dbPath, bugID, domain.BugAcknowledged)
}

// Marks a bug as dismissed in the topology database.
func (m *TopologyManager) DismissBug(bugID string) error {
	return helper.UpdateBugState(m.dbPath, bugID, domain.BugDismissed)
}

// Deletes a single bug record by ID from the database.
func (m *TopologyManager) DeleteBug(bugID string) error {
	return helper.DeleteBug(m.dbPath, bugID)
}

// Deletes all bug records from the database.
func (m *TopologyManager) DeleteAllBugs() error {
	return helper.DeleteAllBugs(m.dbPath)
}

// Updates the description of a resource in the topology database.
func (m *TopologyManager) UpdateDescription(id string, kind domain.ResourceKind, description string) error {
	return helper.UpdateDescription(m.dbPath, kind, id, description)
}

// Removes descriptions for the specified resource kinds from the database and returns the count of cleared entries
func (m *TopologyManager) ClearDescriptions(targets []domain.ResourceKind) (int64, error) {
	return helper.ClearDescriptions(m.dbPath, targets)
}

// Retrieves all topology warnings from the database.
func (m *TopologyManager) GetWarnings() (map[string]domain.TopologyWarning, error) {
	topo, err := helper.ReadDb(m.dbPath)
	if err != nil {
		return nil, err
	}
	return topo.Warnings, nil
}

// Retrieves topology warnings filtered by source ID, target ID, and kind.
func (m *TopologyManager) ListWarnings(sourceID, targetID string, kind domain.WarningKind) ([]domain.TopologyWarning, error) {
	topo, err := helper.ReadDb(m.dbPath)
	if err != nil {
		return nil, err
	}
	var results []domain.TopologyWarning
	for _, w := range topo.Warnings {
		if sourceID != "" && w.SourceID != sourceID {
			continue
		}
		if targetID != "" && w.TargetID != targetID {
			continue
		}
		if kind != "" && w.Kind != kind {
			continue
		}
		results = append(results, w)
	}
	return results, nil
}
