package universaltools

import (
	"regexp"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/llm/languages/readunit"
	"github.com/Rhuan-Marques/aracne/internal/llm/languages/renderstate"
	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// skeletonBody renders a whole-file read as the file's shape rather than its text: every
// top-level declaration in source order, each shown in full when it is short and reduced to
// its signature plus an elision marker when it is not.
//
// WHY THIS EXISTS. A benchmark run measured whole-file reads returning 1.00x the bytes on
// disk, with the "# CONTEXT:" block added on top -- so a file read through aracne cost
// strictly more than `cat` and the context block was the only thing it bought. Symbol reads
// were already compact; only the file path was not.
//
// THE CONSTRAINT THAT SHAPES EVERYTHING HERE. The `edit` tool matches old_string against the
// bytes on disk. So every byte this function emits is either copied verbatim from the file or
// is an unmistakable marker -- there is no third category. A "reconstructed" or "summarized"
// signature would be exactly the text a model would paste into old_string and get a
// confusing miss from, which is why the elision marker (renderstate.ElisionMarker) opens with
// a character that begins no comment in any language aracne scans.
func skeletonBody(topo *domain.Topology, mgr *topology.TopologyManager, fileID string, smallThreshold int) (string, error) {
	members := domain.FileTopLevelMembers(topo, fileID)
	if len(members) == 0 {
		// Nothing is declared here that the topology knows about -- a config file, an
		// unsupported language. There is no skeleton to render, so the file itself is the
		// only honest answer.
		entry, err := mgr.Cut(domain.Location{Path: fileID})
		if err != nil {
			return "", err
		}
		return entry.Cut, nil
	}

	var b strings.Builder
	for _, id := range members {
		res, ok := topo.Resources[id]
		if !ok {
			continue
		}
		entry, err := mgr.Cut(res.Location)
		if err != nil {
			// One unreadable member must not cost the model the whole file. Name it and
			// keep going: the marker is already the "read this yourself" instruction.
			b.WriteString(renderstate.ElisionMarker(id, "could not be cut from the file"))
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		if countLines(entry.Cut) <= smallThreshold {
			b.WriteString(entry.Cut)
			b.WriteString("\n")
			continue
		}
		head, elided := splitSignature(entry.Cut)
		b.WriteString(head)
		if !strings.HasSuffix(head, "\n") {
			b.WriteString("\n")
		}
		if elided > 0 {
			b.WriteString(renderstate.ElisionMarker(id, pluralLines(elided)+" of body"))
		}
	}
	return b.String(), nil
}

// splitSignature returns the leading lines of a declaration to show verbatim, and how many
// lines were left out.
//
// The split is heuristic because the topology stores no body-start offset: a resource carries
// only StartsAt/EndsAt. It leans toward showing MORE, because over-inclusion costs tokens
// while under-inclusion could cut a signature mid-way. Correctness does not rest on it -- the
// elision marker does, since whatever this returns is verbatim file bytes either way.
func splitSignature(cut string) (head string, elidedLines int) {
	lines := strings.Split(strings.TrimRight(cut, "\n"), "\n")
	for i, line := range lines {
		t := strings.TrimRight(strings.TrimSpace(line), " \t")
		// A brace or colon at end of line opens the body in every language aracne scans.
		if strings.HasSuffix(t, "{") || strings.HasSuffix(t, ":") {
			return strings.Join(lines[:i+1], "\n"), len(lines) - i - 1
		}
		// A signature this long is no longer a signature -- stop guessing and show it.
		if i >= 8 {
			break
		}
	}
	if len(lines) == 0 {
		return "", 0
	}
	return lines[0], len(lines) - 1
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(strings.TrimRight(s, "\n"), "\n") + 1
}

func pluralLines(n int) string {
	if n == 1 {
		return "1 line"
	}
	return itoa(n) + " lines"
}

// itoa avoids pulling strconv in for one call site in a file that otherwise only formats.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// abridgeSymbolBody caps ONE symbol's source the way skeletonBody caps a file's declarations.
//
// WHY THIS EXISTS. Every read path had a ceiling except the one the tool actively encourages.
// read.max_file_size bounds a file; file_mode "skeleton" bounds a whole-file read; nothing at
// all bounded a symbol. Reading `['into_config', 'EngineState.merge_env']` out of nushell
// returned 75,893 bytes in compact-blocked-after-bs-20260830c -- the largest single tool
// result across two benchmark runs -- because `into_config` is one enormous function and a
// symbol read hands back the whole thing.
//
// Files are left alone here: fileUnit has already applied file_mode to them, and re-abridging
// a skeleton would elide the signatures that ARE the skeleton. The marker names the id and the
// `full: true` escape hatch, so the exact bytes are always one call away.
func abridgeSymbolBody(u readunit.Unit, maxLines int) string {
	if maxLines <= 0 || u.Kind == domain.ResourceFile || countLines(u.Body) <= maxLines {
		return u.Body
	}
	// A method's body is NOT one declaration: the enclosing type is inlined above it, so
	// splitting at the first opening brace would keep the struct and elide the method the
	// model actually asked for. Abridge the LAST top-level declaration instead, which is the
	// target in every rendering this package produces.
	lead, tail := splitLastDeclaration(u.Body)
	head, elided := splitSignature(tail)
	if elided <= 0 {
		return u.Body
	}
	if !strings.HasSuffix(head, "\n") {
		head += "\n"
	}
	return lead + head + renderstate.ElisionMarker(u.ID,
		pluralLines(elided)+" of body over the read.max_symbol_lines cap; pass full: true for the exact bytes")
}

// declStart matches the first token of a declaration in the languages aracne scans. Leading
// whitespace is allowed on purpose: Python renders a method INSIDE its class, so the
// declaration that matters is indented.
var declStart = regexp.MustCompile(`^\s*(?:pub\s+|pub\([^)]*\)\s+|async\s+|unsafe\s+|export\s+|default\s+|static\s+|final\s+|public\s+|private\s+|protected\s+|abstract\s+)*(?:func|fn|def|class|type|struct|enum|trait|impl|interface|const|var|let|function)\b`)

// splitLastDeclaration cuts a rendered body into everything before its LAST declaration and
// that declaration itself. A body with only one declaration returns ("", body), which is the
// single-symbol case and behaves exactly as no split at all.
//
// The last one, not the first, because a rendered body is not always one declaration: a method
// carries its enclosing type above it (Go, Rust) or around it (Python). Splitting at the first
// opening brace would keep the type and elide the method the model actually asked for -- the
// exact opposite of the point.
//
// Indentation is deliberately ignored rather than used to find a "top level". Ignoring it is
// safe in both directions: on a nested closure late in a long function the split lands there
// and simply elides less, which costs tokens and never costs the model its answer. Preferring
// column zero would have been wrong for Python, where the method is always indented inside its
// class and the class line would win every time.
func splitLastDeclaration(body string) (lead, last string) {
	lines := strings.Split(body, "\n")
	idx := 0
	for i, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if declStart.MatchString(l) {
			idx = i
		}
	}
	if idx == 0 {
		return "", body
	}
	return strings.Join(lines[:idx], "\n") + "\n", strings.Join(lines[idx:], "\n")
}
