package scanner

import (
	"errors"

	"aracne/internal/topology/domain"
)

// ErrPartialFallback is returned by a PartialUpdater.UpdateFilePartial when it
// cannot safely apply a scoped update and the caller must use the full path
// instead. It is a control signal, not a failure: the manager routes to the
// existing full ReadDb path when it sees it.
var ErrPartialFallback = errors.New("scanner: partial update not safe, fall back to full path")

// Defines the contract for scanning a codebase in a specific language. Requires methods to return the scanner name, supported file extensions, detect whether a root applies, perform a full scan returning a Topology, and incrementally update a single file.
type LanguageScanner interface {
	Name() string
	Extensions() []string
	Detect(root string) bool
	Scan(root string) (*domain.Topology, error)
	UpdateFile(topo *domain.Topology, path string) ([]domain.TopologyWarning, error)
}

// PartialUpdater is implemented by scanners that can update a single changed
// file WITHOUT loading the whole topology graph. Instead of mutating an
// in-memory full topology, the scanner loads only the working set it needs from
// the DB (via the helper partial readers) and returns a scoped delta:
//
//   - upserts: the changed file's new resources plus any neighbor resources
//     whose connections were mutated (callers, the owning package, matched
//     structs/interfaces, structs whose method set changed).
//   - deletes: old resource IDs of the changed file that no longer exist.
//   - warnings: the COMPLETE updated warnings map (the small warnings table is
//     rewritten wholesale from it), so existing add/clear logic is preserved.
//
// Manager only routes to this path when it is safe (no deleted files, and every
// changed file's scanner implements PartialUpdater); otherwise it falls back to
// the existing full UpdateFile path. Correctness over coverage.
type PartialUpdater interface {
	UpdateFilePartial(dbPath, root, absPath string) (upserts []domain.Resource, deletes []string, warnings map[string]domain.TopologyWarning, err error)
}
