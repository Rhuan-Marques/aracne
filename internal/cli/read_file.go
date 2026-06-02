package cli

import (
	"fmt"
	"os"
	"path/filepath"
)

func RunReadFile() {
	args := os.Args[2:]
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: ltp read_file <path>")
		os.Exit(1)
	}
	path := args[0]

	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	name := filepath.Base(path)
	fmt.Printf("%s\n%s", name, string(data))
}
