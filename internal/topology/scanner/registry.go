package scanner

type Registry struct {
	scanners []LanguageScanner
}

func NewRegistry() *Registry {
	return &Registry{}
}

func (r *Registry) Register(s LanguageScanner) {
	r.scanners = append(r.scanners, s)
}

func (r *Registry) Detect(root string) LanguageScanner {
	for _, s := range r.scanners {
		if s.Detect(root) {
			return s
		}
	}
	return nil
}

func (r *Registry) All() []LanguageScanner {
	return r.scanners
}
