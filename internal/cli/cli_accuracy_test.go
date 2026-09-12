package cli

import (
	"bytes"
	"errors"
	"flag"
	"regexp"
	"strings"
	"testing"
)

// TestParseCheckUpdatesArgs pins ST-11's flag half: `arac check-updates` skipped every argument
// it did not recognise, so `--josn` or a misspelled `--db` ran some other report and exited 0.
func TestParseCheckUpdatesArgs(t *testing.T) {
	for _, bad := range [][]string{{"--bogus"}, {"--josn"}, {"stray"}, {"--json", "extra"}, {"--db"}} {
		var out bytes.Buffer
		if _, _, _, err := parseCheckUpdatesArgs(bad, &out); err == nil {
			t.Errorf("check-updates %v must be refused", bad)
		} else if out.Len() == 0 {
			t.Errorf("check-updates %v must say why on stderr", bad)
		}
	}

	var out bytes.Buffer
	db, root, asJSON, err := parseCheckUpdatesArgs([]string{"--db", "x.db", "--root=/r", "--json"}, &out)
	if err != nil || db != "x.db" || root != "/r" || !asJSON {
		t.Fatalf("the documented flags parse: db=%q root=%q json=%v err=%v", db, root, asJSON, err)
	}
	db, root, asJSON, err = parseCheckUpdatesArgs(nil, &out)
	if err != nil || db != ProjectDBPath(DefaultDBRelative) || root != "" || asJSON {
		t.Fatalf("no flags keeps the defaults: db=%q root=%q json=%v err=%v", db, root, asJSON, err)
	}
	if _, _, _, err := parseCheckUpdatesArgs([]string{"-h"}, &out); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("-h asks for usage, got %v", err)
	}
}

// TestUsageClearExamplesParse pins ST-11's example half: the banner's own
// `arac descriptions clear --target function,type` failed with `invalid describe target
// "type"`. Every --target example in the banner must be accepted by the command it documents.
func TestUsageClearExamplesParse(t *testing.T) {
	examples := regexp.MustCompile(`arac descriptions clear --target (\S+)`).FindAllStringSubmatch(usageText, -1)
	if len(examples) == 0 {
		t.Fatal("expected a descriptions clear --target example in the banner")
	}
	for _, ex := range examples {
		if _, err := parseClearDescriptionTargets(ex[1]); err != nil {
			t.Errorf("usage example %q fails: %v", strings.TrimSpace(ex[0]), err)
		}
	}
}
