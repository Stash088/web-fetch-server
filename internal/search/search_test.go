package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
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

func TestSearchParsesEngines(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"results":[
			{"title":"T1","url":"https://a.com","content":"snippet one","engines":["google","bing"]},
			{"title":"T2","url":"https://b.com","content":"snippet two","engines":["ddg"]}
		]}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "", 5*time.Second)
	res, err := c.Search(context.Background(), "golang", "", "", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("got %d results, want 2", len(res))
	}
	if want := []string{"google", "bing"}; !reflect.DeepEqual(res[0].Engines, want) {
		t.Errorf("Engines[0] = %v, want %v", res[0].Engines, want)
	}
	if want := []string{"ddg"}; !reflect.DeepEqual(res[1].Engines, want) {
		t.Errorf("Engines[1] = %v, want %v", res[1].Engines, want)
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

func TestDedupResults(t *testing.T) {
	in := []Result{
		{Title: "a", URL: "https://docs.docker.com/compose/"},
		{Title: "b", URL: "https://docs.docker.com/compose/?utm_source=bing&msockid=abc"},
		{Title: "c", URL: "https://docs.docker.com/compose/#restart"},
		{Title: "d", URL: "https://docs.Docker.com/compose"},
		{Title: "e", URL: "https://docs.docker.com/engine/"},
		{Title: "f", URL: "https://example.com/"},
	}
	got := dedupResults(in)
	if len(got) != 3 {
		t.Fatalf("got %d results after dedup, want 3: %+v", len(got), got)
	}
	want := []string{"https://docs.docker.com/compose/", "https://docs.docker.com/engine/", "https://example.com/"}
	for i, w := range want {
		if got[i].URL != w {
			t.Errorf("URL[%d] = %q, want %q", i, got[i].URL, w)
		}
	}
}
