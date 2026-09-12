package universaltools_test

// RD-9, TypeScript half: a TS function read listed the interfaces and type aliases it uses
// without consulting read.context_filter, so under "normal" an undescribed one printed as
// "## id: no description" -- the one neighbour kind that filter never hid. Go's function
// context already filters its used interfaces.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner/jsscanner"
)

const tsContextSource = `export interface Shape {
  area(): number;
}

export type Unit = "cm" | "in";

export function measure(s: Shape, u: Unit): number {
  return s.area();
}
`

func scanTS(t *testing.T) (*topology.TopologyManager, *scanner.Registry) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{"package.json": "{}\n", "geo.ts": tsContextSource} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, ".aracne"), 0o755); err != nil {
		t.Fatal(err)
	}
	mgr := topology.New()
	if err := mgr.Load(filepath.Join(dir, ".aracne", "topology.db")); err != nil {
		t.Fatal(err)
	}
	reg := scanner.NewRegistry()
	reg.Register(jsscanner.NewTypeScriptScanner())
	if err := mgr.FullScan(dir, reg); err != nil {
		t.Fatal(err)
	}
	return mgr, reg
}

func TestTSFunctionReadNormalHidesUndescribedInterfacesAndTypes(t *testing.T) {
	mgr, reg := scanTS(t)
	out := readWith(t, mgr, reg, "normal", "geo.measure")
	wantNotIn(t, out, "## geo.Shape: no description", "## geo.Unit: no description")

	describe(t, mgr, domain.ResourceInterface, "geo.Shape", "Something with an area.")
	describe(t, mgr, domain.ResourceNamedType, "geo.Unit", "A length unit.")
	out = readWith(t, mgr, reg, "normal", "geo.measure")
	wantIn(t, out, "## geo.Shape: Something with an area.", "## geo.Unit: A length unit.")
}

// "full" keeps undescribed neighbours, as it does for every other kind.
func TestTSFunctionReadFullKeepsUndescribedInterfacesAndTypes(t *testing.T) {
	mgr, reg := scanTS(t)
	out := readWith(t, mgr, reg, "full", "geo.measure")
	wantIn(t, out, "## geo.Shape: no description", "## geo.Unit: no description")
}
