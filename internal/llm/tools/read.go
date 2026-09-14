package tools

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/idresolve"
)

// ResourceSource returns a resource's raw source text, with no topology context attached.
//
// This used to be the `read` MCP tool. It is no longer exposed to models -- the single
// context-aware `read` replaced it -- but two internal callers still want the plain cut:
// description generation (whose executor is asked to describe the code, and for which a
// "# CONTEXT:" tree is noise that skews the description) and the bug-solver prompt. Keeping
// them on the raw path also decouples them from the read tool's runtime rename.
func ResourceSource(mgr *topology.TopologyManager, resourceID string) (string, error) {
	if resourceID == "" {
		return "", fmt.Errorf("missing resource id")
	}

	id := helper.NormalizeResourceID(resourceID)

	// The topology may not be readable (e.g. not scanned yet); we still fall back to reading
	// the input as a raw file below.
	root := ""
	topo, topoErr := mgr.ReadAll()
	if topoErr == nil {
		root = topo.Root
	}

	candidates := readPathCandidates(id, root)

	if topoErr == nil {
		for _, cand := range candidates {
			res, ok := topo.Resources[cand]
			if !ok {
				continue
			}
			entry, err := mgr.Cut(res.Location)
			if err != nil {
				return "", fmt.Errorf("read resource: %w", err)
			}
			return fmt.Sprintf("%s\n%s", filepath.Base(res.Location.Path), entry.Cut), nil
		}
	}

	// Not a known topology resource: read the input as a raw text file. Try each candidate so
	// a relative path resolves against both the working directory and the topology root.
	maxSize := helper.LoadConfig(helper.ConfigPath(mgr.DbPath())).EffectiveMaxFileSize()
	for _, cand := range candidates {
		if info, err := os.Stat(cand); err == nil && !info.IsDir() {
			return helper.ReadRawFile(cand, maxSize)
		}
	}

	// Neither an exact resource nor a file on disk. Before giving up, let the shared resolver
	// try: it tolerates a wrong root prefix and the wrong separator convention, and follows
	// the alias table for IDs minted under a previous id-scheme. This tier is LAST because the
	// raw-file fallback is a legitimate answer that must not be pre-empted by a fuzzy match.
	if topoErr == nil {
		res := idresolve.Resolve(topo, resourceID, idresolve.Options{
			Alias: func(old string) (string, bool) {
				return helper.ResolveAlias(mgr.DbPath(), old)
			},
		})
		if res.Found() {
			entry, err := mgr.Cut(res.Resource.Location)
			if err != nil {
				return "", fmt.Errorf("read resource: %w", err)
			}
			return fmt.Sprintf("%s\n%s", filepath.Base(res.Resource.Location.Path), entry.Cut), nil
		}
		if hint := idresolve.FormatCandidates(resourceID, res.Candidates); hint != "" {
			return "", fmt.Errorf("resource %q not found in topology. %s", resourceID, hint)
		}
	}

	return "", fmt.Errorf("resource %q not found in topology", resourceID)
}

// readPathCandidates returns the input followed by alternative path forms to try when
// resolving a file: its absolute form, its form joined onto the topology root, and the
// canonical (symlink-resolved) form of each.
//
// The list itself lives in helper.PathCandidates. It was written out here and again in
// universaltools, and the two copies are how the read side came to be one spelling short of
// the write side: the scan canonicalizes, these did not.
func readPathCandidates(id, root string) []string {
	return helper.PathCandidates(id, root)
}
