package viz

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rhuan-Marques/aracne/internal/helper"
	"github.com/Rhuan-Marques/aracne/internal/topology/domain"
)

// configTestServer serves an empty topology whose .aracne/config.json holds exactly raw.
func configTestServer(t *testing.T, raw string) (*httptest.Server, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, ".aracne", "topology.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := helper.WriteDb(&domain.Topology{}, dbPath); err != nil {
		t.Fatalf("WriteDb: %v", err)
	}
	cfgPath := helper.ConfigPath(dbPath)
	if err := os.WriteFile(cfgPath, []byte(raw), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	server := httptest.NewServer(NewServer(dbPath))
	t.Cleanup(server.Close)
	return server, cfgPath
}

func putConfig(t *testing.T, server *httptest.Server, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, server.URL+"/api/config", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT /api/config: %v", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(data)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	return string(data)
}

// TestConfigPutRefusesInvalidConfig is VZ-3. The PUT never validated: loading coerces a
// mistyped mode to cli (and a mistyped context_filter to normal), and saving wrote the
// coerced values back -- one click in Settings silently switched the project's integration
// mode and erased the typo `arac setup` exists to report. The file must be left as it was.
func TestConfigPutRefusesInvalidConfig(t *testing.T) {
	raw := `{
  "mode": "intercept_lineranges",
  "read": {"context_filter": "fulll"},
  "descriptions": {"kinds": ["function"]}
}
`
	server, cfgPath := configTestServer(t, raw)

	status, body := putConfig(t, server, `{"descriptions":{"kinds":["function","struct"]}}`)
	if status != http.StatusBadRequest {
		t.Fatalf("PUT over an invalid config: status %d, want 400 (body %s)", status, body)
	}
	if !strings.Contains(body, "intercept_lineranges") {
		t.Fatalf("the refusal should name what is invalid, got %s", body)
	}
	if got := readFile(t, cfgPath); got != raw {
		t.Fatalf("an invalid config must be left unchanged; it now reads:\n%s", got)
	}

	// The page must stay usable: GET still answers with the values aracne runs on, with the
	// reason alongside, so Settings can say why it will not save before the user tries.
	var view struct {
		Mode         string `json:"mode"`
		ConfigError  string `json:"config_error"`
		Descriptions struct {
			Kinds []string `json:"kinds"`
		} `json:"descriptions"`
		Features struct {
			Chat bool `json:"chat"`
		} `json:"features"`
	}
	getJSON(t, server.URL+"/api/config", &view)
	if !strings.Contains(view.ConfigError, "mode") || !strings.Contains(view.ConfigError, "intercept_lineranges") {
		t.Fatalf("GET /api/config: config_error = %q, want the mode problem", view.ConfigError)
	}
	if len(view.Descriptions.Kinds) != 1 || view.Descriptions.Kinds[0] != "function" {
		t.Fatalf("GET /api/config must still carry the stored values, got kinds %v", view.Descriptions.Kinds)
	}
	if got := readFile(t, cfgPath); got != raw {
		t.Fatalf("GET must not rewrite an invalid config; it now reads:\n%s", got)
	}
}

// TestConfigGetValidHasNoConfigError pins the ordinary case: a valid config answers exactly
// as before, with no config_error key for the page to show.
func TestConfigGetValidHasNoConfigError(t *testing.T) {
	server := chatTestServer(t, true)
	resp, err := http.Get(server.URL + "/api/config")
	if err != nil {
		t.Fatalf("GET /api/config: %v", err)
	}
	defer resp.Body.Close()
	var fields map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&fields); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := fields["config_error"]; ok {
		t.Fatalf("a valid config must not carry config_error: %s", fields["config_error"])
	}
	if _, ok := fields["features"]; !ok {
		t.Fatal("GET /api/config must still carry features")
	}
}

// TestConfigPutKeepsUnknownTopLevelKeys is the other half of VZ-3: the PUT re-encoded the
// struct, so any top-level key the schema does not define was deleted by changing one setting.
func TestConfigPutKeepsUnknownTopLevelKeys(t *testing.T) {
	raw := `{
  "mode": "mcp",
  "descriptions": {"kinds": ["function"]},
  "my_note": {"owner": "team-a", "list": [1, 2]},
  "$schema": "https://example.com/aracne.json"
}
`
	server, cfgPath := configTestServer(t, raw)

	status, body := putConfig(t, server, `{"descriptions":{"kinds":["function","struct"]}}`)
	if status != http.StatusOK {
		t.Fatalf("PUT: status %d, body %s", status, body)
	}
	var saved map[string]json.RawMessage
	if err := json.Unmarshal([]byte(readFile(t, cfgPath)), &saved); err != nil {
		t.Fatalf("saved config is not valid JSON: %v\n%s", err, readFile(t, cfgPath))
	}
	var note struct {
		Owner string `json:"owner"`
		List  []int  `json:"list"`
	}
	if err := json.Unmarshal(saved["my_note"], &note); err != nil || note.Owner != "team-a" || len(note.List) != 2 {
		t.Fatalf("my_note was not kept: %s (%v)", saved["my_note"], err)
	}
	if string(saved["$schema"]) != `"https://example.com/aracne.json"` {
		t.Fatalf("$schema was not kept: %s", saved["$schema"])
	}
	cfg, ok := helper.LoadConfigStrict(cfgPath)
	if !ok {
		t.Fatal("saved config no longer loads")
	}
	if cfg.Mode != helper.ModeMCP {
		t.Fatalf("mode = %q, want mcp", cfg.Mode)
	}
	if len(cfg.Descriptions.Kinds) != 2 || cfg.Descriptions.Kinds[1] != domain.ResourceStruct {
		t.Fatalf("descriptions.kinds = %v, want [function struct]", cfg.Descriptions.Kinds)
	}
}

// TestConfigPutRejectsEmptyKinds is VZ-6. Config loading reads descriptions.kinds [] as
// "use the defaults", so storing [] reported "None" as saved and then served the four
// defaults on the next read. It is refused, as `--target ""` is, and nothing is written.
func TestConfigPutRejectsEmptyKinds(t *testing.T) {
	raw := `{
  "mode": "cli",
  "descriptions": {"kinds": ["function", "file"]}
}
`
	server, cfgPath := configTestServer(t, raw)

	status, body := putConfig(t, server, `{"descriptions":{"kinds":[]}}`)
	if status != http.StatusBadRequest {
		t.Fatalf("PUT kinds []: status %d, want 400 (body %s)", status, body)
	}
	if got := readFile(t, cfgPath); got != raw {
		t.Fatalf("a refused PUT must not write; the file now reads:\n%s", got)
	}
	var cfg helper.Config
	getJSON(t, server.URL+"/api/config", &cfg)
	if len(cfg.Descriptions.Kinds) != 2 || cfg.Descriptions.Kinds[0] != domain.ResourceFunction || cfg.Descriptions.Kinds[1] != domain.ResourceFile {
		t.Fatalf("stored kinds changed: %v", cfg.Descriptions.Kinds)
	}

	// A non-empty list still saves, and reads back as saved.
	if status, body := putConfig(t, server, `{"descriptions":{"kinds":["package"]}}`); status != http.StatusOK {
		t.Fatalf("PUT kinds [package]: status %d, body %s", status, body)
	}
	getJSON(t, server.URL+"/api/config", &cfg)
	if len(cfg.Descriptions.Kinds) != 1 || cfg.Descriptions.Kinds[0] != domain.ResourcePackage {
		t.Fatalf("kinds after saving [package] = %v", cfg.Descriptions.Kinds)
	}

	// A descriptions section that does not mention kinds leaves them alone -- it used to store
	// [] and so reset them to the defaults.
	if status, body := putConfig(t, server, `{"descriptions":{},"description_batch_size":2}`); status != http.StatusOK {
		t.Fatalf("PUT without kinds: status %d, body %s", status, body)
	}
	getJSON(t, server.URL+"/api/config", &cfg)
	if len(cfg.Descriptions.Kinds) != 1 || cfg.Descriptions.Kinds[0] != domain.ResourcePackage {
		t.Fatalf("kinds after a PUT that did not name them = %v, want [package]", cfg.Descriptions.Kinds)
	}
}
