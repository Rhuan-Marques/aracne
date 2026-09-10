package prompts

import (
	"regexp"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
)

// THE CONTRACT'S EXAMPLE HAS TO BE A LINE THE RENDERER EMITS.
//
// Under intercept_line_ranges the high contract used to show
// `## src/shapes.rs:12-48 Circle: Description` -- the span keying the line with the id gone --
// while readunit.withLocations keeps BOTH: the id ties the entry to the symbol the reader just
// saw in the code above it, the span says how to fetch it. A documented shape the tool never
// emits costs the model exactly the re-derivation the long contract is paid to save.
//
// The grammar asserted here is withLocations': `<marker> <id> (<span>)<separator>`. It is the
// same shape tests/context_dedup_test.go pins on the rendering side, so the two cannot drift
// apart without one of them failing.
func TestHighContractContextExampleMatchesTheRenderer(t *testing.T) {
	cfg := helper.DefaultConfig()
	cfg.Mode = helper.ModeInterceptLineRanges
	cfg.ContractVerbosity = helper.ContractVerbosityHigh

	contract := ContractContent(cfg, []string{"rust"})
	block := contextExampleBlock(t, contract)
	if len(block) == 0 {
		t.Fatal("the high contract must show what a CONTEXT block looks like")
	}

	// `## <id> (<path>:<start>-<end>): <description>`, and the indented member form.
	entry := regexp.MustCompile(`^(?:##\s+|\s+)(\S+) \(([^)]*:\d+-\d+)\): .+$`)
	for _, line := range block {
		if line == "# CONTEXT:" {
			continue
		}
		if !entry.MatchString(line) {
			t.Errorf("the example line %q is not the shape withLocations produces "+
				"(`## <id> (<path>:<start>-<end>): <description>`)", line)
		}
	}
}

// And the id-keyed modes keep their own shape: no span, because nothing prints one there.
func TestHighContractContextExampleIsSpanFreeWhereSpansAreNot(t *testing.T) {
	cfg := helper.DefaultConfig()
	cfg.Mode = helper.ModeMCP
	cfg.ContractVerbosity = helper.ContractVerbosityHigh

	contract := ContractContent(cfg, []string{"go"})
	for _, line := range contextExampleBlock(t, contract) {
		if regexp.MustCompile(`:\d+-\d+`).MatchString(line) {
			t.Errorf("mcp addresses by id and has no reader for a span, but the example shows one: %q", line)
		}
	}
}

// contextExampleBlock returns the lines of the fenced block that follows the first `# CONTEXT:`
// in the contract.
func contextExampleBlock(t *testing.T, contract string) []string {
	t.Helper()
	lines := strings.Split(contract, "\n")
	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "# CONTEXT:" {
			start = i
			break
		}
	}
	if start < 0 {
		return nil
	}
	var out []string
	for _, line := range lines[start:] {
		if strings.HasPrefix(line, "```") {
			break
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		out = append(out, line)
	}
	return out
}
