package cli

import (
	"fmt"
	"os"
	"strings"

	"aracne/internal/helper"
	"aracne/internal/llm/languages/universaltools"
	"aracne/internal/topology/domain"
)

// RunRead reads one or more topology resources (functions, types, interfaces, files, ...) and
// prints their source with connected context.
//
// The CLI and the MCP tool share one implementation, so `arac read` output and what a model
// sees are byte-identical. That was not true before: the CLI had its own formatters that
// printed a "# File Context" index of the very file it had just printed in full.
//
// There is no --lines. Reading a file in successive windows was the most expensive habit
// benchmarking found -- a turn per window, each re-sending the whole transcript -- so it is
// gone from both surfaces. `sed -n '10,40p' file` remains for the rare case that needs it.
func RunRead() {
	args := os.Args[2:]
	ids, forcedKind, full, parseErr := parseReadArgs(args)
	if parseErr != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", parseErr)
		printReadUsage()
		os.Exit(1)
	}

	manager, reg := InitRegistry(".aracne/topology.db")
	runReadScan(manager, reg)

	cfg := helper.EnsureConfig(helper.ConfigPath(manager.DbPath()))
	// The CLI is the human/bash surface, so it reads every kind regardless of read.kinds --
	// that setting exists to narrow what a MODEL is offered, not to lock a person out.
	out, err := universaltools.NewRead(manager, cfg, false, reg).ReadIDs(ids, universaltools.ReadIDsOptions{
		Kinds:         helper.AllReadKinds(),
		ForcedKind:    forcedKind,
		ForceFullFile: full,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(out)
}

// parseReadArgs collects the resource IDs and an optional --kind narrowing hint.
func parseReadArgs(args []string) ([]string, domain.ResourceKind, bool, error) {
	var ids []string
	var forcedKind domain.ResourceKind
	var full bool

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--kind":
			if i+1 >= len(args) {
				return nil, "", false, fmt.Errorf("--kind requires a value")
			}
			kind := MapResourceKind(args[i+1])
			if kind == "" {
				return nil, "", false, fmt.Errorf("unknown resource kind %q", args[i+1])
			}
			forcedKind = kind
			i++
		case strings.HasPrefix(arg, "--kind="):
			value := strings.TrimPrefix(arg, "--kind=")
			kind := MapResourceKind(value)
			if kind == "" {
				return nil, "", false, fmt.Errorf("unknown resource kind %q", value)
			}
			forcedKind = kind
		case arg == "--full":
			// Counterpart to the tool's `full` parameter: return whole file bodies even
			// under read.file_mode "skeleton".
			full = true
		case strings.HasPrefix(arg, "-"):
			return nil, "", false, fmt.Errorf("unknown flag %q", arg)
		default:
			ids = append(ids, arg)
		}
	}

	if len(ids) == 0 {
		return nil, "", false, fmt.Errorf("missing resource ID")
	}
	return ids, forcedKind, full, nil
}

// Prints usage information and examples for the read command to stderr.
func printReadUsage() {
	fmt.Fprintln(os.Stderr, "Usage: arac read [--kind <kind>] <resource-id> [<resource-id>...]")
	fmt.Fprintln(os.Stderr, "Kinds: function, method, type, named_type, interface, variable, file, package, dependency")
	fmt.Fprintln(os.Stderr, "Examples:")
	fmt.Fprintln(os.Stderr, "  arac read internal/cli/read.go")
	fmt.Fprintln(os.Stderr, "  arac read aracne/internal/cli.RunRead aracne/internal/cli.parseReadArgs")
	fmt.Fprintln(os.Stderr, "  arac read --kind function aracne/internal/topology/golang.(GoManager).ReadFunction")
}
