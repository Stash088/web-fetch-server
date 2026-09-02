package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/amir/web-fetch-server/internal/config"
)

func TestStartIndexBeyondEndReturnsError(t *testing.T) {
	pageSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html><head><title>Short</title></head><body><p>just a short page</p></body></html>"))
	}))
	defer pageSrv.Close()

	cfg := config.Load()
	cfg.BlockPrivateNetworks = false
	server := BuildWithLogger(cfg, nil, CacheDeps{})
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), t1, nil); err != nil {
		t.Fatal(err)
	}
	session, err := client.Connect(context.Background(), t2, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "web_fetch",
		Arguments: map[string]any{"url": pageSrv.URL, "start_index": 99999},
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected tool error for start_index beyond the end, got %+v", res)
	}
	var msg string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			msg += tc.Text
		}
	}
	if !strings.Contains(msg, "start_index") || !strings.Contains(msg, "beyond the end") {
		t.Errorf("error should name start_index and the end of content, got: %q", msg)
	}

	// A valid continuation request still works.
	out, isErr := callTool(t, session, "web_fetch", map[string]any{"url": pageSrv.URL + "?x=1", "start_index": 0, "max_length": 5})
	if isErr {
		t.Fatalf("start_index 0 returned tool error (out=%v)", out)
	}
}

func TestExtractionFallbackFlagged(t *testing.T) {
	// A page whose bulk is an enormous <script> block with almost no visible
	// text: readability finds no article, the full page is converted instead,
	// and the response must say so instead of presenting a raw dump.
	script := "<script>/* " + strings.Repeat("var x=1;", 4000) + " */</script>"
	fallbackSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html><head><title>JS page</title></head><body>" + script + "<p>tiny</p></body></html>"))
	}))
	defer fallbackSrv.Close()

	articleSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><head><title>Article</title></head><body><article><h1>Real Article</h1>` +
			strings.Repeat(`<p>This is a real article paragraph with plenty of meaningful article text for readability to pick up and keep as the main content of the page.</p>`, 8) +
			`</article></body></html>`))
	}))
	defer articleSrv.Close()

	cfg := config.Load()
	cfg.BlockPrivateNetworks = false
	session, closeFn := newCacheTestServer(t, cfg, CacheDeps{})
	defer closeFn()

	out, isErr := callTool(t, session, "web_fetch", map[string]any{"url": fallbackSrv.URL})
	if isErr {
		t.Fatal("fallback fetch returned tool error")
	}
	if extracted, _ := out["extracted"].(bool); extracted {
		t.Error("extracted = true, want false for a fallback page")
	}
	meta, _ := out["metadata"].(map[string]any)
	if v, _ := meta["extraction_fallback"].(string); v != "full_page" {
		t.Errorf("metadata extraction_fallback = %v, want full_page (metadata=%v)", meta["extraction_fallback"], meta)
	}

	out, isErr = callTool(t, session, "web_fetch", map[string]any{"url": articleSrv.URL})
	if isErr {
		t.Fatal("article fetch returned tool error")
	}
	if extracted, _ := out["extracted"].(bool); !extracted {
		t.Errorf("extracted = false, want true for an article page (out=%v)", out)
	}
	meta, _ = out["metadata"].(map[string]any)
	if _, has := meta["extraction_fallback"]; has {
		t.Error("article page must not carry extraction_fallback metadata")
	}
}

func TestOversizedExtractionFlagged(t *testing.T) {
	// An infobox-heavy portal that readability "successfully" extracts as a
	// 100K+ rune dump: still extracted, but flagged so the model knows it is
	// not a curated article.
	paras := strings.Repeat(
		`<p>Секция таблицы с данными и параметрами региона, населением, площадью и прочими характеристиками.</p>`, 1800)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html><head><title>Portal</title></head><body><article><h1>Портал</h1>" + paras + "</article></body></html>"))
	}))
	defer srv.Close()

	cfg := config.Load()
	cfg.BlockPrivateNetworks = false
	session, closeFn := newCacheTestServer(t, cfg, CacheDeps{})
	defer closeFn()

	out, isErr := callTool(t, session, "web_fetch", map[string]any{"url": srv.URL, "max_length": 200})
	if isErr {
		t.Fatal("oversized fetch returned tool error")
	}
	if extracted, _ := out["extracted"].(bool); !extracted {
		t.Errorf("extracted = false, want true (readability keeps this page)")
	}
	meta, _ := out["metadata"].(map[string]any)
	if v, _ := meta["extraction_fallback"].(string); v != "oversized_extract" {
		t.Errorf("metadata extraction_fallback = %v, want oversized_extract", meta["extraction_fallback"])
	}
}

func TestSearchCategoriesArgAndEnginesStatus(t *testing.T) {
	var gotCats string
	searchSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCats = r.URL.Query().Get("categories")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"results":[{"title":"T","url":"https://a.example","content":"s","engine":"google cse"}],
		"unresponsive_engines":[["google cse","too many requests"]]}`))
	}))
	defer searchSrv.Close()

	cfg := config.Load()
	cfg.SearxngURL = searchSrv.URL
	session, closeFn := newCacheTestServer(t, cfg, CacheDeps{})
	defer closeFn()

	// Default (cfg.SearchCategories) is sent as-is.
	out, isErr := callTool(t, session, "web_search", map[string]any{"query": "пуэр"})
	if isErr {
		t.Fatal("web_search returned tool error")
	}
	if gotCats != cfg.SearchCategories {
		t.Errorf("default categories = %q, want %q", gotCats, cfg.SearchCategories)
	}
	if st, _ := out["engines_status"].([]any); len(st) != 1 || st[0] != "google cse: too many requests" {
		t.Errorf("engines_status = %v, want google cse quota reason", out["engines_status"])
	}

	// Per-call override reaches SearXNG.
	if _, isErr := callTool(t, session, "web_search", map[string]any{"query": "пуэр", "categories": "general,it"}); isErr {
		t.Fatal("web_search with categories returned tool error")
	}
	if gotCats != "general,it" {
		t.Errorf("per-call categories = %q, want general,it", gotCats)
	}
}
