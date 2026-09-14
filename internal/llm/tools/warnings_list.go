package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// WarningReader renders the full source of the code a list of warnings names, capped at
// features.warning_read_limit, plus the note saying how many warnings it left out. It returns
// "" when there is nothing to add.
//
// INJECTED RATHER THAN CALLED. The read path lives in universaltools, which imports this
// package -- so this package cannot import it back, and the tool cannot build its own read.
// The wiring that constructs the registry is above both (see cli.BuildToolRegistry) and
// supplies the closure there.
type WarningReader func(warnings []domain.TopologyWarning) string

// Exposes topology warnings to LLM tools via a TopologyManager.
type WarningsList struct {
	mgr *topology.TopologyManager
	// reads is the expansion behind features.warning_reads. NIL IS THE SWITCHED-OFF STATE,
	// and it switches off the tool's `read` PARAMETER too, not just its effect: a schema
	// property is re-sent on every request, so advertising an option this project cannot
	// honour would charge every turn for a capability that does nothing.
	reads WarningReader
}

// Creates a WarningsList tool for retrieving topology consistency warnings.
func NewWarningsList(mgr *topology.TopologyManager) *WarningsList {
	return &WarningsList{mgr: mgr}
}

// WithReads turns on the `read` parameter, backed by fn. Returns the receiver so it can be
// chained onto NewWarningsList. A nil fn leaves the option off.
func (w *WarningsList) WithReads(fn WarningReader) *WarningsList {
	w.reads = fn
	return w
}

// Returns the tool name "warnings_list".
func (w *WarningsList) Name() string {
	return "warnings_list"
}

// Returns the description of the warnings_list tool explaining its purpose and supported filters.
func (w *WarningsList) Description() string {
	d := "List outstanding topology warnings: missing references, removed resources, changed signatures."
	if w.reads != nil {
		d += " Pass read=true to get the source of the warned code back with the list, and fix it " +
			"without a separate read."
	}
	return d
}

// Returns optional filter parameters for warnings_list: source_id, target_id, kind, and --
// where the project enabled it -- read.
func (w *WarningsList) Parameters() []Parameter {
	params := []Parameter{
		{Name: "source_id", Type: "string", Description: "Filter by source resource ID", Required: false},
		{Name: "target_id", Type: "string", Description: "Filter by target resource ID", Required: false},
		{Name: "kind", Type: "string", Description: "Kind: use_missing_node | node_removed | signature_changed | interface_conflict", Required: false},
	}
	if w.reads != nil {
		params = append(params, Parameter{
			Name: "read", Type: "boolean", Required: false,
			Description: "Return the full source of the code the first few warnings name (the count is " +
				"features.warning_read_limit), so they can be fixed from this reply. Call again after " +
				"fixing them for the next batch.",
		})
	}
	return params
}

// Executes warnings_list query, returning filtered topology warnings grouped by kind with summary and details.
func (w *WarningsList) Run(args json.RawMessage) (string, error) {
	var params struct {
		SourceID string `json:"source_id"`
		TargetID string `json:"target_id"`
		Kind     string `json:"kind"`
		Read     bool   `json:"read"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	var kind domain.WarningKind
	if params.Kind != "" {
		kind = domain.WarningKind(params.Kind)
	}

	warnings, err := w.mgr.ListWarnings(params.SourceID, params.TargetID, kind)
	if err != nil {
		return "", fmt.Errorf("error reading warnings: %w", err)
	}
	return w.render(warnings, params.Read), nil
}

// render turns a warning list into the tool's reply, optionally with the read appended.
//
// Split from Run so the two decisions that have nothing to do with the database -- the order
// the list goes out in, and whether the expander is called -- can be exercised without one.
func (w *WarningsList) render(warnings []domain.TopologyWarning, read bool) string {
	if len(warnings) == 0 {
		return "No outstanding warnings."
	}

	// The shared total order, not a local one: `read` expands the FIRST N of this list and
	// promises the next call continues where it stopped. See domain.SortWarnings.
	domain.SortWarnings(warnings)

	counts := make(map[domain.WarningKind]int)
	for _, warning := range warnings {
		counts[warning.Kind]++
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Found %d warning(s):\n\n", len(warnings)))
	b.WriteString("Summary:\n")
	for _, kind := range []domain.WarningKind{domain.WarnUseMissingNode, domain.WarnNodeRemoved, domain.WarnSignatureChanged,
		domain.WarnInterfaceConflict} {
		if count := counts[kind]; count > 0 {
			b.WriteString(fmt.Sprintf("  - %s: %d\n", kind, count))
		}
	}
	b.WriteString("\nDetails:\n")
	for _, warning := range warnings {
		b.WriteString(fmt.Sprintf("  [%s] %s\n    source: %s", warning.Kind, warning.Message, warning.SourceID))
		if warning.TargetID != "" {
			b.WriteString(fmt.Sprintf("\n    target: %s", warning.TargetID))
		}
		b.WriteString("\n\n")
	}

	// `read` on a project that left features.warning_reads off is not reachable -- the
	// parameter is absent from the schema -- so a model that sent it anyway is answered with
	// the listing alone rather than an error about a key it was never offered.
	if read && w.reads != nil {
		if section := w.reads(warnings); section != "" {
			b.WriteString(section)
			b.WriteString("\n")
		}
	}

	return b.String()
}
