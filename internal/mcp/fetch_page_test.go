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
