package scanner

import (
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// Registry of LanguageScanner instances supporting multiple programming languages. Key field: scanners (list of registered scanners for each supported language).
type Registry struct {
	scanners []LanguageScanner
}

// Creates and returns a new empty Scanner Registry for detecting and using language-specific scanners.
func NewRegistry() *Registry {
	return &Registry{}
}

// Registers a LanguageScanner implementation into the scanner registry for use during project scanning.
func (r *Registry) Register(s LanguageScanner) {
	r.scanners = append(r.scanners, s)
}

// Iterates the registered scanners and returns the first one whose Detect method returns true for the given root directory, or nil if no matching scanner is found.
func (r *Registry) Detect(root string) LanguageScanner {
	for _, s := range r.scanners {
		if s.Detect(root) {
			return s
		}
	}
	return nil
}

// Returns all language scanners that can detect or find files in the given root directory.
func (r *Registry) DetectAll(root string) []LanguageScanner {
	var detected []LanguageScanner
	for _, s := range r.scanners {
		if s.Detect(root) || scannerHasFiles(root, s) {
			detected = append(detected, s)
		}
	}
	return detected
}

// Returns the appropriate LanguageScanner for a file path by matching its extension against registered scanners.
func (r *Registry) DetectFile(path string) LanguageScanner {
	ext := strings.ToLower(filepath.Ext(path))
	for _, s := range r.scanners {
		for _, supported := range s.Extensions() {
			if ext == strings.ToLower(supported) {
				return s
			}
		}
	}
	return nil
}

// Checks whether a directory tree contains any files matching a scanner's
// supported extensions, skipping standard exclusion directories and anything
// excluded by the project's scan.ignore / path-visibility rules. A directory the
// scan would never parse must not be able to detect a language either --
// otherwise an ignored corpus (a benchmark tree, a vendored sample project)
// makes every language it contains look like a language of the project.
//
// The walk stops at the first match: one hit is the whole answer, so there is no
// reason to keep descending.
func scannerHasFiles(root string, s LanguageScanner) bool {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	found := false
	filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == ".aracne" || name == "node_modules" || name == "vendor" || name == "__pycache__" {
				return filepath.SkipDir
			}
			// The root itself is never pruned by its own basename: WalkDir does
			// not visit ancestors, so a repo that happens to live under an
			// ignored-looking directory must still detect its languages.
			if path != absRoot && domain.PathPruneDir(path) {
				return filepath.SkipDir
			}
			return nil
		}
		if domain.PathHidden(path) {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		for _, supported := range s.Extensions() {
			if ext == strings.ToLower(supported) {
				found = true
				return filepath.SkipAll
			}
		}
		return nil
	})
	return found
}

// Returns a slice of all registered LanguageScanner instances in the registry for iteration and detection purposes.
func (r *Registry) All() []LanguageScanner {
	return r.scanners
}
