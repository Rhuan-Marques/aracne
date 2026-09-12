package tests_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The "# CONTEXT:" block used to print a member TWICE on a whole-file read: once flat under
// "##" as one of the file's own functions, and once indented under the type that owns it. On
// ripgrep's crates/core/logger.rs that was 450 of 780 context bytes -- an index of the fence
// printed directly above it, charged to the model at full price.
//
// The read rework removed both halves: a file's own members are excluded from its context
// section entirely (the source above already shows them), and every remaining entry goes
// through one ledger so no ID can be rendered twice anywhere in a response. These tests pin
// that for Rust, the language the duplication was reported against, and check the general
// no-ID-twice rule for a batch as well.

// contextSection returns just the "# CONTEXT:" block of a read, or "" when there is none.
func contextSection(out string) string {
	i := strings.Index(out, "# CONTEXT:")
	if i < 0 {
		return ""
	}
	rest := out[i:]
	if j := strings.Index(rest, "\n# USED BY:"); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

// entryIDs returns the resource ID of every context entry, flat ("## id: desc") or indented
// ("\tid: desc"), so a duplicate across the two shapes is visible.
func entryIDs(section string) []string {
	// The description separator is ": " (colon then space); a Rust "::" never is, so a
	// non-greedy run of non-space characters stops exactly at the ID's end.
	re := regexp.MustCompile(`(?m)^(?:##\s+|\t)(\S+?)(?:\s+\([^)]*\))?:\s`)
	var ids []string
	for _, m := range re.FindAllStringSubmatch(section, -1) {
		ids = append(ids, strings.TrimSpace(m[1]))
	}
	return ids
}

// rustProject scans a Rust crate whose module holds a struct with several described methods --
// the shape that produced the duplication (module functions listed flat, struct methods listed
// again indented under the struct).
func rustProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "Cargo.toml"), "[package]\nname = \"demo\"\nversion = \"0.1.0\"\nedition = \"2021\"\n")
	writeFile(t, filepath.Join(dir, "src", "lib.rs"), "pub mod logger;\npub mod app;\n")
	writeFile(t, filepath.Join(dir, "src", "logger.rs"), `/// The simplest possible logger.
pub struct Logger {
    level: u8,
}

impl Logger {
    /// Create a logger at the given level.
    pub fn new(level: u8) -> Logger {
        Logger { level }
    }

    /// Report whether a record at this level would be printed.
    pub fn enabled(&self, level: u8) -> bool {
        level <= self.level
    }

    /// Print a record to stderr.
    pub fn log(&self, msg: &str) {
        if self.enabled(1) {
            eprintln!("{}", msg);
        }
    }
}

/// Build the process-wide logger.
pub fn init() -> Logger {
    Logger::new(2)
}
`)
	writeFile(t, filepath.Join(dir, "src", "app.rs"), `use crate::logger::Logger;

/// Run the application with a logger.
pub fn run() {
    let l = Logger::new(1);
    l.log("started");
}
`)
	mustRun(t, dir, "scan", "--hard", "--root", ".", "--output", ".aracne/topology.db")
	// Described in the topology, not just in `///` comments (which are not stored as
	// descriptions): read.context_filter "normal" omits an undescribed neighbour, so without
	// these the struct read would have no methods left to list once.
	for _, d := range [][2]string{
		{"demo::logger::Logger", "struct"},
		{"demo::logger::Logger::new", "method"},
		{"demo::logger::Logger::enabled", "method"},
		{"demo::logger::Logger::log", "method"},
		{"demo::logger::init", "function"},
		{"demo::app::run", "function"},
	} {
		mustRun(t, dir, "update-description", d[0], d[1], "Described "+d[0]+".")
	}
	return dir
}

func TestRustFileReadDoesNotListItsOwnMembersTwice(t *testing.T) {
	dir := rustProject(t)

	out := mustRun(t, dir, "read", "src/logger.rs")
	section := contextSection(out)

	seen := map[string]bool{}
	for _, id := range entryIDs(section) {
		if seen[id] {
			t.Fatalf("context lists %q twice (flat and indented under its type):\n%s", id, section)
		}
		seen[id] = true
	}
	// The file's own declarations are already the body; listing them again was an index of
	// the fence above.
	for _, member := range []string{"demo::logger::Logger", "demo::logger::init",
		"demo::logger::Logger::log", "demo::logger::Logger::enabled"} {
		if strings.Contains(section, member) {
			t.Fatalf("file read repeats its own member %q in CONTEXT:\n%s", member, section)
		}
	}
	if !strings.Contains(out, "pub fn init()") {
		t.Fatalf("the file read should still contain the file source:\n%s", out)
	}
}

func TestRustStructReadListsEachMethodOnce(t *testing.T) {
	dir := rustProject(t)

	// A struct read DOES carry topology -- its methods are the point -- but each of them
	// exactly once, no matter how many edges reach them.
	out := mustRun(t, dir, "read", "demo::logger::Logger")
	section := contextSection(out)

	seen := map[string]bool{}
	for _, id := range entryIDs(section) {
		if seen[id] {
			t.Fatalf("context lists %q twice:\n%s", id, section)
		}
		seen[id] = true
	}
	if !strings.Contains(section, "demo::logger::Logger::log") {
		t.Fatalf("a struct read should name its methods:\n%s", out)
	}
}

func TestBatchedReadNeverRendersAnIDTwice(t *testing.T) {
	dir := rustProject(t)

	// Two files, one of which uses the other: the cross-file neighbour is reachable from both
	// the batch's bodies and its context, which is where a ledger that is not response-wide
	// would print it twice.
	out := mustRun(t, dir, "read", "src/logger.rs", "src/app.rs", "demo::logger::Logger::log")
	section := contextSection(out)

	seen := map[string]bool{}
	for _, id := range entryIDs(section) {
		if seen[id] {
			t.Fatalf("batched context lists %q twice:\n%s", id, section)
		}
		seen[id] = true
	}
	if got := strings.Count(out, "# CONTEXT:"); got > 1 {
		t.Fatalf("a batch should share ONE context section, got %d:\n%s", got, out)
	}
}
