package helper

import "testing"

func TestShellReadNudgeToggle(t *testing.T) {
	on := true
	off := false
	for _, tc := range []struct {
		name string
		mode string
		flag *bool
		want bool
	}{
		{"cli default is on", ModeCLI, nil, true},
		{"cli explicit on", ModeCLI, &on, true},
		{"cli switched off", ModeCLI, &off, false},
		{"intercept_line_ranges never nudges", ModeInterceptLineRanges, nil, false},
		{"intercept_line_ranges off stays off", ModeInterceptLineRanges, &off, false},
		{"mcp never nudges", ModeMCP, &on, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{Mode: tc.mode}
			c.Terminal.ShellReadNudge = tc.flag
			if got := c.NudgesShellReads(); got != tc.want {
				t.Fatalf("NudgesShellReads() = %v, want %v", got, tc.want)
			}
		})
	}
}
