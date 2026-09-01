package fetch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testChromeBin returns a usable Chrome/Chromium binary for tests: CHROME_BIN
// when set, otherwise a Chrome for Testing install from the playwright cache.
func testChromeBin() string {
	if b := os.Getenv("CHROME_BIN"); b != "" {
		return b
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	found, _ := filepath.Glob(filepath.Join(home, "Library/Caches/ms-playwright/chromium-*/chrome-mac-arm64/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing"))
	if len(found) == 0 {
		return ""
	}
	return found[len(found)-1]
}

// TestRenderStealthInjection renders a local probe page and verifies the
// stealth patches actually ran before the page scripts: navigator.webdriver
// must not be truthy, window.chrome present, and the navigator plugins and
// languages mocks in place.
func TestRenderStealthInjection(t *testing.T) {
	r := NewChromeRenderer(RendererOptions{ChromeBin: testChromeBin()}).(*chromeRenderer)
	if err := r.ensureChrome(); err != nil {
		t.Skip("no chrome binary available:", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><html><head><title>Stealth probe</title></head><body><div id="out"></div>
<script>
document.getElementById('out').textContent = JSON.stringify({
  webdriver: navigator.webdriver,
  chrome: !!window.chrome,
  plugins: navigator.plugins.length,
  languages: (navigator.languages || []).length
});
</script></body></html>`))
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	page, err := r.Render(context.Background(), u, "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36")
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	body := string(page.Body)
	i := strings.Index(body, `id="out">`)
	if i < 0 {
		t.Fatalf("probe output not found in rendered page:\n%s", body)
	}
	rest := body[i+len(`id="out">`):]
	j := strings.Index(rest, "</div>")
	if j < 0 {
		t.Fatalf("probe output not terminated:\n%s", body)
	}
	var probe map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(rest[:j])), &probe); err != nil {
		t.Fatalf("parse probe output %q: %v", rest[:j], err)
	}

	// Real Chrome (not in automation) reports navigator.webdriver === false
	// (see the WebDriver spec / MDN); it is only ever true when automation is
	// on. The stealth script leaves a false value as-is by design, so any
	// truthy value means the patching failed.
	if wd, ok := probe["webdriver"]; ok && wd == true {
		t.Errorf("navigator.webdriver must not be true, got %v", wd)
	}
	if probe["chrome"] != true {
		t.Errorf("window.chrome must be present, got %v", probe["chrome"])
	}
	if n, ok := probe["plugins"].(float64); !ok || n < 1 {
		t.Errorf("navigator.plugins must be mocked with entries, got %v", probe["plugins"])
	}
	if n, ok := probe["languages"].(float64); !ok || n < 1 {
		t.Errorf("navigator.languages must be non-empty, got %v", probe["languages"])
	}
}

// TestRenderChallengeClassifiedAsBlock renders a local page that looks like a
// Cloudflare challenge and verifies the renderer returns a structured
// BlockError instead of challenge HTML.
func TestRenderChallengeClassifiedAsBlock(t *testing.T) {
	r := NewChromeRenderer(RendererOptions{ChromeBin: testChromeBin()}).(*chromeRenderer)
	if err := r.ensureChrome(); err != nil {
		t.Skip("no chrome binary available:", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><html><head><title>Just a moment...</title></head>
<body><script>window._cf_chl_opt = {"chl":1};</script><h1>Checking your browser</h1></body></html>`))
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Render(context.Background(), u, "test-agent")
	if err == nil {
		t.Fatal("expected BlockError for a challenge page, got nil")
	}
	be, ok := err.(*BlockError)
	if !ok {
		t.Fatalf("expected *BlockError, got %T: %v", err, err)
	}
	if be.Kind != BlockChallengeCloudflare {
		t.Errorf("block kind = %q, want %q", be.Kind, BlockChallengeCloudflare)
	}
}
