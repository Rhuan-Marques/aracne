package topogrep

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aracne/internal/topology/domain"
)

func TestSearchAnnotatesResourceMatch(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "sample.go")
	content := "package main\n\nfunc Target() {\n\tfmt.Println(\"needle\")\n}\n"
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	topo := &domain.Topology{Resources: map[string]domain.Resource{
		"example.Target": {
			ID:          "example.Target",
			Kind:        domain.ResourceFunction,
			Name:        "Target",
			Description: "prints a test needle",
			Location:    domain.Location{Path: filePath, StartsAt: 3, EndsAt: 5},
		},
	}}

	matches, err := Search("needle", dir, topo)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	if matches[0].ResourceID != "example.Target" || matches[0].Description != "prints a test needle" {
		t.Fatalf("unexpected annotation: %+v", matches[0])
	}

	formatted := Format(matches)
	for _, want := range []string{"sample.go:4:", "ResourceID: example.Target", "Description: prints a test needle"} {
		if !strings.Contains(formatted, want) {
			t.Fatalf("formatted output missing %q:\n%s", want, formatted)
		}
	}
}

func TestSearchIgnoresMatchesOutsideTopology(t *testing.T) {
	dir := t.TempDir()
	topoFile := filepath.Join(dir, "topo.go")
	rawFile := filepath.Join(dir, "raw.go")

	if err := os.WriteFile(topoFile, []byte("package main\n\nfunc Target() {\n\tfmt.Println(\"needle\")\n}\n"), 0644); err != nil {
		t.Fatalf("write topology file: %v", err)
	}
	if err := os.WriteFile(rawFile, []byte("package main\n\nfunc Other() {\n\tfmt.Println(\"needle\")\n}\n"), 0644); err != nil {
		t.Fatalf("write raw file: %v", err)
	}

	topo := &domain.Topology{Resources: map[string]domain.Resource{
		"example.Target": {
			ID:       "example.Target",
			Kind:     domain.ResourceFunction,
			Name:     "Target",
			Location: domain.Location{Path: topoFile, StartsAt: 3, EndsAt: 5},
		},
	}}

	matches, err := Search("needle", dir, topo)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected only topology-backed match, got %d: %+v", len(matches), matches)
	}
	if matches[0].ResourceID != "example.Target" || !strings.HasSuffix(matches[0].Path, "topo.go") {
		t.Fatalf("unexpected match: %+v", matches[0])
	}
}

func TestSearchReturnsNoMatchesWithoutTopology(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "sample.go")
	if err := os.WriteFile(filePath, []byte("package main\n\nfunc Target() {\n\tfmt.Println(\"needle\")\n}\n"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	matches, err := Search("needle", dir, nil)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("expected no matches without topology, got %d: %+v", len(matches), matches)
	}
}
