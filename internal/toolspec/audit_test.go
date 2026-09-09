//go:build audit

package toolspec

import "testing"

// A-11: gitSubcommandKey picks the first argument that does not start with "-" as the
// subcommand, so a global option that takes a separate value hides it. `git -C /repo apply`
// rewrites worktree files and is documented in gitSubcommandKey's own comment as an edit; it
// is classified as nothing at all.
func TestAudit_GitGlobalOptionsDoNotHideTheSubcommand(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"apply behind -C", []string{"-C", "/repo", "apply", "p.patch"}, "edit"},
		{"restore behind -C", []string{"-C", "/repo", "restore", "src/main.go"}, "edit"},
		{"show behind -C", []string{"-C", "/repo", "show", "HEAD:src/main.go"}, "read"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ShellCommandKeyForArgs("git", tc.args, false)
			if !ok || got != tc.want {
				t.Errorf("ShellCommandKeyForArgs(git, %v) = (%q, %v), want (%q, true); "+
					"the value of -C was taken for the subcommand",
					tc.args, got, ok, tc.want)
			}
		})
	}
}
