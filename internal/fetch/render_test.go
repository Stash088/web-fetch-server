package fetch

import (
	"context"
	"net/url"
	"strings"
	"testing"
)

func TestEnsureChromeExplicitBinMissing(t *testing.T) {
	r := NewChromeRenderer(RendererOptions{ChromeBin: "/nonexistent/chrome"}).(*chromeRenderer)
	err := r.ensureChrome()
	if err == nil {
		t.Fatal("expected error for missing CHROME_BIN")
	}
	if !strings.Contains(err.Error(), "CHROME_BIN") {
		t.Errorf("error should mention CHROME_BIN, got: %v", err)
	}
}

func TestRenderWithoutBrowserReturnsClearError(t *testing.T) {
	r := NewChromeRenderer(RendererOptions{ChromeBin: "/nonexistent/chrome"}).(*chromeRenderer)
	u, _ := url.Parse("https://example.com/")
	_, err := r.Render(context.Background(), u, "test-agent")
	if err == nil {
		t.Fatal("expected error when browser binary is missing")
	}
	if !strings.Contains(err.Error(), "CHROME_BIN") {
		t.Errorf("error should be actionable, got: %v", err)
	}
}

func TestEnsureChromeAutoDetectFallback(t *testing.T) {
	r := NewChromeRenderer(RendererOptions{}).(*chromeRenderer)
	// On a machine with any of the common browser binaries this passes; without
	// one it must produce a "not found" style error rather than panic.
	if err := r.ensureChrome(); err != nil {
		if !strings.Contains(err.Error(), "not found") && !strings.Contains(err.Error(), "CHROME_BIN") {
			t.Errorf("unexpected error message: %v", err)
		}
	}
}
