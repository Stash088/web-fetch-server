package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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
	cfg.BlockPrivateNetworks = false
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

// TestWebFetchExtractsArticle verifies the readability path end-to-end through
// the tool: navigation/footer noise is dropped, extracted/metadata are set.
func TestWebFetchExtractsArticle(t *testing.T) {
	page := `<!doctype html><html><head><title>Blog Post</title>
<meta name="description" content="A long article about testing extraction.">
</head><body>
<nav><a href="/">Home</a> <a href="/tags">Tags</a></nav>
<div class="cookie">Accept all cookies please.</div>
<article><h1>Blog Post</h1>
<p>` + strings.Repeat("Deep meaningful paragraph content about extraction quality. ", 10) + `</p>
<p>Second paragraph. ` + strings.Repeat("More analysis follows here. ", 8) + `</p>
</article>
<footer>Footer Corp 2026 all rights reserved privacy terms</footer>
</body></html>`
	pageSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(page))
	}))
	defer pageSrv.Close()

	cfg := config.Load()
	cfg.BlockPrivateNetworks = false
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
		Arguments: map[string]any{"url": pageSrv.URL, "max_length": 4000},
	})
	if err != nil {
		t.Fatalf("call web_fetch: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %+v", res)
	}
	b, _ := json.Marshal(res.StructuredContent)
	var out struct {
		Title     string            `json:"title"`
		Content   string            `json:"content"`
		Extracted bool              `json:"extracted"`
		Metadata  map[string]string `json:"metadata"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("decode structured content: %v", err)
	}
	if !out.Extracted {
		t.Error("expected extracted=true for an article page")
	}
	if strings.Contains(out.Content, "Footer Corp") || strings.Contains(out.Content, "Accept all cookies") || strings.Contains(out.Content, "Tags") {
		t.Errorf("noise leaked into extracted content:\n%s", out.Content)
	}
	if !strings.Contains(out.Content, "Deep meaningful paragraph") {
		t.Errorf("article body missing:\n%s", out.Content)
	}
	if out.Metadata["description"] != "A long article about testing extraction." {
		t.Errorf("metadata description = %q", out.Metadata["description"])
	}
}
