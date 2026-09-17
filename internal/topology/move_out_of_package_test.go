package topology_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// Moving a file into a subdirectory while its package keeps other files -- the shape of SWE-Atlas
// k6-10d4b447, where `git mv data.go metrics_client.go … v1/` produced 81 warnings, most of them
// the old package "referencing" its own former members via has_function. The surviving caller is
// the only thing that broke, and the only thing that may warn.
func TestMovingAFileOutOfAPackageWarnsOnlyItsCallers(t *testing.T) {
	dir := t.TempDir()
	additionWrite(t, dir, map[string]string{
		"go.mod":          "module example.com/m\n\ngo 1.21\n",
		"cloud/data.go":   "package cloud\n\ntype Sample struct{ V int }\n\nvar DataTypeMap = 1\n\nfunc NewSample(v int) Sample { return Sample{V: v} }\n\nfunc helper() int { return 2 }\n",
		"cloud/output.go": "package cloud\n\n// Output uses what data.go declares.\nfunc Output() Sample { return NewSample(DataTypeMap) }\n",
	}, time.Time{})
	reg := contractRegistry()
	mgr := topology.New()
	mgr.Load(filepath.Join(dir, "topology.db"))
	if err := mgr.FullScan(dir, reg); err != nil {
		t.Fatalf("FullScan: %v", err)
	}

	// git mv cloud/data.go cloud/v1/data.go
	if err := os.MkdirAll(filepath.Join(dir, "cloud", "v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "cloud", "data.go"), filepath.Join(dir, "cloud", "v1", "data.go")); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(3 * time.Second)
	_ = os.Chtimes(filepath.Join(dir, "cloud", "v1", "data.go"), future, future)

	warnings, err := mgr.IncrementalScan(dir, reg)
	if err != nil {
		t.Fatalf("IncrementalScan: %v", err)
	}
	if stored, err := mgr.ListWarnings("", "", ""); err == nil {
		warnings = append(warnings, stored...)
	}

	var fromCaller bool
	for _, w := range warnings {
		if w.Kind != domain.WarnNodeRemoved {
			continue
		}
		if strings.Contains(w.Message, "via has_") || strings.Contains(w.Message, "via methods") ||
			w.SourceID == "example.com/m/cloud" {
			t.Errorf("containment warned: %s", w.Message)
		}
		if strings.HasSuffix(w.SourceID, "cloud.Output") {
			fromCaller = true
		}
	}
	if !fromCaller {
		t.Errorf("the caller left behind in output.go must still be warned; got %+v", warnings)
	}
}

// Moving code TOGETHER changes every id but breaks nothing: `a.Fun1 calls b.Fun2` becomes
// `v1.Fun1 calls v1.Fun2`. Same file or different files, one command or not -- when caller and
// callee both leave, no warning may be left behind.
func TestMovingCallerAndCalleeTogetherWarnsNothing(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
		move  []string
	}{
		{
			name: "caller and callee in the same file",
			files: map[string]string{
				"cloud/data.go":   "package cloud\n\nfunc Fun1() int { return Fun2() }\n\nfunc Fun2() int { return 2 }\n",
				"cloud/output.go": "package cloud\n\nfunc Output() int { return 1 }\n",
			},
			move: []string{"data.go"},
		},
		{
			name: "caller and callee in different files moved in one go",
			files: map[string]string{
				"cloud/data.go":           "package cloud\n\ntype Sample struct{ V int }\n\nfunc Fun1() Sample { return Sample{V: Fun2()} }\n",
				"cloud/metrics_client.go": "package cloud\n\nfunc Fun2() int { return helper() }\n\nfunc helper() int { return len(Samples()) }\n\nfunc Samples() []Sample { return nil }\n",
				"cloud/output.go":         "package cloud\n\nfunc Output() int { return 1 }\n",
			},
			move: []string{"data.go", "metrics_client.go"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			files := map[string]string{"go.mod": "module example.com/m\n\ngo 1.21\n"}
			for k, v := range tc.files {
				files[k] = v
			}
			additionWrite(t, dir, files, time.Time{})
			reg := contractRegistry()
			mgr := topology.New()
			mgr.Load(filepath.Join(dir, "topology.db"))
			if err := mgr.FullScan(dir, reg); err != nil {
				t.Fatalf("FullScan: %v", err)
			}
			if err := os.MkdirAll(filepath.Join(dir, "cloud", "v1"), 0o755); err != nil {
				t.Fatal(err)
			}
			future := time.Now().Add(3 * time.Second)
			for _, f := range tc.move {
				dst := filepath.Join(dir, "cloud", "v1", f)
				if err := os.Rename(filepath.Join(dir, "cloud", f), dst); err != nil {
					t.Fatal(err)
				}
				_ = os.Chtimes(dst, future, future)
			}
			warnings, err := mgr.IncrementalScan(dir, reg)
			if err != nil {
				t.Fatalf("IncrementalScan: %v", err)
			}
			stored, _ := mgr.ListWarnings("", "", "")
			for _, w := range append(warnings, stored...) {
				t.Errorf("nothing broke, but got: [%s] %s", w.Kind, w.Message)
			}
		})
	}
}
