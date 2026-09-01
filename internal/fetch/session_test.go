package fetch

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
)

// TestRenderSessionCookiePersistence verifies the core T1 property: the
// browser profile (and its cookies) survives between renders, so the second
// request to the same host carries the cookie the first one received. This is
// what lets a cf_clearance cookie stop challenge pages from re-appearing.
func TestRenderSessionCookiePersistence(t *testing.T) {
	var mu sync.Mutex
	var received []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/" {
			http.NotFound(w, req) // ignore favicon and other side requests
			return
		}
		val := ""
		if ck, err := req.Cookie("probe"); err == nil {
			val = ck.Value
		}
		mu.Lock()
		received = append(received, val)
		n := len(received)
		mu.Unlock()
		if val == "" {
			http.SetCookie(w, &http.Cookie{Name: "probe", Value: "persisted-1", Path: "/"})
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<!doctype html><html><head><title>Cookie probe %d</title></head><body>sent=%s</body></html>`, n, val)
	}))
	defer srv.Close()

	r := NewChromeRenderer(RendererOptions{ChromeBin: testChromeBin()}).(*chromeRenderer)
	defer r.Close()
	if err := r.ensureChrome(); err != nil {
		t.Skip("no chrome binary available:", err)
	}

	for i := 0; i < 2; i++ {
		u, err := url.Parse(srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.Render(context.Background(), u, "test-agent"); err != nil {
			t.Fatalf("render %d: %v", i+1, err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(received))
	}
	if received[0] != "" {
		t.Errorf("first request must be cookie-less, got %q", received[0])
	}
	if received[1] != "persisted-1" {
		t.Errorf("second request must carry the cookie from the first session, got %q", received[1])
	}
}
