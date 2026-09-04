//go:build minimal

package cli

import (
	"fmt"
	"os"
)

// vizBuilt reports whether this binary carries the web visualizer. See viz.go for why
// this one file is the whole seam between the Basic and Full builds.
const vizBuilt = false

// RunViz reports that this build has no visualizer, rather than pretending `viz` is not a
// command at all. A user who reaches for it has the wrong binary, not a typo, and the
// difference is worth one line of output.
func RunViz([]string) {
	fmt.Fprintln(os.Stderr, "arac: this is the Basic build, which has no web visualizer.")
	fmt.Fprintln(os.Stderr, "Install the Full build to use `arac viz serve`:")
	fmt.Fprintln(os.Stderr, "  go install github.com/Rhuan-Marques/aracne/cmd/arac@latest")
	os.Exit(1)
}
