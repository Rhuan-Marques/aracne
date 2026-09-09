package helper

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// BODY FINGERPRINTS, AND WHY THERE ARE TWO OF THEM.
//
// A description is attached to a resource, and a resource that MOVES gets a new id in four of
// the six languages -- Python, JavaScript, TypeScript and Rust all mint the module part of an
// id from the filename. Recognising the moved code as the same code is what lets the
// description follow it, and that recognition is a hash of the resource's own source span.
//
// The hash has to be taken while the code is still on disk. The sidecar matcher computes its
// SrcSHA256 at MATCH time, from the old resource's recorded path -- which after a real `git mv`
// no longer exists, so hashSpan returns "" and the two source-hash tiers it feeds are dead
// exactly when they are needed. (TestSurvivesAFileMoveViaSourceHash only passes because the
// fixture copies the file and leaves the original behind.) So these are computed on the scan
// path, stored on the resource, and persisted.
//
// ExactHash is the span with trailing whitespace trimmed per line -- the same bytes hashSpan
// has always hashed. NormHash additionally strips comments and drops blank lines.
//
// TWO HASHES RATHER THAN ONE, because the overwhelmingly common move is verbatim and should
// not depend on the normaliser being right. ExactHash answers it. NormHash only has to carry
// "moved AND reformatted or recommented", where a conservative miss costs one description.

// WeakTierMinLines is the size below which a body may not be used by a match tier that has
// dropped an identity discriminator.
//
// WHY THE FLOOR IS PER-TIER AND NOT GLOBAL. The hash tiers say "same kind, same signature,
// same body, therefore the same resource". How much that reasoning is worth depends entirely
// on what ELSE the tier is matching on:
//
//   - moved_source keeps the name AND the enclosing type. For two different resources to
//     collide there they would have to share kind, name, parent, signature and body -- at
//     which point they are the same declaration by any reasonable reading. No floor is needed.
//   - name_source drops the parent, so ten error types' `func (e *E) Error() string { return
//     e.msg }` all land in one bucket. That is where a floor is the only thing standing
//     between a short shared body and a description on the wrong type.
//
// A GLOBAL floor was measured against aracne's own topology and refused 208 of 1,690 described
// declarations -- 12.3%, all of them the short helpers a codebase is full of. Losing move
// tracking for an eighth of the database to defend a tier that did not need defending is the
// wrong trade, so the floor sits on the tier that needs it.
const WeakTierMinLines = 3

// strDelim is one string-literal form: how it opens, how it closes, whether it may span lines,
// and whether a backslash escapes the closing delimiter inside it.
type strDelim struct {
	open   string
	close  string
	multi  bool
	escape bool
}

// langSyntax is as much of a language's lexical grammar as finding its comments requires.
// Deliberately not a tokenizer: see stripComments for why that is enough, and where it stops.
type langSyntax struct {
	lineComments []string
	blockStart   string
	blockEnd     string
	// nestedBlocks is Rust, where /* /* */ */ is one comment rather than an unterminated one.
	nestedBlocks bool
	// delims is checked in order, so longer openers must come first: """ before ", ''' before '.
	delims []strDelim
	// rustRaw enables r"...", r#"..."#, b"..." and br#"..."#.
	rustRaw bool
	// rustChar enables the lifetime-versus-char-literal decision for '.
	rustChar bool
	// jsRegex enables the regex-literal bail-out. See stripComments.
	jsRegex bool
}

var (
	goSyntax = &langSyntax{
		lineComments: []string{"//"},
		blockStart:   "/*", blockEnd: "*/",
		delims: []strDelim{
			{open: "`", close: "`", multi: true, escape: false},
			{open: `"`, close: `"`, escape: true},
			{open: "'", close: "'", escape: true},
		},
	}
	pySyntax = &langSyntax{
		lineComments: []string{"#"},
		delims: []strDelim{
			{open: `"""`, close: `"""`, multi: true, escape: true},
			{open: "'''", close: "'''", multi: true, escape: true},
			{open: `"`, close: `"`, escape: true},
			{open: "'", close: "'", escape: true},
		},
	}
	jsSyntax = &langSyntax{
		lineComments: []string{"//"},
		blockStart:   "/*", blockEnd: "*/",
		delims: []strDelim{
			{open: "`", close: "`", multi: true, escape: true},
			{open: `"`, close: `"`, escape: true},
			{open: "'", close: "'", escape: true},
		},
		jsRegex: true,
	}
	rustSyntax = &langSyntax{
		lineComments: []string{"//"},
		blockStart:   "/*", blockEnd: "*/",
		nestedBlocks: true,
		delims: []strDelim{
			{open: `"`, close: `"`, multi: true, escape: true},
		},
		rustRaw:  true,
		rustChar: true,
	}
	javaSyntax = &langSyntax{
		lineComments: []string{"//"},
		blockStart:   "/*", blockEnd: "*/",
		delims: []strDelim{
			{open: `"""`, close: `"""`, multi: true, escape: true},
			{open: `"`, close: `"`, escape: true},
			{open: "'", close: "'", escape: true},
		},
	}
)

// syntaxFor resolves a resource's language to its lexical grammar, falling back to the file
// extension when the language field is empty -- which it is for resources written before the
// language column existed, and for anything normalizeTopologyLanguages has not reached yet.
//
// An unknown language returns nil, which makes normalization whitespace-only. That is the safe
// answer: no comments are stripped, so nothing can be stripped WRONGLY.
func syntaxFor(language, path string) *langSyntax {
	switch language {
	case "go":
		return goSyntax
	case "python":
		return pySyntax
	case "javascript", "typescript":
		return jsSyntax
	case "rust":
		return rustSyntax
	case "java":
		return javaSyntax
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return goSyntax
	case ".py", ".pyi":
		return pySyntax
	case ".js", ".mjs", ".cjs", ".jsx", ".ts", ".tsx", ".mts", ".cts":
		return jsSyntax
	case ".rs":
		return rustSyntax
	case ".java":
		return javaSyntax
	}
	return nil
}

// stripState carries lexical state ACROSS lines, because a raw string, a triple-quoted
// docstring, a Java text block and a block comment all span them.
type stripState struct {
	syn *langSyntax
	// blockDepth is 0 outside a block comment. Only Rust ever exceeds 1.
	blockDepth int
	// inStr is the delimiter currently open, or the zero value when in code.
	inStr    bool
	strClose string
	strMulti bool
	strEsc   bool
}

// stripComments removes comments from one line and returns the code that is left.
//
// A LINE SCANNER, NOT A TOKENIZER, AND WHY THAT IS ENOUGH. The scanners upstream already hold
// real parsers -- go/ast for Go, tree-sitter for JS/TS/Rust/Java -- but none of them records
// comment positions in the topology, threading "give me the comment ranges for this span"
// through all six is a large change to every scanner, and pyscanner could not answer at all:
// it shells out to python3 and gets back a JSON AST with no comment nodes in it.
//
// WHICH DIRECTION THE ERRORS HAVE TO POINT. Failing to strip a comment costs a missed match:
// the resource moved, the hash differs, the description is regenerated. Stripping something
// that is NOT a comment is far worse -- take `//` seriously inside a string and
// `x := "a//b"` and `x := "a//c"` both normalise to `x := "a`, so two different functions get
// one hash and a description lands on the wrong resource, silently.
//
// So the rule is: strip only what can be PROVEN to be a comment, and when a construct cannot
// be classified with confidence, emit the line verbatim. Both genuinely ambiguous cases in
// these five grammars -- a JavaScript `/` that may open a regex, and a Rust `'` that may open
// a lifetime rather than a char literal -- resolve that way.
func (st *stripState) stripComments(line string) string {
	syn := st.syn
	if syn == nil {
		return line
	}
	var out strings.Builder
	n := len(line)
	for i := 0; i < n; {
		switch {
		case st.blockDepth > 0:
			if syn.nestedBlocks && strings.HasPrefix(line[i:], syn.blockStart) {
				st.blockDepth++
				i += len(syn.blockStart)
				continue
			}
			if strings.HasPrefix(line[i:], syn.blockEnd) {
				st.blockDepth--
				i += len(syn.blockEnd)
				continue
			}
			i++

		case st.inStr:
			if st.strEsc && line[i] == '\\' && i+1 < n {
				out.WriteString(line[i : i+2])
				i += 2
				continue
			}
			if strings.HasPrefix(line[i:], st.strClose) {
				out.WriteString(st.strClose)
				i += len(st.strClose)
				st.inStr = false
				continue
			}
			out.WriteByte(line[i])
			i++

		default:
			adv, verbatim := st.stepCode(line, i, &out)
			if verbatim {
				// Unclassifiable: emit the remainder untouched and give up on this line.
				out.WriteString(line[i:])
				return out.String()
			}
			if adv == 0 {
				// A comment opened and runs to end of line.
				return out.String()
			}
			i += adv
		}
	}
	// An unterminated single-line string is broken source, not state worth carrying forward.
	if st.inStr && !st.strMulti {
		st.inStr = false
	}
	return out.String()
}

// stepCode handles one position in code state. It returns how far to advance, or verbatim=true
// to abandon classification of this line, or 0 to mean "a line comment starts here".
func (st *stripState) stepCode(line string, i int, out *strings.Builder) (adv int, verbatim bool) {
	syn := st.syn
	rest := line[i:]

	if syn.rustRaw {
		if open, closeTok, ok := rustRawOpener(line, i); ok {
			out.WriteString(open)
			st.inStr, st.strClose, st.strMulti, st.strEsc = true, closeTok, true, false
			return len(open), false
		}
	}

	if syn.rustChar && line[i] == '\'' {
		if isRustLifetime(line, i) {
			out.WriteByte('\'')
			return 1, false
		}
		out.WriteByte('\'')
		st.inStr, st.strClose, st.strMulti, st.strEsc = true, "'", false, true
		return 1, false
	}

	for _, d := range syn.delims {
		if strings.HasPrefix(rest, d.open) {
			out.WriteString(d.open)
			st.inStr, st.strClose, st.strMulti, st.strEsc = true, d.close, d.multi, d.escape
			return len(d.open), false
		}
	}

	// Comment markers are settled BEFORE the regex question, because neither `//` nor `/*` can
	// open a regex in JavaScript -- and asking the other way round would make the commonest
	// comment of all, a whole line of `// ...`, look like a regex at the start of a line and
	// survive unstripped.
	for _, lc := range syn.lineComments {
		if strings.HasPrefix(rest, lc) {
			return 0, false
		}
	}
	if syn.blockStart != "" && strings.HasPrefix(rest, syn.blockStart) {
		st.blockDepth = 1
		return len(syn.blockStart), false
	}

	// What is left is a lone `/`, which is either division or the start of a regex, and
	// telling those apart needs the parse state a line scanner does not have. `/https:\/\//`
	// contains a literal `//` that a naive strip would cut the line at, so an unresolvable
	// `/` abandons the line rather than guessing.
	if syn.jsRegex && line[i] == '/' && regexPossible(line, i) {
		return 0, true
	}

	out.WriteByte(line[i])
	return 1, false
}

// rustRawOpener recognises a Rust raw-string opener at i -- r"…", r#"…"#, r##"…"##, br#"…"# --
// and returns the opening token plus the closing token that ends it.
//
// The hash count is part of the delimiter and has to be carried, or r##"…"# would be read as
// closed one hash early. A plain b"…" is deliberately NOT matched here: it is an ordinary
// escaped string and the generic delimiter table handles it, whereas treating it as raw would
// let b"\"" terminate at the escaped quote.
func rustRawOpener(line string, i int) (open, closeTok string, ok bool) {
	if i > 0 && isIdentByte(line[i-1]) {
		return "", "", false // the r is the tail of an identifier, not a prefix
	}
	j := i
	if j < len(line) && line[j] == 'b' {
		j++
	}
	if j >= len(line) || line[j] != 'r' {
		return "", "", false
	}
	j++
	hashes := 0
	for j < len(line) && line[j] == '#' {
		hashes++
		j++
	}
	if j >= len(line) || line[j] != '"' {
		return "", "", false
	}
	j++
	return line[i:j], `"` + strings.Repeat("#", hashes), true
}

// isRustLifetime reports whether the quote at i opens a lifetime (`'a`, `'static`) rather than
// a char literal (`'a'`, `'\n'`).
//
// Getting this wrong in the lifetime direction would open a string that never closes and
// swallow the rest of the declaration, so the test is on the char-literal side: a backslash
// means an escape sequence, and a single character followed by a closing quote means a char.
// Anything else is a lifetime. Decoded as a rune, not a byte, because 'é' is two bytes and a
// byte-wise check would call it a lifetime and then eat the line.
func isRustLifetime(line string, i int) bool {
	rest := line[i+1:]
	if rest == "" {
		return true
	}
	if rest[0] == '\\' {
		return false // '\n', '\'', '\u{1F600}'
	}
	_, size := utf8.DecodeRuneInString(rest)
	return len(rest) <= size || rest[size] != '\''
}

// regexPossible reports whether a `/` at i could begin a regex literal rather than division.
//
// The classic JavaScript ambiguity, decided the only way a line scanner can: by what came
// before. A regex may follow an operator, an opening bracket, a separator or a keyword; it may
// not follow a value. Callers treat `true` as "cannot classify this line", so the bias here is
// deliberately toward saying yes.
func regexPossible(line string, i int) bool {
	j := i - 1
	for j >= 0 && (line[j] == ' ' || line[j] == '\t') {
		j--
	}
	if j < 0 {
		return true // nothing before it on this line
	}
	switch line[j] {
	case ')', ']', '}':
		return false // a call, an index or a block result: division
	case '"', '\'', '`':
		return false
	}
	if isIdentByte(line[j]) {
		// An identifier or a number divides -- unless it is one of the keywords a regex may
		// legally follow.
		k := j
		for k >= 0 && isIdentByte(line[k]) {
			k--
		}
		switch line[k+1 : j+1] {
		case "return", "typeof", "case", "in", "of", "new", "delete", "void", "throw",
			"do", "else", "yield", "await":
			return true
		}
		return false
	}
	return true
}

func isIdentByte(b byte) bool {
	return b == '_' || b == '$' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

// NormalizeBody strips comments from a source span and drops blank lines, returning the lines
// that carry code. Trailing whitespace goes too, so a re-indent or a stray tab does not change
// the result.
func NormalizeBody(language, path string, lines []string) []string {
	st := &stripState{syn: syntaxFor(language, path)}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		code := strings.TrimRight(st.stripComments(line), " \t\r")
		if strings.TrimSpace(code) == "" {
			continue
		}
		out = append(out, code)
	}
	return out
}

// BodyHashes fingerprints a source span both ways -- exact (trailing whitespace trimmed) and
// normalized (comments and blank lines gone) -- and reports how many lines the normalized body
// came to.
//
// normLines is the SIZE the tier floor is judged on, and it counts only lines that carry at
// least one letter or digit, after normalization. Both exclusions are load-bearing:
//
//   - after normalization, because a declaration wrapped in twenty lines of comment around
//     `return nil` is a one-line body wearing a disguise, and a raw span would wave it through;
//   - letters or digits, because in a brace language a body's closing `}` is a line that says
//     nothing. Counting punctuation put `func Todo() { panic("not implemented") }` at three
//     lines, which is every stub in the repository clearing a three-line floor.
//
// A body that normalizes to nothing is not fingerprinted at all. Every empty body in the
// repository would otherwise hash identically and fill one bucket.
func BodyHashes(language, path string, lines []string) (exact, norm string, normLines int) {
	normalized := NormalizeBody(language, path, lines)
	if len(normalized) == 0 {
		return "", "", 0
	}
	for _, l := range normalized {
		if hasAlnum(l) {
			normLines++
		}
	}
	return hashLines(lines, true), hashLines(normalized, false), normLines
}

// hasAlnum reports whether a line carries any letter or digit -- i.e. whether it says anything
// beyond punctuation.
func hasAlnum(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

// hashLines is the shared digest. trimRight is for the exact form, whose input is raw source;
// normalized lines have already been trimmed.
func hashLines(lines []string, trimRight bool) string {
	h := sha256.New()
	for _, l := range lines {
		if trimRight {
			l = strings.TrimRight(l, " \t\r")
		}
		io.WriteString(h, l)
		io.WriteString(h, "\n")
	}
	return hex.EncodeToString(h.Sum(nil))
}

// StampBodyHashes fills ExactHash and NormHash on the resources of `files`, reading each file
// at most once. A nil `files` stamps everything, which is what a full scan wants.
//
// WHY IT IS A SEPARATE PASS AND NOT THE SCANNERS' JOB. Six scanners produce resources, and
// asking each to fingerprint its own would be six copies of a rule that has to agree exactly
// -- two scanners disagreeing about whitespace would silently stop descriptions crossing
// between their languages. The span and the path are already on the domain resource by the
// time the parse is done, so one pass over the graph can do it once, for everyone.
//
// `files` holds absolute paths, matching Location.Path and the id of a file resource.
func StampBodyHashes(topo *domain.Topology, files map[string]bool) {
	if topo == nil {
		return
	}
	cache := newLineCache()
	for id, res := range topo.Resources {
		if files != nil && !files[resourcePath(res)] {
			continue
		}
		exact, norm, n := cache.bodyHashes(res)
		if res.ExactHash == exact && res.NormHash == norm && res.NormLines == n {
			continue
		}
		res.ExactHash, res.NormHash, res.NormLines = exact, norm, n
		topo.Resources[id] = res
	}
}

// StampBodyHashesSlice is StampBodyHashes for a scoped delta -- the Phase-3 partial path,
// which never builds a whole topology and hands the writer a plain slice of upserts.
//
// It has to exist, and forgetting it is a quiet data-loss bug rather than a missing feature:
// the resources in that slice come fresh out of a re-parse with no fingerprints on them, the
// hashes are part of the row, so the write would REPLACE a correctly stamped row with an
// unstamped one. The next move of that code would then have nothing to match on, and nothing
// would report that anything was lost.
func StampBodyHashesSlice(upserts []domain.Resource) {
	cache := newLineCache()
	for i := range upserts {
		exact, norm, n := cache.bodyHashes(upserts[i])
		upserts[i].ExactHash, upserts[i].NormHash, upserts[i].NormLines = exact, norm, n
	}
}

// RestampUnchangedRows repairs the fingerprints the scoped partial path cannot see.
//
// THE PROBLEM IT SOLVES. That path decides what to rewrite by diffing signatures across a
// round trip through the TYPED language topology (golang.ToGeneric), and the typed structs
// carry no hash fields -- so both sides of its diff read empty and the hashes contribute
// nothing to it. An edit that changes only a BODY, leaving the span, the name and the
// signature alone, therefore produces no upsert at all, and the row keeps the fingerprint of
// the body that used to be there. Nothing reports it; the code simply stops being findable
// after a later move.
//
// THE REPAIR. A resource whose span did not move is still described accurately by its stored
// row -- every column except the hashes. So for each of the file's rows that the delta does
// not already carry, re-fingerprint the source at that span and, when it differs from what is
// stored, add the row back with corrected hashes. A resource whose span DID move is already in
// the delta (starts_at and ends_at are in the signature) and is stamped there.
func RestampUnchangedRows(dbPath, absPath string, upserts []domain.Resource) []domain.Resource {
	stored, err := ReadResourcesByFile(dbPath, absPath)
	if err != nil || len(stored) == 0 {
		return upserts
	}
	inDelta := make(map[string]bool, len(upserts))
	for _, u := range upserts {
		inDelta[u.ID] = true
	}
	cache := newLineCache()
	for id, res := range stored {
		if inDelta[id] {
			continue
		}
		exact, norm, n := cache.bodyHashes(res)
		if res.ExactHash == exact && res.NormHash == norm && res.NormLines == n {
			continue
		}
		res.ExactHash, res.NormHash, res.NormLines = exact, norm, n
		upserts = append(upserts, res)
	}
	return upserts
}

// bodyHashes fingerprints one resource's span, or the whole file when it has no line range
// (files, packages, dependencies). Unreadable source yields two empty hashes rather than an
// error: a missing fingerprint costs a match tier, and nothing else.
func (c *lineCache) bodyHashes(res domain.Resource) (exact, norm string, normLines int) {
	span := c.spanLines(res)
	if len(span) == 0 {
		return "", "", 0
	}
	return BodyHashes(res.Language, resourcePath(res), span)
}

// spanLines returns the source lines a resource occupies, clamped to the file.
func (c *lineCache) spanLines(res domain.Resource) []string {
	path := resourcePath(res)
	if path == "" {
		return nil
	}
	lines := c.lines(path)
	if lines == nil {
		return nil
	}
	start, end := res.Location.StartsAt, res.Location.EndsAt
	if start <= 0 || end <= 0 || start > end {
		start, end = 1, len(lines)
	}
	if start > len(lines) {
		return nil
	}
	if end > len(lines) {
		end = len(lines)
	}
	return lines[start-1 : end]
}
