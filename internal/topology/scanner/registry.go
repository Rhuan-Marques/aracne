package scanner

import (
	"io/fs"
	"path/filepath"
	"strings"
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

func (r *Registry) DetectAll(root string) []LanguageScanner {
	var detected []LanguageScanner
	for _, s := range r.scanners {
		if s.Detect(root) || scannerHasFiles(root, s) {
			detected = append(detected, s)
		}
	}
	return detected
}

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

func scannerHasFiles(root string, s LanguageScanner) bool {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	found := false
	filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || found {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == ".aracne" || name == "node_modules" || name == "vendor" || name == "__pycache__" {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		for _, supported := range s.Extensions() {
			if ext == strings.ToLower(supported) {
				found = true
				return nil
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
