package providers

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Rhuan-Marques/aracne/internal/llm"
)

// DE-8: every provider goes through the client with a timeout. OpenAI and DeepSeek used
// http.DefaultClient, which has none, and Chat passes context.Background() -- so a gateway that
// accepted the connection and never answered hung a description sweep forever.
func TestProvidersTimeOutAStalledServer(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) }) // runs first: unblock the handlers, then close

	saved := providerHTTP.Timeout
	providerHTTP.Timeout = 300 * time.Millisecond
	t.Cleanup(func() { providerHTTP.Timeout = saved })

	for name, p := range map[string]llm.Provider{
		"anthropic": NewAnthropic("key", "m", server.URL),
		"openai":    NewOpenAI("key", "m", server.URL),
		"deepseek":  NewDeepSeekWithConfig("key", "m", server.URL),
	} {
		done := make(chan error, 1)
		go func() {
			_, err := p.Chat([]llm.Message{{Role: "user", Content: "hi"}}, nil)
			done <- err
		}()
		select {
		case err := <-done:
			if err == nil {
				t.Errorf("%s: a server that never answered produced no error", name)
			}
		case <-time.After(10 * time.Second):
			t.Errorf("%s: Chat against a stalled server did not time out", name)
		}
	}
}
