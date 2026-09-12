package topology

import (
	"os"
	"sync"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/helper"
)

// manifestCache holds the last parsed file manifest per manifest path, keyed by the manifest's
// own mtime and size.
//
// StaleFiles runs before every read, and the manifest can be tens of megabytes on a large
// project. Re-parsing it per read would put a JSON decode of the whole tree on the hot path of
// a long-lived `arac serve`; one stat of the manifest tells us whether the copy we already have
// is still the one on disk. WriteManifest replaces the file by rename, so any rewrite moves
// its mtime.
var manifestCache sync.Map // map[string]cachedManifest

type cachedManifest struct {
	modTime time.Time
	size    int64
	entries helper.FileManifest
}

// recordedManifest returns the manifest this topology's scans wrote, or nil when there is none.
func (m *TopologyManager) recordedManifest() helper.FileManifest {
	path := helper.ManifestPath(m.dbPath)
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	if cached, ok := manifestCache.Load(path); ok {
		c := cached.(cachedManifest)
		if c.modTime.Equal(info.ModTime()) && c.size == info.Size() {
			return c.entries
		}
	}
	entries := helper.ReadManifest(path)
	manifestCache.Store(path, cachedManifest{modTime: info.ModTime(), size: info.Size(), entries: entries})
	return entries
}

// StaleFiles returns the files among paths that changed on disk after the index last recorded
// them: an mtime other than their manifest stamp (older counts too), or deleted.
//
// WHY A READ NEEDS THIS. Every span a read cuts comes from the index, and Cut can only notice
// drift that runs past the end of the file (StaleIndexError). A file edited outside the session
// -- an editor save, a checkout, another agent -- whose old spans still fit is otherwise served
// as-is: three comment lines inserted at the top of a Go file turned a method read into the
// tail of the type above it, with nothing in the output to say so.
//
// It costs one stat per path. The rule is the one an incremental scan applies
// (helper.MtimeChanged: on-disk mtime differs from the recorded one), so what a read refreshes and
// what `arac scan` would re-parse can never disagree. A path the manifest has no entry for is
// not judged -- a dependency, a file outside the scan, a topology written without a manifest --
// because there is nothing recorded to compare it against.
func (m *TopologyManager) StaleFiles(paths []string) []string {
	if m.dbPath == "" || len(paths) == 0 {
		return nil
	}
	manifest := m.recordedManifest()
	if len(manifest) == 0 {
		return nil
	}
	var stale []string
	seen := make(map[string]bool, len(paths))
	for _, path := range paths {
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		stamp, ok := manifest[path]
		if !ok {
			continue
		}
		recorded, err := time.Parse(time.RFC3339Nano, stamp)
		if err != nil {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			// Gone from disk: re-indexing it is what removes its resources from the graph.
			if os.IsNotExist(err) {
				stale = append(stale, path)
			}
			continue
		}
		if helper.MtimeChanged(info.ModTime(), recorded) {
			stale = append(stale, path)
		}
	}
	return stale
}
