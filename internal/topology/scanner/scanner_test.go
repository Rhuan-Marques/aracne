package scanner

import (
	"testing"

	"aracne/internal/topology/domain"
)

type mockScanner struct {
	name       string
	extensions []string
	detectVal  bool
}

func (m *mockScanner) Name() string                               { return m.name }
func (m *mockScanner) Extensions() []string                       { return m.extensions }
func (m *mockScanner) Detect(root string) bool                    { return m.detectVal }
func (m *mockScanner) Scan(root string) (*domain.Topology, error) { return &domain.Topology{}, nil }
func (m *mockScanner) UpdateFile(topo *domain.Topology, path string) ([]domain.TopologyWarning, error) {
	return nil, nil
}

func TestNewRegistry(t *testing.T) {
	r := NewRegistry()
	if r == nil {
		t.Fatal("NewRegistry returned nil")
	}
	if len(r.All()) != 0 {
		t.Errorf("expected empty registry, got %d scanners", len(r.All()))
	}
}

func TestRegisterAndDetect(t *testing.T) {
	r := NewRegistry()
	s := &mockScanner{name: "go", detectVal: true}
	r.Register(s)

	if len(r.All()) != 1 {
		t.Fatalf("expected 1 scanner, got %d", len(r.All()))
	}

	detected := r.Detect("/some/path")
	if detected == nil {
		t.Fatal("expected non-nil scanner")
	}
	if detected.Name() != "go" {
		t.Errorf("expected scanner 'go', got %q", detected.Name())
	}
}

func TestDetectNoMatch(t *testing.T) {
	r := NewRegistry()
	s := &mockScanner{name: "python", detectVal: false}
	r.Register(s)

	detected := r.Detect("/some/path")
	if detected != nil {
		t.Errorf("expected nil, got %v", detected)
	}
}

func TestDetectFirstMatch(t *testing.T) {
	r := NewRegistry()
	s1 := &mockScanner{name: "python", detectVal: false}
	s2 := &mockScanner{name: "go", detectVal: true}
	s3 := &mockScanner{name: "rust", detectVal: true}
	r.Register(s1)
	r.Register(s2)
	r.Register(s3)

	detected := r.Detect("/some/path")
	if detected == nil {
		t.Fatal("expected non-nil scanner")
	}
	if detected.Name() != "go" {
		t.Errorf("expected first match 'go', got %q", detected.Name())
	}
}

func TestMultipleScanners(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockScanner{name: "go", detectVal: true})
	r.Register(&mockScanner{name: "python", detectVal: true})

	all := r.All()
	if len(all) != 2 {
		t.Fatalf("expected 2 scanners, got %d", len(all))
	}
}

func TestEmptyRegistryDetect(t *testing.T) {
	r := NewRegistry()
	detected := r.Detect("/some/path")
	if detected != nil {
		t.Errorf("expected nil from empty registry, got %v", detected)
	}
}
