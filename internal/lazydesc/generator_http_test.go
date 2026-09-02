package lazydesc

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aracne/internal/helper"
	"aracne/internal/topology/domain"
)

// The production generator against a real HTTP server: prompt out, SSE in, descriptions back.
//
// The fakes elsewhere prove the plumbing around the generator; this proves the generator
// itself -- that the request carries the ids and the source, and that the reply the model
// actually sends over the wire parses into what gets written.
func TestLLMGeneratorRoundTrip(t *testing.T) {
	// A key is required even against a local endpoint: every provider aracne speaks to sends
	// one, and a gateway that does not check it is happy with any value.
	clearKeys(t)
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	var body string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		w.Header().Set("Content-Type", "text/event-stream")
		reply := "pkg.Alpha :: parses the alpha header\npkg.Beta :: writes the beta row"
		block, _ := json.Marshal(map[string]any{
			"type": "content_block_delta", "index": 0,
			"delta": map[string]any{"type": "text_delta", "text": reply},
		})
		w.Write([]byte("data: " + string(block) + "\n\n"))
	}))
	defer server.Close()

	gen, err := GeneratorFactory(helper.ResolvedLazyDescriptions{
		Provider: providerAnthropic, Model: "claude-haiku-4-5", BaseURL: server.URL,
	})
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	if gen == nil {
		t.Fatal("no generator built for an explicit provider with a key present")
	}

	got, err := gen.Describe(context.Background(), Batch{Resources: []Request{
		{ID: "pkg.Alpha", Name: "Alpha", Kind: domain.ResourceFunction, Source: "func Alpha() {}"},
		{ID: "pkg.Beta", Name: "Beta", Kind: domain.ResourceFunction, Source: "func Beta() {}"},
	}})
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if got["pkg.Alpha"] != "parses the alpha header" || got["pkg.Beta"] != "writes the beta row" {
		t.Fatalf("descriptions = %v", got)
	}

	// The request has to carry what the model needs to answer: the exact ids, and the source.
	for _, want := range []string{"pkg.Alpha", "pkg.Beta", "func Alpha() {}", "func Beta() {}"} {
		if !strings.Contains(body, want) {
			t.Errorf("request body is missing %q", want)
		}
	}
	// And no tool definitions: this path is a single completion, not an agent loop.
	var sent struct {
		Tools []any `json:"tools"`
	}
	if err := json.Unmarshal([]byte(body), &sent); err != nil {
		t.Fatalf("request body is not JSON: %v", err)
	}
	if len(sent.Tools) != 0 {
		t.Errorf("the lazy generator sent %d tool definitions; it runs without tools", len(sent.Tools))
	}
}

// A provider that errors is an error the Filler turns into "nothing changed" -- never a
// panic, and never a partial write.
func TestLLMGeneratorSurfacesTransportErrors(t *testing.T) {
	clearKeys(t)
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "overloaded", http.StatusTooManyRequests)
	}))
	defer server.Close()

	gen, _ := GeneratorFactory(helper.ResolvedLazyDescriptions{
		Provider: providerAnthropic, Model: "claude-haiku-4-5", BaseURL: server.URL,
	})
	if _, err := gen.Describe(context.Background(), Batch{Resources: []Request{
		{ID: "pkg.Alpha", Name: "Alpha", Kind: domain.ResourceFunction},
	}}); err == nil {
		t.Fatal("a 429 should surface as an error")
	}
}

// An empty batch never reaches the network.
func TestLLMGeneratorSkipsAnEmptyBatch(t *testing.T) {
	clearKeys(t)
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer server.Close()

	gen, _ := GeneratorFactory(helper.ResolvedLazyDescriptions{
		Provider: providerAnthropic, Model: "claude-haiku-4-5", BaseURL: server.URL,
	})
	if _, err := gen.Describe(context.Background(), Batch{}); err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if called {
		t.Fatal("an empty batch made a request")
	}
}
