package mcp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/amir/web-fetch-server/internal/config"
)

// TestWebFetchToolEndToEnd verifies web_fetch returns markdown from a live page.
func TestWebFetchToolEndToEnd(t *testing.T) {
	pageSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html><head><title>Doc</title></head><body><h1>Heading</h1><p>Some <b>content</b> here.</p></body></html>"))
	}))
	defer pageSrv.Close()

	cfg := config.Load()
	server := Build(cfg)

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
		Arguments: map[string]any{"url": pageSrv.URL, "max_length": 2000},
	})
	if err != nil {
		t.Fatalf("call web_fetch: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %+v", res)
	}
	text := ""
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text += tc.Text
		}
	}
	if text == "" {
		t.Fatal("expected non-empty result text")
	}
	if len(text) < 5 {
		t.Fatalf("suspiciously short result: %q", text)
	}
}

// TestWebSearchToolError verifies web_search surfaces a SearXNG failure cleanly.
func TestWebSearchToolError(t *testing.T) {
	searx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "backend down", http.StatusBadGateway)
	}))
	defer searx.Close()

	cfg := config.Load()
	cfg.SearxngURL = searx.URL
	cfg.FetchTimeout = 5 * time.Second
	server := Build(cfg)

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
		Name:      "web_search",
		Arguments: map[string]any{"query": "test"},
	})
	if err == nil && !res.IsError {
		t.Fatal("expected an error result when SearXNG is down")
	}
	_ = fmt.Sprint(res)
}
