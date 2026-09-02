package search

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"
)

type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
	logger  *slog.Logger
}

type Result struct {
	Title    string   `json:"title"`
	URL      string   `json:"url"`
	Snippet  string   `json:"content"`
	Engine   string   `json:"engine"`
	Engines  []string `json:"engines,omitempty"`
	Score    float64  `json:"score,omitempty"`
	Reranked bool     `json:"reranked,omitempty"`
}

type response struct {
	Results []Result `json:"results"`
}

func NewClient(baseURL, apiKey string, timeout time.Duration) *Client {
	return NewClientWithLogger(baseURL, apiKey, timeout, nil)
}

// NewClientWithLogger creates a client with a custom logger. If logger is nil,
// the package default logger is used.
func NewClientWithLogger(baseURL, apiKey string, timeout time.Duration, logger *slog.Logger) *Client {
	if logger == nil {
		logger = slog.Default()
	}
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		http:    &http.Client{Timeout: timeout},
		logger:  logger,
	}
}

// newRequestID returns a short random hex id used to correlate [request]/[response] log pairs.
func newRequestID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (c *Client) Search(ctx context.Context, query, language, timeRange string, maxResults int) ([]Result, error) {
	u, err := url.Parse(c.baseURL + "/search")
	if err != nil {
		return nil, fmt.Errorf("parse searxng url: %w", err)
	}
	q := u.Query()
	q.Set("q", query)
	q.Set("format", "json")
	if language != "" {
		q.Set("language", language)
	}
	if timeRange != "" {
		q.Set("time_range", timeRange)
	}
	u.RawQuery = q.Encode()

	reqID := newRequestID()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	c.logger.Info("[request] searxng",
		"request_id", reqID,
		"tool", "web_search",
		"method", http.MethodGet,
		"url", u.String(),
		"query", query,
		"language", language,
		"time_range", timeRange,
		"max_results", maxResults,
	)

	start := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		c.logger.Error("[request] searxng failed", "request_id", reqID, "error", err.Error())
		return nil, fmt.Errorf("searxng request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		c.logger.Warn("[response] searxng non-200",
			"request_id", reqID,
			"status", resp.StatusCode,
			"body", string(body),
		)
		return nil, fmt.Errorf("searxng status %d: %s", resp.StatusCode, string(body))
	}

	var parsed response
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		c.logger.Error("[response] searxng decode failed", "request_id", reqID, "error", err.Error())
		return nil, fmt.Errorf("decode searxng response: %w", err)
	}

	if maxResults <= 0 || maxResults > len(parsed.Results) {
		maxResults = len(parsed.Results)
	}
	out := parsed.Results[:maxResults]

	c.logger.Info("[response] searxng",
		"request_id", reqID,
		"tool", "web_search",
		"status", resp.StatusCode,
		"latency_ms", time.Since(start).Milliseconds(),
		"total_results", len(parsed.Results),
		"returned_results", len(out),
		"first_result", firstSnippet(out),
	)

	return out, nil
}

// firstSnippet returns a compact preview of the first result for logging.
func firstSnippet(results []Result) string {
	if len(results) == 0 {
		return ""
	}
	r := results[0]
	return fmt.Sprintf("%s | %s | %.100s", r.Title, r.URL, r.Snippet)
}
