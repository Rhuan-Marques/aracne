package javatools

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
	"github.com/Rhuan-Marques/aracne/internal/topology/java"
)

// The schema tells the model to pass Method or Constructor, and the scanner stores both as kind
// `method`. They used to map to `function`, and the write filters on kind, so every method and
// constructor came back "no function resource with id ...".
func TestUpdateDescriptionWritesMethodsAndConstructors(t *testing.T) {
	const method = "com.aracne.annotations.Marked.compute()"
	const ctor = "com.aracne.annotations.Marked.Marked(int)"
	path := filepath.Join(t.TempDir(), "topology.db")
	topo := &domain.Topology{Resources: map[string]domain.Resource{
		method: {ID: method, Kind: domain.ResourceMethod, Name: "compute"},
		ctor:   {ID: ctor, Kind: domain.ResourceMethod, Name: "Marked"},
	}}
	if err := helper.WriteDb(topo, path); err != nil {
		t.Fatalf("WriteDb: %v", err)
	}
	mgr := topology.New()
	if err := mgr.Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}
	tool := NewUpdateDescriptionTool(java.NewJavaManager(mgr))

	for _, tc := range []struct{ id, resourceName string }{{method, "Method"}, {ctor, "Constructor"}} {
		args, _ := json.Marshal(map[string]string{"id": tc.id, "resource_name": tc.resourceName, "description": "Computes the marked value"})
		if _, err := tool.Run(args); err != nil {
			t.Errorf("resource_name %s on %s: %v", tc.resourceName, tc.id, err)
		}
	}

	read, err := helper.ReadDb(path)
	if err != nil {
		t.Fatalf("ReadDb: %v", err)
	}
	for _, id := range []string{method, ctor} {
		if read.Resources[id].Description == "" {
			t.Errorf("%s was not described", id)
		}
	}
}
