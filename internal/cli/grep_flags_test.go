package cli

import (
	"bytes"
	"strings"
	"testing"
)

// An unknown -output-mode used to render as content, uncapped, with exit 0 -- so a typo got a
// full match list back and no sign it had been ignored. The three real modes still work.
func TestAracGrepRejectsAnUnknownOutputMode(t *testing.T) {
	root, _ := scannedProject(t)
	t.Chdir(root)

	var out, errOut bytes.Buffer
	if code := runGrep([]string{"-output-mode", "bogus", "Serve", "app.go"}, &out, &errOut); code != 2 {
		t.Errorf("exit %d, want 2 (stdout %q)", code, out.String())
	}
	if !strings.Contains(errOut.String(), "bogus") || out.Len() != 0 {
		t.Errorf("want the bad value named on stderr and nothing on stdout, got %q / %q", errOut.String(), out.String())
	}

	for _, mode := range []string{"content", "files_with_matches", "count"} {
		out.Reset()
		errOut.Reset()
		if code := runGrep([]string{"-output-mode", mode, "Serve", "app.go"}, &out, &errOut); code != 0 {
			t.Errorf("-output-mode %s: exit %d, stderr %q", mode, code, errOut.String())
		}
	}
}
