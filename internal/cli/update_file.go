package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/topology"
)

// Re-scans a file and updates the topology, optionally reporting warnings from structural changes.
func RunUpdateFile(args []string) {
	if len(args) == 1 && args[0] == "--claude-hook" {
		runClaudeUpdateFileHook(os.Stdin, os.Stdout)
		return
	}
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: arac update-file <path> [--db <dbpath>]\n       arac update-file --claude-hook")
		os.Exit(1)
	}
	path := args[0]
	dbPath := ProjectDBPath(DefaultDBRelative)
	for i := 1; i < len(args); i++ {
		if args[i] == "--db" && i+1 < len(args) {
			dbPath = args[i+1]
			i++
		}
	}
	manager, reg := InitRegistry(dbPath)
	warnings, err := manager.UpdateFile(path, reg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error updating file: %v\n", err)
		os.Exit(1)
	}
	count := fmt.Sprintf("Warning number %d\n", len(warnings))
	fmt.Print(count)
	// The count line above, and the newline Println ends the report with.
	if msg := driftWarningReport(dbPath, warnings, len(count)+1); msg != "" {
		fmt.Println(msg)
	}
}

// Processes Claude file-edit hooks to update topology and report warnings for modified files.
//
// THE DATABASE IS RESOLVED THE WAY THE GUARD RESOLVES IT, and that is not cosmetic. This used
// to take ProjectDBPath, an upward walk from the process working directory -- which is the one
// thing a hook cannot depend on, and the reason guardDBPath exists (see its comment for the
// benchmark run lost to it). Run from anywhere outside the tree, InitRegistry did not bail: it
// CREATED a `.aracne/` there, printed "No topology found. Scanning project...", and exited 1 --
// leaving the real topology unsynced and a junk directory behind. A hook with no topology has
// nothing to do.
//
// WARNINGS GO THROUGH THE SHARED LEDGER, not through UpdateFile's return value. Both PostToolUse
// hooks can now see the same native edit -- this one when the edit-update-db-plugin is installed,
// the guard's drift check always -- and unreportedWarnings is what makes that safe: whichever
// runs first reports, the other finds nothing new. Reporting UpdateFile's own list here would
// have printed the same warnings twice for anyone who opted into the plugin.
func runClaudeUpdateFileHook(input io.Reader, output io.Writer) {
	inputJSON, err := io.ReadAll(input)
	if err != nil || strings.TrimSpace(string(inputJSON)) == "" {
		return
	}

	var event struct {
		ToolInput map[string]interface{} `json:"tool_input"`
		// Cwd is the SESSION directory Claude Code reports on every hook event, and the
		// first tier guardDBPath consults.
		Cwd string `json:"cwd"`
	}
	if err := json.Unmarshal(inputJSON, &event); err != nil || event.ToolInput == nil {
		return
	}

	paths := claudeHookPaths(event.ToolInput)
	if len(paths) == 0 {
		return
	}

	dbPath := guardDBPath(event.Cwd)
	if _, err := os.Stat(dbPath); err != nil {
		return
	}
	manager := topology.New()
	if err := manager.Load(dbPath); err != nil {
		return
	}
	reg := NewScannerRegistry()

	var parts []string
	for _, path := range paths {
		if _, err := manager.UpdateFile(path, reg); err != nil {
			parts = append(parts, fmt.Sprintf("Aracne update-file failed for %s:\n%v", path, err))
		}
	}
	// Rendered the way the guard renders the same list, so a warning reads identically
	// whichever path produced the change.
	if msg := driftWarningReport(dbPath, unreportedWarnings(dbPath), joinedLen(parts)); msg != "" {
		parts = append(parts, msg)
	}
	if len(parts) == 0 {
		return
	}
	// THROUGH THE SAME CHANNEL THE GUARD USES, and that is the whole fix.
	//
	// This wrote plain text to stdout. A PostToolUse hook that exits 0 has its stdout treated
	// as transcript material, not as context -- so the topology warnings raised by a NATIVE
	// Edit/Write, the one path this hook exists to cover and the one where the model has no
	// other signal, were formatted, written and dropped. The guard's drift check for shell
	// writes emits hookSpecificOutput.additionalContext and does reach the model, which is
	// exactly why the gap was invisible from either side.
	emitPostToolWarning(output, strings.Join(parts, "\n\n"))
}

// Extracts and deduplicates file paths from tool input, checking file_path/filePath/path fields and nested edits
func claudeHookPaths(toolInput map[string]interface{}) []string {
	seen := make(map[string]bool)
	var paths []string
	add := func(value interface{}) {
		path, ok := value.(string)
		if !ok || strings.TrimSpace(path) == "" || seen[path] {
			return
		}
		seen[path] = true
		paths = append(paths, path)
	}

	for _, name := range []string{"file_path", "filePath", "path"} {
		add(toolInput[name])
	}
	if edits, ok := toolInput["edits"].([]interface{}); ok {
		for _, item := range edits {
			edit, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			for _, name := range []string{"file_path", "filePath", "path"} {
				add(edit[name])
			}
		}
	}
	return paths
}
