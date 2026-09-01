package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/amir/web-fetch-server/internal/config"
	"github.com/amir/web-fetch-server/internal/content"
	"github.com/amir/web-fetch-server/internal/fetch"
	"github.com/amir/web-fetch-server/internal/search"
)

// Build creates and returns the MCP server with web_search and web_fetch tools.
func Build(cfg config.Config) *mcp.Server {
	return BuildWithLogger(cfg, nil)
}

// BuildWithLogger creates the MCP server using a custom logger for request/response
// tracing of upstream calls (SearXNG and page fetches).
func BuildWithLogger(cfg config.Config, logger *slog.Logger) *mcp.Server {
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
		res, err := searchClient.Search(ctx, in.Query, lang, tr, max)
		if err != nil {
			return nil, searchOut{}, err
		}
		return nil, searchOut{Results: res}, nil
	})

	type fetchArgs struct {
		URL        string  `json:"url" jsonschema:"the URL to fetch"`
		MaxLength  *int    `json:"max_length,omitempty" jsonschema:"maximum number of characters to return, default 8000"`
		StartIndex *int    `json:"start_index,omitempty" jsonschema:"start reading content from this character index, default 0"`
		Format     *string `json:"format,omitempty" jsonschema:"output format: markdown (default) or text"`
		Render     *bool   `json:"render,omitempty" jsonschema:"render with a headless browser (JS) instead of fetching raw HTML, default false"`
	}
	type fetchOut struct {
		Title      string `json:"title"`
		URL        string `json:"url"`
		Content    string `json:"content"`
		TotalChars int    `json:"total_chars"`
		Format     string `json:"format"`
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "web_fetch",
		Description: "Fetch a web page and return its content as markdown or text. For long pages, use start_index to read in chunks. Set render=true to load the page in a headless browser (handles JS-rendered content and some bot protections).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in fetchArgs) (*mcp.CallToolResult, fetchOut, error) {
		logger.Info("[request] tool web_fetch",
			"tool", "web_fetch",
			"url", in.URL,
			"max_length", in.MaxLength,
			"start_index", in.StartIndex,
			"format", in.Format,
			"render", in.Render,
		)
		page, err := fetchClient.FetchWithOptions(ctx, in.URL, fetch.FetchOptions{Render: in.Render != nil && *in.Render})
		if err != nil {
			return nil, fetchOut{}, err
		}

		var text string
		format := "markdown"
		if in.Format != nil {
			format = strings.ToLower(*in.Format)
		}
		if format == "text" {
			text = content.ToText(page.Body)
		} else {
			format = "markdown"
			text = content.ToMarkdown(page.Body)
		}

		maxLen := cfg.DefaultMaxLen
		if in.MaxLength != nil && *in.MaxLength > 0 {
			maxLen = *in.MaxLength
		}
		startIndex := 0
		if in.StartIndex != nil && *in.StartIndex > 0 {
			startIndex = *in.StartIndex
		}

		chunk, total := content.Chunk(text, startIndex, maxLen)
		out := fetchOut{
			Title:      page.Title,
			URL:        page.URL,
			Content:    chunk,
			TotalChars: total,
			Format:     format,
		}
		if startIndex > 0 {
			out.Content = fmt.Sprintf("(continued from char %d of %d)\n\n%s", startIndex, total, chunk)
		}
		return nil, out, nil
	})

	return server
}
