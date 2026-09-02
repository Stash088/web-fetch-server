package config

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLoadAPIKeys(t *testing.T) {
	tests := []struct {
		name    string
		apiKeys string
		apiKey  string
		want    []string
	}{
		{
			name:    "multiple keys",
			apiKeys: "key1,key2,key3",
			want:    []string{"key1", "key2", "key3"},
		},
		{
			name:    "whitespace and empty entries dropped",
			apiKeys: " key1 , ,key2,, ",
			want:    []string{"key1", "key2"},
		},
		{
			name:    "duplicates deduped",
			apiKeys: "key1,key1,key2,key1",
			want:    []string{"key1", "key2"},
		},
		{
			name:    "single key via API_KEYS",
			apiKeys: "only-one",
			want:    []string{"only-one"},
		},
		{
			name:   "fallback to legacy API_KEY",
			apiKey: "legacy-secret",
			want:   []string{"legacy-secret"},
		},
		{
			name:    "API_KEYS takes precedence over API_KEY",
			apiKeys: "new1,new2",
			apiKey:  "legacy-secret",
			want:    []string{"new1", "new2"},
		},
		{
			name:    "both unset means no auth",
			apiKeys: "",
			want:    nil,
		},
		{
			name:    "blank API_KEYS ignored, fallback applies",
			apiKeys: "  , , ",
			apiKey:  "legacy-secret",
			want:    []string{"legacy-secret"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("API_KEYS", tt.apiKeys)
			t.Setenv("API_KEY", tt.apiKey)
			got := loadAPIKeys()
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("loadAPIKeys() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseKeys(t *testing.T) {
	got := parseKeys(" a ,b,,c , c ")
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseKeys() = %v, want %v", got, want)
	}
}

func TestLoadRerankMode(t *testing.T) {
	tests := []struct {
		rerank string
		want   string
	}{
		{rerank: "", want: "rrf"},
		{rerank: "rrf", want: "rrf"},
		{rerank: "none", want: "none"},
		{rerank: "NONE", want: "none"},
		{rerank: " none ", want: "none"},
		{rerank: "semantic", want: "semantic"},
		{rerank: "SEMANTIC", want: "semantic"},
		{rerank: "bogus", want: "rrf"},
	}
	for _, tt := range tests {
		t.Run("RERANK="+tt.rerank, func(t *testing.T) {
			t.Setenv("RERANK", tt.rerank)
			if got := Load().RerankMode; got != tt.want {
				t.Errorf("RerankMode = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLoadBrowserOptions(t *testing.T) {
	t.Setenv("TLS_FINGERPRINT", "off")
	t.Setenv("JS_RENDER", "auto")
	t.Setenv("JS_RENDER_TIMEOUT", "45s")
	t.Setenv("CHROME_BIN", "/usr/bin/chromium")
	cfg := Load()

	if cfg.TLSFingerprint != "off" {
		t.Errorf("TLSFingerprint = %q, want off", cfg.TLSFingerprint)
	}
	if cfg.JSRenderMode != "auto" {
		t.Errorf("JSRenderMode = %q, want auto", cfg.JSRenderMode)
	}
	if cfg.JSRenderTimeout != 45*time.Second {
		t.Errorf("JSRenderTimeout = %v, want 45s", cfg.JSRenderTimeout)
	}
	if cfg.ChromeBin != "/usr/bin/chromium" {
		t.Errorf("ChromeBin = %q, want /usr/bin/chromium", cfg.ChromeBin)
	}
}

func TestLoadBrowserDefaults(t *testing.T) {
	t.Setenv("TLS_FINGERPRINT", "")
	t.Setenv("JS_RENDER", "")
	t.Setenv("JS_RENDER_TIMEOUT", "")
	t.Setenv("CHROME_BIN", "")
	cfg := Load()

	if cfg.TLSFingerprint != "chrome" {
		t.Errorf("TLSFingerprint default = %q, want chrome", cfg.TLSFingerprint)
	}
	if cfg.JSRenderMode != "never" {
		t.Errorf("JSRenderMode default = %q, want never", cfg.JSRenderMode)
	}
	if cfg.JSRenderTimeout != 30*time.Second {
		t.Errorf("JSRenderTimeout default = %v, want 30s", cfg.JSRenderTimeout)
	}
	if !strings.HasPrefix(cfg.UserAgent, "Mozilla/5.0") {
		t.Errorf("UserAgent default = %q, want a browser-like UA", cfg.UserAgent)
	}
}
