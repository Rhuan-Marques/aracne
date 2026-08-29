package topology

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"aracne/internal/helper"
)

// StaleIndexError reports that a resource's recorded location no longer fits the file that is
// on disk: the index describes a version of the source that is not the one present.
//
// WHY IT IS A TYPE AND NOT A STRING. A read that hits this is NOT a bad ID -- the resource is
// in the graph and the caller named it correctly; only its span is out of date, because the
// file changed without a re-scan (a checkout, a branch switch, a restored database, an edit
// made outside aracne). Reported as a bare "EndsAt 347 out of range (max 338)" it reads to a
// model exactly like "that symbol does not exist", and the recovery it picks is a whole
// exploration turn -- which is what a benchmarked flask instance did on all three of its
// seeds. Typed, the read path can re-index the one file and answer the question instead, and
// when it cannot the message says "not indexed" so a stale database is distinguishable from a
// wrong ID.
type StaleIndexError struct {
	// Path is the file whose index is stale. It is what a single-file re-index needs.
	Path string
	// StartsAt/EndsAt are the span the index recorded.
	StartsAt int
	EndsAt   int
	// Lines is how many lines the file actually has now.
	Lines int
}

// Error names the file, not the resource: the file is the unit that has to be re-indexed, and
// it is the same answer for every symbol declared in it.
func (e *StaleIndexError) Error() string {
	return fmt.Sprintf(
		"not indexed: %s is indexed at lines %d-%d but the file on disk has %d line(s) — "+
			"the topology is stale for this file (re-scan with `arac scan`)",
		e.Path, e.StartsAt, e.EndsAt, e.Lines)
}

// AsStaleIndex reports whether err is, or wraps, a stale-index error.
func AsStaleIndex(err error) (*StaleIndexError, bool) {
	var se *StaleIndexError
	if errors.As(err, &se) {
		return se, true
	}
	return nil, false
}

// IndexHealth is how far the stored topology has drifted from the source tree it describes.
//
// It is the same manifest diff an incremental scan starts from, exposed on its own so a miss
// can be attributed. Without it every dead end looks alike: "not found" is the answer both to
// a typo and to a database that was never told the file changed.
type IndexHealth struct {
	// Root is the topology root the diff ran against.
	Root string
	// Added are source files on disk with no manifest entry (never indexed).
	Added []string
	// Modified are indexed files whose mtime is newer than their manifest entry.
	Modified []string
	// Deleted are manifest entries with no file on disk.
	Deleted []string
}

// Stale reports whether anything at all has drifted.
func (h IndexHealth) Stale() bool {
	return len(h.Added)+len(h.Modified)+len(h.Deleted) > 0
}

// Drifted is the total number of files out of sync.
func (h IndexHealth) Drifted() int {
	return len(h.Added) + len(h.Modified) + len(h.Deleted)
}

// Summary is the one-line form, e.g. "3 file(s) out of sync (2 modified, 1 new)". Empty when
// the index is current, so a caller can print it unconditionally.
func (h IndexHealth) Summary() string {
	if !h.Stale() {
		return ""
	}
	parts := make([]string, 0, 3)
	if n := len(h.Modified); n > 0 {
		parts = append(parts, fmt.Sprintf("%d modified", n))
	}
	if n := len(h.Added); n > 0 {
		parts = append(parts, fmt.Sprintf("%d new", n))
	}
	if n := len(h.Deleted); n > 0 {
		parts = append(parts, fmt.Sprintf("%d deleted", n))
	}
	return fmt.Sprintf("%d file(s) out of sync (%s)", h.Drifted(), strings.Join(parts, ", "))
}

// Files lists every drifted path, relative to the root where possible, sorted.
func (h IndexHealth) Files() []string {
	out := make([]string, 0, h.Drifted())
	add := func(paths []string) {
		for _, p := range paths {
			if rel, err := filepath.Rel(h.Root, p); err == nil && !strings.HasPrefix(rel, "..") {
				p = filepath.ToSlash(rel)
			}
			out = append(out, p)
		}
	}
	add(h.Modified)
	add(h.Added)
	add(h.Deleted)
	sort.Strings(out)
	return out
}

// IndexHealth diffs the source tree against the file manifest, per indexed language, without
// touching the database. Pass root == "" to use the root recorded in the topology.
//
// It is deliberately the same primitive the watch loop and `arac check-updates` use
// (helper.DiffScanFiles), so "what the status command reports", "what a read blames a miss on"
// and "what an incremental scan would re-parse" can never disagree. A root that cannot be
// walked yields a zero IndexHealth and an error; callers treat that as "unknown", never as
// "healthy".
func (m *TopologyManager) IndexHealth(root string) (IndexHealth, error) {
	topo, err := helper.ReadDb(m.dbPath)
	if err != nil {
		return IndexHealth{}, err
	}
	if root == "" {
		root = topo.Root
	}
	if root == "" {
		root = "."
	}
	// Same filters the scan itself applies, so a hidden or ignored path is never reported as
	// drift the caller cannot act on.
	m.applyPathVisibility(root)

	languages := topo.Languages
	if len(languages) == 0 && topo.Language != "" {
		languages = []string{topo.Language}
	}

	health := IndexHealth{Root: root}
	manifestPath := helper.ManifestPath(m.dbPath)
	// One file can be claimed by two language scanners (a .ts file under both the JS and TS
	// walks); reporting it twice would inflate the drift count.
	seen := map[string]bool{}
	appendNew := func(dst *[]string, paths []string) {
		for _, p := range paths {
			if seen[p] {
				continue
			}
			seen[p] = true
			*dst = append(*dst, p)
		}
	}
	for _, lang := range languages {
		added, modified, deleted, diffErr := helper.DiffScanFiles(root, lang, manifestPath)
		if diffErr != nil {
			return IndexHealth{Root: root}, diffErr
		}
		appendNew(&health.Added, added)
		appendNew(&health.Modified, modified)
		appendNew(&health.Deleted, deleted)
	}
	sort.Strings(health.Added)
	sort.Strings(health.Modified)
	sort.Strings(health.Deleted)
	return health, nil
}
