package topology_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner/goscanner"
)

// A var, const or type removed from a Go file, judged by what its users reference AFTER the edit.
//
// Measured on a real run (k6, Sonnet): `thresholdsRate` moved from core/engine.go to another
// package along with its only use, in one edit. The watcher's scan warned
// `startBackgroundProcesses uses variable thresholdsRate which was removed` against a function
// that no longer used it, and the warning stood in the table from then on. A removed FUNCTION
// already skipped same-file callers; vars, consts and types warned them from the old edges. The
// other half is the opposite gap: a same-file user that still names the removed node must stay
// warned on both routes -- the resolver raises nothing for an unresolved bare name.

const removedCoreBefore = `package core

import "time"

type Limits struct{ N int }

const (
	collectRate    = 50 * time.Millisecond
	thresholdsRate = 2 * time.Second
)

func Start(n int) time.Duration {
	_ = Limits{N: n}
	return thresholdsRate + collectRate
}
`

type removedDeclCase struct {
	name       string
	core       string
	engine     string // "" leaves metrics/engine/engine.go untouched
	wantSource string // the user that must be warned; "" means no stored breakage warning
}

func removedDeclCases() []removedDeclCase {
	return []removedDeclCase{
		{
			name: "const moved to another package with its only use",
			core: "package core\n\nimport \"time\"\n\ntype Limits struct{ N int }\n\nconst collectRate = 50 * time.Millisecond\n\nfunc Start(n int) time.Duration {\n\t_ = Limits{N: n}\n\treturn collectRate\n}\n",
			engine: "package engine\n\nimport \"time\"\n\nconst thresholdsRate = 2 * time.Second\n\n" +
				"func Run() time.Duration { return thresholdsRate }\n",
		},
		{
			name:       "const removed, same-file user still names it",
			core:       "package core\n\nimport \"time\"\n\ntype Limits struct{ N int }\n\nconst collectRate = 50 * time.Millisecond\n\nfunc Start(n int) time.Duration {\n\t_ = Limits{N: n}\n\treturn thresholdsRate + collectRate\n}\n",
			wantSource: "ex/core.Start",
		},
		{
			name: "const removed, same-file user only has a field of that name",
			core: "package core\n\nimport \"time\"\n\ntype Limits struct{ thresholdsRate time.Duration }\n\nconst collectRate = 50 * time.Millisecond\n\nfunc Start(l Limits) time.Duration {\n\treturn l.thresholdsRate + collectRate\n}\n",
		},
		{
			name: "struct moved out, same-file user stops using it",
			core: "package core\n\nimport \"time\"\n\nconst (\n\tcollectRate    = 50 * time.Millisecond\n\tthresholdsRate = 2 * time.Second\n)\n\nfunc Start(n int) time.Duration {\n\treturn thresholdsRate + collectRate\n}\n",
		},
		{
			name:       "struct removed, same-file user still takes it",
			core:       "package core\n\nimport \"time\"\n\nconst (\n\tcollectRate    = 50 * time.Millisecond\n\tthresholdsRate = 2 * time.Second\n)\n\nfunc Start(n int) time.Duration {\n\t_ = Limits{N: n}\n\treturn thresholdsRate + collectRate\n}\n",
			wantSource: "ex/core.Start",
		},
	}
}

func TestRemovedDeclarationIsJudgedOnItsUsersAfterTheEdit(t *testing.T) {
	for _, tc := range removedDeclCases() {
		for _, route := range []string{"IncrementalScan", "UpdateFile"} {
			t.Run(tc.name+"/"+route, func(t *testing.T) {
				dir := t.TempDir()
				write := func(rel, body string, bump int) {
					path := filepath.Join(dir, rel)
					if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
						t.Fatal(err)
					}
					mtime := time.Now().Add(time.Duration(bump) * time.Second)
					_ = os.Chtimes(path, mtime, mtime)
				}
				write("go.mod", "module ex\n\ngo 1.21\n", 0)
				write("core/engine.go", removedCoreBefore, 0)
				write("metrics/engine/engine.go", "package engine\n\nfunc Run() int { return 1 }\n", 0)
				reg := scanner.NewRegistry()
				reg.Register(goscanner.NewGoScanner())
				mgr := topology.New()
				mgr.Load(filepath.Join(dir, "topology.db"))
				if err := mgr.FullScan(dir, reg); err != nil {
					t.Fatal(err)
				}

				write("core/engine.go", tc.core, 3)
				if tc.engine != "" {
					write("metrics/engine/engine.go", tc.engine, 3)
				}
				if route == "IncrementalScan" {
					if _, err := mgr.IncrementalScan(dir, reg); err != nil {
						t.Fatal(err)
					}
				} else {
					if _, err := mgr.UpdateFile(filepath.Join(dir, "core/engine.go"), reg); err != nil {
						t.Fatal(err)
					}
					if tc.engine != "" {
						if _, err := mgr.UpdateFile(filepath.Join(dir, "metrics/engine/engine.go"), reg); err != nil {
							t.Fatal(err)
						}
					}
				}

				stored, err := mgr.ListWarnings("", "", "")
				if err != nil {
					t.Fatal(err)
				}
				var broken []domain.TopologyWarning
				for _, w := range stored {
					if breakageKinds[w.Kind] {
						broken = append(broken, w)
					}
				}
				if tc.wantSource == "" {
					if len(broken) > 0 {
						t.Errorf("warned a user that no longer references the removed node: %v", describe(broken))
					}
					return
				}
				found := false
				for _, w := range broken {
					if w.SourceID == tc.wantSource && w.Kind == domain.WarnNodeRemoved {
						found = true
					}
				}
				if !found {
					t.Errorf("want node_removed on %s, stored: %v", tc.wantSource, describe(broken))
				}
			})
		}
	}
}
