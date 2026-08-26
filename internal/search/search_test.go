package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSearchParsesResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("q"); got != "golang" {
			t.Errorf("q = %q, want golang", got)
		}
		if got := r.URL.Query().Get("format"); got != "json" {
			t.Errorf("format = %q, want json", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"results":[
			{"title":"T1","url":"https://a.com","content":"snippet one","engine":"google"},
			{"title":"T2","url":"https://b.com","content":"snippet two","engine":"bing"}
		]}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "", 5*time.Second)
	res, err := c.Search(context.Background(), "golang", "en", "", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("got %d results, want 2", len(res))
	}
	if res[0].Title != "T1" || res[0].URL != "https://a.com" || res[0].Snippet != "snippet one" {
		t.Errorf("unexpected first result: %+v", res[0])
	}
}

func TestSearchRespectsMaxResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"results":[
			{"title":"A","url":"https://a.com","content":"x","engine":"g"},
			{"title":"B","url":"https://b.com","content":"y","engine":"g"},
			{"title":"C","url":"https://c.com","content":"z","engine":"g"}
		]}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "", 5*time.Second)
	res, err := c.Search(context.Background(), "q", "", "", 2)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("got %d results, want 2", len(res))
	}
}

func TestSearchErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusForbidden)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "", 5*time.Second)
	if _, err := c.Search(context.Background(), "q", "", "", 10); err == nil {
		t.Fatal("expected error on 403")
	}
}
