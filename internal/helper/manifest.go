package helper

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

type FileManifest map[string]string

var ownedConnTypes = map[string]bool{
	"has_function":   true,
	"has_struct":     true,
	"has_interface":  true,
	"has_named_type": true,
	"has_extvar":     true,
	"has_class":      true,
}

// Returns the file_manifest.json path given a database path.
func ManifestPath(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), "file_manifest.json")
}

// Reads a file manifest from JSON, returning an empty manifest if the file is missing or invalid.
func ReadManifest(path string) FileManifest {
	data, err := os.ReadFile(path)
	if err != nil {
		return make(FileManifest)
	}
	var m FileManifest
	if json.Unmarshal(data, &m) != nil {
		return make(FileManifest)
	}
	if m == nil {
		m = make(FileManifest)
	}
	return m
}

// WriteManifest serializes a FileManifest to JSON and replaces the file at path with it,
// atomically: the JSON goes to a temp file in the same directory and is renamed over the
// destination, so a reader either sees the whole old manifest or the whole new one.
//
// WHY ATOMIC. os.WriteFile truncates first and writes second, and this manifest can be tens of
// megabytes on a large project -- a wide enough window that any interruption in between (a
// Ctrl-C, a hook whose timeout fires and takes the process with it, an OOM kill) leaves a
// ZERO-BYTE manifest. That state is silent and self-perpetuating: ReadManifest treats an
// unparseable file as an empty one, so every later incremental scan sees an empty baseline,
// re-parses the entire project, notices nothing was deleted, and takes long enough to be
// interrupted again. This repo's own .aracne sat in exactly that state -- an empty manifest
// beside a database that had grown to 2.5 GB because no scan ever completed to prune it.
//
// A failed rename leaves the previous manifest in place, which is the safe outcome: a stale
// baseline costs one re-parse of the files it missed, an empty one costs a full scan forever.
func WriteManifest(m FileManifest, path string) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return AtomicWriteFile(path, data, 0644)
}

// SyncManifest stamps the manifest for the files this scan covered and drops the entries for
// files it no longer holds.
//
// `attempted` is the set of paths the scan TRIED to parse, whether or not each produced a
// node, and it is why this takes an argument at all. The kept set used to be derived purely
// from the topology's file nodes -- so a file the scanner could not parse produced no node,
// was never stamped, and DiffScanFiles reported it as `added` on the next run, and the run
// after that, forever. Every incremental scan then re-parsed it: on this repository that was
// a python3 subprocess per scan, which under the default `scan.pre_tool` is a subprocess in
// front of every tool call. Retrying a file that has not changed on disk cannot succeed, so
// the mtime is the right thing to trust; the parse error is recorded in topo.Errors, which is
// where a reader should learn about it.
//
// A path that no longer exists must not be passed here: the stamp would resurrect a manifest
// entry the deletion sweep below exists to remove.
func SyncManifest(topo *domain.Topology, dbPath string, attempted []string) {
	manifestPath := ManifestPath(dbPath)
	manifest := ReadManifest(manifestPath)

	currentFiles := make(map[string]bool)
	for _, res := range topo.Resources {
		language := res.Language
		if language == "" {
			language = topo.Language
		}
		if res.Kind == domain.ResourceFile && IsSourceFile(topo.Root, res.ID, language) {
			currentFiles[res.ID] = true
		}
	}
	for _, path := range attempted {
		abs, err := filepath.Abs(path)
		if err != nil {
			abs = path
		}
		if info, statErr := os.Stat(abs); statErr == nil && info.Mode().IsRegular() {
			currentFiles[abs] = true
		}
	}

	for path := range currentFiles {
		if fi, err := os.Stat(path); err == nil {
			manifest[path] = fi.ModTime().UTC().Format(time.RFC3339Nano)
		} else {
			manifest[path] = time.Now().UTC().Format(time.RFC3339Nano)
		}
	}

	for path := range manifest {
		if !currentFiles[path] {
			delete(manifest, path)
		}
	}

	if err := WriteManifest(manifest, manifestPath); err != nil {
		os.Stderr.WriteString("Warning: failed to write file manifest: " + err.Error() + "\n")
	}
}

// SyncManifestFiles updates the manifest entries for only the given file paths
// (the partial-path equivalent of SyncManifest, which enumerates every file in a
// full topology). Each path is stamped with its on-disk mtime so the next
// IncrementalScan diff sees it as unchanged. Paths that no longer exist on disk
// should not reach here (the partial path only handles added/modified files), so
// no deletion sweep is performed.
func SyncManifestFiles(dbPath string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	manifestPath := ManifestPath(dbPath)
	manifest := ReadManifest(manifestPath)
	for _, path := range paths {
		abs, err := filepath.Abs(path)
		if err != nil {
			abs = path
		}
		if fi, statErr := os.Stat(abs); statErr == nil {
			manifest[abs] = fi.ModTime().UTC().Format(time.RFC3339Nano)
		} else {
			manifest[abs] = time.Now().UTC().Format(time.RFC3339Nano)
		}
	}
	return WriteManifest(manifest, manifestPath)
}

// ForgetManifestFiles drops the manifest entries for the given paths: the scoped counterpart
// of the deletion sweep SyncManifest performs over a whole topology.
//
// Needed by the single-file update path, which must not leave an entry behind for a file it
// just removed from the graph (deleted, hidden by config, or no longer a source file). A stale
// entry there makes the next incremental scan report the file as deleted again, forever.
func ForgetManifestFiles(dbPath string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	manifestPath := ManifestPath(dbPath)
	manifest := ReadManifest(manifestPath)
	changed := false
	for _, path := range paths {
		abs, err := filepath.Abs(path)
		if err != nil {
			abs = path
		}
		for _, key := range []string{abs, path} {
			if _, ok := manifest[key]; ok {
				delete(manifest, key)
				changed = true
			}
		}
	}
	if !changed {
		return nil
	}
	return WriteManifest(manifest, manifestPath)
}

// Recursively collects source files of a given language from a directory, skipping ignored directories.
func CollectSourceFiles(root, language string) ([]string, error) {
	var files []string
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(absRoot)
	if err != nil {
		return nil, fmt.Errorf("access root %s: %w", absRoot, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("root is not a directory: %s", absRoot)
	}
	err = filepath.WalkDir(absRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// The root itself is never pruned by its own basename: WalkDir does
			// not visit ancestors, so a repo that simply lives at ~/.dotfiles
			// must still scan.
			if path == absRoot {
				return nil
			}
			if isIgnoredSourceDir(d.Name()) {
				return filepath.SkipDir
			}
			if domain.PathPruneDir(path) {
				return filepath.SkipDir
			}
			return nil
		}
		if IsSourceFile(absRoot, path, language) {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

// Checks if a file path is a valid source file for a given language, excluding
// tests and ignored paths.
//
// root is the topology root. Ignore rules are applied to the path RELATIVE to
// it: a checkout that happens to live under a hidden or vendor-named ancestor
// (~/.claude/scratch/app, /home/runner/.cache/x, /srv/build/app) must scan
// normally. An empty root falls back to examining the whole path.
func IsSourceFile(root, path, language string) bool {
	if path == "" || isIgnoredSourcePath(root, path) {
		return false
	}
	// Paths marked hidden by config are excluded from both the indexing stage
	// (this gate feeds manifest diffing / file discovery) and the scan stage.
	if domain.PathHidden(path) {
		return false
	}
	name := filepath.Base(path)
	switch language {
	case "go":
		return strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
	case "python":
		return strings.HasSuffix(name, ".py") && !strings.HasPrefix(name, "test_")
	case "javascript":
		return isJavaScriptSourceName(name)
	case "typescript":
		return isTypeScriptSourceName(name)
	case "rust":
		return isRustSourceName(name) && !inRustIgnoredDir(root, path)
	case "java":
		return isJavaSourceName(name) && !inJavaIgnoredDir(root, path)
	default:
		ext := filepath.Ext(name)
		return ext == ".go" || ext == ".py" || isJavaScriptSourceName(name) || isTypeScriptSourceName(name) ||
			(isRustSourceName(name) && !inRustIgnoredDir(root, path)) ||
			(isJavaSourceName(name) && !inJavaIgnoredDir(root, path))
	}
}

// isJavaSourceName reports whether name is a Java source file (a .java file),
// excluding the common test-file suffixes (*Test.java, *Tests.java, *IT.java).
func isJavaSourceName(name string) bool {
	return strings.HasSuffix(name, ".java") &&
		!strings.HasSuffix(name, "Test.java") && !strings.HasSuffix(name, "Tests.java") && !strings.HasSuffix(name, "IT.java")
}

// inJavaIgnoredDir reports whether a path lives under a Java directory we skip:
// build output (target, build, out, bin, .gradle) and the test-source dirs
// (test, tests), which are not part of the library/application topology.
func inJavaIgnoredDir(root, path string) bool {
	for _, part := range pathComponents(relPath(root, path)) {
		switch part {
		case "target", "build", ".gradle", "out", "bin", "test", "tests":
			return true
		}
	}
	return false
}

// isRustSourceName reports whether name is a Rust source file (a .rs file).
func isRustSourceName(name string) bool {
	return filepath.Ext(name) == ".rs"
}

// inRustIgnoredDir reports whether a path lives under a Rust directory we skip:
// build output (target) and the integration-test/bench dirs (tests, benches),
// which are not part of the library/binary topology. Scoped to Rust so other
// languages' tests/ dirs are unaffected.
func inRustIgnoredDir(root, path string) bool {
	for _, part := range pathComponents(relPath(root, path)) {
		if part == "target" || part == "tests" || part == "benches" {
			return true
		}
	}
	return false
}

// isTypeScriptSourceName reports whether name is a TypeScript/TSX source file we should
// parse, excluding test files (*.test.*, *.spec.*) and minified bundles (*.min.*).
// Accepted extensions are .ts, .tsx, .mts, .cts (and therefore .d.ts).
func isTypeScriptSourceName(name string) bool {
	ext := filepath.Ext(name)
	switch ext {
	case ".ts", ".tsx", ".mts", ".cts":
	default:
		return false
	}
	base := strings.ToLower(strings.TrimSuffix(name, ext))
	if strings.HasSuffix(base, ".test") || strings.HasSuffix(base, ".spec") || strings.HasSuffix(base, ".min") {
		return false
	}
	return true
}

// isJavaScriptSourceName reports whether name is a JavaScript/JSX source file we
// should parse, excluding test files (*.test.*, *.spec.*) and minified bundles
// (*.min.*). The accepted extensions are .js, .mjs, .cjs, and .jsx.
func isJavaScriptSourceName(name string) bool {
	ext := filepath.Ext(name)
	switch ext {
	case ".js", ".mjs", ".cjs", ".jsx":
	default:
		return false
	}
	base := strings.ToLower(strings.TrimSuffix(name, ext))
	if strings.HasSuffix(base, ".test") || strings.HasSuffix(base, ".spec") || strings.HasSuffix(base, ".min") {
		return false
	}
	return true
}

// pathComponents splits a path on both separators.
func pathComponents(path string) []string {
	return strings.FieldsFunc(filepath.Clean(path), func(r rune) bool {
		return r == '/' || r == '\\'
	})
}

// isIgnoredSourcePath reports whether a path lies under a directory we never
// index. Only the components INSIDE root are examined.
//
// This used to split the absolute path, so a repo under a hidden or
// vendor-named ancestor matched on its own ancestry: a project at
// ~/.claude/scratch/app scanned to zero files, silently, and every later edit
// treated its files as non-source and deleted their resources from the graph.
// relPath returns the cleaned input when the path is already relative or lies
// outside root, which preserves the old behaviour for callers with no root.
func isIgnoredSourcePath(root, path string) bool {
	for _, part := range pathComponents(relPath(root, path)) {
		if isIgnoredSourceDir(part) {
			return true
		}
	}
	return false
}

// Checks if a directory name should be skipped during source scanning (vendor, .git, node_modules, etc.).
func isIgnoredSourceDir(name string) bool {
	return name == "vendor" || name == ".git" || name == "node_modules" ||
		name == "__pycache__" || name == ".pytest_cache" ||
		name == "venv" || name == ".venv" || name == "env" ||
		strings.HasPrefix(name, ".")
}

// Compares source files in a directory against a manifest to identify added, modified, and deleted files by language.
func DiffScanFiles(root, language, manifestPath string) (added, modified, deleted []string, err error) {
	manifest := ReadManifest(manifestPath)

	manifestTimes := make(map[string]time.Time)
	for path, ts := range manifest {
		if !IsSourceFile(root, path, language) {
			continue
		}
		normalizedPath := normalizeManifestPath(root, path)
		t, parseErr := time.Parse(time.RFC3339Nano, ts)
		if parseErr == nil {
			manifestTimes[normalizedPath] = t
		}
	}

	currentFiles, err := CollectSourceFiles(root, language)
	if err != nil {
		return nil, nil, nil, err
	}
	// THE MASS-DELETION GUARD, AND THE CASE IT MUST NOT REFUSE.
	//
	// A walk that returns nothing while the manifest holds files is usually a walk that went
	// wrong -- a config change that hid a tree, a new ignore rule, a scan rooted somewhere
	// unexpected -- and marking every one of those files deleted would take the language out
	// of the graph. That is what this refuses.
	//
	// But it also refused the honest case: remove the last .py file from a mixed repo and the
	// diff errored instead of reporting the deletion, permanently, because nothing ever cleared
	// the manifest entry that triggered it.
	//
	// The two are told apart by asking whether the files are still THERE. A file the walk
	// missed still stats; a file that was deleted does not. So the refusal now stands only
	// while some manifest file survives on disk, and a genuine last-file deletion is processed.
	if len(currentFiles) == 0 && len(manifestTimes) > 0 {
		if survivor, ok := anyPathExists(manifestTimes); ok {
			return nil, nil, nil, fmt.Errorf(
				"no %s source files found under root %s, but %s is still on disk; "+
					"refusing to mark %d manifest files deleted",
				language, root, survivor, len(manifestTimes))
		}
	}

	currentSet := make(map[string]bool, len(currentFiles))
	for _, f := range currentFiles {
		currentSet[f] = true
		t, inManifest := manifestTimes[f]
		if !inManifest {
			added = append(added, f)
		} else {
			fi, statErr := os.Stat(f)
			if statErr == nil && fi.ModTime().UTC().After(t) {
				modified = append(modified, f)
			}
		}
	}

	for path := range manifestTimes {
		if !currentSet[path] {
			deleted = append(deleted, path)
		}
	}

	return added, modified, deleted, nil
}

// anyPathExists reports the first manifest path that is still a file on disk, in sorted order
// so the message names the same one every run. It is what separates "the walk missed these"
// from "these are gone"; see DiffScanFiles.
func anyPathExists(paths map[string]time.Time) (string, bool) {
	keys := make([]string, 0, len(paths))
	for p := range paths {
		keys = append(keys, p)
	}
	sort.Strings(keys)
	for _, p := range keys {
		if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() {
			return p, true
		}
	}
	return "", false
}

// Resolves a manifest path to absolute form, with fallback to WSL path conversion on Windows.
func normalizeManifestPath(root, path string) string {
	if _, err := os.Stat(path); err == nil {
		if abs, absErr := filepath.Abs(path); absErr == nil {
			return abs
		}
		return path
	}

	converted := windowsPathToWSL(path)
	if converted != path {
		if _, err := os.Stat(converted); err == nil {
			return converted
		}
	}

	return path
}

// Converts Windows absolute paths (C:\...) to WSL-compatible paths (/mnt/c/...).
func windowsPathToWSL(path string) string {
	if len(path) < 3 || path[1] != ':' || (path[2] != '\\' && path[2] != '/') {
		return path
	}
	drive := path[0]
	if drive >= 'A' && drive <= 'Z' {
		drive = drive - 'A' + 'a'
	}
	if drive < 'a' || drive > 'z' {
		return path
	}
	rest := strings.ReplaceAll(path[3:], "\\", "/")
	return filepath.Join("/mnt", string(drive), rest)
}

// Removes a file and its owned resources from the topology, cleaning up all references and returning warnings for resources that now reference deleted nodes.
//
// A SYMBOL THAT HAS MOVED IS NOT THIS FILE'S TO DELETE, and that is the whole of the rename
// case. IncrementalScan registers every added and modified file BEFORE it removes the deleted
// ones, so by the time this runs a symbol that lives in another file now has already had its
// Location rewritten there. Deleting by id alone therefore took the declarations the new file
// had just re-registered: `mv pkg/a.go pkg/b.go` left the b.go file node with no members, the
// package with no functions, and `arac read One` answering "not found" until someone ran
// `arac scan --all`. In Go a resource id is `module/package.Symbol` and carries no filename, so
// a rename inside a package collides on every id in the file; the same applies to Java's FQNs.
// Renaming the project directory is this at whole-graph scale.
//
// The test is ownership, not rename detection -- which is the wrong question, and a harder one.
// "Did a.go become b.go?" needs content hashing (defeated by the rename-plus-edit that `git mv`
// usually is) or an id-set overlap threshold, and it answers per FILE where the graph needs an
// answer per SYMBOL: a rename that also moves two of five declarations to a third file has a
// different answer for each of them. "Does this resource still say it lives here?" is a fact the
// graph already holds, and it is right in every one of those shapes.
//
// The file node itself is never subject to the test: a file resource carries an empty
// Location.Path because its identity IS its path, and it really is gone.
func RemoveFileResources(topo *domain.Topology, fileID string) []domain.TopologyWarning {
	fileRes, ok := topo.Resources[fileID]
	if !ok {
		return nil
	}

	toRemove := map[string]bool{fileID: true}
	for connType, targets := range fileRes.Connections {
		if ownedConnTypes[connType] {
			for _, target := range targets {
				if movedOutOfFile(topo, target, fileID) {
					continue
				}
				toRemove[target] = true
			}
		}
	}

	// Whole-file removal counts every edge kind as a reference, and skips no
	// referrer: nothing in this update was re-parsed from source.
	warnings := ScanReferrers(topo, ReferrerScan{Removed: toRemove, Origin: fileID})

	for resID := range toRemove {
		delete(topo.Resources, resID)
	}

	for id, res := range topo.Resources {
		for connType, targets := range res.Connections {
			var kept []string
			for _, t := range targets {
				if !toRemove[t] {
					kept = append(kept, t)
				}
			}
			if len(kept) > 0 {
				res.Connections[connType] = kept
			} else {
				delete(res.Connections, connType)
			}
		}
		topo.Resources[id] = res
	}

	return warnings
}

// movedOutOfFile reports whether a resource the removed file used to own now lives somewhere
// else, which is what makes it survive the removal. See RemoveFileResources.
//
// An id that is no longer in the graph at all reports false: there is nothing to keep, and
// leaving it in the removal set is what strips the stale edges still pointing at it.
// A resource with no recorded path reports false too -- absence of evidence is not a move, and
// the conservative answer here is the old behaviour.
func movedOutOfFile(topo *domain.Topology, id, fileID string) bool {
	res, ok := topo.Resources[id]
	if !ok || res.Location.Path == "" {
		return false
	}
	return filepath.Clean(res.Location.Path) != filepath.Clean(fileID)
}

// Removes warnings from topology for resources that no longer exist.
//
// A signature_changed warning is also dropped when its TargetID -- the caller it
// asks you to go verify, see ExpandSignatureWarnings -- is gone, because there
// is nothing left to check. The other two kinds carry a TargetID that names a
// symbol which is missing on purpose, so only their SourceID is tested.
func CleanupOrphanedWarnings(topo *domain.Topology) {
	for id, w := range topo.Warnings {
		if _, ok := topo.Resources[w.SourceID]; !ok {
			delete(topo.Warnings, id)
			continue
		}
		if w.Kind != domain.WarnSignatureChanged || w.TargetID == "" {
			continue
		}
		if _, ok := topo.Resources[w.TargetID]; !ok {
			delete(topo.Warnings, id)
		}
	}
}

// CleanupOrphanedWarningsScoped is the partial path's counterpart to
// CleanupOrphanedWarnings, which cannot run there. On a working set an id that
// is absent from topo.Resources only means "not loaded", so the whole-graph
// test would delete nearly every warning in the table. This drops exactly the
// ones the delta is known to have orphaned: those whose SourceID, or whose
// signature_changed TargetID caller, is among the ids being deleted.
//
// The partial path needs this because WriteScopedResources rewrites the whole
// warnings table from the map it is handed, so anything left in the map is
// re-inserted. referrerPass has no safe scoped form either -- it must sweep
// every resource in the repo to find surviving referrers -- and is deliberately
// not mirrored here; the fast path falls back to the full path whenever a
// change could need it.
func CleanupOrphanedWarningsScoped(warnings map[string]domain.TopologyWarning, deleted []string) {
	if len(warnings) == 0 || len(deleted) == 0 {
		return
	}
	gone := make(map[string]bool, len(deleted))
	for _, id := range deleted {
		gone[id] = true
	}
	for id, w := range warnings {
		if gone[w.SourceID] {
			delete(warnings, id)
			continue
		}
		if w.Kind == domain.WarnSignatureChanged && w.TargetID != "" && gone[w.TargetID] {
			delete(warnings, id)
		}
	}
}
