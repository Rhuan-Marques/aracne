package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/Rhuan-Marques/aracne/internal/llm/tools"
)

// Writes a file to disk from JSON input on stdin using the topology-aware write tool.
func RunWrite() {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading stdin: %v\n", err)
		os.Exit(1)
	}
	if len(data) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: echo '{\"file_path\":\"...\",\"content\":\"...\"}' | arac write")
		os.Exit(1)
	}

	manager, reg := InitRegistry(".aracne/topology.db")
	result, err := tools.NewWrite(manager, reg).Run(data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(result)
}
