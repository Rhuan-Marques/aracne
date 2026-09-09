package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	"github.com/Rhuan-Marques/aracne/internal/topology/scanner"
)

// Tool implementation for the "edit" command. Wraps a TopologyManager and scanner Registry to perform file edits and auto-update the topology database in response.
type Edit struct {
	mgr *topology.TopologyManager
	reg *scanner.Registry
}

// Creates a new Edit tool instance with the given topology manager and scanner registry. Returns a pointer to the initialized Edit struct.
func NewEdit(mgr *topology.TopologyManager, reg *scanner.Registry) *Edit {
	return &Edit{mgr: mgr, reg: reg}
}

// Returns the tool name "edit" used to register the Edit tool in the MCP tool registry.
func (e *Edit) Name() string {
	return "edit"
}

// Returns the description string for the Edit tool, explaining it replaces exact text in a file with automatic topology updates.
func (e *Edit) Description() string {
	return "Replace exact text in one or more files, or delete it by passing an empty new_string. " +
		"old_string must match exactly once unless replace_all is set. " +
		"PASS EVERY CHANGE YOU ALREADY KNOW YOU NEED IN ONE CALL via `edits`: they apply in order, " +
		"across as many files as you like, and nothing is written unless all of them match -- so a " +
		"batch is safer than a sequence of single edits as well as far cheaper. " +
		"The topology is updated automatically."
}

// Returns the parameter schema for the Edit tool.
//
// The three single-edit fields are optional here because a batch passes them inside `edits`
// instead. That loses the old schema-level guarantee that an omitted new_string could not
// silently delete code -- JSON Schema `required` cannot reach into an array of objects -- so
// parseEdits enforces presence directly, for both call forms. See editOp.NewString.
func (e *Edit) Parameters() []Parameter {
	return []Parameter{
		{Name: "edits", Type: "array", Items: "object", Required: false,
			Description: "A list of {file_path, old_string, new_string, replace_all} to apply in order. " +
				"May span several files. Preferred over the single-edit form: one call, one topology " +
				"sync per file, and all-or-nothing."},
		{Name: "file_path", Type: "string", Description: "The absolute path to the file to edit. Single-edit form; use `edits` for more than one", Required: false},
		{Name: "old_string", Type: "string", Description: "The exact text to search for and replace. Must appear exactly once unless replace_all is true", Required: false},
		{Name: "new_string", Type: "string", Description: "The replacement text. Pass an empty string to delete the matched text", Required: false},
		{Name: "replace_all", Type: "boolean", Description: "Replace every occurrence instead of requiring a unique match (default false)", Required: false},
	}
}

// editOp is one replacement inside a batch. Index is 1-based and only exists so a failure can
// name which edit failed -- with a dozen edits in a call, "old_string not found" alone would
// leave the model to bisect its own request.
type editOp struct {
	FilePath  string `json:"file_path"`
	OldString string `json:"old_string"`
	// NewString is a POINTER so that "absent" and "empty" stay distinguishable. An empty
	// new_string is a legal deletion; an omitted one is a mistake that would delete code
	// silently. JSON Schema `required` used to catch that, but it cannot reach inside the
	// `edits` array, so the check moved here -- where it now covers both call forms.
	NewString  *string `json:"new_string"`
	ReplaceAll bool    `json:"replace_all"`
	Index      int     `json:"-"`
}

// text is the replacement, once presence has been verified by parseEdits.
func (o editOp) text() string {
	if o.NewString == nil {
		return ""
	}
	return *o.NewString
}

// Run applies one edit or a whole batch.
//
// WHY BATCHING EXISTS. Measured over netguard-20260831b, the aracne arm spent 4.8 tool calls
// per task on edits against the baseline's 2.2 -- and the baseline was not editing less, it
// was editing in bulk: a single `python3` heredoc doing ten replacements in one Bash call.
// Twenty-one bursts of consecutive edits held 100 calls between them; batching per file
// collapses those to 46, and across files to 21. Extra turns were the aracne arm's one real
// regression (+33.5%, significant), and edits were the largest single contributor to it --
// larger than guard denials.
func (e *Edit) Run(args json.RawMessage) (string, error) {
	ops, err := parseEdits(args)
	if err != nil {
		return "", err
	}
	if e.mgr == nil {
		return e.applyBatch(ops, false)
	}
	// Locks are taken in sorted path order. Two agents batching the same pair of files in
	// opposite orders would otherwise deadlock, and a batch is the first thing in this tool
	// that can hold more than one lock at a time.
	paths := distinctPaths(ops)
	return e.withFileLocks(paths, func(waited bool) (string, error) {
		return e.applyBatch(ops, waited)
	})
}

// parseEdits accepts either the batch form (`edits: [...]`) or the original flat one. The flat
// form stays because it is a legal single edit and because existing callers use it; it is
// simply the one-element case.
func parseEdits(args json.RawMessage) ([]editOp, error) {
	var params struct {
		Edits      []editOp `json:"edits"`
		FilePath   string   `json:"file_path"`
		OldString  string   `json:"old_string"`
		NewString  *string  `json:"new_string"`
		ReplaceAll bool     `json:"replace_all"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	ops := params.Edits
	if len(ops) == 0 {
		// new_string is deliberately not checked: an empty one is a deletion.
		if params.FilePath == "" || params.OldString == "" {
			return nil, fmt.Errorf("missing required arguments: pass `edits`, or file_path and old_string")
		}
		ops = []editOp{{FilePath: params.FilePath, OldString: params.OldString,
			NewString: params.NewString, ReplaceAll: params.ReplaceAll}}
	}
	for i := range ops {
		ops[i].Index = i + 1
		if ops[i].FilePath == "" || ops[i].OldString == "" {
			return nil, fmt.Errorf("edit %d: file_path and old_string are required", i+1)
		}
		if ops[i].NewString == nil {
			return nil, fmt.Errorf(
				"edit %d: new_string is required — pass \"\" explicitly to delete the matched text",
				i+1)
		}
	}
	return ops, nil
}

// distinctPaths returns the absolute paths a batch touches, sorted, each once.
func distinctPaths(ops []editOp) []string {
	seen := map[string]bool{}
	var out []string
	for _, op := range ops {
		abs, err := filepath.Abs(op.FilePath)
		if err != nil {
			abs = op.FilePath
		}
		if !seen[abs] {
			seen[abs] = true
			out = append(out, abs)
		}
	}
	sort.Strings(out)
	return out
}

// withFileLocks holds every path's lock at once by nesting WithFileLock in the order given.
// `waited` is true if ANY of them had to queue, which is what the stale-old_string hint keys
// off: some other agent moved at least one of these files under us.
func (e *Edit) withFileLocks(paths []string, fn func(waited bool) (string, error)) (string, error) {
	if len(paths) == 0 {
		return fn(false)
	}
	return e.mgr.WithFileLock(paths[0], func(waited bool) (string, error) {
		return e.withFileLocks(paths[1:], func(restWaited bool) (string, error) {
			return fn(waited || restWaited)
		})
	})
}

// applyBatch is all-or-nothing on purpose.
//
// Every edit is applied to an in-memory copy first and only written once the whole batch has
// matched. A partly-applied batch is the worst outcome available here: the model believes it
// made one coherent change, the repository holds half of it, and the half that landed has
// already invalidated the old_strings of the half that did not. Failing with nothing written
// leaves the model exactly where it was, holding a precise report of which edit was wrong.
//
// Edits apply in sequence against the accumulating content, so an edit may legitimately target
// text an earlier edit in the same batch produced -- the same semantics a shell heredoc doing
// several replacements has, which is what this replaces.
func (e *Edit) applyBatch(ops []editOp, waited bool) (string, error) {
	// Phase 1: resolve every edit in memory. Nothing touches disk.
	order := []string{}
	pending := map[string]string{}
	for _, op := range ops {
		abs, err := filepath.Abs(op.FilePath)
		if err != nil {
			abs = op.FilePath
		}
		content, loaded := pending[abs]
		if !loaded {
			data, err := os.ReadFile(abs)
			if err != nil {
				return "", fmt.Errorf("%s: read file: %w", editLabel(ops, op), err)
			}
			content = string(data)
			order = append(order, abs)
		}
		next, err := applyOne(content, op, waited)
		if err != nil {
			return "", fmt.Errorf("%s: %w%s", editLabel(ops, op), err, nothingWritten(ops))
		}
		pending[abs] = next
	}

	// Phase 2: commit. A write failure here can still leave earlier files written, which is
	// why phase 1 exists -- by this point the only remaining causes are disk-level.
	for _, abs := range order {
		// Atomic per file: interrupted between truncate and write, os.WriteFile leaves a
		// half-written source file, and phase 1 exists precisely so this phase cannot leave
		// the repository in a state the model does not know about. See helper.AtomicWriteFile.
		if err := helper.AtomicWriteFile(abs, []byte(pending[abs]), 0644); err != nil {
			return "", fmt.Errorf("write file %s: %w", abs, err)
		}
	}

	// Phase 3: one topology sync per FILE rather than per edit. Ten edits to one file used to
	// mean ten re-scans of it; now it means one, and one coherent set of warnings.
	var warnings []domain.TopologyWarning
	if e.mgr != nil {
		for _, abs := range order {
			w, err := e.mgr.UpdateFile(abs, e.reg)
			if err != nil {
				return "", fmt.Errorf("update topology for %s: %w", abs, err)
			}
			warnings = append(warnings, w...)
		}
	}
	return editSummary(len(ops), order, warnings), nil
}

// apply is the single-edit entry point this tool had before batching. Kept because a
// one-element batch is exactly it, and because the delete and lock tests exercise the
// read-modify-write path through here rather than through argument parsing.
func (e *Edit) apply(filePath, oldString, newString string, replaceAll, waited bool) (string, error) {
	return e.applyBatch([]editOp{{
		FilePath: filePath, OldString: oldString, NewString: &newString,
		ReplaceAll: replaceAll, Index: 1,
	}}, waited)
}

// applyOne resolves a single edit against the content it is handed, returning the new content.
// It never touches disk, so the caller decides whether the batch commits.
func applyOne(content string, op editOp, waited bool) (string, error) {
	oldString, newString, ok := matchLineEndings(content, op.OldString, op.text())
	if !ok {
		if waited {
			return "", fmt.Errorf("old_string not found in %s — another agent changed this file while your edit was queued; re-read the resource and retry with the current text", op.FilePath)
		}
		return "", fmt.Errorf("old_string not found in %s", op.FilePath)
	}

	// Require a unique match unless the caller opted into replacing every one. Silently taking
	// the first of several matches is a wrong edit that looks like a successful one, and it is
	// worse for a deletion than a replacement.
	if occurrences := strings.Count(content, oldString); occurrences > 1 && !op.ReplaceAll {
		return "", fmt.Errorf("old_string matched %d times in %s — include more surrounding context so it matches exactly once, or pass replace_all: true", occurrences, op.FilePath)
	}
	if op.ReplaceAll {
		return strings.ReplaceAll(content, oldString, newString), nil
	}
	return strings.Replace(content, oldString, newString, 1), nil
}

// matchLineEndings finds the spelling of old_string that occurs in THIS file's bytes, and
// converts new_string to the same convention. It reports false when neither spelling occurs.
//
// WHY IT REWRITES THE STRINGS AND NOT THE FILE. The previous fallback normalized the whole
// FILE to LF and edited that, so a one-hunk edit against a CRLF file committed the entire file
// with its line endings changed -- a whole-file diff, and a conflict on every line for anyone
// with `* text=auto`. The file's bytes are the one thing an edit must leave alone outside its
// hunk, so the conversion belongs on the caller's strings.
//
// The mismatch is not hypothetical in either direction: a model that has read through any
// aracne surface holds LF text for a CRLF file, and a model that pasted from a CRLF terminal
// holds CRLF text for an LF file.
func matchLineEndings(content, oldString, newString string) (string, string, bool) {
	if strings.Contains(content, oldString) {
		return oldString, newString, true
	}
	toLF := func(s string) string { return strings.ReplaceAll(s, "\r\n", "\n") }
	toCRLF := func(s string) string { return strings.ReplaceAll(toLF(s), "\n", "\r\n") }

	if crlf := toCRLF(oldString); crlf != oldString && strings.Contains(content, crlf) {
		return crlf, toCRLF(newString), true
	}
	if lf := toLF(oldString); lf != oldString && strings.Contains(content, lf) {
		return lf, toLF(newString), true
	}
	return "", "", false
}

// editLabel names the failing edit. A one-edit call keeps the old bare wording, so a caller
// that never batches sees no change in its error text.
func editLabel(ops []editOp, op editOp) string {
	if len(ops) == 1 {
		return "edit"
	}
	return fmt.Sprintf("edit %d of %d (%s)", op.Index, len(ops), op.FilePath)
}

// nothingWritten is the half of a batch failure the model most needs: not just which edit was
// wrong, but that the repository is untouched and the whole call can be resent once fixed.
func nothingWritten(ops []editOp) string {
	if len(ops) == 1 {
		return ""
	}
	return " — nothing was written; fix this edit and resend the batch"
}

func editSummary(n int, files []string, warnings []domain.TopologyWarning) string {
	head := "edit succeeded"
	if n > 1 {
		head = fmt.Sprintf("%d edits applied across %d file(s)", n, len(files))
	}
	if len(warnings) == 0 {
		return head
	}
	var msgs []string
	for _, w := range warnings {
		msgs = append(msgs, fmt.Sprintf("  - [%s] %s (source: %s, target: %s)", w.Kind, w.Message, w.SourceID, w.TargetID))
	}
	return head + "\n\nTopology warnings (functions that may need manual review):\n" + strings.Join(msgs, "\n")
}
