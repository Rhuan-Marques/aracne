package tui

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// The key sequences a test presses, spelled once. A test that wrote "\x1b[B" inline would be
// a test whose intent is in an escape code.
const (
	keyDown  = "\x1b[B"
	keyUp    = "\x1b[A"
	keyEnter = "\r"
	keyEsc   = "\x1b"
	keyCtrlC = "\x03"
	keyBksp  = "\x7f"
)

// run drives one question with a scripted keyboard and returns what was chosen, plus
// everything that was drawn.
func run(t *testing.T, q Question, keys string) (int, string, string, error) {
	t.Helper()
	var screen bytes.Buffer
	s := NewScriptedSession(strings.NewReader(keys), &screen)
	i, v, err := Select(s, q)
	return i, v, screen.String(), err
}

func modes() Question {
	return Question{
		Title: "Which mode?",
		Step:  "2/6",
		Options: []Option{
			{Label: "cli", Summary: "No MCP tools."},
			{Label: "mcp", Summary: "One read tool."},
			{Label: "intercept_id", Summary: "Reads answered from the topology."},
		},
	}
}

// Enter with no movement takes the default, which is the whole point of having one.
func TestEnterTakesTheDefault(t *testing.T) {
	q := modes()
	q.Default = 1
	i, v, _, err := run(t, q, keyEnter)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if i != 1 || v != "mcp" {
		t.Fatalf("chose (%d, %q), want (1, \"mcp\")", i, v)
	}
}

func TestArrowsMoveTheHighlight(t *testing.T) {
	i, v, _, err := run(t, modes(), keyDown+keyDown+keyEnter)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if i != 2 || v != "intercept_id" {
		t.Fatalf("chose (%d, %q), want (2, \"intercept_id\")", i, v)
	}
}

// j/k navigate too, on a question with nothing to type into.
func TestViKeysNavigate(t *testing.T) {
	if _, v, _, err := run(t, modes(), "jj"+keyEnter); err != nil || v != "intercept_id" {
		t.Fatalf("jj -> (%q, %v), want intercept_id", v, err)
	}
	if _, v, _, err := run(t, modes(), "jjk"+keyEnter); err != nil || v != "mcp" {
		t.Fatalf("jjk -> (%q, %v), want mcp", v, err)
	}
}

// The cursor stops at the ends rather than wrapping: a held arrow must not land on an option
// nobody looked at.
func TestTheCursorClampsAtBothEnds(t *testing.T) {
	if _, v, _, err := run(t, modes(), keyUp+keyUp+keyUp+keyEnter); err != nil || v != "cli" {
		t.Fatalf("up past the top -> (%q, %v), want cli", v, err)
	}
	if _, v, _, err := run(t, modes(), strings.Repeat(keyDown, 9)+keyEnter); err != nil || v != "intercept_id" {
		t.Fatalf("down past the bottom -> (%q, %v), want intercept_id", v, err)
	}
}

func TestEscapeAndCtrlCCancel(t *testing.T) {
	for name, keys := range map[string]string{"escape": keyEsc, "ctrl-c": keyCtrlC} {
		if _, _, _, err := run(t, modes(), keys); !errors.Is(err, ErrCancelled) {
			t.Errorf("%s: err = %v, want ErrCancelled", name, err)
		}
	}
}

// Stdin running out mid-question is nobody there to answer, which is a cancel and not the
// default. Reading it as the default would write a config on a hang-up.
func TestExhaustedInputCancels(t *testing.T) {
	if _, _, _, err := run(t, modes(), ""); !errors.Is(err, ErrCancelled) {
		t.Fatalf("err = %v, want ErrCancelled", err)
	}
}

func freeform() Question {
	return Question{
		Title:          "Which environment variable holds the key?",
		Options:        []Option{{Label: "ANTHROPIC_API_KEY"}, {Label: "Other:", Freeform: true}},
		FreeformPrompt: "Other:",
		EmptyError:     "Name a variable, or pick the default above.",
	}
}

// The default answer is one keystroke; the unusual one costs the typing and nothing else.
func TestFreeformDefaultIsOneKeystroke(t *testing.T) {
	if _, v, _, err := run(t, freeform(), keyEnter); err != nil || v != "ANTHROPIC_API_KEY" {
		t.Fatalf("(%q, %v), want ANTHROPIC_API_KEY", v, err)
	}
}

func TestFreeformCollectsTypedText(t *testing.T) {
	_, v, _, err := run(t, freeform(), keyDown+"GATEWAY_KEY"+keyEnter)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if v != "GATEWAY_KEY" {
		t.Fatalf("value = %q, want GATEWAY_KEY", v)
	}
}

// j and k are letters in a field being typed into, not a cursor that jumps out mid-word.
func TestViKeysAreLettersWhileTyping(t *testing.T) {
	_, v, _, err := run(t, freeform(), keyDown+"jk_KEY"+keyEnter)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if v != "jk_KEY" {
		t.Fatalf("value = %q, want jk_KEY -- j/k navigated instead of typing", v)
	}
}

func TestBackspaceErases(t *testing.T) {
	_, v, _, err := run(t, freeform(), keyDown+"ABX"+keyBksp+"C"+keyEnter)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if v != "ABC" {
		t.Fatalf("value = %q, want ABC", v)
	}
}

// Enter on an empty field is a question that has not been answered: it says so, above the
// options, and stays open.
func TestEmptyFreeformShowsTheErrorAndStaysOpen(t *testing.T) {
	q := freeform()
	_, v, screen, err := run(t, q, keyDown+keyEnter+"K"+keyEnter)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if v != "K" {
		t.Fatalf("value = %q, want K -- the empty Enter was accepted", v)
	}
	if !strings.Contains(screen, q.EmptyError) {
		t.Fatalf("the empty answer drew no error:\n%s", screen)
	}
	// Above the options, not below: on a short terminal the bottom is where text goes to
	// be missed.
	if strings.Index(screen, q.EmptyError) > strings.LastIndex(screen, "Other:") {
		t.Error("the error was drawn below the options")
	}
}

// The panel is the reason this is a full-screen question and not a numbered list: it has to
// change as the highlight does.
func TestTheDetailPanelFollowsTheHighlight(t *testing.T) {
	q := modes()
	q.Options[0].Detail = []string{"the cli detail"}
	q.Options[1].Detail = []string{"the mcp detail"}
	_, _, screen, err := run(t, q, keyDown+keyEnter)
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if !strings.Contains(screen, "the cli detail") || !strings.Contains(screen, "the mcp detail") {
		t.Fatalf("both panels should have been drawn once:\n%s", screen)
	}
	if strings.Contains(screen, "No MCP tools.") && !strings.Contains(screen, "One read tool.") {
		t.Error("the summary did not follow the highlight")
	}
}

// The banner says which program has taken the terminal over, and the step counter says how
// much of it is left.
func TestTheFrameCarriesTheBannerAndTheStep(t *testing.T) {
	_, _, screen, _ := run(t, modes(), keyEnter)
	if !strings.Contains(screen, Banner()[0]) {
		t.Error("no banner")
	}
	if !strings.Contains(screen, "2/6") {
		t.Error("no step counter")
	}
	if !strings.Contains(screen, homeAndClear) {
		t.Error("the frame did not clear the screen before drawing")
	}
	// Raw mode does not translate line endings, so every break has to carry its own
	// carriage return or the screen staircases off the right edge.
	if strings.Contains(strings.ReplaceAll(screen, "\r\n", ""), "\n") {
		t.Error("a line break was written without its carriage return")
	}
}

func TestWrapKeepsLongWordsWhole(t *testing.T) {
	got := wrap("run internal/cli.RunSetup now", 12)
	want := []string{"run", "internal/cli.RunSetup", "now"}
	if len(got) != len(want) {
		t.Fatalf("wrap = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("wrap = %q, want %q", got, want)
		}
	}
}

// An indented line is an example block, not prose, and reflowing it would destroy the shape
// that makes it readable.
func TestWrapAllPassesIndentedLinesThrough(t *testing.T) {
	got := wrapAll([]string{"a b c", "", "  $ arac read x"}, 40)
	if len(got) != 3 || got[1] != "" || got[2] != "  $ arac read x" {
		t.Fatalf("wrapAll = %q", got)
	}
}

// truncate and padTo count cells, not bytes: the banner and the hint line are multi-byte, and
// slicing one by byte leaves the terminal drawing half a glyph.
func TestTruncateAndPadCountRunes(t *testing.T) {
	if got := truncate("█████", 3); got != "██…" {
		t.Errorf("truncate = %q, want \"██…\"", got)
	}
	if got := padTo("██", 4); got != "██  " {
		t.Errorf("padTo = %q, want \"██  \"", got)
	}
}

// Two questions on one session must both get their answers. A decoder built per question
// buffers ahead and throws away what it read, which loses every answer after the first --
// scripted, or typed ahead by someone who already knows the questions.
func TestOneSessionAnswersSuccessiveQuestions(t *testing.T) {
	s := NewScriptedSession(strings.NewReader(keyDown+keyEnter+keyEnter), &bytes.Buffer{})
	if _, v, err := Select(s, modes()); err != nil || v != "mcp" {
		t.Fatalf("first question: (%q, %v), want mcp", v, err)
	}
	if _, v, err := Select(s, modes()); err != nil || v != "cli" {
		t.Fatalf("second question: (%q, %v) -- the first question ate its keys", v, err)
	}
}
