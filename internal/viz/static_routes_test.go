package viz

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStaticAppRoutesServeIndex(t *testing.T) {
	server := httptest.NewServer(NewServer("missing.db"))
	defer server.Close()

	for _, route := range []string{"/graph", "/chat", "/chat/session_123", "/chat/history", "/settings"} {
		resp, err := http.Get(server.URL + route)
		if err != nil {
			t.Fatalf("GET %s: %v", route, err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatalf("read %s: %v", route, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status = %d", route, resp.StatusCode)
		}
		if !strings.Contains(string(body), "Aracne Viz") {
			t.Fatalf("GET %s did not serve app index", route)
		}
	}
}
