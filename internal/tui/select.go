package tui

import (
	"strings"
)

// Option is one row of a question.
//
// Value is separate from Label because the two audiences are different: Label is what a person
// reads ("An API key"), Value is what the config records ("anthropic"). Where they are the
// same -- a mode name, a model name -- Value is left empty and the Label is used, so the
// common case does not have to say it twice.
type Option struct {
	Label string
	// Summary is the one-line answer to "what is this", shown at the top of the detail
	// panel when this row is highlighted.
	Summary string
	// Detail is the rest of the panel: what the option means, what it costs, and an example
	// of what it looks like. A line that begins with a space is passed through unreflowed,
	// which is how example blocks keep their shape.
	Detail []string
	// Value is what the caller records. Empty means Label.
	Value string
	// Freeform makes this the "Other:" row: highlighting it enables typing, and the answer
	// is what was typed rather than Value.
	//
	// It is a row and not a separate widget because an open question in this flow always has
	// a sensible default -- the provider's usual key variable, `claude -p`, haiku -- and
	// pressing Enter on that default is the answer most of the time. Making the free text an
	// option among the others means the common answer costs one keystroke and the unusual
	// one costs no more than a bare text field would have.
	Freeform bool
}

// Question is one screen.
type Question struct {
	// Title is the question itself, e.g. "Which mode?".
	Title string
	// Step is the position indicator drawn to the right of the title, e.g. "2/6". Empty
	// draws nothing.
	Step string
	// Intro is a short paragraph under the title, above the options.
	Intro []string
	// Options are the rows, in the order they are offered.
	Options []Option
	// Default is the index highlighted on entry. Out of range resolves to 0.
	Default int
	// FreeformPrompt is the label of the freeform row's input, e.g. "Other:". Empty uses
	// the option's own Label.
	FreeformPrompt string
	// EmptyError is what is shown above the options when Enter is pressed on an empty
	// freeform field. Empty uses a default.
	EmptyError string
}

// answer is what a resolved option means to the caller.
func (o Option) answer(typed string) string {
	if o.Freeform {
		return strings.TrimSpace(typed)
	}
	if o.Value != "" {
		return o.Value
	}
	return o.Label
}

// Select draws one question and returns the chosen option's index and value.
//
// The loop is deliberately the whole widget: read a key, change one piece of state, redraw
// everything. There is no partial repaint and no damage tracking, because a screen this size
// repaints faster than a person can press the next key, and the version of this function that
// tracked what had changed would be the version with a stale row in it.
func Select(s *Session, q Question) (int, string, error) {
	if len(q.Options) == 0 {
		return 0, "", ErrCancelled
	}
	cursor := q.Default
	if cursor < 0 || cursor >= len(q.Options) {
		cursor = 0
	}
	var (
		typed  string
		errMsg string
	)
	keys := s.keyReader()
	for {
		draw(s, q, cursor, typed, errMsg)
		key := keys.next()

		// Typing wins over navigation while the freeform row is highlighted, so that j and
		// k are letters in a command someone is typing rather than a cursor that jumps out
		// of the field mid-word. The arrows still navigate -- they are unambiguous.
		editing := q.Options[cursor].Freeform
		if editing {
			switch key.Type {
			case KeyRune:
				typed += string(key.Rune)
				errMsg = ""
				continue
			case KeyBackspace:
				if n := len(typed); n > 0 {
					typed = typed[:n-1]
				}
				errMsg = ""
				continue
			}
		}

		switch key.Type {
		case KeyCtrlC, KeyEscape:
			return 0, "", ErrCancelled
		case KeyEnter:
			if editing && strings.TrimSpace(typed) == "" {
				errMsg = q.EmptyError
				if errMsg == "" {
					errMsg = "Type a value here, or pick one of the options above."
				}
				continue
			}
			return cursor, q.Options[cursor].answer(typed), nil
		}

		// Any navigation that survived the editing branch above: the arrows always, and
		// j/k only when nothing is being typed into.
		if delta, ok := navigation(key); ok {
			cursor = clampCursor(cursor+delta, len(q.Options))
			errMsg = ""
		}
	}
}

// clampCursor keeps the highlight inside the list, stopping at the ends rather than wrapping.
//
// Stopping, not wrapping: these lists are three or four rows and the defaults sit at the top,
// so a held-down arrow that wrapped would land somewhere the user did not look at, on a
// question that decides what gets billed.
func clampCursor(i, n int) int {
	if i < 0 {
		return 0
	}
	if i >= n {
		return n - 1
	}
	return i
}

// draw paints one frame: banner, title, intro, any error, the option column beside the
// highlighted option's detail panel, and the key hints.
func draw(s *Session, q Question, cursor int, typed, errMsg string) {
	width, height := s.Size()
	f := newFrame(width)

	for _, l := range Banner() {
		f.line(l)
	}
	f.blank()
	f.line(titleLine(q, width))
	if len(q.Intro) > 0 {
		f.blank()
		for _, l := range wrapAll(q.Intro, width-marginLeft) {
			f.line(l)
		}
	}
	if errMsg != "" {
		f.blank()
		// Above the options, where the eye already is, and not below them where it would
		// be off the bottom of a short terminal.
		for _, l := range wrap("! "+errMsg, width-marginLeft) {
			f.line(l)
		}
	}
	f.blank()

	rows := optionRows(q, cursor, typed)
	labelWidth := 0
	for _, r := range rows {
		if n := len([]rune(r)); n > labelWidth {
			labelWidth = n
		}
	}
	detail := detailLines(q.Options[cursor], width-marginLeft-labelWidth-gutter)
	// Whatever is left of the screen after the chrome above and the hint line below. The
	// panel is what gives, not the options: an option the reader cannot see is a choice they
	// cannot make, while a truncated paragraph is still most of an explanation.
	if budget := height - f.lineCount() - 3; budget > 0 && len(detail) > budget {
		detail = detail[:budget]
	}

	for i := 0; i < len(rows) || i < len(detail); i++ {
		var row, panel string
		if i < len(rows) {
			row = rows[i]
		}
		if i < len(detail) {
			panel = detail[i]
		}
		if panel == "" {
			f.line(row)
			continue
		}
		f.line(padTo(row, labelWidth) + strings.Repeat(" ", gutter) + panel)
	}

	f.blank()
	f.line(hint(q.Options[cursor].Freeform))
	f.flush(s)
}

// titleLine is the question with its step counter pushed to the right margin.
func titleLine(q Question, width int) string {
	if q.Step == "" {
		return q.Title
	}
	room := width - marginLeft - len([]rune(q.Step))
	if room <= len([]rune(q.Title))+1 {
		return q.Title + "  (" + q.Step + ")"
	}
	return padTo(q.Title, room) + q.Step
}

// optionRows renders the option column, marking the highlighted row and drawing the freeform
// field's contents and caret.
func optionRows(q Question, cursor int, typed string) []string {
	rows := make([]string, len(q.Options))
	for i, opt := range q.Options {
		marker := "  "
		if i == cursor {
			marker = "> "
		}
		label := opt.Label
		if opt.Freeform {
			prompt := q.FreeformPrompt
			if prompt == "" {
				prompt = opt.Label
			}
			label = prompt + " " + typed
			if i == cursor {
				// A caret, because Open hid the real cursor -- and it is hidden
				// because a terminal cursor parked at the end of the last line drawn
				// is not where the user is looking.
				label += "▏"
			}
		}
		rows[i] = marker + label
	}
	return rows
}

// detailLines is the panel for the highlighted option: its summary, then its detail. Returns
// nothing at all when the terminal is too narrow to reflow prose into what is left.
func detailLines(opt Option, width int) []string {
	if width < minDetailWidth {
		return nil
	}
	if width > maxDetailWidth {
		width = maxDetailWidth
	}
	var out []string
	if opt.Summary != "" {
		out = append(out, wrap(opt.Summary, width)...)
	}
	if len(opt.Detail) > 0 {
		if len(out) > 0 {
			out = append(out, "")
		}
		out = append(out, wrapAll(opt.Detail, width)...)
	}
	return out
}

// hint is the key legend. It names Backspace only where there is something to erase.
func hint(editing bool) string {
	if editing {
		return "type to answer   ⌫ erase   ↑/↓ move   ⏎ select   esc cancel"
	}
	return "↑/↓ move   ⏎ select   esc cancel"
}
