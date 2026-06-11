package helper

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"aracne/internal/topology/domain"
)

type resourceEntry struct {
	Description string
	Loc         domain.Location
}

func ApplyDescriptions(topo *domain.Topology) error {
	fileResources := make(map[string][]resourceEntry)

	for _, res := range topo.Resources {
		if res.Description == "" {
			continue
		}
		switch res.Kind {
		case domain.ResourceFunction, domain.ResourceMethod, domain.ResourceType, domain.ResourceNamedType, domain.ResourceInterface, domain.ResourceVariable:
		default:
			continue
		}
		if res.Location.Path == "" || res.Location.StartsAt == 0 {
			continue
		}
		fileResources[res.Location.Path] = append(fileResources[res.Location.Path], resourceEntry{
			Description: res.Description,
			Loc:         res.Location,
		})
	}

	for path, entries := range fileResources {
		if err := applyFile(path, entries); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: %s: %v\n", path, err)
		}
	}

	return nil
}

func applyFile(path string, entries []resourceEntry) error {
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Loc.StartsAt > entries[j].Loc.StartsAt
	})

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}

	lines := strings.Split(string(data), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	for _, entry := range entries {
		next := insertComment(lines, entry.Loc.StartsAt, entry.Description)
		if next == nil {
			continue
		}
		lines = next
	}

	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0644)
}

func insertComment(lines []string, startLine int, description string) []string {
	idx := startLine - 1

	blank := idx
	for blank > 0 && strings.TrimSpace(lines[blank-1]) == "" {
		blank--
	}

	commentEnd := blank
	commentStart := blank - 1
	for commentStart >= 0 && isCommentLine(lines[commentStart]) {
		commentStart--
	}
	commentStart++

	comment := formatComment(description)

	if commentStart < commentEnd {
		existing := lines[commentStart]
		indent := leadingWhitespace(existing)
		indented := strings.Split(comment, "\n")
		for i := range indented {
			indented[i] = indent + indented[i]
		}

		result := make([]string, 0, len(lines)-(commentEnd-commentStart)+len(indented))
		result = append(result, lines[:commentStart]...)
		result = append(result, indented...)
		result = append(result, lines[commentEnd:]...)
		return result
	}

	result := make([]string, 0, len(lines)+1)
	result = append(result, lines[:idx]...)
	result = append(result, comment)
	result = append(result, lines[idx:]...)
	return result
}

func isCommentLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*")
}

func leadingWhitespace(line string) string {
	for i, c := range line {
		if c != ' ' && c != '\t' {
			return line[:i]
		}
	}
	return ""
}

func formatComment(desc string) string {
	parts := strings.Split(desc, "\n")
	comment := make([]string, len(parts))
	for i, line := range parts {
		if line == "" {
			comment[i] = "//"
		} else {
			comment[i] = "// " + line
		}
	}
	return strings.Join(comment, "\n")
}
