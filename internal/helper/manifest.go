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
		if res.Kind == domain.ResourceFile && IsSourceFile(res.ID, topo.Language) {
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

func CollectSourceFiles(root, language string) []string {
	var files []string
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return files
	}
	filepath.WalkDir(absRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
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
	return files
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

func DiffScanFiles(root, language, manifestPath string) (added, modified, deleted []string) {
	manifest := ReadManifest(manifestPath)

	manifestTimes := make(map[string]time.Time)
	for path, ts := range manifest {
		t, err := time.Parse(time.RFC3339Nano, ts)
		if err == nil {
			manifestTimes[path] = t
		}
	}

	currentFiles := CollectSourceFiles(root, language)
	currentSet := make(map[string]bool, len(currentFiles))
	for _, f := range currentFiles {
		currentSet[f] = true
		t, inManifest := manifestTimes[f]
		if !inManifest {
			added = append(added, f)
		} else {
			fi, err := os.Stat(f)
			if err == nil && fi.ModTime().UTC().After(t) {
				modified = append(modified, f)
			}
		}
	}

	for path := range manifestTimes {
		if !currentSet[path] {
			deleted = append(deleted, path)
		}
	}

	return
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
