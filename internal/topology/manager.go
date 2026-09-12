package topology

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	"github.com/Rhuan-Marques/aracne/internal/topology/idresolve"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner"
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
	// A moved project's stored root names a directory that is gone, so every mode below would
	// fail on it -- silently, since a hook cannot report. Rebuilding under the new root is
	// already a full rescan, which is at least what any mode asks for.
	if moved, err := m.SyncRelocation(reg); moved || err != nil {
		return err
	}
	root := "."
	if stored := helper.ReadStoredRoot(m.dbPath); stored != "" {
		root = stored
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
		domain.SetActiveMaxFileSize(0)
		return
	}
	cfg := helper.LoadConfig(helper.ConfigPath(m.dbPath))
	domain.SetActivePathVisibility(domain.BuildPathVisibility(root, cfg.Paths))
	domain.SetActiveIgnore(domain.BuildIgnoreMatcher(root, cfg.Scan.Ignore))
	domain.SetActiveMaxFileSize(cfg.EffectiveMaxFileSize())
}

// Scans codebase and writes the complete topology to the database.
func (m *TopologyManager) FullScan(root string, reg *scanner.Registry) error {
	return helper.WithTopologyLock(m.dbPath, func() error {
		return m.fullScanLocked(root, reg)
	})
}

// fullScanLocked is FullScan's body, run with the database lock already held. See
// helper.WithTopologyLock for why the two halves are separate: the lock is the OS lock and is
// not reentrant, so every in-package path that already holds it calls the locked form.
func (m *TopologyManager) fullScanLocked(root string, reg *scanner.Registry) error {
	root = helper.CanonicalPath(root)
	m.applyPathVisibility(root)
	// Before the parse, so a file edited while the scan runs stays stale; see
	// helper.SnapshotManifest.
	stamps := helper.SnapshotManifest(attemptedSourceFiles(root, reg))
	topo, err := scanAllLanguages(root, reg)
	if err != nil {
		return err
	}
	// A declared implementer that does not deliver is a property of the code, not of an
	// edit, so a cold scan has to find it too -- otherwise `arac scan --all` would be the
	// one way to make these warnings disappear.
	syncInterfaceConflicts(topo, nil)
	// nil: everything was just parsed, so everything needs a fingerprint.
	helper.StampBodyHashes(topo, nil)
	if err := helper.WriteDb(topo, m.dbPath); err != nil {
		return err
	}
	// From scratch includes the lazy fill's attempt ledger. The rebuild wrote every resource
	// back without its generated description, but the ledger lives outside the graph and is
	// keyed on fingerprints the rebuild leaves unchanged, so it went on suppressing exactly the
	// fills that would describe them again. Best-effort, like the ledger itself.
	_ = helper.ClearDescriptionAttempts(m.dbPath)
	helper.SyncManifest(topo, m.dbPath, stamps)
	m.recordProjectManifests(root, fileIDs(topo))
	return nil
}

// IncrementalLanguages is what an incremental scan diffs: every language detected on disk, plus
// every registered language the manifest still records files for.
//
// Exported because `arac scanner run` has to ask the same question -- it diffs the tree itself
// on every tick, and diffing the detected languages alone left the watcher blind to exactly
// the deletion described below. One definition, two callers, rather than two that drift.
//
// The second half is a language whose LAST file was just deleted. It is no longer detected, so
// diffing detected languages alone never reported the deletion: its nodes stayed in the graph
// through every later scan while `check-updates` -- which diffs the same union, see
// detectedLanguages -- called the index stale. DiffScanFiles already processes a genuine
// last-file deletion; it was simply never asked.
func (m *TopologyManager) IncrementalLanguages(root string, reg *scanner.Registry) []scanner.LanguageScanner {
	langs := reg.DetectAll(root)
	manifest := helper.ReadManifest(helper.ManifestPath(m.dbPath))
	if len(manifest) == 0 {
		return langs
	}
	seen := make(map[string]bool, len(langs))
	for _, ls := range langs {
		seen[ls.Name()] = true
	}
	for _, ls := range reg.All() {
		if !seen[ls.Name()] && helper.ManifestHoldsLanguage(root, manifest, ls.Name()) {
			seen[ls.Name()] = true
			langs = append(langs, ls)
		}
	}
	return langs
}

// Scans codebase for changes and updates topology incrementally, handling added/modified/deleted files with optional partial-update optimization and cascading re-resolution of affected callers.
func (m *TopologyManager) IncrementalScan(root string, reg *scanner.Registry) ([]domain.TopologyWarning, error) {
	var warnings []domain.TopologyWarning
	err := helper.WithTopologyLock(m.dbPath, func() error {
		var err error
		warnings, err = m.incrementalScanLocked(root, reg)
		return err
	})
	return warnings, err
}

// incrementalScanLocked is IncrementalScan's body, run with the database lock already held.
func (m *TopologyManager) incrementalScanLocked(root string, reg *scanner.Registry) ([]domain.TopologyWarning, error) {
	root = helper.CanonicalPath(root)
	// The manifest and every file ID name the directory the project USED to be in. Diffing them
	// against the new one reports every file deleted and every file new, and the result is an
	// empty graph; see Relocation.
	if _, projectRoot, moved := m.Relocation(); moved {
		return m.fullReScanLocked(projectRoot, reg)
	}
	m.applyPathVisibility(root)
	if info, err := os.Stat(root); err != nil {
		return nil, fmt.Errorf("topology root %s is not accessible: %w", root, err)
	} else if !info.IsDir() {
		return nil, fmt.Errorf("topology root %s is not a directory", root)
	}
	langScanners := m.IncrementalLanguages(root, reg)
	if len(langScanners) == 0 {
		return nil, fmt.Errorf("no language scanner detected for %s", root)
	}

	// Change detection only needs the manifest plus a directory walk, so gate on
	// the cheap files (db + manifest existence) here and defer the expensive graph
	// load until we know there is actually work to do.
	manifestPath := helper.ManifestPath(m.dbPath)
	if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
		return m.fullReScanLocked(root, reg)
	}
	if _, err := os.Stat(m.dbPath); os.IsNotExist(err) {
		return m.fullReScanLocked(root, reg)
	}
	// A changed go.mod or Cargo.toml re-roots every id, which no per-file diff can see; see
	// projectManifestNames.
	if m.projectManifestsChanged(root) {
		warnings, err := m.fullReScanLocked(root, reg)
		if err != nil {
			// Retrying the same inputs cannot succeed, and retrying costs a full scan in front
			// of every tool call. The error is the report; the next change tries again.
			m.recordProjectManifests(root, nil)
		}
		return warnings, err
	}

	var added, modified, deleted []string
	// A LANGUAGE WHOSE DIFF FAILS IS SKIPPED, NOT FATAL TO THE WHOLE SCAN.
	//
	// This loop used to `return nil, diffErr` on the first failure, which handed one language
	// a veto over every other. The reachable case is DiffScanFiles' mass-deletion guard: remove
	// the last .py file from a mixed repo and every incremental scan from then on -- `arac
	// scan`, the guard's pre-tool scan, the drift check -- aborted before it reached Go,
	// TypeScript or Rust. Nothing removed the manifest entry that caused it, so the graph
	// silently stopped tracking the whole project until someone ran `arac scan --all`, which
	// nothing told them to do.
	//
	// Recorded rather than swallowed: the errors go into topo.Errors below, which is what
	// `info` surfaces and what a reader of the graph can act on.
	diffErrs := map[string]string{}
	for _, ls := range langScanners {
		// Not `m`: that is this method's receiver, and shadowing it here is correct only
		// for as long as nothing in the loop needs m.dbPath.
		a, mod, d, diffErr := helper.DiffScanFiles(root, ls.Name(), manifestPath)
		if diffErr != nil {
			diffErrs["diff:"+ls.Name()] = diffErr.Error()
			continue
		}
		added = append(added, a...)
		modified = append(modified, mod...)
		deleted = append(deleted, d...)
	}

	// Nothing changed: the manifest already matches the tree, so there is no
	// reason to deserialize the topology graph (the only remaining work,
	// SyncManifest, is a no-op when the diff is empty).
	if len(added) == 0 && len(modified) == 0 && len(deleted) == 0 {
		// Unless the reason nothing changed is that every diff failed. Reporting success
		// there would make a broken scan indistinguishable from a quiet one.
		if len(diffErrs) > 0 {
			return nil, fmt.Errorf("%s", firstValue(diffErrs))
		}
		return nil, nil
	}

	// Phase 3: the scoped partial change-path. When it is safe — no deleted
	// files (deletes need a whole-graph referrer sweep) and every changed file's
	// scanner can update without loading the whole graph — persist each changed
	// file's delta directly from the DB, never reading or rewriting the full
	// graph. Correctness over coverage: anything unsafe falls through to the
	// existing full path below, unchanged.
	//
	// A failed language diff also disqualifies it: the fast path never loads the graph, so it
	// has nowhere to record the error, and taking it would lose the one report of a language
	// that is no longer being scanned.
	if len(diffErrs) == 0 {
		if handled, warnings, err := m.tryPartialIncremental(root, reg, added, modified, deleted); handled {
			return warnings, err
		}
	}

	// A change exists; only now is it worth loading the full graph.
	topo, err := helper.ReadDb(m.dbPath)
	if err != nil {
		return m.fullReScanLocked(root, reg)
	}
	// The diff errors describe THIS scan, so the previous scan's are cleared first. ReadDb
	// loads the stored `error:` rows back into topo.Errors and writeScanErrors rewrites the
	// map wholesale, so without this a `diff:` error would outlive the problem that caused it.
	for key := range topo.Errors {
		if strings.HasPrefix(key, "diff:") {
			delete(topo.Errors, key)
		}
	}
	for key, msg := range diffErrs {
		topo.Errors[key] = msg
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
		// DEEP copy. A struct copy shares Properties and Connections with the live graph,
		// and removeLanguageResources filters connection slices in place (`kept :=
		// targets[:0]`) during the update -- so the "pre-update snapshot" was being
		// truncated by the update it exists to be compared against.
		beforeResources[id] = cloneResource(res)
		beforeSigKeys[id] = resourceSignatureKey(res)
	}
	// Needed to keep a re-raised signature_changed warning pointing at the
	// signature its callers were originally written against; see
	// helper.RestoreSignatureBaselines.
	beforeWarnings := cloneWarnings(topo.Warnings)

	var allWarnings []domain.TopologyWarning

	changedFiles := append(append([]string(nil), added...), modified...)
	// Before the parse, so a file edited while this scan runs stays stale; see
	// helper.SnapshotManifest.
	stamps := helper.SnapshotManifest(changedFiles)

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
	// Snapshot the files that actually changed on disk BEFORE the expansions below widen
	// resolveSet. Only an edited file is authoritative about what it references; one that is
	// re-parsed because something it depends on moved is the file a warning points AT.
	editedFiles := make(map[string]bool, len(resolveSet))
	for path := range resolveSet {
		editedFiles[path] = true
	}
	// A deleted file is never re-resolved, in either direction: it is removed below, and
	// re-parsing it first would only record an error -- one nothing would ever clear.
	deletedFiles := make(map[string]bool, len(deleted))
	for _, path := range deleted {
		if abs, absErr := filepath.Abs(path); absErr == nil {
			deletedFiles[abs] = true
		}
	}
	callerFiles := m.reverseCallerFiles(topo, beforeResources, beforeSigKeys, resolveSet)
	for f := range callerFiles {
		if !deletedFiles[f] {
			resolveSet[f] = true
		}
	}
	// The other direction: files that already name something this batch ADDED, or import a
	// module whose exports changed.
	settled := make(map[string]bool, len(resolveSet)+len(deleted))
	for path := range resolveSet {
		settled[path] = true
	}
	for path := range deletedFiles {
		settled[path] = true
	}
	for f := range dependentFiles(topo, beforeResources, beforeSigKeys, editedFiles, settled) {
		resolveSet[f] = true
	}
	for f := range repeatedDeclarationSiblings(topo, deleted, settled) {
		resolveSet[f] = true
	}
	for f := range coOwnerFiles(topo, deleted, resolveSet) {
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

	removalWarnings, resettled, _ := removeDeletedFiles(topo, reg, deleted)
	allWarnings = append(allWarnings, removalWarnings...)
	for f := range resettled {
		resolveSet[f] = true
	}

	// Re-fingerprint what was re-parsed, before anything tries to match on it. Scoped to the
	// re-resolved set: a file nobody touched keeps the hash ReadDb loaded for it.
	helper.StampBodyHashes(topo, resolveSet)

	// A description belongs to an ID, not to the file that happened to hold it last.
	restoreDescriptions(topo, beforeResources)

	// Files that were EDITED are authoritative about what they reference. A file re-parsed only
	// because something it depends on moved is not: every scanner but goscanner drops a
	// reference it can no longer resolve instead of reporting it, so clearing its warnings here
	// would erase the one record that the broken reference exists.
	for path := range editedFiles {
		helper.ClearReferrerWarningsForFile(topo, path)
	}

	// Symbols removed from files that still exist: RemoveFileResources above
	// only covers whole-file deletion, and only goscanner did the reverse
	// lookup for the symbol case. This does it from the graph, so it works for
	// every language.
	referrerWarnings := referrerPass(topo, beforeResources, removedSince(beforeSigs, topo.Resources), "", editedFiles)

	// Every scanner but goscanner reports a signature change against the symbol
	// that changed and names no caller. Nothing can ever clear that shape, so fan
	// it out to the callers that now need verifying before it reaches
	// topo.Warnings and, from there, the database.
	allWarnings = helper.ExpandSignatureWarnings(topo, allWarnings)

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
	// changedFiles, not just the ones that produced nodes: a file this scan tried and failed
	// to parse still has to be stamped, or it comes back as `added` on every later scan. And
	// ONLY changedFiles: every other file in the graph keeps the stamp it had, because this scan
	// never read it. See helper.SyncManifest.
	helper.SyncManifest(topo, m.dbPath, stamps)

	return allWarnings, nil
}

// restoreDescriptions carries a description across a re-parse BY ID, from the pre-update
// snapshot the caller already holds.
//
// WHY THE SCANNERS CANNOT DO THIS THEMSELVES. Every scanner preserves descriptions, and every
// one of them keys the carry-over on the FILE: goscanner builds its `oldFunctions` map from
// `oldFile.Functions()` and fills a blank description out of that, and jsscanner, pyscanner,
// rustscanner and javascanner all have the same shape. That is exactly right for an edit and
// blind to a rename -- the file being registered is BRAND NEW, so the map is empty, and the
// descriptions sitting on the identical ids under the old path are never consulted. Renaming
// `pkg/a.go` to `pkg/b.go` therefore returned every symbol in it with an empty description,
// even once F-01's ownership guard kept the symbols themselves alive.
//
// The loss is not symmetric across a codebase, and it falls on the expensive half. A Go
// function with a doc comment is re-described from source on every parse, so it never notices.
// A function with no doc comment is precisely the one an `arac descriptions generate` sweep
// paid a model to describe, and it came back blank -- silently, with no way to tell that
// anything had been dropped.
//
// So the rule is applied once, here, where ids rather than files are the unit of identity.
// It is the same rule FullReScan already applies to a cold rescan and the same rule each
// scanner applies within a file; this is only the third place it has to hold.
//
// WHAT IT DELIBERATELY DOES NOT DO. It never overwrites: a freshly harvested doc comment
// arrives non-empty and wins, which is what keeps a renamed comment from being shadowed by the
// old one. It cannot resurrect a cleared description either -- `descriptions clear` writes the
// database directly, so the next scan's snapshot is already empty. And it can only help where
// the ID SURVIVES: Go ids are `module/package.Symbol` and Java's are an FQN plus a signature,
// so a rename inside a package keeps them, while Python, JavaScript, TypeScript and Rust are
// modules-first and mint a new id from the new filename. Those need the identity remap
// (helper.MatchDescriptions), which today only FullReScan reaches.
func restoreDescriptions(topo *domain.Topology, before map[string]domain.Resource) {
	if topo == nil || len(before) == 0 {
		return
	}
	for id, res := range topo.Resources {
		if strings.TrimSpace(res.Description) != "" {
			continue
		}
		old, ok := before[id]
		if !ok || strings.TrimSpace(old.Description) == "" {
			continue
		}
		res.Description = old.Description
		topo.Resources[id] = res
	}
}

// firstValue returns one value from a map, chosen by sorted key so the message a caller sees
// does not change between runs over the same failures.
func firstValue(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return ""
	}
	return m[keys[0]]
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
	// Before the parse; see helper.SnapshotManifest.
	stamps := helper.SnapshotManifest(changed)

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
		// These came straight out of a re-parse and carry no fingerprints, and the hashes are
		// columns on the row -- so writing them unstamped would overwrite a correctly stamped
		// row with an empty one and silently lose the ability to track this code through a
		// later move.
		helper.StampBodyHashesSlice(upserts)
		// And repair the rows this path's own diff is blind to -- a body edited in place,
		// leaving the span and the signature untouched, is invisible to it. See
		// helper.RestampUnchangedRows.
		upserts = helper.RestampUnchangedRows(m.dbPath, absPath, upserts, deletes)
		// WriteScopedResources rewrites the whole warnings table from this map, so
		// a warning this very delta orphaned would be re-inserted and outlive the
		// code it points at. The whole-graph CleanupOrphanedWarnings cannot run on
		// a working set; this drops the ones the delta is known to have
		// invalidated. Runs before the report loop below so a dropped warning is
		// not surfaced either.
		helper.CleanupOrphanedWarningsScoped(warnings, deletes)
		// The baseline steps of settleSignatureWarnings, scoped. They need no whole-graph
		// snapshot: a warning raised here names a callee in THIS file, whose previous rows are
		// still in the database until WriteScopedResources below. Without a baseline a
		// return-type-only change is invisible to the reconcile that follows -- the call still
		// fits what it passes -- so a same-file caller was never warned. Only warnings raised
		// by this update are stamped; one that stood before keeps (or is restored to) the
		// baseline it was raised against.
		if old, err := helper.ReadResourcesByFile(m.dbPath, absPath); err == nil {
			helper.RestoreSignatureBaselines(&domain.Topology{Warnings: warnings}, preexisting)
			raised := make(map[string]domain.TopologyWarning)
			for id, w := range warnings {
				if _, stood := preexisting[id]; !stood {
					raised[id] = w
				}
			}
			helper.StampSignatureBaselines(&domain.Topology{Warnings: raised}, old)
			for id, w := range raised {
				warnings[id] = w
			}
		}
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
	if err := helper.StampManifest(m.dbPath, stamps); err != nil {
		return true, allWarnings, fmt.Errorf("sync manifest: %w", err)
	}
	return true, allWarnings, nil
}

// Rescans codebase and preserves existing resource descriptions when re-indexing.
//
// Descriptions are matched by resource ID first, then BY IDENTITY -- always, not only when
// something detected a change. IDs drift for several reasons (a renamed file, a workspace
// member resolving differently, a change to how a scanner builds them), and ID-only matching
// silently discards a description every time: both sides describe the same code and no longer
// spell the same string. So this also runs the identity remap (same tiers as the descriptions
// sidecar: path+kind+name+parent, then source hash), carries bugs across, and records
// old -> new in `resource_alias` so IDs an agent already knows keep resolving.
//
// This is the ONLY path that remaps. An incremental scan re-parses just the changed files, so
// on a database whose IDs were minted under an older grammar it re-mints those files and
// leaves the rest -- see the note above WriteResourceAliases in helper/db.go.
func (m *TopologyManager) FullReScan(root string, reg *scanner.Registry) ([]domain.TopologyWarning, error) {
	var warnings []domain.TopologyWarning
	err := helper.WithTopologyLock(m.dbPath, func() error {
		var err error
		warnings, err = m.fullReScanLocked(root, reg)
		return err
	})
	return warnings, err
}

// fullReScanLocked is FullReScan's body, run with the database lock already held.
func (m *TopologyManager) fullReScanLocked(root string, reg *scanner.Registry) ([]domain.TopologyWarning, error) {
	root = helper.CanonicalPath(root)
	m.applyPathVisibility(root)
	// Before the parse; see helper.SnapshotManifest.
	stamps := helper.SnapshotManifest(attemptedSourceFiles(root, reg))
	newTopo, err := scanAllLanguages(root, reg)
	if err != nil {
		return nil, err
	}
	// Before the remap, which matches moved code by these hashes. The OLD side's hashes come
	// out of the database, where the previous scan stored them while the old files still
	// existed -- which is the whole reason they are persisted rather than recomputed here.
	helper.StampBodyHashes(newTopo, nil)

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
		// A relocated project re-mints every ID that is a path. Those map exactly, so they
		// carry their description and bugs, and resolve as aliases, whether or not the
		// identity remap above reached them.
		for oldID, newID := range rebasedPathIDs(oldTopo, newTopo) {
			if newRes := newTopo.Resources[newID]; newRes.Description == "" && oldTopo.Resources[oldID].Description != "" {
				newRes.Description = oldTopo.Resources[oldID].Description
				newTopo.Resources[newID] = newRes
			}
			if _, taken := aliases[oldID]; !taken {
				if aliases == nil {
					aliases = make(map[string]string)
				}
				aliases[oldID] = newID
			}
		}
	}

	// Warnings whose cause is still on disk survive the rebuild, re-judged against the new
	// graph. Without this, `scan --all`, a dependency-file edit or `pre_tool: "full"` emptied
	// the table while the broken callers it named were untouched. See carryForwardWarnings.
	carryForwardWarnings(oldTopo, newTopo, aliases)

	// Same reason as in FullScan: a declared implementer that does not deliver is a
	// property of the code, so every scan mode has to find it. `scan --all` reaches this
	// function rather than FullScan, and missing it here would make a full rescan the one
	// way to make these warnings disappear.
	syncInterfaceConflicts(newTopo, nil)

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
	helper.SyncManifest(newTopo, m.dbPath, stamps)
	m.recordProjectManifests(root, fileIDs(newTopo))
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
		// An empty file has no lines, and asking for the whole of it is a legitimate
		// question with an empty answer -- a placeholder __init__.py, a stubbed module.
		// Falling through would report "EndsAt 0 < StartsAt 1", which reads as a corrupt
		// record rather than as an empty file.
		if len(lines) == 0 {
			return &domain.CodeEntry{Location: domain.Location{Path: loc.Path}, Cut: ""}, nil
		}
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
	var warnings []domain.TopologyWarning
	err := helper.WithTopologyLock(m.dbPath, func() error {
		var err error
		warnings, err = m.updateFileLocked(path, reg)
		return err
	})
	return warnings, err
}

// updateFileLocked is UpdateFile's body, run with the database lock already held.
func (m *TopologyManager) updateFileLocked(path string, reg *scanner.Registry) ([]domain.TopologyWarning, error) {
	topo, err := helper.ReadDb(m.dbPath)
	if err != nil {
		return nil, fmt.Errorf("read topology db: %w", err)
	}
	beforeSigs := helper.ResourceSignatures(topo.Resources)
	beforeResources := make(map[string]domain.Resource, len(topo.Resources))
	for id, res := range topo.Resources {
		// Deep, for the same reason as in IncrementalScan: the update mutates connection
		// slices in place, and a shallow snapshot shares them.
		beforeResources[id] = cloneResource(res)
	}
	beforeWarnings := cloneWarnings(topo.Warnings)
	// CANONICAL, not merely absolute. The caller's path comes from wherever the agent last
	// cd'd to, which may reach the project through a symlinked directory, while the stored root
	// is the real one. filepath.Abs keeps the link in the path, and every id minted from the
	// relative path between the two spellings (`_/../real/pkg.One`) belongs to no project and
	// leaves the manifest naming files the next scan reports deleted. See helper.CanonicalPath.
	absPath := helper.CanonicalPath(path)
	if absPath == "" {
		return nil, fmt.Errorf("resolve path %s", path)
	}
	// Before the parse; see helper.SnapshotManifest.
	stamps := helper.SnapshotManifest([]string{absPath})
	// The files this update re-parsed: absPath, plus whatever removing it had to re-settle.
	reparsed := map[string]bool{absPath: true}

	finish := func(warnings []domain.TopologyWarning) ([]domain.TopologyWarning, error) {
		// Same placement argument as restoreDescriptions below: in finish, so that every exit
		// path -- including the four that only REMOVE a file's resources -- agrees about what
		// is fingerprinted.
		helper.StampBodyHashes(topo, reparsed)
		// In finish rather than beside the re-parse, so every exit path gets it. A rename
		// reaches this verb as two calls -- one registering the new path, one removing the old
		// -- which is how OpenCode's edit-sync plugin and the `arac update-file` hook see one,
		// so the id-keyed carry-over has to hold here exactly as it does in IncrementalScan.
		restoreDescriptions(topo, beforeResources)
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
		// SCOPED to this one file. SyncManifest used to stamp EVERY file in the topology with
		// its current mtime, which is right after a scan that parsed them all and badly wrong
		// here: parsing one file would declare the whole tree freshly indexed. Anything that
		// had changed without being re-parsed -- a checkout, a restored database, a branch
		// switch -- was then invisible to every later incremental scan, because the manifest
		// said it was current. One edit froze that staleness in permanently, which is how a
		// benchmark fixture could stay wrong for the whole run.
		if _, stillIndexed := topo.Resources[absPath]; stillIndexed {
			if err := helper.StampManifest(m.dbPath, stamps); err != nil {
				return nil, fmt.Errorf("sync manifest: %w", err)
			}
		} else if err := helper.ForgetManifestFiles(m.dbPath, []string{absPath}); err != nil {
			return nil, fmt.Errorf("sync manifest: %w", err)
		}
		return warnings, nil
	}

	// Removing the file can take down more than its own resources (see removeDeletedFiles), and
	// what a re-settled file drops has referrers too.
	remove := func() []domain.TopologyWarning {
		warnings, resettled, vanished := removeDeletedFiles(topo, reg, []string{absPath})
		for f := range resettled {
			reparsed[f] = true
		}
		return append(warnings, referrerPass(topo, beforeResources, vanished, absPath, nil)...)
	}

	// Hidden paths (config) are excluded from the topology: install the filter
	// and, when this file is hidden, drop any stale resources and make the update
	// a no-op so native edits / MCP writes to hidden files never re-index them.
	m.applyPathVisibility(topo.Root)
	if domain.PathHidden(absPath) {
		return finish(remove())
	}

	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		return finish(remove())
	} else if err != nil {
		return nil, err
	}

	langScanner := reg.DetectFile(absPath)
	if langScanner == nil {
		return finish(remove())
	}
	if !helper.IsSourceFile(topo.Root, absPath, langScanner.Name()) {
		return finish(remove())
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
	scannerWarnings = helper.ExpandSignatureWarnings(topo, scannerWarnings)
	mergeNewWarnings(topo, scannerWarnings)

	// This file was just re-parsed from source, so what it references now is
	// authoritative: drop node_removed warnings attributed to its own
	// resources. Anything still broken is re-emitted by the pass below.
	helper.ClearReferrerWarningsForFile(topo, absPath)

	// Symbols that disappeared in this update get caller-attributed warnings,
	// for every language. beforeSigs' keys are the pre-update id set.
	removed := removedSince(beforeSigs, topo.Resources)
	mergeNewWarnings(topo, referrerPass(topo, beforeResources, removed, absPath, map[string]bool{absPath: true}))

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
// Referrers living in skipPaths are skipped: those files were EDITED in this
// same update and re-parsed from source, so goscanner has already emitted
// use_missing_node for them and the other scanners simply dropped the edge.
// Warning about them here would duplicate that.
//
// The sweep reads the pre-update graph as well as the current one, and in a batch the second
// read is the one that finds anything. IncrementalScan re-parses every file that called a
// removed symbol (reverseCallerFiles), and every scanner but goscanner answers a reference it
// can no longer resolve by dropping the edge -- so by the time this runs, the only record that
// app.py called helper() is `before`. Reading the current graph alone is why deleting a
// function called from another file warned in Go and in no other language, and why a Java call
// that the resolver quietly re-linked to a surviving overload was never reported either. The
// current graph is still swept, for the referrers nothing re-parsed: their edge is still there,
// and it is stripped.
func referrerPass(topo *domain.Topology, before map[string]domain.Resource, removed map[string]bool, origin string, skipPaths map[string]bool) []domain.TopologyWarning {
	if len(removed) == 0 {
		return nil
	}
	skip := func(id string) bool {
		res, ok := topo.Resources[id]
		// A referrer that is itself gone has nothing left to verify.
		return !ok || skipPaths[res.Location.Path]
	}
	scan := helper.ReferrerScan{
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
	}
	warnings := helper.ScanReferrers(topo, scan)
	seen := make(map[string]bool, len(warnings))
	for _, w := range warnings {
		seen[w.ID] = true
	}
	// The snapshot is evidence, not state: never strip it.
	scan.Strip = false
	for _, w := range helper.ScanReferrers(&domain.Topology{Resources: before}, scan) {
		if !seen[w.ID] {
			seen[w.ID] = true
			warnings = append(warnings, w)
		}
	}
	return warnings
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
	// ONE LANGUAGE'S FAILURE IS RECORDED, NOT FATAL TO THE WHOLE SCAN.
	//
	// This loop used to return on the first scanner error, which handed every language a veto
	// over all the others: a single .go file with no go.mod above it made goscanner fail, and a
	// Python, JavaScript or Rust project got no topology at all -- `arac scan` exited 1 and
	// `arac init` stopped before writing the integration. The failure goes into Errors, where
	// `arac scan` prints it, and the other languages are indexed. Only when EVERY detected
	// language failed is it an error: there is nothing to write, and writing an empty graph over
	// the stored one would be worse than leaving it.
	var firstErr error
	failed := 0
	for _, langScanner := range langScanners {
		topo, err := langScanner.Scan(root)
		if err != nil {
			err = fmt.Errorf("scan %s: %w", langScanner.Name(), err)
			if firstErr == nil {
				firstErr = err
			}
			failed++
			merged.Errors["scan:"+langScanner.Name()] = err.Error()
			continue
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
	if failed == len(langScanners) {
		return nil, firstErr
	}
	normalizeTopologyLanguages(merged)
	return merged, nil
}

// attemptedSourceFiles is every file a cold scan of root would hand to a scanner: one walk
// per detected language, filtered exactly as DiffScanFiles filters the manifest, so the two
// agree about what counts as a source file.
//
// It exists so SyncManifest can stamp a file the scan tried and failed to parse. Without it
// such a file produces no node, is never stamped, and is reported `added` by every scan from
// then on -- see helper.SyncManifest.
func attemptedSourceFiles(root string, reg *scanner.Registry) []string {
	if reg == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, ls := range reg.DetectAll(root) {
		files, err := helper.CollectSourceFiles(root, ls.Name())
		if err != nil {
			continue
		}
		for _, f := range files {
			if !seen[f] {
				seen[f] = true
				out = append(out, f)
			}
		}
	}
	return out
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
	var ownDeps []string
	for id, res := range subTopo.Resources {
		if res.Kind == domain.ResourceDependency {
			ownDeps = append(ownDeps, id)
		}
	}
	warnings, err := langScanner.UpdateFile(subTopo, path)
	if err != nil {
		return nil, err
	}
	tagTopologyLanguage(subTopo, lang)
	topo.Warnings = cloneWarnings(subTopo.Warnings)
	removeLanguageResources(topo, lang)
	mergeTopology(topo, subTopo)
	if lang == "go" {
		pruneDroppedDependencies(topo, subTopo, ownDeps)
	}
	normalizeTopologyLanguages(topo)
	return warnings, nil
}

// pruneDroppedDependencies deletes the dependency nodes the scanner stopped emitting and
// nothing references any more. removeLanguageResources keeps every dependency node, because
// one can be shared with another language, so without this a dependency whose last import was
// removed stayed in the graph for good -- a cold scan never writes it. Go only: goscanner
// re-derives its whole dependency set from the files' imports on every update, which is what
// makes "no longer emitted" mean "no longer imported"; the other scanners' update paths have
// not been checked for the same property.
func pruneDroppedDependencies(topo, subTopo *domain.Topology, ownDeps []string) {
	var dropped []string
	for _, id := range ownDeps {
		if _, still := subTopo.Resources[id]; !still {
			dropped = append(dropped, id)
		}
	}
	if len(dropped) == 0 {
		return
	}
	referenced := make(map[string]bool)
	for _, res := range topo.Resources {
		for _, targets := range res.Connections {
			for _, t := range targets {
				referenced[t] = true
			}
		}
	}
	for _, id := range dropped {
		if res, ok := topo.Resources[id]; ok && res.Kind == domain.ResourceDependency && !referenced[id] {
			delete(topo.Resources, id)
		}
	}
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
	// A package-level var or const is referenced the same way and goes away the same way; a
	// user left un-re-resolved keeps an edge to an id nothing declares any more.
	"uses_extvar",
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
	// And a warning naming a caller the same update made irrelevant -- one that no longer calls
	// the callee, or was written against its new signature -- goes too.
	helper.RetireStaleSignatureCallers(topo, beforeResources)
	// Conformance is derived wholesale from current state, so it is rebuilt rather than
	// reconciled: every previous verdict is dropped and re-derived. That is what keeps a
	// fixed implementer from leaving a stale row behind, with nothing stored and nothing
	// to clear.
	raised := syncInterfaceConflicts(topo, beforeWarnings)

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
	// A conflict raised by THIS update is news the agent needs now -- it usually means the
	// edit it just made unhooked an implementer. One that was already standing is not, and
	// repeating it after every unrelated edit is how a channel gets ignored.
	return append(kept, raised...)
}

// syncInterfaceConflicts replaces every interface_conflict warning with the set the graph
// currently justifies.
//
// Clear-then-rebuild, deliberately. The warning is a statement about the graph as it stands
// -- this type promises this interface and does not deliver it -- so there is no event to
// remember and no lifecycle to get wrong. Fixing the implementer, fixing the interface, or
// deleting either simply stops producing the warning on the next pass.
func syncInterfaceConflicts(
	topo *domain.Topology, before map[string]domain.TopologyWarning,
) []domain.TopologyWarning {
	for id, w := range topo.Warnings {
		if w.Kind == domain.WarnInterfaceConflict {
			delete(topo.Warnings, id)
		}
	}
	var raised []domain.TopologyWarning
	for _, w := range helper.InterfaceConflictWarnings(topo) {
		topo.Warnings[w.ID] = w
		if _, stood := before[w.ID]; !stood {
			raised = append(raised, w)
		}
	}
	sort.Slice(raised, func(i, j int) bool { return raised[i].ID < raised[j].ID })
	return raised
}

// resourceSignatureKey returns a fingerprint of the parts of a resource whose
// change invalidates a *caller's* body resolution: its name and its declared
// input/output types (the signature). Connections are deliberately excluded —
// a body-edge change in the resource itself does not, by itself, invalidate its
// callers. Used to detect signature changes across an incremental batch in a
// language-agnostic way (the typed Input/Output live in Properties).
//
// CANONICAL, through helper.SignatureBaseline, because the two sides of every comparison
// arrive in different Go shapes: `before` is read back from SQLite (maps, marshalled in
// sorted-key order) and the re-parsed side carries the scanner's typed structs (marshalled
// in field order). Marshalling them raw made every Python, JavaScript and TypeScript function
// with a parameter look re-signatured on every scan -- their parameter structs are not
// declared in alphabetical order -- so an edit to any file re-resolved the callers of every
// such function in the language.
//
// A file's key also carries its export surface, which is to an importer what a signature is
// to a caller: `export default one` becoming `two` re-points every default import without
// any declaration changing. See dependentFiles.
func resourceSignatureKey(res domain.Resource) string {
	var b strings.Builder
	b.WriteString(helper.SignatureBaseline(res))
	b.WriteByte('|')
	if u, ok := res.Properties["underlying"]; ok {
		j, _ := json.Marshal(u)
		b.Write(j)
	}
	if res.Kind == domain.ResourceFile {
		b.WriteByte('|')
		b.WriteString(exportSurfaceKey(res))
	}
	return b.String()
}

// exportSurfaceKey renders what a file offers its importers beyond its own declarations: the
// binding behind its default export and whatever it re-exports, by name or wholesale. Only
// JavaScript and TypeScript record any of it; for every other language this is empty and a
// file's key is its name.
func exportSurfaceKey(res domain.Resource) string {
	var parts []string
	if def, _ := res.Properties["default_export"].(string); def != "" {
		parts = append(parts, "default="+def)
	}
	for name, target := range reExportsNamed(res) {
		parts = append(parts, "named="+name+"="+target.Module+"#"+target.Name)
	}
	for _, target := range res.Connections["re_exports_module"] {
		parts = append(parts, "all="+target)
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// reExportTarget is one entry of a file's re_exports_named property.
type reExportTarget struct {
	Module string `json:"Module"`
	Name   string `json:"Name"`
}

// reExportsNamed reads a file's named re-exports whichever shape they arrive in -- the
// scanner's typed map, or its JSON round-trip read back from SQLite.
func reExportsNamed(res domain.Resource) map[string]reExportTarget {
	raw, ok := res.Properties["re_exports_named"]
	if !ok || raw == nil {
		return nil
	}
	blob, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var out map[string]reExportTarget
	if err := json.Unmarshal(blob, &out); err != nil {
		return nil
	}
	return out
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

	removedTargets := make(map[string]bool)
	for targetID := range changedTargets {
		if _, stillPresent := topo.Resources[targetID]; !stillPresent {
			removedTargets[targetID] = true
		}
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
	// Not callers, and just as stale: a Rust `impl Circle` in another file attaches its methods
	// to a type that was just renamed away, and a record naming a removed trait or interface
	// keeps a relationship nothing declares any more. Neither file references the type through
	// a body edge, so the loop above never finds it.
	for f := range structuralDependentFiles(before, removedTargets, false) {
		if !alreadyResolving[f] {
			files[f] = true
		}
	}
	return files
}

// structuralRecordConnTypes are the private per-file records that justify a structural edge
// between two OTHER resources: `impl Trait for Type` (Rust, Java) and `extends` (Java). Each
// is "<id>=>><id>", the second optionally tagged ":class"/":iface"; the scanners rebuild
// implements/inherits whole-graph from them on every pass.
var structuralRecordConnTypes = []string{"__impl_records", "__extends_records"}

// structuralRecordIDs returns the resource ids a structural record names.
func structuralRecordIDs(rec string) []string {
	i := strings.Index(rec, "=>>")
	if i <= 0 {
		return nil
	}
	parent := rec[i+len("=>>"):]
	for _, tag := range []string{":class", ":iface"} {
		parent = strings.TrimSuffix(parent, tag)
	}
	return []string{rec[:i], parent}
}

// structuralDependentFiles returns the surviving files whose structural relationships are
// justified by -- or attached to -- something in removed, and so go stale unless the file is
// re-parsed after the removal. resources must still hold the removed ids.
//
// Always: a method whose method_from is removed (a Rust impl block in another file than its
// type), and a record naming a removed id (an impl of a deleted interface, an extends of a
// deleted class). When a whole FILE was removed, also: a type whose method set just shrank
// (Go derives implements from method sets), and the records the removed file itself held, where
// they tie two resources that both survive (`impl Shape for Circle` in its own file). A changed
// file needs neither: it is re-parsed, which already rebuilds both.
func structuralDependentFiles(resources map[string]domain.Resource, removed map[string]bool, wholeFile bool) map[string]bool {
	files := make(map[string]bool)
	if len(removed) == 0 {
		return files
	}
	add := func(res domain.Resource) {
		if path := fileOf(res); path != "" {
			if abs, err := filepath.Abs(path); err == nil {
				path = abs
			}
			files[path] = true
		}
	}
	for id, res := range resources {
		if removed[id] {
			if !wholeFile {
				continue
			}
			for _, kind := range structuralRecordConnTypes {
				for _, rec := range res.Connections[kind] {
					ids := structuralRecordIDs(rec)
					if len(ids) == 0 || removed[ids[0]] || removed[ids[1]] {
						continue
					}
					for _, tied := range ids {
						if t, ok := resources[tied]; ok {
							add(t)
						}
					}
				}
			}
			continue
		}
		if owner, _ := res.Properties["method_from"].(string); owner != "" && removed[owner] {
			add(res)
			continue
		}
		tied := false
		for _, kind := range structuralRecordConnTypes {
			for _, rec := range res.Connections[kind] {
				for _, named := range structuralRecordIDs(rec) {
					tied = tied || removed[named]
				}
			}
		}
		if wholeFile {
			for _, method := range res.Connections["methods"] {
				tied = tied || removed[method]
			}
		}
		if tied {
			add(res)
		}
	}
	return files
}

// removeDeletedFiles removes each deleted file's resources, then re-parses the surviving files
// structuralDependentFiles names, so the scanners' whole-graph structural passes run against the
// graph WITHOUT the deleted files. Removal alone strips edges to what is gone but cannot undo a
// relationship between two survivors that the deleted file justified: `Circle implements Shape`
// outlived the file holding `impl Shape for Circle`, and a Go type kept implementing an interface
// after the file holding its method was deleted.
//
// Returns the warnings, the files it re-parsed, and the ids that re-parse dropped (a Rust method
// whose type went with the deleted file), whose referrers the caller still has to be told about.
func removeDeletedFiles(topo *domain.Topology, reg *scanner.Registry, deleted []string) ([]domain.TopologyWarning, map[string]bool, map[string]bool) {
	removed := make(map[string]bool)
	for _, path := range deleted {
		for id := range helper.FileRemovalSet(topo, path) {
			removed[id] = true
		}
	}
	tied := structuralDependentFiles(topo.Resources, removed, true)

	var warnings []domain.TopologyWarning
	for _, path := range deleted {
		warnings = append(warnings, helper.RemoveFileResources(topo, path)...)
	}

	reparsed := make(map[string]bool)
	if len(tied) == 0 || reg == nil {
		return warnings, reparsed, nil
	}
	present := make(map[string]bool, len(topo.Resources))
	for id := range topo.Resources {
		present[id] = true
	}
	paths := make([]string, 0, len(tied))
	for path := range tied {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if _, indexed := topo.Resources[path]; !indexed || domain.PathHidden(path) {
			continue
		}
		if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
			continue
		}
		langScanner := reg.DetectFile(path)
		if langScanner == nil || !helper.IsSourceFile(topo.Root, path, langScanner.Name()) {
			continue
		}
		ws, err := updateFileWithScanner(topo, langScanner, path)
		if err != nil {
			warnings = append(warnings, domain.TopologyWarning{
				ID:       "error:" + path,
				SourceID: path,
				Message:  fmt.Sprintf("error updating %s: %v", path, err),
			})
			continue
		}
		warnings = append(warnings, ws...)
		reparsed[path] = true
	}
	vanished := make(map[string]bool)
	for id := range present {
		if _, still := topo.Resources[id]; !still {
			vanished[id] = true
		}
	}
	return warnings, reparsed, vanished
}

// dependentFiles returns the unchanged files that must be re-resolved because this batch
// ADDED something they may already name, or changed what a module exports.
//
// reverseCallerFiles covers the other direction -- a symbol that went away or changed shape --
// and can read its answer off the pre-update graph, because the stale edges it looks for are
// still there. An addition leaves no such trail. A call written against a function that did
// not exist yet resolved to nothing, so no edge records that it is waiting, and nothing
// re-parsed it when the function appeared: `b.js` importing `later` from `a.js` kept no
// `calls` edge after `later` was written, a cold scan had one, and further plain scans never
// caught up. The same held for a new method, a new constructor, a new file satisfying a
// dangling import, a barrel gaining `export *`, and -- worse, because the graph then points at
// the WRONG symbol rather than none -- a changed `export default` or a retargeted re-export.
//
// Bounded, because this runs before every tool call and each file it returns is re-parsed:
//
//   - A module whose export surface changed re-resolves every file that depends on it,
//     following the barrels that re-export it. Nothing narrower is sound: `export default one`
//     becoming `two` changes what an importer's `def()` means without the importer naming
//     either.
//   - A declaration added to an existing module re-resolves the files that already depend on
//     that module (any edge into it, again through barrels) AND mention the new name as a
//     word. A constructor is named by its type, so it contributes its type's name.
//   - A brand-new name -- a top-level declaration no file of the language declared before, or
//     a new file's stem -- can be named from a file with no edge to anything yet: a dangling
//     import, a Java class in the same package. Those are found by content. Every file of the
//     language is READ, not parsed, and only the ones mentioning the name are re-resolved.
//
// Go is left out: goscanner re-links a dangling reference itself when its target appears (the
// use_missing_node lifecycle), and its single-file edits take the partial path.
func dependentFiles(
	topo *domain.Topology,
	before map[string]domain.Resource,
	beforeSigKeys map[string]string,
	edited map[string]bool,
	settled map[string]bool,
) map[string]bool {
	// What each module's dependents have to be told: surface means "re-resolve regardless",
	// names means "re-resolve if you mention one of these".
	type moduleChange struct {
		surface bool
		names   map[string]bool
	}
	modules := make(map[string]*moduleChange)
	// language -> names that no file of that language could have resolved before this batch.
	fresh := make(map[string]map[string]bool)
	addFresh := func(lang, name string) {
		if fresh[lang] == nil {
			fresh[lang] = make(map[string]bool)
		}
		fresh[lang][name] = true
	}
	for path := range edited {
		file, ok := topo.Resources[path]
		if !ok || file.Kind != domain.ResourceFile || file.Language == "" || file.Language == "go" {
			continue
		}
		change := &moduleChange{names: make(map[string]bool)}
		if _, existed := before[path]; !existed {
			for _, stem := range moduleStems(path) {
				addFresh(file.Language, stem)
			}
		} else if beforeSigKeys[path] != resourceSignatureKey(file) {
			change.surface = true
		}
		modules[path] = change
	}
	if len(modules) == 0 {
		return nil
	}

	var topLevel []domain.Resource
	for id, res := range topo.Resources {
		change, ok := modules[res.Location.Path]
		if !ok || res.Kind == domain.ResourceFile || res.Name == "" {
			continue
		}
		if _, existed := before[id]; existed {
			continue
		}
		change.names[res.Name] = true
		owner, _ := res.Properties["method_from"].(string)
		switch {
		case owner == "":
			topLevel = append(topLevel, res)
		case res.Name == "constructor" || res.Name == "__init__":
			// Called through its type's name -- `new A()`, `A()` -- and never through its own.
			if o, ok := topo.Resources[owner]; ok && o.Name != "" {
				change.names[o.Name] = true
			}
		}
	}
	if len(topLevel) > 0 {
		declared := make(map[string]bool)
		for _, res := range before {
			if res.Kind != domain.ResourceFile {
				declared[res.Language+"\x00"+res.Name] = true
			}
		}
		for _, res := range topLevel {
			if !declared[res.Language+"\x00"+res.Name] {
				addFresh(res.Language, res.Name)
			}
		}
	}

	// A barrel exposes whatever it re-exports to ITS importers, so it inherits the change of
	// every module it re-exports, transitively.
	reexporters := make(map[string][]string)
	for id, res := range before {
		if res.Kind != domain.ResourceFile {
			continue
		}
		for _, target := range res.Connections["re_exports_module"] {
			reexporters[target] = append(reexporters[target], id)
		}
		for _, target := range reExportsNamed(res) {
			reexporters[target.Module] = append(reexporters[target.Module], id)
		}
	}
	for super, subs := range inheritingFiles(before) {
		reexporters[super] = append(reexporters[super], subs...)
	}
	changes := make(map[string]*moduleChange, len(modules))
	for path, change := range modules {
		changes[path] = change
	}
	for path, change := range modules {
		if !change.surface && len(change.names) == 0 {
			continue
		}
		visited := map[string]bool{path: true}
		queue := []string{path}
		for len(queue) > 0 {
			module := queue[0]
			queue = queue[1:]
			for _, barrel := range reexporters[module] {
				if visited[barrel] {
					continue
				}
				visited[barrel] = true
				queue = append(queue, barrel)
				inherited, ok := changes[barrel]
				if !ok {
					inherited = &moduleChange{names: make(map[string]bool)}
					changes[barrel] = inherited
				}
				inherited.surface = inherited.surface || change.surface
				for name := range change.names {
					inherited.names[name] = true
				}
			}
		}
	}

	always := make(map[string]bool)
	mention := make(map[string]map[string]bool)
	require := func(file string, names map[string]bool) {
		if mention[file] == nil {
			mention[file] = make(map[string]bool)
		}
		for name := range names {
			mention[file][name] = true
		}
	}
	for _, res := range before {
		src := fileOf(res)
		if src == "" || settled[src] {
			continue
		}
		for connType, targets := range res.Connections {
			if !isDependencyEdge(connType) {
				continue
			}
			for _, target := range targets {
				targetRes, ok := before[target]
				if !ok {
					continue
				}
				module := fileOf(targetRes)
				change, ok := changes[module]
				if !ok || module == src {
					continue
				}
				if change.surface {
					always[src] = true
				} else if len(change.names) > 0 {
					require(src, change.names)
				}
			}
		}
	}
	for id, res := range topo.Resources {
		if res.Kind != domain.ResourceFile || settled[id] || len(fresh[res.Language]) == 0 {
			continue
		}
		require(id, fresh[res.Language])
	}

	out := make(map[string]bool, len(always))
	for file := range always {
		if _, indexed := topo.Resources[file]; indexed {
			out[file] = true
		}
	}
	for file, names := range mention {
		if out[file] {
			continue
		}
		if _, indexed := topo.Resources[file]; indexed && fileMentionsAny(file, names) {
			out[file] = true
		}
	}
	return out
}

// fileOf returns the file a resource lives in: its own id for a file node, whose identity IS
// its path, and its recorded location for everything else.
func fileOf(res domain.Resource) string {
	if res.Kind == domain.ResourceFile {
		return res.ID
	}
	return res.Location.Path
}

// isDependencyEdge reports whether an edge means its source DEPENDS on its target. Ownership
// (has_*), the reverse structural edges every scanner recomputes globally, and the private
// "__" records say nothing about that.
func isDependencyEdge(connType string) bool {
	if strings.HasPrefix(connType, "has_") || strings.HasPrefix(connType, "__") {
		return false
	}
	switch connType {
	case "methods", "constructor", "implemented_by", "inherited_by":
		return false
	}
	return true
}

// moduleStems returns the names an import of this file would spell: its stem, and for a
// directory's index module the directory, which is what `from pkg import x`, `import './lib'`
// and `mod helpers;` name instead.
func moduleStems(path string) []string {
	base := filepath.Base(path)
	stem := base
	if i := strings.IndexByte(base, '.'); i > 0 {
		stem = base[:i]
	}
	switch stem {
	case "index", "mod", "__init__":
		return []string{stem, filepath.Base(filepath.Dir(path))}
	}
	return []string{stem}
}

// fileMentionsAny reports whether the file's text contains any of names as a whole word.
// It is a prefilter, deliberately generous: a comment or a string that happens to spell the
// name costs one re-parse, while missing a real reference costs a wrong graph.
func fileMentionsAny(path string, names map[string]bool) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for name := range names {
		if containsWord(data, name) {
			return true
		}
	}
	return false
}

func containsWord(data []byte, word string) bool {
	if word == "" {
		return false
	}
	w := []byte(word)
	for from := 0; from < len(data); {
		i := bytes.Index(data[from:], w)
		if i < 0 {
			return false
		}
		start, end := from+i, from+i+len(w)
		if (start == 0 || !isWordByte(data[start-1])) && (end == len(data) || !isWordByte(data[end])) {
			return true
		}
		from = start + 1
	}
	return false
}

// isWordByte is generous about what continues an identifier -- `$` for JavaScript, and any
// non-ASCII byte -- so a boundary is only claimed where one plainly is.
func isWordByte(c byte) bool {
	return c == '_' || c == '$' || c >= 0x80 ||
		('0' <= c && c <= '9') || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
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
		existing, exists := dst.Resources[id]
		if !exists || resourceLanguage(existing, dst.Language) == resourceLanguage(res, src.Language) {
			dst.Resources[id] = cloneResource(res)
			continue
		}
		// TWO LANGUAGES NAMING THE SAME DEPENDENCY IS THE SHARED-NODE CASE, NOT AN ERROR.
		//
		// A dependency node is a bare external package name with no location and no body, and
		// removeLanguageResources already treats one as language-neutral for exactly this
		// reason: "the SAME external package can be imported from files of other languages".
		// mergeTopology disagreed -- it recorded an error and dropped the second side -- so a
		// repository holding both Go's `path` and Node's `path`, or JS's and TS's `react`,
		// wrote one `resource-collision:` row per pair into `info` on every scan and kept
		// whichever language merged first. Merging their edges decides the question the way
		// the rest of the code already had.
		if existing.Kind == domain.ResourceDependency && res.Kind == domain.ResourceDependency {
			dst.Resources[id] = mergeDependencyNode(existing, res)
			continue
		}
		// A genuine kind conflict is still worth reporting: two languages minting the same id
		// for different things is a modelling problem someone has to look at.
		dst.Errors["resource-collision:"+id] = fmt.Sprintf("resource id %s exists as a %s in %s and a %s in %s",
			id, existing.Kind, resourceLanguage(existing, dst.Language), res.Kind, resourceLanguage(res, src.Language))
	}
	for id, warning := range src.Warnings {
		dst.Warnings[id] = warning
	}
	for path, msg := range src.Errors {
		dst.Errors[path] = msg
	}
}

// mergeDependencyNode unions two languages' view of one external package.
//
// The node itself carries nothing that can conflict -- an id, a name, and whatever
// description a language happened to harvest -- so the merge keeps the first non-empty of
// each and unions the connection sets. Language is deliberately left on whichever side
// already had it: the tag says which scanner registered the package first, and nothing reads
// it as a claim of ownership.
func mergeDependencyNode(existing, incoming domain.Resource) domain.Resource {
	out := cloneResource(existing)
	if out.Name == "" {
		out.Name = incoming.Name
	}
	if out.Description == "" {
		out.Description = incoming.Description
	}
	if out.Language == "" {
		out.Language = incoming.Language
	}
	if out.Connections == nil {
		out.Connections = map[string][]string{}
	}
	for kind, targets := range incoming.Connections {
		seen := make(map[string]bool, len(out.Connections[kind]))
		for _, t := range out.Connections[kind] {
			seen[t] = true
		}
		for _, t := range targets {
			if !seen[t] {
				seen[t] = true
				out.Connections[kind] = append(out.Connections[kind], t)
			}
		}
	}
	for key, value := range incoming.Properties {
		if _, ok := out.Properties[key]; !ok {
			out.Properties[key] = value
		}
	}
	return out
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
