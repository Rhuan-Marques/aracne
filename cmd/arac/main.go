package main

import (
	"fmt"
	"os"
	"runtime/debug"

	"github.com/Rhuan-Marques/aracne/internal/buildinfo"
	"github.com/Rhuan-Marques/aracne/internal/cli"
)

// Version is the build version, injected at link time:
//
//	go build -ldflags "-X main.Version=$(git describe --tags --always --dirty)"
//
// An un-stamped build reports "dev". `arac --version` previously printed the usage banner,
// so nothing — including the benchmark harness's fixture provenance — could record which
// binary produced a topology.
var Version = "dev"

// version is Version, or the module version the toolchain recorded when no ldflags were
// passed.
//
// `go install github.com/Rhuan-Marques/aracne/cmd/arac@<tag>` -- the install the README
// documents, and so the one most people run -- does not pass ldflags, so v1.0.0-rc.1
// installed that way reported "dev" while the binary downloaded from that same release
// reported the tag. Two classes of released binary, and the headline path produced the
// useless one. The toolchain had the answer all along: it stamps the module version into
// build info, and nothing read it.
func version() string {
	if Version != "dev" {
		return Version // an ldflags build -- the Makefile and release.yml both pass it
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		// "(devel)" is what a plain `go build` in a checkout records: no better than "dev".
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return v
		}
	}
	return Version
}

// main dispatches Aracne subcommands
func main() {
	// Hand the stamped version to the packages that report it over a protocol.
	buildinfo.Version = version()
	if len(os.Args) < 2 {
		cli.PrintUsage()
		return
	}

	switch os.Args[1] {
	case "--version", "-v", "version":
		fmt.Printf("arac %s\n", version())
	case "--help", "-h", "help":
		// Asked for, so stdout and status 0 -- like a bare `arac`, unlike a typo below.
		cli.PrintUsage()
	case "scan":
		cli.RunScan(os.Args[2:])
	case "agent":
		// Gated by features.agent, and dispatchable either way -- same contract as
		// `arac bug`. An unlisted command is the "unlisted" half of gating an unshipped
		// feature; refusing to dispatch it would also break the debugging path.
		cli.RunAgent(os.Args[2:])
	case "serve":
		cli.RunServe(os.Args[2:])
	case "viz":
		cli.RunViz(os.Args[2:])
	case "init":
		cli.RunInit(os.Args[2:])
	case "setup":
		cli.RunSetup(os.Args[2:])
	case "disable":
		cli.RunDisable(os.Args[2:])
	case "descriptions":
		// A missing or unknown sub-verb is a typo, not a request for the banner. Same rule as
		// the default arm below: it goes to stderr and exits non-zero.
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: arac descriptions <generate|clear|export|import> [flags]")
			os.Exit(1)
		}
		switch os.Args[2] {
		case "generate":
			cli.RunGenerateDescriptions(os.Args[3:])
		case "clear":
			cli.RunClearDescriptions(os.Args[3:])
		case "export":
			cli.RunDescriptionsExport(os.Args[3:])
		case "import":
			cli.RunDescriptionsImport(os.Args[3:])
		default:
			fmt.Fprintf(os.Stderr, "arac descriptions: unknown subcommand %q\n", os.Args[2])
			fmt.Fprintln(os.Stderr, "Usage: arac descriptions <generate|clear|export|import> [flags]")
			os.Exit(1)
		}
	case "update-file":
		cli.RunUpdateFile(os.Args[2:])
	case "guard":
		cli.RunGuard(os.Args[2:])
	case "cmd":
		cli.RunCmd(os.Args[2:])
	case "read":
		cli.RunRead()
	case "grep":
		cli.RunGrep(os.Args[2:])
	case "update-description":
		cli.RunUpdateDescription(os.Args[2:])
	case "edit":
		cli.RunEdit()
	case "write":
		cli.RunWrite()
	case "resource":
		if len(os.Args) < 3 || os.Args[2] != "list" {
			fmt.Fprintln(os.Stderr, "Usage: arac resource list [query] [--kind <kind>]... [--no-description]")
			os.Exit(1)
		}
		cli.RunResourceList(os.Args[3:])
	case "node":
		// One subcommand, so one check. The inner switch this replaces re-tested a value the
		// guard above had already required, with one reachable arm and no default.
		if len(os.Args) < 3 || os.Args[2] != "count" {
			fmt.Fprintln(os.Stderr, "Usage: arac node count [--no-description]")
			os.Exit(1)
		}
		noDesc := false
		for _, arg := range os.Args[3:] {
			if arg == "--no-description" {
				noDesc = true
			}
		}
		if noDesc {
			cli.RunNodeCountNoDescription()
		} else {
			cli.RunNodeCount()
		}
	case "warnings":
		if len(os.Args) < 3 || os.Args[2] != "list" {
			fmt.Fprintln(os.Stderr, "Usage: arac warnings list [--source <id>] [--target <id>] [--kind <kind>]")
			os.Exit(1)
		}
		cli.RunWarningsList(os.Args[3:])
	case "bug":
		cli.RunBug(os.Args[2:])
	case "scanner":
		cli.RunScanner(os.Args[2:])
	case "check-updates":
		cli.RunCheckUpdates(os.Args[2:])
	default:
		// A TYPO IS A FAILURE. Printing the banner and exiting 0 meant no script, Makefile or
		// agent could tell `arac scna` from a command that ran -- and the banner on stdout
		// looked like output. A bare `arac` (above) is the deliberate ask and still exits 0.
		fmt.Fprintf(os.Stderr, "arac: unknown command %q\n\n", os.Args[1])
		cli.PrintUsageTo(os.Stderr)
		os.Exit(1)
	}
}
