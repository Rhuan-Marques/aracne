package tui

import (
	"bufio"
	"io"
	"unicode"
)

// KeyType is what a keypress means to a question, which is a much smaller set than what a
// terminal can send. Anything this package has no use for is reported as KeyUnknown and
// ignored by the caller rather than guessed at.
type KeyType int

const (
	KeyUnknown KeyType = iota
	KeyUp
	KeyDown
	KeyEnter
	KeyEscape
	KeyCtrlC
	KeyBackspace
	KeyRune
)

// Key is one decoded keypress. Rune is meaningful only for KeyRune.
type Key struct {
	Type KeyType
	Rune rune
}

// keyReader decodes a raw terminal byte stream into Keys.
//
// It is a type and not a function because an escape sequence spans several reads, and the
// buffered reader that holds the tail of one has to be the same one the next read comes from.
// A fresh reader per keypress would drop the "[A" of an arrow key on the floor.
type keyReader struct{ in *bufio.Reader }

func newKeyReader(r io.Reader) *keyReader { return &keyReader{in: bufio.NewReader(r)} }

// The control bytes a terminal in raw mode sends for the keys this package cares about.
const (
	byteCtrlC     = 0x03
	byteEnter     = '\r' // raw mode does not translate this to \n
	byteLineFeed  = '\n'
	byteEscape    = 0x1b
	byteBackspace = 0x7f // DEL, which is what most terminals send for Backspace
	byteCtrlH     = 0x08
)

// next reads one keypress.
//
// An error is reported as KeyEscape rather than propagated: stdin ending mid-question is
// nobody there to answer, and the answer to that is the same as the answer to Escape -- leave,
// having written nothing. Returning an error instead would make every call site handle a case
// whose only correct handling is the one Escape already has.
func (k *keyReader) next() Key {
	b, err := k.in.ReadByte()
	if err != nil {
		return Key{Type: KeyEscape}
	}
	switch b {
	case byteCtrlC:
		return Key{Type: KeyCtrlC}
	case byteEnter, byteLineFeed:
		return Key{Type: KeyEnter}
	case byteBackspace, byteCtrlH:
		return Key{Type: KeyBackspace}
	case byteEscape:
		return k.escapeSequence()
	}
	r := rune(b)
	// Only what can be typed into an "Other:" field, and only single-byte. A pasted é is not
	// worth a UTF-8 decoder here: every value these questions collect -- an environment
	// variable, a command, a model name -- is ASCII, and a stray high byte silently becoming a
	// different character in a saved config is worse than it being ignored.
	if r > unicode.MaxASCII || !unicode.IsPrint(r) {
		return Key{Type: KeyUnknown}
	}
	return Key{Type: KeyRune, Rune: r}
}

// escapeSequence decodes what follows an ESC byte.
//
// A bare ESC -- nothing buffered behind it -- is the Escape key. Buffered() is what tells the
// two apart without a timeout: an arrow key arrives as one write, so its "[A" is already in
// the reader by the time we are asked, while a person pressing Escape leaves nothing behind.
func (k *keyReader) escapeSequence() Key {
	if k.in.Buffered() == 0 {
		return Key{Type: KeyEscape}
	}
	if b, err := k.in.ReadByte(); err != nil || b != '[' {
		return Key{Type: KeyUnknown}
	}
	b, err := k.in.ReadByte()
	if err != nil {
		return Key{Type: KeyUnknown}
	}
	switch b {
	case 'A':
		return Key{Type: KeyUp}
	case 'B':
		return Key{Type: KeyDown}
	}
	return Key{Type: KeyUnknown}
}

// navigation maps a key to a move, so the vi keys and the arrows stay one rule.
//
// j/k are accepted everywhere EXCEPT while a freeform field is being typed into, where they
// are letters. Select is where that distinction lives; this function only reports what a key
// would mean if it were navigation.
func navigation(k Key) (delta int, ok bool) {
	switch {
	case k.Type == KeyUp:
		return -1, true
	case k.Type == KeyDown:
		return 1, true
	case k.Type == KeyRune && k.Rune == 'k':
		return -1, true
	case k.Type == KeyRune && k.Rune == 'j':
		return 1, true
	}
	return 0, false
}
