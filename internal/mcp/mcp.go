package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/amir/web-fetch-server/internal/cache"
	"github.com/amir/web-fetch-server/internal/config"
	"github.com/amir/web-fetch-server/internal/content"
	"github.com/amir/web-fetch-server/internal/fetch"
	"github.com/amir/web-fetch-server/internal/search"
)

// CacheDeps carries optional TTL caches shared by the MCP tools. A nil cache
// disables caching for that kind. Fetch and Render are separate so browser
// renders can use a shorter TTL (cookies/profiles drift over time).
type CacheDeps struct {
	Fetch  *cache.Cache // direct HTTP fetches
	Render *cache.Cache // headless-browser renders
	Search *cache.Cache // SearXNG search results
}

// Build creates and returns the MCP server with web_search and web_fetch tools.
func Build(cfg config.Config) *mcp.Server {
	return BuildWithLogger(cfg, nil, CacheDeps{})
}

// BuildWithLogger creates the MCP server using a custom logger for request/response
// tracing of upstream calls (SearXNG and page fetches).
func BuildWithLogger(cfg config.Config, logger *slog.Logger, deps CacheDeps) *mcp.Server {
	if logger == nil {
		logger = slog.Default()
	}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "web-fetch-server",
		Version: "0.1.0",
	}, nil)

	searchClient := search.NewClientWithLogger(cfg.SearxngURL, cfg.SearxngKey, cfg.FetchTimeout, logger)
	fetchClient := fetch.NewClientWithOptions(fetch.Options{
		Timeout:          cfg.FetchTimeout,
		MaxBody:          cfg.MaxFetchBytes,
		UserAgent:        cfg.UserAgent,
		BlockPrivate:     cfg.BlockPrivateNetworks,
		TLSFingerprint:   cfg.TLSFingerprint,
		JSRenderMode:     cfg.JSRenderMode,
		JSRenderTimeout:  cfg.JSRenderTimeout,
		ChromeBin:        cfg.ChromeBin,
		RenderProfileDir: cfg.RenderProfileDir,
		RenderPoolSize:   cfg.RenderPoolSize,
		Logger:           logger,
	})

	type searchArgs struct {
		Query      string  `json:"query" jsonschema:"the search query"`
		MaxResults *int    `json:"max_results,omitempty" jsonschema:"maximum number of results to return, default 10"`
		Language   *string `json:"language,omitempty" jsonschema:"optional language code, e.g. en or ru"`
		TimeRange  *string `json:"time_range,omitempty" jsonschema:"optional time range filter: day, month or year"`
	}
	type searchOut struct {
		Results []search.Result `json:"results"`
		Cached  bool            `json:"cached,omitempty"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "web_search",
		Description: "Search the web via the SearXNG metasearch backend. Returns a list of results with title, url, snippet and engine.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in searchArgs) (*mcp.CallToolResult, searchOut, error) {
		max := 0
		if in.MaxResults != nil {
			max = *in.MaxResults
		}
		if max <= 0 {
			max = cfg.MaxResults
		}
		lang, tr := "", ""
		if in.Language != nil {
			lang = *in.Language
		}
		if in.TimeRange != nil {
			tr = *in.TimeRange
		}
		logger.Info("[request] tool web_search",
			"tool", "web_search",
			"query", in.Query,
			"max_results", max,
			"language", lang,
			"time_range", tr,
		)

		cacheKey := fmt.Sprintf("%s|%s|%s|%d", in.Query, lang, tr, max)
		if deps.Search != nil {
			if v, ok := deps.Search.Get(cacheKey); ok {
				if res, ok := v.([]search.Result); ok && len(res) > 0 {
					logger.Info("[response] tool web_search (cached)",
						"tool", "web_search",
						"query", in.Query,
						"results", len(res),
					)
					return nil, searchOut{Results: res, Cached: true}, nil
				}
			}
		}

		res, err := searchClient.Search(ctx, in.Query, lang, tr, max)
		if err != nil {
			return nil, searchOut{}, err
		}
		if deps.Search != nil && len(res) > 0 {
			deps.Search.Set(cacheKey, res)
		}
		return nil, searchOut{Results: res}, nil
	})

	type fetchArgs struct {
		URL        string  `json:"url" jsonschema:"the URL to fetch"`
		MaxLength  *int    `json:"max_length,omitempty" jsonschema:"maximum number of characters to return, default 8000"`
		StartIndex *int    `json:"start_index,omitempty" jsonschema:"start reading content from this character index, default 0"`
		Format     *string `json:"format,omitempty" jsonschema:"output format: markdown (default) or text"`
		Render     *bool   `json:"render,omitempty" jsonschema:"render with a headless browser (JS) instead of fetching raw HTML, default false"`
		Extract    *bool   `json:"extract,omitempty" jsonschema:"extract the main article content (drop navigation, footers, banners) before converting, default true"`
	}
	type fetchOut struct {
		Title      string            `json:"title"`
		URL        string            `json:"url"`
		Content    string            `json:"content"`
		TotalChars int               `json:"total_chars"`
		Format     string            `json:"format"`
		Extracted  bool              `json:"extracted,omitempty"`
		Metadata   map[string]string `json:"metadata,omitempty"`
		Cached     bool              `json:"cached,omitempty"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "web_fetch",
		Description: "Fetch a web page and return its content as markdown or text. For long pages, use start_index to read in chunks. Set render=true to load the page in a headless browser (handles JS-rendered content and some bot protections).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in fetchArgs) (*mcp.CallToolResult, fetchOut, error) {
		format := "markdown"
		if in.Format != nil {
			format = strings.ToLower(*in.Format)
		}
		if format != "text" {
			format = "markdown"
		}
		render := in.Render != nil && *in.Render
		extract := in.Extract == nil || *in.Extract
		logger.Info("[request] tool web_fetch",
			"tool", "web_fetch",
			"url", in.URL,
			"max_length", in.MaxLength,
			"start_index", in.StartIndex,
			"format", in.Format,
			"render", in.Render,
			"extract", in.Extract,
		)

		var entry *fetchCacheEntry
		if fetchCacheFor(deps, render) != nil {
			cacheKey := fetchCacheKey(in.URL, render, format, extract)
			if v, ok := fetchCacheFor(deps, render).Get(cacheKey); ok {
				if e, ok := v.(*fetchCacheEntry); ok && e.Text != "" {
					entry = e
				}
			}
		}

		if entry == nil {
			page, err := fetchClient.FetchWithOptions(ctx, in.URL, fetch.FetchOptions{Render: render})
			if err != nil {
				return nil, fetchOut{}, err
			}
			e := convertPage(page, format, extract)
			if fetchCacheFor(deps, render) != nil && e.Text != "" {
				fetchCacheFor(deps, render).Set(fetchCacheKey(in.URL, render, format, extract), e)
			}
			entry = e
		} else {
			entry.Cached = true
			logger.Info("[response] tool web_fetch (cached)",
				"tool", "web_fetch",
				"url", entry.URL,
				"bytes", len(entry.Text),
			)
		}

		maxLen := cfg.DefaultMaxLen
		if in.MaxLength != nil && *in.MaxLength > 0 {
			maxLen = *in.MaxLength
		}
		startIndex := 0
		if in.StartIndex != nil && *in.StartIndex > 0 {
			startIndex = *in.StartIndex
		}

		chunk, total := content.Chunk(entry.Text, startIndex, maxLen)
		out := fetchOut{
			Title:      entry.Title,
			URL:        entry.URL,
			Content:    chunk,
			TotalChars: total,
			Format:     entry.Format,
			Extracted:  entry.Extracted,
			Metadata:   entry.Metadata,
			Cached:     entry.Cached,
		}
		if startIndex > 0 {
			out.Content = fmt.Sprintf("(continued from char %d of %d)\n\n%s", startIndex, total, chunk)
		}
		return nil, out, nil
	})

	return server
}

// fetchCacheEntry is what gets stored in the fetch/render TTL caches: the
// fully converted text so that chunking (start_index paging) works on cache
// hits without re-fetching or re-converting.
type fetchCacheEntry struct {
	Title     string
	URL       string
	Text      string
	Format    string
	Extracted bool
	Metadata  map[string]string
	Cached    bool // true when served from cache
}

func fetchCacheFor(deps CacheDeps, render bool) *cache.Cache {
	if render {
		return deps.Render
	}
	return deps.Fetch
}

func fetchCacheKey(rawURL string, render bool, format string, extract bool) string {
	return fmt.Sprintf("%s|render=%t|format=%s|extract=%t", rawURL, render, format, extract)
}

// convertPage turns a fetched page into the requested format, optionally via
// readability extraction (HTML only — PDFs and other non-HTML bodies are
// already text). Falls back to full-page conversion when extraction fails or
// yields too little text.
func convertPage(page *fetch.Page, format string, extract bool) *fetchCacheEntry {
	title := page.Title
	extracted := false
	metadata := map[string]string{}
	body := page.Body

	if extract && page.MediaType == "text/html" {
		if clean, meta, ok := content.ExtractHTML(page.Body, page.URL); ok {
			body = clean
			metadata = meta
			extracted = true
			if t := meta["title"]; t != "" {
				title = t
			}
		}
	}

	var text string
	if format == "text" {
		text = content.ToText(body)
	} else {
		text = content.ToMarkdown(body)
	}

	return &fetchCacheEntry{
		Title:     title,
		URL:       page.URL,
		Text:      text,
		Format:    format,
		Extracted: extracted,
		Metadata:  metadata,
	}
}
