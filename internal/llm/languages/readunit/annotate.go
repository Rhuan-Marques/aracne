package readunit

import "strings"

// Annotation is one inline note attached to a single line of a Unit's body: "this exact
// source line, and here is what is wrong with it".
//
// KEYED BY LINE TEXT, NOT BY LINE NUMBER, and that is forced rather than chosen. A rendered
// body is not a contiguous file slice. A Go method carries its receiver type's cut above it
// with a synthetic blank line between (gotools.FunctionUnit); a Python method's body is often
// its whole enclosing class (pythontools.FunctionUnit); and an elided parent or an abridged
// tail replaces an N-line span with a one-line marker. Unit.Line is the resource's own
// StartsAt in every one of those, so Line+i indexes into the wrong code.
type Annotation struct {
	// Line is the exact source line to mark, indentation included. Compared with trailing
	// whitespace stripped from both sides; LEADING whitespace is significant, since it is
	// what tells two otherwise identical closing braces apart.
	Line string
	// Note is what gets written after "<- ". The caller owns the wording.
	Note string
}

// minAnchorLen is the shortest line text that can carry an annotation.
//
// A one- or two-character line -- `}`, `)`, `end`, `};` -- occurs everywhere in a body, so
// "the first match" would be an arbitrary one. Refusing is the honest answer: the caller
// still reports the warning, it just does not point at a line it guessed. See AnchorIndex.
const minAnchorLen = 3

// anchorKey normalizes a line for comparison: trailing whitespace is noise a cut may or may
// not carry, leading whitespace is the depth that tells two identical statements apart.
func anchorKey(line string) string { return strings.TrimRight(line, " \t\r") }

// AnchorIndex is the ONE place that decides which line an annotation means.
//
// Shared by the windowing abridger and the renderer deliberately: if they disagreed about
// which occurrence was meant, the window would be centred on one line and the note printed on
// another. Returns -1 for no match and for an anchor too short to discriminate.
//
// FIRST MATCH ONLY. Marking every occurrence of a repeated line would tell the reader three
// places are broken when one is, and invites the fix to land on the wrong one.
func AnchorIndex(lines []string, a Annotation) int {
	want := anchorKey(a.Line)
	if len(strings.TrimSpace(want)) < minAnchorLen {
		return -1
	}
	for i, line := range lines {
		if anchorKey(line) == want {
			return i
		}
	}
	return -1
}

// Annotate appends "<- <note>" to each annotated line of body, one line per annotation.
//
// Returns body unchanged when anns is empty or nothing matches, which is what keeps every
// unannotated render in the product byte-for-byte identical.
//
// Every index is resolved against the ORIGINAL lines before any of them is rewritten. Two
// warnings can name one line -- a call that is both missing and re-signatured, a class that
// fails two interfaces -- and appending to it in place would change the text the second
// lookup is still searching for, silently dropping the second note.
func Annotate(body string, anns []Annotation) string {
	if len(anns) == 0 || body == "" {
		return body
	}
	lines := strings.Split(body, "\n")
	notes := map[int][]string{}
	var order []int
	for _, a := range anns {
		if a.Note == "" {
			continue
		}
		i := AnchorIndex(lines, a)
		if i < 0 {
			continue
		}
		if _, seen := notes[i]; !seen {
			order = append(order, i)
		}
		notes[i] = append(notes[i], a.Note)
	}
	if len(order) == 0 {
		return body
	}
	for _, i := range order {
		lines[i] = strings.TrimRight(lines[i], " \t\r") + " <- " + strings.Join(notes[i], "; ")
	}
	return strings.Join(lines, "\n")
}
