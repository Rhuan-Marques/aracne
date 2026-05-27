package scanner

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

// Returns a slice of all registered LanguageScanner instances in the registry for iteration and detection purposes.
func (r *Registry) All() []LanguageScanner {
	return r.scanners
}
