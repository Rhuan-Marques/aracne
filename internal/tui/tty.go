// Package tui is the full-screen, arrow-key question flow `arac init` asks its setup
// questions through.
//
// WHY THIS EXISTS. Everything else aracne asks is a line at a time: a numbered list printed to
// stderr, an answer read off stdin. That shape is right for a question that interrupts a
// command someone already started -- it leaves a transcript, it composes with a pipe, and it
// costs nothing when there is nothing to ask. It is the wrong shape for `arac init`, which is
// not an interruption but the whole command: six questions whose answers decide what the
// repository becomes, several of which need a paragraph and an example to answer well. Printed
// as scrollback that is six screens of prose the reader has to hold in their head; drawn in
// place it is one screen that changes as the selection moves.
//
// So this package is deliberately small and deliberately not a framework. It draws one
// question at a time, reads keys, and hands the terminal back exactly as it found it. There is
// no layout engine, no widget tree and no event loop beyond the one in Select: the day aracne
// wants a second screen of this kind, that is the day to find out what actually generalises.
package tui

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"golang.org/x/term"
)

// ErrCancelled is Escape or Ctrl-C at a question.
//
// It is a distinct error, and not an answer, because every caller has to treat it as one:
// there is no default that means "the user changed their mind", and reading a cancel as the
// highlighted option would write a config nobody chose.
var ErrCancelled = errors.New("cancelled")

// Session is the terminal, borrowed.
//
// It owns three things that MUST be given back -- raw mode, the alternate screen, and the
// cursor -- and the whole design of this type is about the giving back. Close is idempotent, a
// signal handler calls it on SIGINT/SIGTERM, and the caller defers it. A wizard that exits
// leaving a terminal in raw mode is worse than a wizard that never ran: the shell that comes
// back does not echo, and the user has no way to know that `reset` is the fix.
type Session struct {
	in  io.Reader
	out io.Writer
	// fd is the descriptor raw mode and the size query act on, or -1 when there is no real
	// terminal behind this session -- which is the case in tests, where in and out are a
	// scripted reader and a buffer. Everything that touches the terminal itself checks it,
	// so the drawing and key-reading code is the same code in both cases.
	fd int
	// tty is the file both ends came from when it is one file we opened ourselves, and nil
	// when we are borrowing the process's own stdin/stderr. Only the former is ours to close.
	tty     *os.File
	state   *term.State
	once    sync.Once
	signals chan os.Signal
	// keys is ONE decoder for the whole session, created on first use.
	//
	// One, and not one per question: the decoder buffers, so a reader built for question
	// two would read question three's keystrokes into a buffer that is thrown away with it.
	// On a scripted run that loses every answer after the first; at a real keyboard it
	// loses whatever was typed ahead, which is a wizard that drops the keys of anyone who
	// already knows the questions.
	keys *keyReader
}

// keyReader returns the session's decoder, building it once.
func (s *Session) keyReader() *keyReader {
	if s.keys == nil {
		s.keys = newKeyReader(s.in)
	}
	return s.keys
}

// NewScriptedSession is a Session with no terminal behind it: keys come from r, the screen is
// drawn into w, and nothing is borrowed so there is nothing to give back.
//
// It exists for tests, and it is exported because the tests that need it most are not in this
// package: `arac init`'s question ORDER -- which branch question three takes, whether question
// four is asked at all -- is the part most worth pinning, and it lives in internal/cli. Aracne
// already keeps seams like this one (stdinIsTerminal, promptReader, Reporter.SetOutput);
// production code never calls it.
func NewScriptedSession(r io.Reader, w io.Writer) *Session {
	return &Session{in: r, out: w, fd: -1}
}

// The escape sequences this package writes, named so a reader of Open/Close can see what is
// being taken and what is being handed back without decoding them.
const (
	enterAltScreen = "\x1b[?1049h"
	exitAltScreen  = "\x1b[?1049l"
	hideCursor     = "\x1b[?25l"
	showCursor     = "\x1b[?25h"
	homeAndClear   = "\x1b[H\x1b[2J"
)

// Open borrows the terminal: raw mode, the alternate screen, no cursor.
//
// It goes to /dev/tty rather than to os.Stdin, so a run whose stdin is a pipe but whose
// terminal is right there still works -- `arac init < /dev/null` is a mistake worth failing on
// (Available says so), but `some | arac init` is not obviously one, and the tty is the honest
// place to ask a person a question. Falling back to stdin/stderr keeps the package usable
// where /dev/tty does not exist.
func Open() (*Session, error) {
	s := &Session{}
	if f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0); err == nil {
		s.tty, s.in, s.out, s.fd = f, f, f, int(f.Fd())
	} else {
		s.in, s.out, s.fd = os.Stdin, os.Stderr, int(os.Stdin.Fd())
	}

	state, err := term.MakeRaw(s.fd)
	if err != nil {
		if s.tty != nil {
			s.tty.Close()
		}
		return nil, fmt.Errorf("this terminal cannot be put into raw mode: %w", err)
	}
	s.state = state

	// The handler is installed BEFORE the screen is switched. A Ctrl-C in the window between
	// the two would otherwise land on a terminal that is raw and has no handler to restore it,
	// which is the exact failure this whole type exists to prevent.
	s.signals = make(chan os.Signal, 1)
	signal.Notify(s.signals, os.Interrupt, syscall.SIGTERM)
	go func() {
		if _, ok := <-s.signals; !ok {
			return
		}
		s.Close()
		os.Exit(130)
	}()

	fmt.Fprint(s.out, enterAltScreen+hideCursor)
	return s, nil
}

// Close hands the terminal back. It is safe to call more than once, and the caller should:
// the deferred call and the signal handler are both real paths and either may get there first.
func (s *Session) Close() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		signal.Stop(s.signals)
		// Reverse order of Open. The cursor and the screen are restored while raw mode is
		// still on because they are writes to the same terminal either way; raw mode goes
		// last so nothing is echoed on the way out.
		fmt.Fprint(s.out, showCursor+exitAltScreen)
		if s.state != nil {
			_ = term.Restore(s.fd, s.state)
		}
		if s.tty != nil {
			s.tty.Close()
		}
	})
}

// Size is the terminal's width and height in cells, with a defensible answer for a terminal
// that will not say. 80x24 is the fallback because it is the size every terminal is at least,
// so a layout that fits it fits everywhere.
func (s *Session) Size() (width, height int) {
	if s == nil || s.fd < 0 {
		return 80, 24
	}
	w, h, err := term.GetSize(s.fd)
	if err != nil || w <= 0 || h <= 0 {
		return 80, 24
	}
	return w, h
}

// Available reports whether there is a terminal to draw on at both ends.
//
// It is checked BEFORE Open, and it is what turns `arac init` in a pipe or in CI into an error
// naming `arac setup` rather than into a hang or a garbled screen. Both ends matter: reading
// keys from a pipe would block forever, and drawing a screen into a log file is not a question
// anyone is going to answer.
func Available() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stderr.Fd()))
}
