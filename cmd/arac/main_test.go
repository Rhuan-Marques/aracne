package main

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// mainArgsEnv, when set, makes this test binary run main() with the given NUL-separated
// arguments instead of the tests -- so the dispatch can be exercised, exit status included,
// without building a separate arac binary.
const mainArgsEnv = "ARAC_MAIN_TEST_ARGS"

func TestMain(m *testing.M) {
	if args, ok := os.LookupEnv(mainArgsEnv); ok {
		os.Args = append([]string{"arac"}, strings.Split(args, "\x00")...)
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// runMain runs main() with args in a child process and returns its stdout and exit status.
func runMain(t *testing.T, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), mainArgsEnv+"="+strings.Join(args, "\x00"))
	out, err := cmd.Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return string(out), exit.ExitCode()
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(out), 0
}

// TestHelpFlagsPrintUsage pins ST-11: `arac --help`, `-h` and `help` printed "unknown command"
// and exited 1. They are requests for the banner, as a bare `arac` is.
func TestHelpFlagsPrintUsage(t *testing.T) {
	for _, arg := range []string{"--help", "-h", "help"} {
		out, code := runMain(t, arg)
		if code != 0 || !strings.Contains(out, "Usage:") {
			t.Errorf("`arac %s` must print the usage on stdout and exit 0, got exit %d, stdout %q", arg, code, out)
		}
	}
}

// TestUnknownCommandStillFails pins the behaviour the help case sits next to: a typo is still
// an error, with nothing on stdout.
func TestUnknownCommandStillFails(t *testing.T) {
	out, code := runMain(t, "scna")
	if code == 0 || out != "" {
		t.Errorf("a typo must exit non-zero with an empty stdout, got exit %d, stdout %q", code, out)
	}
}
