package rerank

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/amir/web-fetch-server/internal/search"
)

func scoreServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func scoreFnFor(srvURL string, timeout time.Duration) ScoreFn {
	return NewRouterAIScoreFn(srvURL, "test-key", "test-model", timeout)
}

func urlOrder(in []search.Result) []string {
	return urls(in)
}

func TestSemanticAddsVote(t *testing.T) {
	in := []search.Result{
		{Title: "Новости спорта", URL: "https://sport.example", Snippet: "Обзор матча и интервью после игры"},
		{Title: "Рецепт борща", URL: "https://food.example", Snippet: "Пошаговый рецепт со свёклой"},
		{Title: "Расписание электричек", URL: "https://trains.example", Snippet: "Пригородные поезда на сегодня"},
	}
	srv := scoreServer(t, 200, `{"results":[
		{"index":2,"relevance_score":0.99},
		{"index":0,"relevance_score":0.5},
		{"index":1,"relevance_score":0.1}
	]}`)

	got := NewSemantic(nil, scoreFnFor(srv.URL, time.Second)).Rank("совсем другой запрос", in)
	if got[0].URL != "https://trains.example" {
		t.Fatalf("semantic vote should lift the top-scored doc, got %s first", got[0].URL)
	}
	if !got[0].Reranked || got[0].Score <= 0 {
		t.Fatalf("expected reranked=true and score>0, got %+v", got[0])
	}
}

func TestSemanticFallbackOnError(t *testing.T) {
	in := []search.Result{
		{Title: "a", URL: "https://a.example", Snippet: "nginx настройка сервера"},
		{Title: "b", URL: "https://b.example", Snippet: "nginx конфигурация proxy"},
		{Title: "c", URL: "https://c.example", Snippet: "борщ рецепт"},
	}
	want := urlOrder(NewRRF(nil).Rank("nginx", in))

	tests := []struct {
		name    string
		status  int
		body    string
		timeout time.Duration
		sleep   time.Duration
	}{
		{name: "500", status: 500, body: "boom", timeout: time.Second},
		{name: "401", status: 401, body: `{"error":"unauthorized"}`, timeout: time.Second},
		{name: "bad json", status: 200, body: `not json at all`, timeout: time.Second},
		{name: "empty results", status: 200, body: `{"results":[]}`, timeout: time.Second},
		{name: "timeout", status: 200, body: `{"results":[{"index":0,"relevance_score":0.9}]}`, timeout: 10 * time.Millisecond, sleep: 50 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.sleep > 0 {
					time.Sleep(tt.sleep)
				}
				w.WriteHeader(tt.status)
				w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			got := NewSemantic(nil, scoreFnFor(srv.URL, tt.timeout)).Rank("nginx", in)
			if !reflect.DeepEqual(urlOrder(got), want) {
				t.Fatalf("on %s error should fall back to rrf order: got %v, want %v", tt.name, urlOrder(got), want)
			}
			for i, r := range got {
				if !r.Reranked {
					t.Errorf("result %d: reranked=false after fallback", i)
				}
			}
		})
	}
}

func TestSemanticScoreFieldFallback(t *testing.T) {
	in := []search.Result{
		{Title: "a", URL: "https://a.example", Snippet: "текст один"},
		{Title: "b", URL: "https://b.example", Snippet: "текст два"},
	}
	srv := scoreServer(t, 200, `{"results":[{"index":1,"score":0.9},{"index":0,"score":0.2}]}`)

	got := NewSemantic(nil, scoreFnFor(srv.URL, time.Second)).Rank("запрос", in)
	if got[0].URL != "https://b.example" {
		t.Fatalf("score field should be parsed as fallback, got %s first", got[0].URL)
	}
}

func TestSemanticTruncatesDocs(t *testing.T) {
	in := []search.Result{
		{Title: "a", URL: "https://a.example", Snippet: strings.Repeat("х", 3000)},
		{Title: "b", URL: "https://b.example", Snippet: strings.Repeat("y", 500)},
	}

	var gotDocs []string
	var gotQuery, gotModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Model     string   `json:"model"`
			Query     string   `json:"query"`
			Documents []string `json:"documents"`
		}
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, &payload); err != nil {
			t.Errorf("decode request: %v", err)
		}
		gotDocs = payload.Documents
		gotQuery = payload.Query
		gotModel = payload.Model
		w.Write([]byte(`{"results":[{"index":0,"relevance_score":0.5},{"index":1,"relevance_score":0.4}]}`))
	}))
	defer srv.Close()

	got := NewSemantic(nil, scoreFnFor(srv.URL, time.Second)).Rank("мой запрос", in)
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2", len(got))
	}
	if gotQuery != "мой запрос" || gotModel != "test-model" {
		t.Errorf("query/model mismatch: query=%q model=%q", gotQuery, gotModel)
	}
	if len(gotDocs) != 2 {
		t.Fatalf("got %d documents, want 2", len(gotDocs))
	}
	for i, d := range gotDocs {
		if n := len([]rune(d)); n > maxDocRunes {
			t.Errorf("doc %d truncated to %d runes, want <= %d", i, n, maxDocRunes)
		}
	}
	if gotDocs[1] != "b "+strings.Repeat("y", 500) {
		t.Errorf("short doc should pass through unchanged, got %d runes: %q", len([]rune(gotDocs[1])), gotDocs[1])
	}
}

func TestSemanticPassthroughNoAPICall(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.Write([]byte(`{"results":[{"index":0,"relevance_score":0.9}]}`))
	}))
	defer srv.Close()
	rk := NewSemantic(nil, scoreFnFor(srv.URL, time.Second))

	if got := rk.Rank("", nil); got != nil {
		t.Errorf("empty query + nil list: got %v, want nil", got)
	}
	one := []search.Result{{Title: "x", URL: "https://x.example", Snippet: "y"}}
	if got := rk.Rank("запрос", one); !reflect.DeepEqual(got, one) {
		t.Errorf("single result should pass through, got %+v", got)
	}
	three := []search.Result{
		{Title: "a", URL: "https://a.example", Snippet: "текст"},
		{Title: "b", URL: "https://b.example", Snippet: "текст"},
	}
	if got := rk.Rank("   ", three); !reflect.DeepEqual(got, three) {
		t.Errorf("blank query should pass through, got %+v", got)
	}
	if called {
		t.Error("rerank API must not be called for passthrough cases")
	}
}

func TestSemanticIgnoresOutOfBoundsIndex(t *testing.T) {
	in := []search.Result{
		{Title: "a", URL: "https://a.example", Snippet: "текст один"},
		{Title: "b", URL: "https://b.example", Snippet: "текст два"},
	}
	srv := scoreServer(t, 200, `{"results":[{"index":0,"relevance_score":0.3},{"index":5,"relevance_score":0.99}]}`)

	got := NewSemantic(nil, scoreFnFor(srv.URL, time.Second)).Rank("запрос", in)
	if got[0].URL != "https://a.example" {
		t.Fatalf("out-of-range index must be ignored, got %s first", got[0].URL)
	}
}

func TestContextCancellationPropagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.Write([]byte(`{"results":[{"index":0,"relevance_score":0.9}]}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	fn := NewRouterAIScoreFn(srv.URL, "k", "m", 5*time.Second)
	if _, err := fn(ctx, "q", []string{"a", "b"}); err == nil {
		t.Fatal("expected error on cancelled context")
	}
}
