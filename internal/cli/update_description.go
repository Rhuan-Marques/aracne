package cli

import (
	"fmt"
	"os"
	"strings"
)

func RunUpdateDescription(args []string) {
	if len(args) < 3 {
		fmt.Fprintln(os.Stderr, "Usage: arac update-description <id> <kind> <description>")
		os.Exit(1)
	}
	id, resourceName := args[0], args[1]
	description := strings.Join(args[2:], " ")

	manager, _ := InitRegistry(".aracne/topology.db")

	kind := MapResourceKind(resourceName)
	if kind == "" {
		fmt.Fprintf(os.Stderr, "Error: unknown resource kind %q\n", resourceName)
		os.Exit(1)
	}

	if err := manager.UpdateDescription(id, kind, description); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("description updated")
}
