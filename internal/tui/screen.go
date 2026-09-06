package tui

import (
	"fmt"
	"strings"
)

// The frame's fixed geometry. These are the only magic numbers in the layout, and they are
// here rather than inline so a change to the indent is one edit and not eleven.
const (
	// marginLeft indents everything. The banner and the question both hang off it, so the
	// screen reads as a document with a left edge rather than as terminal output.
	marginLeft = 2
	// gutter is the space between the option column and the detail panel.
	gutter = 2
	// minDetailWidth is the narrowest a detail panel may be before it is dropped entirely.
	// A paragraph reflowed into six columns is not a paragraph; on a terminal that narrow
	// the options alone are the honest thing to show.
	minDetailWidth = 24
	// maxDetailWidth stops the panel from running to the edge of a very wide terminal. Prose
	// past about this width is measurably harder to read back to the start of the next line.
	maxDetailWidth = 64
)

// frame accumulates the lines of one screen, so a redraw is a single write.
//
// One write and not many: a screen painted line by line over a slow connection is visibly
// painted, and the flicker reads as the program struggling rather than as the cursor moving.
// Every line ends "\r\n" because raw mode has switched off the translation that would
// otherwise supply the carriage return.
type frame struct {
	b     strings.Builder
	width int
	lines int
}

func newFrame(width int) *frame {
	f := &frame{width: width}
	f.b.WriteString(homeAndClear)
	return f
}

// line writes one line at the left margin.
func (f *frame) line(text string) {
	f.lines++
	if text == "" {
		f.b.WriteString("\r\n")
		return
	}
	f.b.WriteString(strings.Repeat(" ", marginLeft))
	f.b.WriteString(truncate(text, f.width-marginLeft))
	f.b.WriteString("\r\n")
}

// blank writes an empty line.
func (f *frame) blank() { f.lines++; f.b.WriteString("\r\n") }

// lineCount is how many lines have been written so far, which is what the caller budgets the
// detail panel against on a short terminal.
func (f *frame) lineCount() int { return f.lines }

func (f *frame) String() string { return f.b.String() }

// flush paints the frame.
func (f *frame) flush(s *Session) { fmt.Fprint(s.out, f.b.String()) }

// wrap reflows text to width, breaking on spaces. A word longer than the width is left long
// rather than hyphenated -- the words that hit this are commands, resource IDs and environment
// variables, and breaking one of those in half makes it unreadable rather than merely wide.
func wrap(text string, width int) []string {
	if width <= 0 {
		return nil
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{""}
	}
	var (
		lines []string
		cur   strings.Builder
	)
	for _, w := range words {
		switch {
		case cur.Len() == 0:
			cur.WriteString(w)
		case cur.Len()+1+len(w) <= width:
			cur.WriteByte(' ')
			cur.WriteString(w)
		default:
			lines = append(lines, cur.String())
			cur.Reset()
			cur.WriteString(w)
		}
	}
	lines = append(lines, cur.String())
	return lines
}

// wrapAll reflows a paragraph list, preserving the blank lines between paragraphs. An empty
// string in the input is a deliberate gap -- an example block set apart from the prose above
// it -- and reflowing it away would run the two together.
func wrapAll(paragraphs []string, width int) []string {
	var out []string
	for _, p := range paragraphs {
		if strings.TrimSpace(p) == "" {
			out = append(out, "")
			continue
		}
		// A line that is already indented is pre-formatted -- an example, a snippet of
		// output -- and is passed through at its own width rather than reflowed into prose.
		if strings.HasPrefix(p, " ") {
			out = append(out, truncate(p, width))
			continue
		}
		out = append(out, wrap(p, width)...)
	}
	return out
}

// truncate clips a string to width, in RUNES rather than bytes: the banner and the "↑/↓" hint
// are multi-byte, and clipping those by byte would cut a glyph in half and leave the terminal
// drawing a replacement character for the rest of the line.
func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	if width <= 1 {
		return string(r[:width])
	}
	return string(r[:width-1]) + "…"
}

// padTo right-pads to width in runes. Same reason as truncate: len() on a string with a block
// glyph in it is not the number of cells it occupies.
func padTo(s string, width int) string {
	n := len([]rune(s))
	if n >= width {
		return s
	}
	return s + strings.Repeat(" ", width-n)
}
