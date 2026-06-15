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

func ManifestPath(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), "file_manifest.json")
}

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

func WriteManifest(m FileManifest, path string) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

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
	default:
		ext := filepath.Ext(name)
		return ext == ".go" || ext == ".py"
	}
}

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

func isIgnoredSourceDir(name string) bool {
	return name == "vendor" || name == ".git" || name == "node_modules" ||
		name == "__pycache__" || name == ".pytest_cache" ||
		name == "venv" || name == ".venv" || name == "env" ||
		strings.HasPrefix(name, ".")
}

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
						SourceID: target,
						Kind:     domain.WarnNodeRemoved,
						TargetID: res.ID,
						Message:  fmt.Sprintf("%s was removed from %s, verify %s %s", target, fileID, connType, res.ID),
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

func CleanupOrphanedWarnings(topo *domain.Topology) {
	for id, w := range topo.Warnings {
		if _, ok := topo.Resources[w.SourceID]; !ok {
			delete(topo.Warnings, id)
		}
	}
}
