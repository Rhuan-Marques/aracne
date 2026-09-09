//go:build audit

package main

import (
	"errors"
	"os/exec"
	"path/filepath"
	"testing"
)

// buildArac compiles the binary once for the exit-status checks below.
func buildArac(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "arac")
	out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput()
	if err != nil {
		t.Skipf("cannot build arac (CGO/gcc required): %v\n%s", err, out)
	}
	return bin
}

// A-13: main's `default` arm, its no-argument arm and `descriptions` with no subcommand all
// print usage and fall out of main with status 0. Every wrapper that branches on the exit
// status -- CI, a shell `&&` chain, the generated slash commands -- reads a typo as success.
// The rest of the CLI is careful about exit vocabulary: `arac grep` and `arac read` both exit
// non-zero deliberately when they find nothing.
func TestAudit_UnknownSubcommandExitsNonZero(t *testing.T) {
	bin := buildArac(t)
	for _, args := range [][]string{
		{"scna"},         // a typo
		{"descriptions"}, // a real verb with its subcommand missing
	} {
		t.Run(args[0], func(t *testing.T) {
			err := exec.Command(bin, args...).Run()
			var exit *exec.ExitError
			if err == nil {
				t.Errorf("`arac %v` exited 0; a caller cannot tell a typo from success", args)
				return
			}
			if !errors.As(err, &exit) {
				t.Fatalf("unexpected failure: %v", err)
			}
		})
	}
}
