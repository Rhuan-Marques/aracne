package topology

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// PROJECT MANIFESTS: the files outside the source tree whose content decides what a scan
// produces for EVERY file.
//
// The Go module path and the Cargo package name root every resource id, and Cargo, Maven and
// Gradle dependency lists decide which imports are external. An incremental scan diffs source
// files only, so renaming the Cargo package from `q` to `renamed` re-rooted just the files that
// happened to be re-parsed: the graph held `renamed::b::fb` beside `q::a::fa`, and the call
// between them was lost until `scan --all`. A change to one of these files is therefore a
// change to the whole project, and is answered with the full rescan that implies -- which
// carries descriptions and bugs across by identity, so the re-rooted ids lose nothing.
//
// Cheap enough to run before every tool call. The files are found once, by a full scan, from
// the directories that hold indexed files, and recorded beside the database; an incremental
// scan only stats what was recorded, and reads a file only when its size or mtime moved. The
// content compared is only the part that matters, so `go get` rewriting go.mod's requirements
// -- which change no id -- costs nothing. package.json, tsconfig.json and pyproject.toml are
// not read by any scanner, so they are not tracked.
var projectManifestNames = []string{"go.mod", "Cargo.toml", "pom.xml", "build.gradle", "build.gradle.kts"}

// projectManifestStamp is one recorded manifest. An absent file is recorded too (Size -1), so
// one created later -- a Cargo.toml added at the root -- reads as a change.
type projectManifestStamp struct {
	Size    int64  `json:"size"`
	ModTime string `json:"mtime"`
	Hash    string `json:"hash"`
}

func projectManifestsPath(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), "project_manifests.json")
}

// stampProjectManifest fingerprints one manifest as it is on disk now.
func stampProjectManifest(path string) projectManifestStamp {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return projectManifestStamp{Size: -1}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return projectManifestStamp{Size: -1}
	}
	sum := sha256.Sum256(manifestContent(filepath.Base(path), data))
	return projectManifestStamp{
		Size:    info.Size(),
		ModTime: info.ModTime().UTC().Format(time.RFC3339Nano),
		Hash:    hex.EncodeToString(sum[:]),
	}
}

// manifestContent is the part of a manifest a scan reads. For go.mod that is the module
// directive alone: goscanner takes nothing else from it.
func manifestContent(name string, data []byte) []byte {
	if name != "go.mod" {
		return data
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "module") {
			return []byte(line)
		}
	}
	return nil
}

// discoverProjectManifests returns every manifest a scan of root could have read: the ones at
// the root, whether they exist or not, and the ones in any directory between an indexed file
// and the root.
func discoverProjectManifests(root string, files []string) []string {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	visit := func(dir string, always bool) {
		for _, name := range projectManifestNames {
			path := filepath.Join(dir, name)
			if seen[path] {
				continue
			}
			if _, err := os.Stat(path); err == nil || always {
				seen[path] = true
				out = append(out, path)
			}
		}
	}
	visit(absRoot, true)
	visitedDirs := map[string]bool{absRoot: true}
	for _, f := range files {
		for dir := filepath.Dir(f); ; dir = filepath.Dir(dir) {
			rel, err := filepath.Rel(absRoot, dir)
			if err != nil || !domain.RelInside(rel) || visitedDirs[dir] {
				break
			}
			visitedDirs[dir] = true
			visit(dir, false)
		}
	}
	sort.Strings(out)
	return out
}

// recordProjectManifests stamps the manifests a scan of root depends on. files are the indexed
// source files; nil reads them from the file manifest.
func (m *TopologyManager) recordProjectManifests(root string, files []string) {
	if m.dbPath == "" {
		return
	}
	if files == nil {
		for path := range helper.ReadManifest(helper.ManifestPath(m.dbPath)) {
			files = append(files, path)
		}
	}
	stamps := map[string]projectManifestStamp{}
	for _, path := range discoverProjectManifests(root, files) {
		stamps[path] = stampProjectManifest(path)
	}
	writeProjectManifests(m.dbPath, stamps)
}

func writeProjectManifests(dbPath string, stamps map[string]projectManifestStamp) {
	data, err := json.MarshalIndent(stamps, "", "  ")
	if err != nil {
		return
	}
	_ = helper.AtomicWriteFile(projectManifestsPath(dbPath), data, 0644)
}

// fileIDs lists a topology's file nodes.
func fileIDs(topo *domain.Topology) []string {
	var out []string
	for id, res := range topo.Resources {
		if res.Kind == domain.ResourceFile {
			out = append(out, id)
		}
	}
	return out
}

// projectManifestsChanged reports whether a recorded manifest differs from disk.
//
// A database with no record yet -- written by an older build -- gets one now and reports no
// change: there is nothing to compare against, and a full rescan on the strength of that would
// be one more on the first scan of every upgraded project for no reason. A file whose size and
// mtime moved but whose relevant content did not (a `touch`, a checkout of the same bytes, a
// go.mod whose requirements changed) is re-stamped, so it is not read again on the next call.
func (m *TopologyManager) projectManifestsChanged(root string) bool {
	if m.dbPath == "" {
		return false
	}
	data, err := os.ReadFile(projectManifestsPath(m.dbPath))
	var recorded map[string]projectManifestStamp
	if err != nil || json.Unmarshal(data, &recorded) != nil || recorded == nil {
		m.recordProjectManifests(root, nil)
		return false
	}
	restamped := false
	for path, was := range recorded {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			if was.Size >= 0 {
				return true
			}
			continue
		}
		if was.Size == info.Size() && was.ModTime == info.ModTime().UTC().Format(time.RFC3339Nano) {
			continue
		}
		now := stampProjectManifest(path)
		if now.Hash != was.Hash {
			return true
		}
		recorded[path] = now
		restamped = true
	}
	if restamped {
		writeProjectManifests(m.dbPath, recorded)
	}
	return false
}
