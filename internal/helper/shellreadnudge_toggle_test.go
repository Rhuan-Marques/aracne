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
		{"aracne_read default is on", ModeAracneRead, nil, true},
		{"aracne_read explicit on", ModeAracneRead, &on, true},
		{"aracne_read switched off", ModeAracneRead, &off, false},
		{"line_range never nudges", ModeLineRange, nil, false},
		{"line_range off stays off", ModeLineRange, &off, false},
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
