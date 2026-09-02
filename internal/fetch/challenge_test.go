package fetch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestChallengeWithSuccessStatus verifies the 200-OK challenge case: a WAF
// page served with HTTP 200 is classified as a block, returned as BlockError,
// and never retried (a challenge does not resolve by retrying).
func TestChallengeWithSuccessStatus(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><html><head><title>Just a moment...</title></head>
<body><script>window._cf_chl_opt = {"chl":1};</script></body></html>`))
	}))
	defer srv.Close()

	c := NewClientWithOptions(Options{Timeout: 5 * time.Second, MaxBody: 1 << 20, BlockPrivate: false})
	_, err := c.FetchWithOptions(context.Background(), srv.URL, FetchOptions{})
	if err == nil {
		t.Fatal("expected BlockError for a 200-OK challenge page, got nil")
	}
	if got := BlockKindOf(err); got != BlockChallengeCloudflare {
		t.Errorf("BlockKindOf = %q, want %q", got, BlockChallengeCloudflare)
	}
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Errorf("expected exactly 1 request (no retries on BlockError), got %d", n)
	}
}

func TestShouldRetryAndIsBlockableOnBlockError(t *testing.T) {
	be := &BlockError{Kind: BlockChallengeJS, URL: "https://x.example"}
	if shouldRetry(be) {
		t.Error("shouldRetry(BlockError) = true, want false")
	}
	if !isBlockable(be) {
		t.Error("isBlockable(BlockError) = false, want true")
	}
}

func TestClientHintsFromUA(t *testing.T) {
	ua := "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"
	req, _ := http.NewRequest(http.MethodGet, "https://example.com", nil)
	applyBrowserHeaders(req, ua)

	if got := req.Header.Get("Sec-CH-UA"); got != `"Chromium";v="126", "Google Chrome";v="126", "Not-A.Brand";v="99"` {
		t.Errorf("Sec-CH-UA = %q", got)
	}
	if got := req.Header.Get("Sec-CH-UA-Mobile"); got != "?0" {
		t.Errorf("Sec-CH-UA-Mobile = %q", got)
	}
	if got := req.Header.Get("Sec-CH-UA-Platform"); got != `"macOS"` {
		t.Errorf("Sec-CH-UA-Platform = %q", got)
	}

	winUA := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"
	if got := chromePlatform(winUA); got != `"Windows"` {
		t.Errorf("platform for Windows UA = %q", got)
	}
	if got := chromeBrand(winUA); !containsAll(got, "130") {
		t.Errorf("brand should carry UA major version, got %q", got)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		found := false
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
