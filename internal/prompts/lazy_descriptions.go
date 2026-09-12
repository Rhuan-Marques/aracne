package prompts

import (
	"fmt"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// The lazy description prompts.
//
// WHY THESE ARE NOT THE EXECUTOR PROMPTS. `descriptions generate` runs a sub-agent: it is
// told to call `read` for anything it is missing and `update_description` once per resource,
// and the loop is worth its round trips when it is describing a whole repository unattended.
//
// A lazy fill happens INSIDE a read the caller is waiting on. There is no budget for an agent
// loop, and no need for one either: the batch is small, the source is already cut and handed
// over, and the only thing wanted back is one line per resource. So the model gets a single
// completion, answers in a flat text format, and aracne does the writing -- through the same
// TopologyManager.UpdateDescription the tool would have called, so the budget check and the
// unknown-id check are not skipped, only moved.
//
// The house style, the per-kind budgets and the exemplars are shared with the sweep, because a
// description's voice must not depend on which path happened to write it.

// LazyDescriptionSeparator is what divides an id from its description in the reply. It is
// three characters no resource ID contains: ids carry dots, slashes, parentheses and colons,
// and a colon alone would split "pkg.(T).M: does x" in the wrong place.
const LazyDescriptionSeparator = " :: "

// LazyDescriptionsPrompt is the system prompt for a lazy fill.
func LazyDescriptionsPrompt() string {
	return `You write one short description per resource, for a code topology database.

## Output format — this is the whole contract
One line per assigned resource, nothing else:

<resource id>` + LazyDescriptionSeparator + `<description>

- Copy each resource id EXACTLY as it was given to you. It is a database key, not prose.
- One line per resource, in the order they were assigned. No blank lines, no numbering, no
  bullets, no code fences, no preamble, no closing summary.
- Describe every resource you were given. If one is genuinely undescribable from what you were
  shown, omit its line entirely rather than guessing — a wrong description is worse than none,
  because it is stored and re-shown on every later lookup.

## What a description is
- One line. Within the character budget stated for its kind — a longer one is rejected, not
  truncated.
- What the resource DOES, not what it is made of. Cut articles and filler before you cut facts.
- No trailing period, no "This function...", no restating the name.
- When house-style examples are given, match their voice and brevity.`
}

// LazyDescriptionsInput renders the batch: each resource with its source, the house-style
// exemplars, and the per-kind budgets.
func LazyDescriptionsInput(resources []DescriptionResource, exemplars []DescriptionExemplar) string {
	var b strings.Builder
	b.WriteString("Describe these ")
	fmt.Fprintf(&b, "%d", len(resources))
	b.WriteString(" resources. Reply with one `id")
	b.WriteString(LazyDescriptionSeparator)
	b.WriteString("description` line each and nothing else.\n\n")

	for _, res := range resources {
		fmt.Fprintf(&b, "### %s\n", res.ID)
		fmt.Fprintf(&b, "- id: %s\n- name: %s\n- kind: %s\n", res.ID, res.Name, res.Kind)
		fmt.Fprintf(&b, "- budget: %d characters\n", domain.StatedDescriptionBudget(res.Kind))
		if src := strings.TrimSpace(res.ReadOutput); src != "" {
			b.WriteString("\n```text\n")
			b.WriteString(src)
			b.WriteString("\n```\n")
		} else {
			// No source is not a reason to skip the resource: the id and the kind often
			// carry enough, and the alternative -- a read tool call -- is the round trip
			// this path exists to avoid.
			b.WriteString("\n(source unavailable; describe it from its id and kind, or omit it)\n")
		}
		b.WriteByte('\n')
	}

	writeExemplars(&b, exemplars)

	b.WriteString("## Per-kind limits\n\n")
	for _, line := range descriptionGuidelinesForKinds(resources) {
		b.WriteString("- ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// ParseLazyDescriptions reads the reply back into id -> description.
//
// It is deliberately forgiving about everything except the separator. Models wrap answers in
// fences, number their lines and add a closing sentence however plainly they are asked not to,
// and a fill that threw the whole batch away over a stray "```" would have burned the call for
// nothing. It is NOT forgiving about ids: an id it did not ask for is dropped, because the one
// thing worse than no description is a description stored against the wrong resource.
func ParseLazyDescriptions(reply string, wanted map[string]domain.ResourceKind) map[string]string {
	out := make(map[string]string, len(wanted))
	for _, raw := range strings.Split(reply, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "```") {
			continue
		}
		id, desc, ok := strings.Cut(line, LazyDescriptionSeparator)
		if !ok {
			// Tolerate a bare "::" without the surrounding spaces, which is the one
			// variation of the separator models actually produce.
			id, desc, ok = cutAtRequestedID(line, wanted)
			if !ok {
				continue
			}
		}
		id = cleanLazyID(id, wanted)
		desc = cleanLazyDescription(desc)
		if id == "" || desc == "" {
			continue
		}
		// First line wins: a model that answers twice for one id has contradicted itself,
		// and the first answer is the one it gave before it started padding.
		if _, seen := out[id]; !seen {
			out[id] = desc
		}
	}
	return out
}

// cutAtRequestedID splits a line at a bare "::" separator, choosing the "::" that ends a
// requested id rather than the first one.
//
// The first "::" is the wrong one wherever ids contain "::" themselves -- every Rust id is
// `crate::module::item` -- so cutting there left `crate` as the id, which names nothing, and
// every such line was dropped. Every "::" is tried and the longest prefix that names a
// requested id wins: a shorter one would split a requested id in two. A line where none does
// is reported as not found, the same outcome the first-"::" cut had for it.
func cutAtRequestedID(line string, wanted map[string]domain.ResourceKind) (id, desc string, ok bool) {
	for i := 0; ; {
		j := strings.Index(line[i:], "::")
		if j < 0 {
			return id, desc, ok
		}
		at := i + j
		if cleanLazyID(line[:at], wanted) != "" {
			id, desc, ok = line[:at], line[at+2:], true
		}
		i = at + 1
	}
}

// lazyIDWrappers are the emphasis pairs a model wraps an id in, longest first so `**x**` is
// peeled as one layer rather than as two `*` layers.
var lazyIDWrappers = []string{"**", "__", "`", "*", "_"}

// cleanLazyID strips the list and emphasis decoration models put in front of an id, and returns
// the requested id the token names -- or "" when it names none.
//
// IT PEELS, IT DOES NOT TRIM, and it asks the requested set after every layer. `_` and `*` are
// real id characters, not only markdown: it used to Trim "`*_" off both ends, which turned
// `shapes.Circle.__init__` into `shapes.Circle.__init`, matched nothing, and dropped the line --
// so every dunder method, and every `_private` name, could never be described through the CLI
// provider or the lazy fill however many times it was retried. Now the token is looked up as
// the model wrote it first, and a wrapper is only ever removed as a matched pair on both ends.
func cleanLazyID(s string, wanted map[string]domain.ResourceKind) string {
	s = strings.TrimSpace(s)
	if _, ok := wanted[s]; ok {
		return s
	}
	s = strings.TrimPrefix(s, "- ")
	s = strings.TrimPrefix(s, "* ")
	// A leading "1. " / "12. " ordinal.
	if i := strings.Index(s, ". "); i > 0 && i <= 3 && isAllDigits(s[:i]) {
		s = s[i+2:]
	}
	return unwrapLazyID(strings.TrimSpace(s), wanted, 3)
}

// unwrapLazyID returns s if it is requested, else the first requested id reached by peeling
// matched wrapper pairs off it, at most depth layers deep. The least-peeled match wins: a token
// that already names a requested id was copied exactly, and peeling it further could only turn
// it into a different one.
func unwrapLazyID(s string, wanted map[string]domain.ResourceKind, depth int) string {
	if _, ok := wanted[s]; ok {
		return s
	}
	if depth == 0 {
		return ""
	}
	for _, w := range lazyIDWrappers {
		if len(s) > 2*len(w) && strings.HasPrefix(s, w) && strings.HasSuffix(s, w) {
			if id := unwrapLazyID(strings.TrimSpace(s[len(w):len(s)-len(w)]), wanted, depth-1); id != "" {
				return id
			}
		}
	}
	return ""
}

// cleanLazyDescription normalises the description to the single line the database stores.
func cleanLazyDescription(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "`")
	// Whitespace is collapsed rather than preserved: the description is re-rendered inside a
	// "## id: description" line in every later CONTEXT block, where a newline would break the
	// grammar the context tests pin.
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimSpace(s)
}

// isAllDigits reports whether every byte is an ASCII digit; "" is not.
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
