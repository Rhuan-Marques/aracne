package universaltools

import (
	"path/filepath"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// See domain.RelInside: a `..data` directory is inside the root, so the group header names it
// relatively rather than repeating the absolute path.
func TestDisplayPathKeepsADotDotNamedDirectoryRelative(t *testing.T) {
	root := filepath.FromSlash("/srv/project")
	topo := &domain.Topology{Root: root}
	if got, want := displayPath(topo, filepath.Join(root, "..data", "app.go")), "..data/app.go"; got != want {
		t.Errorf("displayPath = %q, want %q", got, want)
	}
	if got, want := displayPath(topo, filepath.FromSlash("/srv/other/app.go")), filepath.FromSlash("/srv/other/app.go"); got != want {
		t.Errorf("an outside path must stay absolute: %q", got)
	}
}
