package warnread

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/llm/languages/readunit"
	"github.com/Rhuan-Marques/aracne/internal/llm/languages/universaltools"
	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// anchorFor is the ONE line a warning points at, and the note to hang on it.
//
// COMPUTED, NOT STORED, because nothing stores it: a domain.TopologyWarning is two resource
// ids, a kind and a message, and positions live only on domain.Resource.Location -- which
// describes a whole declaration, never a call site. Connections are a bare
// map[string][]string with no positions either, so even the edge that raised the warning
// cannot say where it was.
//
// Recomputing against the file as it is now is also the only version that can be RIGHT. A line
// number recorded when the warning was raised is stale the moment anything above it moves, and
// warning_carry re-raises warnings across rescans, so a carried-forward line would confidently
// point at the wrong code.
//
// ok is false when the fix site is not in the graph, its source cannot be cut, or no line in
// it discriminates -- in which case the caller reports the warning without a code block rather
// than marking a line it guessed at.
func anchorFor(mgr *topology.TopologyManager, topo *domain.Topology,
	w domain.TopologyWarning) (id string, ann readunit.Annotation, ok bool) {

	site := ""
	if targets := readTargets(w); len(targets) > 0 {
		site = targets[0]
	}
	res, held := topo.Resources[site]
	if !held {
		return "", readunit.Annotation{}, false
	}
	entry, err := mgr.Cut(res.Location)
	if err != nil || entry == nil {
		return "", readunit.Annotation{}, false
	}
	lines := strings.Split(entry.Cut, "\n")

	line, found := "", false
	switch w.Kind {
	case domain.WarnInterfaceConflict:
		// The thing to go fix is the implementer's own declaration, so the declaration line
		// is the anchor -- there is no "call site" for a promise a type failed to keep.
		line, found = declarationLine(lines, res.Name)
	default:
		// A reference warning is about a USE, so anchor on the use. The name searched for is
		// the counterpart's, not the site's: for use_missing_node and node_removed that is the
		// target that is gone, for signature_changed the callee whose shape moved.
		other := w.TargetID
		if w.Kind == domain.WarnSignatureChanged {
			other = w.SourceID
		}
		line, found = referenceLine(lines, shortName(other))
		if !found {
			// A reference through an alias, a re-export, or a dot import spelled some other
			// way. The declaration still puts the reader in the right function.
			line, found = declarationLine(lines, res.Name)
		}
	}
	if !found {
		return "", readunit.Annotation{}, false
	}
	return site, readunit.Annotation{Line: line, Note: noteFor(w, topo)}, true
}

// declarationLine is the first line of a cut that actually declares something: non-blank, not
// a comment, and naming the resource. Falls back to the first non-comment line, which is what
// a language whose declaration does not repeat the name needs.
func declarationLine(lines []string, name string) (string, bool) {
	fallback, haveFallback := "", false
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" || universaltools.IsCommentLine(t) {
			continue
		}
		if !haveFallback {
			fallback, haveFallback = l, true
		}
		if name != "" && wordRe(name).MatchString(l) {
			return l, true
		}
	}
	return fallback, haveFallback
}

// referenceLine is the first line of a cut that USES name.
//
// COMMENTS ARE NOT REFERENCES, and that is not a nicety. testing_ground/go/dotimport is the
// proof: the doc comment above Shout reads "upper-cases its argument using the DOT-IMPORTED
// ToUpper", it sits inside the Go function cut, and a plain search marks the prose instead of
// the call under it.
func referenceLine(lines []string, name string) (string, bool) {
	if name == "" {
		return "", false
	}
	re := wordRe(name)
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" || universaltools.IsCommentLine(t) {
			continue
		}
		if re.MatchString(l) {
			return l, true
		}
	}
	return "", false
}

// wordRe matches name only as a whole identifier, so `Add` does not match `Address`.
func wordRe(name string) *regexp.Regexp {
	return regexp.MustCompile(`(^|[^\p{L}\p{N}_])` + regexp.QuoteMeta(name) + `($|[^\p{L}\p{N}_])`)
}

// shortName is the last segment of a resource id -- the spelling the source actually uses.
// Ids are module paths (`example.com/m/lib.Add`), FQNs (`com.acme.Shape#area`) or Rust paths
// (`crate::shapes::Circle`), and the code calls the tail of them.
func shortName(id string) string {
	for _, sep := range []string{".", "::", "#", "/"} {
		if i := strings.LastIndex(id, sep); i >= 0 {
			id = id[i+len(sep):]
		}
	}
	return id
}

// noteFor is what gets written after the arrow: short, because the line it sits on already
// says which code is broken, and the summary that used to repeat that is gone.
func noteFor(w domain.TopologyWarning, topo *domain.Topology) string {
	body := ""
	switch w.Kind {
	case domain.WarnUseMissingNode:
		body = fmt.Sprintf("%s does not exist", w.TargetID)
	case domain.WarnNodeRemoved:
		body = fmt.Sprintf("%s was removed", w.TargetID)
	case domain.WarnSignatureChanged:
		// Baseline is deliberately NOT quoted here. It is SignatureBaseline's machine form
		// ("Add|[{\"Name\":\"a\",...}]|[...]"), written to be compared after a revert rather
		// than read, and pasting it into a note would hand the model a wall of JSON where a
		// sentence belongs. The shape the caller must now match is in the report already --
		// readTargets reads the callee alongside the call site for exactly this reason.
		body = fmt.Sprintf("%s changed signature", w.SourceID)
		if w.Transient {
			// The whole value of a transient is that it does not claim what a stored warning
			// claims, and on THIS surface the note sits on the very line in question -- so it
			// can say the one thing the reader needs and stop. The tag carries the rest.
			body = fmt.Sprintf("%s changed signature, ignore if still fits.", w.SourceID)
		}
	case domain.WarnInterfaceConflict:
		// The message already reads as a sentence about the implementer, and the implementer
		// is the line this is being written on -- so its name leads the message redundantly.
		// Stripping it covers all three shapes helper.conformance produces: "declares it
		// implements X but does not provide m", "extends B, which declares it ...", and
		// "does not satisfy X.m: why".
		body = strings.TrimSpace(w.Message)
		if impl, ok := topo.Resources[w.TargetID]; ok && impl.Name != "" {
			body = strings.TrimPrefix(body, impl.Name+" ")
		}
		body = strings.TrimPrefix(body, "declares it ")
	default:
		// The scan-error pseudo-warning (Kind "") and anything added later. Its message is
		// all there is, and passing it through unchanged is the honest answer.
		return "[warning] " + strings.TrimSpace(w.Message)
	}
	return fmt.Sprintf("[%s] %s", domain.WarningLabel(w), body)
}
