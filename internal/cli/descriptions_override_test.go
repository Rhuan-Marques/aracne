package cli

import (
	"os"
	"reflect"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// DE-9: `--cli` overrides whatever descriptions.provider says, so an invalid configured provider
// -- a typo, or the retired "claude_cli" -- must not stop a run that never calls it. The provider
// was validated after the override had already replaced it.
func TestCLIOverrideIgnoresAnInvalidConfiguredProvider(t *testing.T) {
	if _, err := os.Stat("/bin/cat"); err != nil {
		t.Skip("needs a real binary on disk")
	}
	for _, bad := range []string{"antrhopic", helper.ProviderNameClaudeCLI} {
		cfg := helper.DefaultConfig()
		cfg.Descriptions.Provider = bad
		descCfg := cfg.EffectiveLazyDescriptions(helper.DefaultLazyHarness)

		runner, _, err := sweepDescriptionRunner(nil, nil, cfg, descCfg,
			&cliCommandFlag{set: true, command: "/bin/cat -"}, false)
		if err != nil {
			t.Errorf("provider %q with --cli: %v", bad, err)
			continue
		}
		if _, ok := runner.(*cliDescriptionRunner); !ok {
			t.Errorf("provider %q with --cli: runner = %T, want the CLI runner", bad, runner)
		}

		// Without --cli the configured provider IS the run's, and is still validated.
		if _, _, err := sweepDescriptionRunner(nil, nil, cfg, descCfg, &cliCommandFlag{}, false); err == nil {
			t.Errorf("provider %q without --cli must still be rejected", bad)
		}
	}
}

// DE-10: `descriptions clear --target function method` is two kinds. The words the shell split
// off --target were joined with a space, which the comma-splitting parser read as one kind named
// "function method".
func TestClearTargetsJoinTheShellSplitWords(t *testing.T) {
	both := []domain.ResourceKind{domain.ResourceFunction, domain.ResourceMethod}
	for _, tc := range []struct {
		target string
		extra  []string
	}{
		{"function", []string{"method"}},
		{"function,", []string{"method"}},
		{"function", []string{",", "method"}},
		{"function", []string{",method"}},
		// The bracketed list, spilled over two words, keeps working.
		{"[function,", []string{"method]"}},
	} {
		got, err := parseClearDescriptionTargets(joinClearTargetArgs(tc.target, tc.extra))
		if err != nil {
			t.Errorf("--target %s %v: %v", tc.target, tc.extra, err)
			continue
		}
		if !reflect.DeepEqual(got, both) {
			t.Errorf("--target %s %v = %v, want %v", tc.target, tc.extra, got, both)
		}
	}
}
