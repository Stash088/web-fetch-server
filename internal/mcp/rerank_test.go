package mcp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/amir/web-fetch-server/internal/config"
)

func TestWebSearchRerankWiring(t *testing.T) {
	const fixture = `{"results":[
		{"title":"Новости спорта","url":"https://sport.example","content":"обзор матча и интервью","engines":["google"]},
		{"title":"nginx reverse proxy: настройка","url":"https://nginx.example","content":"руководство по настройке nginx reverse proxy","engines":["google"]},
		{"title":"Рецепты","url":"https://food.example","content":"борщ, плов и пироги","engines":["google","bing"]}
	]}`
	searchSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(fixture))
	}))
	defer searchSrv.Close()

	t.Run("default rrf reranks", func(t *testing.T) {
		cfg := config.Load()
		cfg.SearxngURL = searchSrv.URL
		session, closeFn := newCacheTestServer(t, cfg, CacheDeps{})
		defer closeFn()

		out, isErr := callTool(t, session, "web_search", map[string]any{"query": "настройка nginx"})
		if isErr {
			t.Fatal("web_search returned tool error")
		}
		results, ok := out["results"].([]any)
		if !ok || len(results) != 3 {
			t.Fatalf("unexpected results: %v", out)
		}
		first, _ := results[0].(map[string]any)
		if first["url"] != "https://nginx.example" {
			t.Fatalf("relevant result should rank first, got %v", first["url"])
		}
		if reranked, _ := first["reranked"].(bool); !reranked {
			t.Errorf("reranked flag missing on first result: %v", first)
		}
		if _, ok := first["score"].(float64); !ok {
			t.Errorf("score missing on first result: %v", first)
		}
	})

	t.Run("none passthrough", func(t *testing.T) {
		cfg := config.Load()
		cfg.SearxngURL = searchSrv.URL
		cfg.RerankMode = "none"
		session, closeFn := newCacheTestServer(t, cfg, CacheDeps{})
		defer closeFn()

		out, isErr := callTool(t, session, "web_search", map[string]any{"query": "настройка nginx"})
		if isErr {
			t.Fatal("web_search returned tool error")
		}
		results, ok := out["results"].([]any)
		if !ok || len(results) != 3 {
			t.Fatalf("unexpected results: %v", out)
		}
		wantOrder := []string{"https://sport.example", "https://nginx.example", "https://food.example"}
		for i, r := range results {
			m, _ := r.(map[string]any)
			if m["url"] != wantOrder[i] {
				t.Fatalf("passthrough order broken at %d: got %v, want %v", i, m["url"], wantOrder[i])
			}
			if _, has := m["reranked"]; has {
				t.Errorf("result %d: reranked must be absent in passthrough", i)
			}
			if _, has := m["score"]; has {
				t.Errorf("result %d: score must be absent in passthrough", i)
			}
		}
	})

	rerankAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"results":[{"index":0,"relevance_score":0.99},{"index":1,"relevance_score":0.5},{"index":2,"relevance_score":0.1}]}`))
	}))
	defer rerankAPI.Close()

	t.Run("semantic uses rerank api", func(t *testing.T) {
		cfg := config.Load()
		cfg.SearxngURL = searchSrv.URL
		cfg.RerankMode = "semantic"
		cfg.RerankAPIURL = rerankAPI.URL
		cfg.RerankAPIKey = "test"
		session, closeFn := newCacheTestServer(t, cfg, CacheDeps{})
		defer closeFn()

		out, isErr := callTool(t, session, "web_search", map[string]any{"query": "что нового"})
		if isErr {
			t.Fatal("web_search returned tool error")
		}
		results, _ := out["results"].([]any)
		if len(results) != 3 {
			t.Fatalf("unexpected results: %v", out)
		}
		first, _ := results[0].(map[string]any)
		if first["url"] != "https://sport.example" {
			t.Fatalf("semantic vote should lift the api-top result, got %v", first["url"])
		}
		if reranked, _ := first["reranked"].(bool); !reranked {
			t.Errorf("reranked flag missing: %v", first)
		}
	})

	t.Run("semantic without key falls back to rrf", func(t *testing.T) {
		cfg := config.Load()
		cfg.SearxngURL = searchSrv.URL
		cfg.RerankMode = "semantic"
		cfg.RerankAPIKey = ""
		session, closeFn := newCacheTestServer(t, cfg, CacheDeps{})
		defer closeFn()

		out, isErr := callTool(t, session, "web_search", map[string]any{"query": "настройка nginx"})
		if isErr {
			t.Fatal("web_search returned tool error")
		}
		results, _ := out["results"].([]any)
		first, _ := results[0].(map[string]any)
		if first["url"] != "https://nginx.example" {
			t.Fatalf("fallback should behave as rrf, got %v first", first["url"])
		}
		if reranked, _ := first["reranked"].(bool); !reranked {
			t.Errorf("reranked flag missing: %v", first)
		}
	})
}
