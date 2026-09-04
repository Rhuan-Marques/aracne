//go:build !minimal

package cli

import (
	"flag"
	"fmt"
	"os"

	"github.com/Rhuan-Marques/aracne/internal/viz"
)

// vizBuilt reports whether this binary carries the web visualizer.
//
// This file is the ONLY importer of internal/viz, which is in turn the only importer of
// internal/chat -- so tagging it out is what makes the Basic build: the linker drops both
// packages and the 2.7 MB `go:embed static/*` payload with them. Nothing else needs a tag.
// `go build ./...` and `go test ./...` still compile and exercise both packages.
const vizBuilt = true

// Starts an HTTP server to visualize the topology graph.
func RunViz(args []string) {
	if len(args) == 0 {
		args = []string{"serve"}
	}
	if args[0] != "serve" {
		fmt.Fprintln(os.Stderr, "Usage: arac viz serve [--db <path>] [--addr <addr>]")
		os.Exit(1)
	}

	fs := flag.NewFlagSet("viz serve", flag.ExitOnError)
	dbPath := fs.String("db", ".aracne/topology.db", "Topology database path")
	addr := fs.String("addr", "127.0.0.1:7331", "HTTP listen address")
	fs.Parse(args[1:])

	if err := viz.Listen(*addr, *dbPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
