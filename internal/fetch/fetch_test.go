package fetch

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestFetchBasic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html><head><title>My Page</title></head><body><p>hi</p></body></html>"))
	}))
	defer srv.Close()

	c := NewClientWithOptions(Options{Timeout: 5 * time.Second, MaxBody: 1 << 20, UserAgent: "test-agent/1.0"})
	page, err := c.Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if page.Title != "My Page" {
		t.Errorf("title = %q, want My Page", page.Title)
	}
	if len(page.Body) == 0 {
		t.Error("expected non-empty body")
	}
}

func TestFetchRejectsNonHTTP(t *testing.T) {
	c := NewClientWithOptions(Options{Timeout: 5 * time.Second, MaxBody: 1 << 20, UserAgent: "test-agent/1.0"})
	if _, err := c.Fetch(context.Background(), "ftp://example.com/x"); err == nil {
		t.Fatal("expected error for ftp URL")
	}
}

func TestFetchErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewClientWithOptions(Options{Timeout: 5 * time.Second, MaxBody: 1 << 20, UserAgent: "test-agent/1.0"})
	if _, err := c.Fetch(context.Background(), srv.URL); err == nil {
		t.Fatal("expected error on 404")
	}
}

func TestFetchSendsUserAgent(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c := NewClientWithOptions(Options{Timeout: 5 * time.Second, MaxBody: 1 << 20, UserAgent: "test-agent/1.0"})
	if _, err := c.Fetch(context.Background(), srv.URL); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if gotUA != "test-agent/1.0" {
		t.Errorf("UA = %q, want test-agent/1.0", gotUA)
	}
}

// TestFetchBlocksPrivateIPWithPort ensures the SSRF guard validates the host
// without its port (u.Hostname), so private IPs with a port are still blocked.
func TestFetchBlocksPrivateIPWithPort(t *testing.T) {
	c := NewClientWithOptions(Options{
		Timeout:      5 * time.Second,
		MaxBody:      1 << 20,
		UserAgent:    "test-agent/1.0",
		BlockPrivate: true,
		LookupIP: func(ctx context.Context, network, host string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("192.168.1.50")}, nil
		},
	})
	_, err := c.Fetch(context.Background(), "http://192.168.1.50:8080/")
	if err == nil || !strings.Contains(err.Error(), "blocked by network policy") {
		t.Fatalf("expected network policy block, got: %v", err)
	}
}

// TestFetchPublicHostnameWithPort ensures a public hostname on a non-default
// port is NOT blocked by the SSRF guard (regression: u.Host included the port).
func TestFetchPublicHostnameWithPort(t *testing.T) {
	c := NewClientWithOptions(Options{
		Timeout:      5 * time.Second,
		MaxBody:      1 << 20,
		UserAgent:    "test-agent/1.0",
		BlockPrivate: true,
		LookupIP: func(ctx context.Context, network, host string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("8.8.8.8")}, nil
		},
	})
	_, err := c.Fetch(context.Background(), "http://example.com:8080/")
	if err == nil {
		t.Fatal("expected connection error (not blocked by policy)")
	}
	if strings.Contains(err.Error(), "blocked by network policy") || strings.Contains(err.Error(), "no such host") {
		t.Fatalf("request was blocked by policy instead of reaching the network: %v", err)
	}
}

func TestFetchSendsBrowserHeaders(t *testing.T) {
	var got map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = map[string]string{
			"Accept":                    r.Header.Get("Accept"),
			"Accept-Language":           r.Header.Get("Accept-Language"),
			"Sec-Fetch-Dest":            r.Header.Get("Sec-Fetch-Dest"),
			"Sec-Fetch-Mode":            r.Header.Get("Sec-Fetch-Mode"),
			"Sec-Fetch-Site":            r.Header.Get("Sec-Fetch-Site"),
			"Upgrade-Insecure-Requests": r.Header.Get("Upgrade-Insecure-Requests"),
			"User-Agent":                r.Header.Get("User-Agent"),
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c := NewClientWithOptions(Options{Timeout: 5 * time.Second, MaxBody: 1 << 20})
	if _, err := c.Fetch(context.Background(), srv.URL); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if got["Accept-Language"] == "" {
		t.Error("expected Accept-Language header")
	}
	if got["Sec-Fetch-Dest"] != "document" || got["Sec-Fetch-Mode"] != "navigate" {
		t.Errorf("expected document Sec-Fetch headers, got %+v", got)
	}
	if !strings.HasPrefix(got["User-Agent"], "Mozilla/5.0") {
		t.Errorf("default UA should look like a browser, got %q", got["User-Agent"])
	}
	if got["Upgrade-Insecure-Requests"] != "1" {
		t.Errorf("Upgrade-Insecure-Requests = %q, want 1", got["Upgrade-Insecure-Requests"])
	}
}

func TestFetchRetriesOn429(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			http.Error(w, "slow down", http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html><head><title>Ok</title></head><body>hi</body></html>"))
	}))
	defer srv.Close()

	c := NewClientWithOptions(Options{Timeout: 5 * time.Second, MaxBody: 1 << 20})
	page, err := c.Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if calls != 2 {
		t.Errorf("expected 2 attempts (1 retry), got %d", calls)
	}
	if page.Title != "Ok" {
		t.Errorf("title = %q, want Ok", page.Title)
	}
}

func TestFetchDoesNotRetryOn404(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewClientWithOptions(Options{Timeout: 5 * time.Second, MaxBody: 1 << 20})
	if _, err := c.Fetch(context.Background(), srv.URL); err == nil {
		t.Fatal("expected error on 404")
	}
	if calls != 1 {
		t.Errorf("expected 1 attempt on 404, got %d", calls)
	}
}

func TestFetchRetriesOn5xx(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			http.Error(w, "boom", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("recovered"))
	}))
	defer srv.Close()

	c := NewClientWithOptions(Options{Timeout: 5 * time.Second, MaxBody: 1 << 20})
	if _, err := c.Fetch(context.Background(), srv.URL); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if calls != 2 {
		t.Errorf("expected 2 attempts on 502, got %d", calls)
	}
}

func TestParseRetryAfter(t *testing.T) {
	resp := &http.Response{Header: http.Header{}}

	if got := parseRetryAfter(resp); got != 0 {
		t.Errorf("parseRetryAfter(no header) = %d, want 0", got)
	}
	resp.Header.Set("Retry-After", "3")
	if got := parseRetryAfter(resp); got != 3 {
		t.Errorf("parseRetryAfter(3) = %d, want 3", got)
	}
	resp.Header.Set("Retry-After", time.Now().UTC().Add(2*time.Second).Format(http.TimeFormat))
	if got := parseRetryAfter(resp); got != 2 {
		t.Errorf("parseRetryAfter(http-date) = %d, want 2", got)
	}
}

func TestRetryDelayCapsRetryAfter(t *testing.T) {
	if d := retryDelay(0, 0); d < 400*time.Millisecond {
		t.Errorf("exponential backoff too small: %v", d)
	}
	if d := retryDelay(0, 30); d > 5*time.Second {
		t.Errorf("Retry-After should be capped at 5s, got %v", d)
	}
	if d := retryDelay(0, 2); d != 2*time.Second {
		t.Errorf("Retry-After=2 should win over exponential backoff, got %v", d)
	}
}

type fakeRenderer struct {
	calls int
	page  *Page
	err   error
}

func (f *fakeRenderer) Render(ctx context.Context, u *url.URL, ua string) (*Page, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	if f.page != nil {
		return f.page, nil
	}
	return &Page{URL: u.String(), Title: "rendered", Body: []byte("<html><body>rendered</body></html>")}, nil
}

func TestFetchRenderForcedByOption(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html><body>direct</body></html>"))
	}))
	defer srv.Close()

	c := NewClientWithOptions(Options{Timeout: 5 * time.Second, MaxBody: 1 << 20})
	fake := &fakeRenderer{}
	c.renderer = fake

	page, err := c.FetchWithOptions(context.Background(), srv.URL, FetchOptions{Render: true})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if fake.calls != 1 {
		t.Fatalf("renderer should be called once, got %d", fake.calls)
	}
	if page.Title != "rendered" {
		t.Errorf("title = %q, want rendered (from renderer)", page.Title)
	}
}

func TestFetchRenderAlwaysMode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("direct"))
	}))
	defer srv.Close()

	c := NewClientWithOptions(Options{Timeout: 5 * time.Second, MaxBody: 1 << 20, JSRenderMode: renderAlways})
	fake := &fakeRenderer{}
	c.renderer = fake

	if _, err := c.Fetch(context.Background(), srv.URL); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if fake.calls != 1 {
		t.Fatalf("renderer should be called in always mode, got %d", fake.calls)
	}
}

func TestFetchAutoFallsBackOnBlock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "blocked", http.StatusForbidden)
	}))
	defer srv.Close()

	c := NewClientWithOptions(Options{Timeout: 5 * time.Second, MaxBody: 1 << 20, JSRenderMode: renderAuto})
	fake := &fakeRenderer{}
	c.renderer = fake

	page, err := c.Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if fake.calls != 1 {
		t.Fatalf("renderer should be used as fallback on 403, got %d calls", fake.calls)
	}
	if page.Title != "rendered" {
		t.Errorf("title = %q, want rendered", page.Title)
	}
}

func TestFetchAutoDoesNotFallbackOn404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewClientWithOptions(Options{Timeout: 5 * time.Second, MaxBody: 1 << 20, JSRenderMode: renderAuto})
	fake := &fakeRenderer{}
	c.renderer = fake

	if _, err := c.Fetch(context.Background(), srv.URL); err == nil {
		t.Fatal("expected error on 404")
	}
	if fake.calls != 0 {
		t.Errorf("renderer should not run on 404, got %d calls", fake.calls)
	}
}

func TestFetchNeverModeIgnoresRenderer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html><body>direct</body></html>"))
	}))
	defer srv.Close()

	c := NewClientWithOptions(Options{Timeout: 5 * time.Second, MaxBody: 1 << 20})
	fake := &fakeRenderer{}
	c.renderer = fake

	if _, err := c.Fetch(context.Background(), srv.URL); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if fake.calls != 0 {
		t.Errorf("renderer should not be called in never mode, got %d calls", fake.calls)
	}
}

func TestShouldRetryAndIsBlockable(t *testing.T) {
	ok404 := &StatusError{Status: 404}
	ok429 := &StatusError{Status: 429}
	ok498 := &StatusError{Status: 498}
	ok403 := &StatusError{Status: 403}
	ok500 := &StatusError{Status: 500}

	if shouldRetry(ok404) {
		t.Error("404 should not be retried")
	}
	for _, e := range []error{ok429, ok498, ok500} {
		if !shouldRetry(e) {
			t.Errorf("%v should be retried", e)
		}
	}
	if !shouldRetry(errors.New("tls handshake timeout")) {
		t.Error("transport errors should be retried")
	}

	if isBlockable(ok404) {
		t.Error("404 should not trigger renderer fallback")
	}
	for _, e := range []error{ok403, ok429, ok498, ok500} {
		if !isBlockable(e) {
			t.Errorf("%v should trigger renderer fallback", e)
		}
	}
}
