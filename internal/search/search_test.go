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
	resp, err := c.Search(context.Background(), "golang", "en", "", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("got %d results, want 2", len(resp.Results))
	}
	r := resp.Results[0]
	if r.Title != "T1" || r.URL != "https://a.com" || r.Snippet != "snippet one" {
		t.Errorf("unexpected first result: %+v", r)
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
	resp, err := c.Search(context.Background(), "q", "", "", 2)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("got %d results, want 2", len(resp.Results))
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
	resp, err := c.Search(context.Background(), "golang", "", "", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("got %d results, want 2", len(resp.Results))
	}
	if want := []string{"google", "bing"}; !reflect.DeepEqual(resp.Results[0].Engines, want) {
		t.Errorf("Engines[0] = %v, want %v", resp.Results[0].Engines, want)
	}
	if want := []string{"ddg"}; !reflect.DeepEqual(resp.Results[1].Engines, want) {
		t.Errorf("Engines[1] = %v, want %v", resp.Results[1].Engines, want)
	}
}

func TestSearchUnresponsiveEngines(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"results":[
			{"title":"T1","url":"https://a.com","content":"x","engine":"google cse"}
		],
		"unresponsive_engines":[["google cse","too many requests"],["duckduckgo","CAPTCHA"]]}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "", 5*time.Second)
	resp, err := c.Search(context.Background(), "q", "", "", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	want := []string{"google cse: too many requests", "duckduckgo: CAPTCHA"}
	if !reflect.DeepEqual(resp.UnresponsiveEngines, want) {
		t.Errorf("UnresponsiveEngines = %v, want %v", resp.UnresponsiveEngines, want)
	}
}

func TestSearchInCategoriesParam(t *testing.T) {
	var gotCats string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCats = r.URL.Query().Get("categories")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "", 5*time.Second).WithCategories("general")
	if _, err := c.Search(context.Background(), "q", "", "", 10); err != nil {
		t.Fatalf("search: %v", err)
	}
	if gotCats != "general" {
		t.Errorf("default categories = %q, want general", gotCats)
	}
	if _, err := c.SearchIn(context.Background(), "general,it", "q", "", "", 10); err != nil {
		t.Fatalf("search-in: %v", err)
	}
	if gotCats != "general,it" {
		t.Errorf("per-call categories = %q, want general,it", gotCats)
	}
	// The per-call override must not leak into the client default.
	if _, err := c.Search(context.Background(), "q", "", "", 10); err != nil {
		t.Fatalf("search after override: %v", err)
	}
	if gotCats != "general" {
		t.Errorf("categories after override = %q, want general (override leaked)", gotCats)
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
