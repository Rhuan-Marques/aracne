package cli

import (
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// domain.PathOversized applies read.max_file_size only to extensions in its own list, so a
// scanner whose extension is missing from it would index files of any size. This keeps the list
// in step with every registered scanner.
func TestSizeCheckCoversEveryScannerExtension(t *testing.T) {
	for _, s := range NewScannerRegistry().All() {
		for _, ext := range s.Extensions() {
			if !domain.SizeChecked(ext) {
				t.Errorf("scanner %s parses %q, which read.max_file_size does not cover", s.Name(), ext)
			}
		}
	}
}
