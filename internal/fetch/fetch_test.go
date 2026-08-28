package fetch

import (
	"context"
	"net/http"
	"net/http/httptest"
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
