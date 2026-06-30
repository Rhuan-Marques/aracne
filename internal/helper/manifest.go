package helper

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"aracne/internal/topology/domain"
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

// Serializes FileManifest to JSON and writes it to disk.
func WriteManifest(m FileManifest, path string) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// Synchronizes file manifest with current topology, updating timestamps and removing stale entries.
func SyncManifest(topo *domain.Topology, dbPath string) {
	manifestPath := ManifestPath(dbPath)
	manifest := ReadManifest(manifestPath)

	currentFiles := make(map[string]bool)
	for _, res := range topo.Resources {
		language := res.Language
		if language == "" {
			language = topo.Language
		}
		if res.Kind == domain.ResourceFile && IsSourceFile(res.ID, language) {
			currentFiles[res.ID] = true
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
			if isIgnoredSourceDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if IsSourceFile(path, language) {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

// Checks if a file path is a valid source file for a given language, excluding tests and ignored paths.
func IsSourceFile(path, language string) bool {
	if path == "" || isIgnoredSourcePath(path) {
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
		return isRustSourceName(name) && !inRustIgnoredDir(path)
	case "java":
		return isJavaSourceName(name) && !inJavaIgnoredDir(path)
	default:
		ext := filepath.Ext(name)
		return ext == ".go" || ext == ".py" || isJavaScriptSourceName(name) || isTypeScriptSourceName(name) ||
			(isRustSourceName(name) && !inRustIgnoredDir(path)) ||
			(isJavaSourceName(name) && !inJavaIgnoredDir(path))
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
func inJavaIgnoredDir(path string) bool {
	for _, part := range strings.FieldsFunc(filepath.Clean(path), func(r rune) bool {
		return r == '/' || r == '\\'
	}) {
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
func inRustIgnoredDir(path string) bool {
	for _, part := range strings.FieldsFunc(filepath.Clean(path), func(r rune) bool {
		return r == '/' || r == '\\'
	}) {
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

// Checks if a file path should be skipped by testing all path components against ignored directories.
func isIgnoredSourcePath(path string) bool {
	for _, part := range strings.FieldsFunc(filepath.Clean(path), func(r rune) bool {
		return r == '/' || r == '\\'
	}) {
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
		if !IsSourceFile(path, language) {
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
	if len(currentFiles) == 0 && len(manifestTimes) > 0 {
		return nil, nil, nil, fmt.Errorf("no %s source files found under root %s; refusing to mark %d manifest files deleted", language, root, len(manifestTimes))
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
func RemoveFileResources(topo *domain.Topology, fileID string) []domain.TopologyWarning {
	fileRes, ok := topo.Resources[fileID]
	if !ok {
		return nil
	}

	var warnings []domain.TopologyWarning

	toRemove := map[string]bool{fileID: true}
	for connType, targets := range fileRes.Connections {
		if ownedConnTypes[connType] {
			for _, target := range targets {
				toRemove[target] = true
			}
		}
	}

	for _, res := range topo.Resources {
		if toRemove[res.ID] {
			continue
		}
		for connType, targets := range res.Connections {
			for _, target := range targets {
				if toRemove[target] {
					warnID := res.ID + "@" + string(domain.WarnNodeRemoved) + "@" + target
					warnings = append(warnings, domain.TopologyWarning{
						ID:       warnID,
						SourceID: res.ID,
						Kind:     domain.WarnNodeRemoved,
						TargetID: target,
						Message:  fmt.Sprintf("%s was removed from %s, verify %s which references it via %s", target, fileID, res.ID, connType),
					})
				}
			}
		}
	}

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

// Removes warnings from topology for resources that no longer exist.
func CleanupOrphanedWarnings(topo *domain.Topology) {
	for id, w := range topo.Warnings {
		if _, ok := topo.Resources[w.SourceID]; !ok {
			delete(topo.Warnings, id)
		}
	}
}
