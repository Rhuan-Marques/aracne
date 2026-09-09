//go:build audit

package universaltools

import (
	"path/filepath"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// A-09c: the same ".."-prefix test as cli.displayRoot and topogrep.displayPath, in the read
// tool's group header. displayPath's own doc says the relative form exists because "repeating
// a long absolute prefix above every group is pure overhead".
func TestAudit_DisplayPathHandlesADotDotPrefixedDirectory(t *testing.T) {
	root := "/srv/project"
	target := filepath.Join(root, "..data", "app.go")
	topo := &domain.Topology{Root: root}

	if got := displayPath(topo, target); filepath.IsAbs(got) {
		t.Errorf("displayPath(root=%q, %q) = %q, want the relative form %q",
			root, target, got, "..data/app.go")
	}
}
