package cli

import (
	"fmt"
	"os"

	"ltp/internal/topology/domain"
	"ltp/internal/topology/golang"
	"ltp/internal/topology/python"
)

func RunReadResourceAndCut(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: ltp read-resource-and-cut <id> <kind>")
		fmt.Fprintln(os.Stderr, "Kind: Function, Struct, Interface, ExternalVar, File, Package")
		os.Exit(1)
	}
	id, resourceName := args[0], args[1]

	manager, _ := InitRegistry(".ltp/topology.db")
	lang := GetLanguage(manager)

	kind := MapResourceKind(resourceName)
	if kind == "" {
		fmt.Fprintf(os.Stderr, "Error: unknown resource kind %q\n", resourceName)
		os.Exit(1)
	}

	if kind == domain.ResourceFile {
		fmt.Printf("File: %s\n", id)
		return
	}
	if kind == domain.ResourcePackage {
		fmt.Printf("Package: %s\n", id)
		return
	}

	var entry *domain.CodeEntry
	var err error

	if lang == "python" {
		pythonManager := python.NewPythonManager(manager)
		entry, err = pythonManager.ReadResourceAndCut(id, kind)
	} else {
		goManager := golang.NewGoManager(manager)
		entry, err = goManager.ReadResourceAndCut(id, kind)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Print(entry.Cut)
}
