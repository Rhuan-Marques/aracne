package helper

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"aracne/internal/topology/domain"
)

// Holds a resource description, its name, language, and source location, plus the
// Python-only body/docstring positions captured by the scanner.
type resourceEntry struct {
	Name        string
	Description string
	Loc         domain.Location
	Language    string
	// Python only (1-based; 0 = absent). BodyLine is the first body statement;
	// DocStart..DocEnd is an existing docstring to replace.
	BodyLine int
	DocStart int
	DocEnd   int
}

// Writes resource descriptions from topology into source files at their definition
// locations, using each file's language-appropriate documentation style.
func ApplyDescriptions(topo *domain.Topology) error {
	fileResources := make(map[string][]resourceEntry)

	for _, res := range topo.Resources {
		if res.Description == "" {
			continue
		}
		switch res.Kind {
		case domain.ResourceFunction, domain.ResourceMethod, domain.ResourceType, domain.ResourceNamedType, domain.ResourceInterface, domain.ResourceVariable:
		default:
			continue
		}
		if res.Location.Path == "" || res.Location.StartsAt == 0 {
			continue
		}
		fileResources[res.Location.Path] = append(fileResources[res.Location.Path], resourceEntry{
			Name:        res.Name,
			Description: res.Description,
			Loc:         res.Location,
			Language:    res.Language,
			BodyLine:    intProp(res.Properties, "py_body_line"),
			DocStart:    intProp(res.Properties, "py_doc_start"),
			DocEnd:      intProp(res.Properties, "py_doc_end"),
		})
	}

	for path, entries := range fileResources {
		if err := applyFile(path, entries); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: %s: %v\n", path, err)
		}
	}

	return nil
}

// Inserts resource descriptions into one source file using its language's style.
func applyFile(path string, entries []resourceEntry) error {
	style, ok := styleForFile(entries, path)
	if !ok {
		fmt.Fprintf(os.Stderr, "Warning: %s: unrecognized language, skipping descriptions apply\n", path)
		return nil
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Loc.StartsAt > entries[j].Loc.StartsAt
	})

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}

	lines := strings.Split(string(data), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	for _, entry := range entries {
		var next []string
		if style.docstring {
			next = insertDocstring(lines, entry)
		} else {
			next = insertAbove(lines, entry, style)
		}
		if next != nil {
			lines = next
		}
	}

	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0644)
}

// docStyle describes how one language renders and recognizes documentation.
type docStyle struct {
	// render returns the (un-indented) documentation lines for an
	// above-declaration placement.
	render func(desc string) []string
	// isDoc reports whether a line belongs to an existing doc block to replace.
	isDoc func(line string) bool
	// isDirective reports a compiler directive line that must stay directly above
	// the declaration and never be treated as documentation (Go only).
	isDirective func(line string) bool
	// docstring selects the Python in-body docstring placement.
	docstring bool
}

// styleForFile picks the documentation style for a file from its resources'
// language (all share one), falling back to the path extension.
func styleForFile(entries []resourceEntry, path string) (docStyle, bool) {
	lang := ""
	for _, e := range entries {
		if e.Language != "" {
			lang = e.Language
			break
		}
	}
	if lang == "" {
		lang = languageFromExt(path)
	}
	switch lang {
	case "go":
		return docStyle{render: renderLineComment, isDoc: isCommentLine, isDirective: isDirectiveLine}, true
	case "javascript", "typescript":
		return docStyle{render: renderJSDoc, isDoc: isJSDocLine}, true
	case "python":
		return docStyle{docstring: true}, true
	}
	return docStyle{}, false
}

// languageFromExt maps a file extension to a scanner language, or "" if unknown.
func languageFromExt(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".js", ".jsx", ".mjs", ".cjs":
		return "javascript"
	case ".ts", ".tsx", ".mts", ".cts":
		return "typescript"
	}
	return ""
}

// insertAbove writes the description as a doc block immediately above the
// declaration at entry.Loc.StartsAt, using style. It returns nil (skip) when the
// location looks stale, keeps compiler directives intact, and replaces only a doc
// block sitting immediately above the declaration (no blank-line crossing).
func insertAbove(lines []string, entry resourceEntry, style docStyle) []string {
	idx := entry.Loc.StartsAt - 1
	if idx < 0 || idx >= len(lines) {
		return nil
	}
	// Guard against stale/out-of-date locations: the target line must actually
	// reference this declaration. Otherwise an off-by-one location would insert the
	// comment inside the body. Skip rather than corrupt.
	if entry.Name != "" && !strings.Contains(lines[idx], entry.Name) {
		return nil
	}

	// Keep compiler directives (//go:embed, …) directly above the declaration; the
	// doc comment belongs above those.
	insertAt := idx
	if style.isDirective != nil {
		for insertAt > 0 && style.isDirective(lines[insertAt-1]) {
			insertAt--
		}
	}

	// The existing doc block is the contiguous run of doc lines immediately above
	// insertAt (no intervening blank line, directives excluded). Replace it when
	// present, otherwise insert fresh. We do NOT cross a blank line: a comment
	// separated by a blank line is a section banner, not a doc comment.
	commentEnd := insertAt
	commentStart := insertAt
	for commentStart > 0 && style.isDoc(lines[commentStart-1]) &&
		!(style.isDirective != nil && style.isDirective(lines[commentStart-1])) {
		commentStart--
	}

	indent := leadingWhitespace(lines[idx])
	rendered := style.render(entry.Description)
	indented := make([]string, len(rendered))
	for i, l := range rendered {
		if l == "" {
			indented[i] = ""
		} else {
			indented[i] = indent + l
		}
	}

	result := make([]string, 0, len(lines)-(commentEnd-commentStart)+len(indented))
	result = append(result, lines[:commentStart]...)
	result = append(result, indented...)
	result = append(result, lines[commentEnd:]...)
	return result
}

// insertDocstring writes the description as a Python docstring for the function or
// class described by entry. It replaces an existing docstring (entry.DocStart > 0)
// or inserts a new one as the first body statement. Returns nil (skip) on a stale
// or unsupported location.
func insertDocstring(lines []string, entry resourceEntry) []string {
	idx := entry.Loc.StartsAt - 1
	if idx < 0 || idx >= len(lines) {
		return nil
	}
	if entry.Name != "" && !strings.Contains(lines[idx], entry.Name) {
		return nil
	}

	if entry.DocStart > 0 {
		s, e := entry.DocStart-1, entry.DocEnd-1
		if s < 0 || e >= len(lines) || s > e {
			return nil
		}
		rendered := renderDocstring(entry.Description, leadingWhitespace(lines[s]))
		result := make([]string, 0, len(lines)-(e-s+1)+len(rendered))
		result = append(result, lines[:s]...)
		result = append(result, rendered...)
		result = append(result, lines[e+1:]...)
		return result
	}

	if entry.BodyLine <= 0 || entry.BodyLine-1 >= len(lines) {
		return nil
	}
	bodyIdx := entry.BodyLine - 1
	// A single-line body (def f(): return 1) shares the declaration line, so a
	// separate docstring line cannot be inserted safely — skip.
	if bodyIdx <= idx {
		return nil
	}
	rendered := renderDocstring(entry.Description, leadingWhitespace(lines[bodyIdx]))
	result := make([]string, 0, len(lines)+len(rendered))
	result = append(result, lines[:bodyIdx]...)
	result = append(result, rendered...)
	result = append(result, lines[bodyIdx:]...)
	return result
}

// intProp reads an integer-valued property, tolerating JSON float64 decoding.
func intProp(props map[string]any, key string) int {
	if props == nil {
		return 0
	}
	switch v := props[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return 0
}

// isDirectiveLine reports whether a line is a Go compiler directive comment
// (e.g. //go:embed, //go:build) that must not be treated as documentation.
func isDirectiveLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "//") {
		return false
	}
	return isDirectiveComment(trimmed[2:])
}

// isDirectiveComment reports whether comment text (without the leading "//") is a
// Go compiler directive such as "go:embed", "go:build", "line 5", or "export Foo".
func isDirectiveComment(c string) bool {
	if strings.HasPrefix(c, "line ") || strings.HasPrefix(c, "extern ") || strings.HasPrefix(c, "export ") {
		return true
	}
	colon := strings.Index(c, ":")
	if colon <= 0 || colon+1 >= len(c) {
		return false
	}
	for i := 0; i <= colon+1; i++ {
		if i == colon {
			continue
		}
		b := c[i]
		if !('a' <= b && b <= 'z' || '0' <= b && b <= '9') {
			return false
		}
	}
	return true
}

// isCommentLine reports whether a line is a Go/JS line or block comment.
func isCommentLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*")
}

// isJSDocLine reports whether a line belongs to an existing JS/TS doc comment: a //
// line comment, a /* or /** block opener, or a "*"/"*/" continuation or closer.
func isJSDocLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "//") ||
		strings.HasPrefix(trimmed, "/*") ||
		strings.HasPrefix(trimmed, "*") ||
		strings.HasSuffix(trimmed, "*/")
}

// leadingWhitespace extracts the leading spaces and tabs of a line.
func leadingWhitespace(line string) string {
	for i, c := range line {
		if c != ' ' && c != '\t' {
			return line[:i]
		}
	}
	return ""
}

// renderLineComment renders a description as Go-style // line comments.
func renderLineComment(desc string) []string {
	parts := strings.Split(desc, "\n")
	out := make([]string, len(parts))
	for i, line := range parts {
		if line == "" {
			out[i] = "//"
		} else {
			out[i] = "// " + line
		}
	}
	return out
}

// renderJSDoc renders a description as a JSDoc comment: a single-line /** ... */ for
// one line, otherwise a /**, " * line", " */" block.
func renderJSDoc(desc string) []string {
	parts := strings.Split(desc, "\n")
	if len(parts) == 1 {
		return []string{"/** " + parts[0] + " */"}
	}
	out := make([]string, 0, len(parts)+2)
	out = append(out, "/**")
	for _, line := range parts {
		if line == "" {
			out = append(out, " *")
		} else {
			out = append(out, " * "+line)
		}
	}
	out = append(out, " */")
	return out
}

// renderDocstring renders a description as an indented Python docstring. A single
// line yields one """...""" line; multiple lines yield a triple-quoted block.
func renderDocstring(desc, indent string) []string {
	quote := `"""`
	if strings.Contains(desc, `"""`) {
		quote = `'''`
	}
	parts := strings.Split(desc, "\n")
	if len(parts) == 1 {
		line := parts[0]
		// Avoid an ambiguous closing quote when the text ends in a quote char.
		if strings.HasSuffix(line, `"`) || strings.HasSuffix(line, `'`) {
			line += " "
		}
		return []string{indent + quote + line + quote}
	}
	out := make([]string, 0, len(parts)+2)
	out = append(out, indent+quote)
	for _, line := range parts {
		if line == "" {
			out = append(out, "")
		} else {
			out = append(out, indent+line)
		}
	}
	out = append(out, indent+quote)
	return out
}
