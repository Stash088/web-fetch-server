package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/amir/web-fetch-server/internal/cache"
	"github.com/amir/web-fetch-server/internal/config"
	"github.com/amir/web-fetch-server/internal/fetch"
)

// newCacheTestServer wires an MCP session over in-memory transport with the
// given cache deps and a JSON-decoding helper.
func newCacheTestServer(t *testing.T, cfg config.Config, deps CacheDeps) (*mcp.ClientSession, func()) {
	t.Helper()
	server := BuildWithLogger(cfg, nil, deps)

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), t1, nil); err != nil {
		t.Fatal(err)
	}
	session, err := client.Connect(context.Background(), t2, nil)
	if err != nil {
		t.Fatal(err)
	}
	return session, func() { _ = session.Close() }
}

func callTool(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) (map[string]any, bool) {
	t.Helper()
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	var out map[string]any
	if res.StructuredContent != nil {
		b, err := json.Marshal(res.StructuredContent)
		if err != nil {
			t.Fatalf("marshal structured content: %v", err)
		}
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatalf("unmarshal structured content: %v", err)
		}
	}
	return out, res.IsError
}

func TestSearchCacheHit(t *testing.T) {
	var hits int32
	searchSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"results":[{"title":"R1","url":"https://a.example","content":"snippet","engine":"google"}]}`))
	}))
	defer searchSrv.Close()

	cfg := config.Load()
	cfg.SearxngURL = searchSrv.URL
	deps := CacheDeps{Search: cache.New(16, 60000000000)} // 60s
	session, closeFn := newCacheTestServer(t, cfg, deps)
	defer closeFn()

	for i, wantCached := range []bool{false, true} {
		out, isErr := callTool(t, session, "web_search", map[string]any{"query": "golang cache"})
		if isErr {
			t.Fatalf("search %d returned tool error", i)
		}
		if cached, _ := out["cached"].(bool); cached != wantCached {
			t.Errorf("search %d: cached = %v, want %v (out=%v)", i, cached, wantCached, out)
		}
	}
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Errorf("expected 1 backend hit, got %d", n)
	}
}

func TestFetchCacheKeysIncludeFlags(t *testing.T) {
	var hits int32
	pageSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html><head><title>Doc</title></head><body><p>" + "word " + "content body text that stays stable across calls</p></body></html>"))
	}))
	defer pageSrv.Close()

	cfg := config.Load()
	cfg.BlockPrivateNetworks = false
	deps := CacheDeps{Fetch: cache.New(16, 60000000000)}
	session, closeFn := newCacheTestServer(t, cfg, deps)
	defer closeFn()

	// Same args twice → second call is cached.
	for i, wantCached := range []bool{false, true} {
		out, isErr := callTool(t, session, "web_fetch", map[string]any{"url": pageSrv.URL})
		if isErr {
			t.Fatalf("fetch %d returned tool error", i)
		}
		if cached, _ := out["cached"].(bool); cached != wantCached {
			t.Errorf("fetch %d: cached = %v, want %v", i, cached, wantCached)
		}
	}
	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Errorf("same args: expected 1 backend hit, got %d", n)
	}

	// Different format → different cache key → backend hit again.
	if _, isErr := callTool(t, session, "web_fetch", map[string]any{"url": pageSrv.URL, "format": "text"}); isErr {
		t.Fatal("text-format fetch returned tool error")
	}
	// Different extract flag → different key.
	if _, isErr := callTool(t, session, "web_fetch", map[string]any{"url": pageSrv.URL, "extract": false}); isErr {
		t.Fatal("no-extract fetch returned tool error")
	}
	if n := atomic.LoadInt32(&hits); n != 3 {
		t.Errorf("expected 3 backend hits for 3 distinct keys, got %d", n)
	}
}

// TestBlockErrorNotCached follows the Hermes hint: errors (including
// BlockError from a 200-OK challenge page) must never enter the cache — the
// next call must hit the backend again (and possibly succeed).
func TestBlockErrorNotCached(t *testing.T) {
	var hits int32
	pageSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "text/html")
		if n == 1 {
			// First call: challenge served with HTTP 200.
			w.Write([]byte(`<!doctype html><html><head><title>Just a moment...</title></head><body><script>window._cf_chl_opt={};</script></body></html>`))
			return
		}
		w.Write([]byte("<html><head><title>Fine</title></head><body><p>Fine content after the challenge.</p></body></html>"))
	}))
	defer pageSrv.Close()

	cfg := config.Load()
	cfg.BlockPrivateNetworks = false
	deps := CacheDeps{Fetch: cache.New(16, 60000000000)}
	session, closeFn := newCacheTestServer(t, cfg, deps)
	defer closeFn()

	_, isErr := callTool(t, session, "web_fetch", map[string]any{"url": pageSrv.URL, "extract": false})
	if !isErr {
		t.Fatal("first fetch should surface the challenge as a tool error")
	}
	if deps.Fetch.Len() != 0 {
		t.Errorf("BlockError must not be cached, cache holds %d entries", deps.Fetch.Len())
	}

	out, isErr := callTool(t, session, "web_fetch", map[string]any{"url": pageSrv.URL, "extract": false})
	if isErr {
		t.Fatalf("second fetch should succeed after the backend recovers: %v", out)
	}
	if cached, _ := out["cached"].(bool); cached {
		t.Error("second fetch must be a fresh fetch, not a cache hit")
	}
	if n := atomic.LoadInt32(&hits); n != 2 {
		t.Errorf("expected 2 backend hits (block error not cached), got %d", n)
	}
}

// Compile-time interface guard: fetch errors flow through the fetch client.
var _ = fetch.BlockKindOf
