package viz

import (
	"regexp"
	"strings"
	"testing"
)

// These pin the static half of the SPA's contracts -- what index.html, styles.css and app.js
// ship -- where a regression is invisible to the API tests. The behaviour they add up to was
// checked in headless Chrome with chat off and on.

func staticText(t *testing.T, name string) string {
	t.Helper()
	data, err := embeddedStatic.ReadFile("static/" + name)
	if err != nil {
		t.Fatalf("read static/%s: %v", name, err)
	}
	return string(data)
}

// openingTag returns the opening tag of the element in index.html whose id is id.
func openingTag(t *testing.T, html, id string) string {
	t.Helper()
	m := regexp.MustCompile(`<[a-z]+[^>]*\sid="` + regexp.QuoteMeta(id) + `"[^>]*>`).FindString(html)
	if m == "" {
		t.Fatalf("index.html has no element with id %q", id)
	}
	return m
}

func hasBareAttr(tag, attr string) bool {
	return regexp.MustCompile(`\s` + attr + `(\s|>|$)`).MatchString(tag)
}

// TestSPAHiddenAttributeHides is VZ-1's CSS half: `.navItem { display: inline-flex }` outranks
// the browser's own [hidden] rule, so setting navChat.hidden left the Chat tab on screen.
func TestSPAHiddenAttributeHides(t *testing.T) {
	css := staticText(t, "styles.css")
	if !regexp.MustCompile(`(?m)^\[hidden\]\s*\{\s*display:\s*none\s*!important;?\s*\}`).MatchString(css) {
		t.Fatal("styles.css needs `[hidden] { display: none !important; }` or the hidden attribute loses to class display rules")
	}
}

// TestSPAChatOnlyElementsStartHidden is VZ-1/VZ-2's markup half. The Chat nav item and the
// chat-only Settings sections (provider list, model defaults, the provider intro) ship hidden
// and tagged data-feature="chat", so nothing chat-shaped shows before /api/config answers and
// applyFeatureGates reveals exactly these when chat is on.
func TestSPAChatOnlyElementsStartHidden(t *testing.T) {
	html := staticText(t, "index.html")
	for _, id := range []string{"navChat", "modelDefaults"} {
		tag := openingTag(t, html, id)
		if !strings.Contains(tag, `data-feature="chat"`) || !hasBareAttr(tag, "hidden") {
			t.Errorf("#%s must carry data-feature=\"chat\" and start hidden: %s", id, tag)
		}
	}
	providers := regexp.MustCompile(`<div[^>]*>\s*<div class="settingsSectionHeader">\s*<h3>Provider Settings</h3>`).FindString(html)
	if !strings.Contains(providers, `data-feature="chat"`) || !hasBareAttr(regexp.MustCompile(`^<div[^>]*>`).FindString(providers), "hidden") {
		t.Errorf("the Provider Settings section must carry data-feature=\"chat\" and start hidden: %q", providers)
	}
	intro := regexp.MustCompile(`<p class="settingsIntro"[^>]*>`).FindString(html)
	if !strings.Contains(intro, `data-feature="chat"`) || !hasBareAttr(intro, "hidden") {
		t.Errorf("the provider intro must carry data-feature=\"chat\" and start hidden: %q", intro)
	}
	// Chat on must look exactly as before: every element that starts hidden is either a
	// chat element applyFeatureGates reveals, or the Settings error box.
	for _, tag := range regexp.MustCompile(`<[a-z]+[^>]*>`).FindAllString(html, -1) {
		if !hasBareAttr(tag, "hidden") {
			continue
		}
		if !strings.Contains(tag, `data-feature="chat"`) && !strings.Contains(tag, `id="settingsError"`) {
			t.Errorf("unexpected element hidden by default: %s", tag)
		}
	}
	js := staticText(t, "app.js")
	if !strings.Contains(js, `querySelectorAll('[data-feature="chat"]')`) {
		t.Error("app.js must reveal the data-feature=\"chat\" elements when chat is on")
	}
}

// TestSPAChatRoutesAndLoadsAreGated is VZ-1's script half: the gate ran only from the Settings
// page, so /chat rendered and every load fetched /api/chat/* into 404s.
func TestSPAChatRoutesAndLoadsAreGated(t *testing.T) {
	js := staticText(t, "app.js")
	for _, want := range []string{
		// renderRoute sends the chat routes home when chat is off.
		`if ((route === 'chat' || route === 'history') && !chatEnabled()) {`,
		// Startup loads the flags before anything chat-shaped.
		"loadAppConfig().then(function () {\n    if (!chatEnabled()) return;\n    loadChatProvider();",
		// Settings only loads provider settings with chat on.
		`if (chatEnabled()) loadChatProvider();`,
		// The socket carries only chat events.
		"function chatSocketNeeded() {\n    // The socket carries nothing but chat events.\n    if (!chatEnabled()) return false;",
	} {
		if !strings.Contains(js, want) {
			t.Errorf("app.js is missing the chat gate:\n%s", want)
		}
	}
}

// TestSPASettingsErrorsShowOnSettings is VZ-2's error half: Settings errors went to #status on
// the hidden graph page, and a plain-text 404 surfaced as a JSON parser error.
func TestSPASettingsErrorsShowOnSettings(t *testing.T) {
	html := staticText(t, "index.html")
	openingTag(t, html, "settingsError")
	js := staticText(t, "app.js")
	for _, fn := range []string{"loadChatProvider", "saveProviderConfig", "loadAppConfig"} {
		body := regexp.MustCompile(`(?s)function ` + fn + `\(.*?\n  \}\n`).FindString(js)
		if !strings.Contains(body, ".catch(showSettingsError)") {
			t.Errorf("%s must report its errors on the Settings page", fn)
		}
	}
	for _, fn := range []string{"api", "apiJSON"} {
		body := regexp.MustCompile(`(?s)function ` + fn + `\(.*?\n  \}\n`).FindString(js)
		if strings.Contains(body, "r.json()") || !strings.Contains(body, "responseJSON") {
			t.Errorf("%s must decode through responseJSON, not r.json() a response that may be a plain-text error", fn)
		}
	}
}

// TestSPAStrictEdgesOnlyInCustom is VZ-5: the checkbox is visible only in Custom, but setGraph
// applied it in every mode, silently dropping nodes from Packages, Data Flow and drill-downs.
func TestSPAStrictEdgesOnlyInCustom(t *testing.T) {
	js := staticText(t, "app.js")
	body := regexp.MustCompile(`(?s)function setGraph\(data\) \{.*?\n  \}\n`).FindString(js)
	if body == "" {
		t.Fatal("app.js has no setGraph")
	}
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, "strictEdges") && !strings.Contains(line, "el('mode').value === 'custom'") {
			t.Fatalf("setGraph applies Strict Edges outside Custom mode: %s", strings.TrimSpace(line))
		}
	}
}

// TestDataFlowHelpMatchesKinds is VZ-9: the help text promised named types, which the Data
// Flow view (dataFlowKinds) has never included. Either both say so or neither does.
func TestDataFlowHelpMatchesKinds(t *testing.T) {
	js := staticText(t, "app.js")
	help := regexp.MustCompile(`data_flow: '([^']*)'`).FindStringSubmatch(js)
	if help == nil {
		t.Fatal("app.js has no Data Flow help text")
	}
	for kind, phrase := range map[string]string{"named_type": "named type", "variable": "variable", "package": "package"} {
		if promised, shown := strings.Contains(strings.ToLower(help[1]), phrase), dataFlowKinds()[kind]; promised != shown {
			t.Errorf("Data Flow help mentions %q = %v, but dataFlowKinds()[%q] = %v:\n%s", phrase, promised, kind, shown, help[1])
		}
	}
	for _, kind := range []string{"function", "method", "struct", "interface"} {
		if !dataFlowKinds()[kind] {
			t.Errorf("dataFlowKinds() lost %q", kind)
		}
	}
}
