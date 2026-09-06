package tui

// Banner is the ARACNE wordmark, drawn at the top of every question.
//
// Two lines and half-block glyphs rather than a six-line figlet: the banner shares a screen
// with a question, a list and a detail panel, and every line it takes is a line of the answer
// the reader cannot see. It exists to say which program has taken over the terminal, which two
// lines do as well as six.
//
// The glyphs are U+2580-U+259F block elements, which every terminal font that can draw the
// progress bar's █ can also draw -- aracne has shipped that one since the bar existed.
func Banner() []string {
	return []string{
		"▄▀█ █▀█ ▄▀█ █▀▀ █▄░█ █▀▀",
		"█▀█ █▀▄ █▀█ █▄▄ █░▀█ ██▄",
	}
}
