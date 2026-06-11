package topogrep

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"aracne/internal/topology/domain"
)

type Match struct {
	Path        string `json:"path"`
	Line        int    `json:"line"`
	Text        string `json:"text"`
	ResourceID  string `json:"resource_id,omitempty"`
	Description string `json:"description,omitempty"`
}

type resourceLocation struct {
	id          string
	description string
	kind        domain.ResourceKind
	startsAt    int
	endsAt      int
}

func Search(pattern, root string, topo *domain.Topology) ([]Match, error) {
	if pattern == "" {
		return nil, fmt.Errorf("pattern is required")
	}
	if root == "" {
		root = "."
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("compile pattern: %w", err)
	}

	index := buildResourceIndex(topo)
	var matches []Match
	if err := walkSearch(root, func(path string) error {
		fileMatches, err := searchFile(path, re, index)
		if err != nil {
			return err
		}
		matches = append(matches, fileMatches...)
		return nil
	}); err != nil {
		return nil, err
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Path != matches[j].Path {
			return matches[i].Path < matches[j].Path
		}
		return matches[i].Line < matches[j].Line
	})
	return matches, nil
}

func Format(matches []Match) string {
	if len(matches) == 0 {
		return ""
	}
	var b strings.Builder
	for i, match := range matches {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%s:%d:%s", match.Path, match.Line, match.Text)
		if match.ResourceID != "" {
			fmt.Fprintf(&b, "\n  ResourceID: %s", match.ResourceID)
			if match.Description != "" {
				fmt.Fprintf(&b, "\n  Description: %s", match.Description)
			}
		}
	}
	return b.String()
}

func walkSearch(root string, visit func(path string) error) error {
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("stat path: %w", err)
	}
	if !info.IsDir() {
		return visit(root)
	}
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if shouldSkipDir(path, d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeType != 0 {
			return nil
		}
		return visit(path)
	})
}

func shouldSkipDir(path, name string) bool {
	if name == "." {
		return false
	}
	switch name {
	case ".git", ".aracne", "node_modules", "vendor":
		return true
	}
	return strings.HasPrefix(name, ".") && path != name
}

func searchFile(path string, re *regexp.Regexp, index map[string][]resourceLocation) ([]Match, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil
	}
	defer f.Close()

	canonical := canonicalPath(path)
	resources := index[canonical]
	var matches []Match
	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimRight(scanner.Text(), "\r")
		if !re.MatchString(line) {
			continue
		}
		resource := bestResource(resources, lineNo)
		if resource == nil {
			continue
		}
		matches = append(matches, Match{
			Path:        displayPath(path),
			Line:        lineNo,
			Text:        line,
			ResourceID:  resource.id,
			Description: resource.description,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, nil
	}
	return matches, nil
}

func buildResourceIndex(topo *domain.Topology) map[string][]resourceLocation {
	index := make(map[string][]resourceLocation)
	if topo == nil {
		return index
	}
	for id, res := range topo.Resources {
		if res.Location.Path == "" {
			continue
		}
		path := canonicalPath(res.Location.Path)
		loc := resourceLocation{
			id:          id,
			description: res.Description,
			kind:        res.Kind,
			startsAt:    res.Location.StartsAt,
			endsAt:      res.Location.EndsAt,
		}
		index[path] = append(index[path], loc)
	}
	for path := range index {
		sort.Slice(index[path], func(i, j int) bool {
			left := span(index[path][i])
			right := span(index[path][j])
			if left != right {
				return left < right
			}
			return index[path][i].id < index[path][j].id
		})
	}
	return index
}

func bestResource(resources []resourceLocation, line int) *resourceLocation {
	var fallback *resourceLocation
	for i := range resources {
		resource := &resources[i]
		if resource.kind == domain.ResourceFile {
			if fallback == nil {
				fallback = resource
			}
			continue
		}
		if resource.startsAt <= line && (resource.endsAt == 0 || line <= resource.endsAt) {
			return resource
		}
	}
	return fallback
}

func span(resource resourceLocation) int {
	if resource.startsAt == 0 || resource.endsAt == 0 {
		return 1 << 30
	}
	return resource.endsAt - resource.startsAt
}

func canonicalPath(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}
	return path
}

func displayPath(path string) string {
	if rel, err := filepath.Rel(".", path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(path)
}
