package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"aracne/internal/llm/tools"
)

// Reads JSON from stdin containing file path and old/new strings, applies the
// replacement and updates the topology database.
//
// This delegates to the MCP edit tool rather than reimplementing it. The two
// had drifted: this path had no file lock and no CRLF fallback, and it rejected
// an empty new_string outright, so deleting a block of code meant rewriting the
// whole file or escaping to a shell the guard exists to discourage.
func RunEdit() {
	var input struct {
		FilePath  string `json:"file_path"`
		OldString string `json:"old_string"`
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
	if input.FilePath == "" || input.OldString == "" {
		fmt.Fprintln(os.Stderr, `Usage: echo '{"file_path":"...","old_string":"...","new_string":"...","replace_all":false}' | arac edit`)
		fmt.Fprintln(os.Stderr, "       an empty new_string deletes the matched text; old_string must match exactly once unless replace_all is true")
		os.Exit(1)
	}

	// InitRegistry now runs before the write rather than after it. It builds the
	// topology when none exists; UpdateFile re-reads the edited file straight
	// afterwards, so the pre-edit snapshot it sees does not survive.
	manager, reg := InitRegistry(".aracne/topology.db")
	out, err := tools.NewEdit(manager, reg).Run(data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(out)
}
