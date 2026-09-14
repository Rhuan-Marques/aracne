package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/Rhuan-Marques/aracne/internal/llm/tools"
)

// Reads JSON from stdin containing file path and old/new strings, applies the
// replacement and updates the topology database.
//
// This delegates to the MCP edit tool rather than reimplementing it. The two
// had drifted: this path had no file lock and no CRLF fallback, and it rejected
// an empty new_string outright, so deleting a block of code meant rewriting the
// whole file or escaping to a shell the guard exists to discourage.
func RunEdit() {
	// Both shapes the underlying tool accepts: the single edit, and the `edits` array that
	// applies several against one file lock and rolls back as a unit.
	//
	// The batch form was reachable only through the MCP edit tool until that tool stopped
	// being registered -- edits are answered on the shell in every mode now, so this verb is
	// the batch path, and validating it away here would have quietly removed the capability.
	// The measured cost of the edit path was call COUNT, not the topology sync: one call per
	// hunk against a baseline that batched ten replacements into a single heredoc.
	var input struct {
		FilePath  string            `json:"file_path"`
		OldString string            `json:"old_string"`
		Edits     []json.RawMessage `json:"edits"`
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading stdin: %v\n", err)
		os.Exit(1)
	}
	if err := json.Unmarshal(data, &input); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing JSON: %v\n", err)
		os.Exit(1)
	}
	// new_string is not checked: an empty one deletes the matched text.
	if len(input.Edits) == 0 && (input.FilePath == "" || input.OldString == "") {
		fmt.Fprintln(os.Stderr, `Usage: echo '{"file_path":"...","old_string":"...","new_string":"...","replace_all":false}' | arac edit`)
		fmt.Fprintln(os.Stderr, `       batch: echo '{"edits":[{"file_path":"...","old_string":"...","new_string":"..."}, ...]}' | arac edit`)
		fmt.Fprintln(os.Stderr, "       an empty new_string deletes the matched text; old_string must match exactly once unless replace_all is true")
		fmt.Fprintln(os.Stderr, "       a batch takes one file lock and rolls back as a unit")
		os.Exit(1)
	}

	// InitRegistry now runs before the write rather than after it. It builds the
	// topology when none exists; UpdateFile re-reads the edited file straight
	// afterwards, so the pre-edit snapshot it sees does not survive.
	manager, reg := InitRegistry(ProjectDBPath(DefaultDBRelative))
	edit := tools.NewEdit(manager, reg)
	// The warnings go through the guard's ledger rather than the tool's own summary, so this
	// edit's breakage is reported exactly ONCE. Printing both meant the PostToolUse drift
	// check repeated it on the same call -- and for the spellings that check skips as pure
	// `arac` commands (`arac edit < f.json`), the warning instead surfaced later, attached to
	// whatever unrelated command ran next. See guard_warnstate.go.
	edit.OmitWarnings = true
	out, err := edit.Run(data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(out)
	if msg := driftWarningReport(manager.DbPath(), unreportedWarnings(manager.DbPath())); msg != "" {
		fmt.Println()
		fmt.Println(msg)
	}
}
