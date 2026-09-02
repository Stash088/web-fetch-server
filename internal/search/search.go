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
	"strings"
	"time"
)

type Client struct {
	baseURL    string
	apiKey     string
	categories string // comma-separated SearXNG categories, e.g. "general,it"
	http       *http.Client
	logger     *slog.Logger
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

// Response is the processed SearXNG answer: deduplicated results plus the
// status of engines that failed to answer (quota, CAPTCHA, blocks) so the
// caller can tell "nothing found" from "the web is unreachable".
type Response struct {
	Results             []Result
	UnresponsiveEngines []string // "engine: reason"
}

type rawResponse struct {
	Results      []Result   `json:"results"`
	Unresponsive [][]string `json:"unresponsive_engines"`
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

// WithCategories sets the SearXNG categories for every search request
// (e.g. "general,it": general web plus the keyless IT vertical APIs that
// survive datacenter egress IPs and feed engine consensus on tech queries).
func (c *Client) WithCategories(cats string) *Client {
	c.categories = cats
	return c
}

func (c *Client) Search(ctx context.Context, query, language, timeRange string, maxResults int) (*Response, error) {
	return c.SearchIn(ctx, "", query, language, timeRange, maxResults)
}

// SearchIn searches the given comma-separated SearXNG categories (e.g.
// "general,it" for tech queries where the IT vertical APIs are useful);
// empty categories falls back to the client default.
func (c *Client) SearchIn(ctx context.Context, categories, query, language, timeRange string, maxResults int) (*Response, error) {
	u, err := url.Parse(c.baseURL + "/search")
	if err != nil {
		return nil, fmt.Errorf("parse searxng url: %w", err)
	}
	q := u.Query()
	q.Set("q", query)
	q.Set("format", "json")
	if cats := categories; cats != "" {
		q.Set("categories", cats)
	} else if c.categories != "" {
		q.Set("categories", c.categories)
	}
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

	var parsed rawResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		c.logger.Error("[response] searxng decode failed", "request_id", reqID, "error", err.Error())
		return nil, fmt.Errorf("decode searxng response: %w", err)
	}

	deduped := dedupResults(parsed.Results)
	c.logger.Info("[response] searxng",
		"request_id", reqID,
		"tool", "web_search",
		"status", resp.StatusCode,
		"latency_ms", time.Since(start).Milliseconds(),
		"total_results", len(parsed.Results),
		"deduped_results", len(deduped),
		"unresponsive", len(parsed.Unresponsive),
	)

	out := &Response{
		Results:             deduped,
		UnresponsiveEngines: flattenUnresponsive(parsed.Unresponsive),
	}
	if maxResults > 0 && maxResults < len(out.Results) {
		out.Results = out.Results[:maxResults]
	}

	c.logger.Info("[response] searxng",
		"request_id", reqID,
		"tool", "web_search",
		"status", resp.StatusCode,
		"latency_ms", time.Since(start).Milliseconds(),
		"total_results", len(parsed.Results),
		"returned_results", len(out.Results),
		"first_result", firstSnippet(out.Results),
	)

	return out, nil
}

// flattenUnresponsive turns SearXNG's [[engine, reason], ...] into
// ["engine: reason", ...] for compact reporting to the agent.
func flattenUnresponsive(pairs [][]string) []string {
	if len(pairs) == 0 {
		return nil
	}
	out := make([]string, 0, len(pairs))
	for _, p := range pairs {
		if len(p) == 0 {
			continue
		}
		if len(p) == 1 {
			out = append(out, p[0])
			continue
		}
		out = append(out, p[0]+": "+strings.Join(p[1:], ", "))
	}
	return out
}

// trackingParams are URL query parameters that identify the same page behind
// campaign/session variants (bing adds msockid, google utm_*).
var trackingParams = map[string]struct{}{
	"utm_source": {}, "utm_medium": {}, "utm_campaign": {}, "utm_term": {},
	"utm_content": {}, "gclid": {}, "fbclid": {}, "msclkid": {}, "msockid": {},
	"yclid": {}, "igshid": {}, "mc_cid": {}, "mc_eid": {}, "ref": {}, "ref_src": {},
}

// normalizeResultURL collapses URL variants that point to the same page:
// lower-cased scheme/host, stripped fragment and tracking parameters, and a
// single trailing slash. Used only as a dedup key, never returned.
func normalizeResultURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.Fragment = ""
	if u.RawQuery != "" {
		q := u.Query()
		for p := range q {
			if _, tracked := trackingParams[strings.ToLower(p)]; tracked {
				q.Del(p)
			}
		}
		u.RawQuery = q.Encode()
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	if u.Path == "" {
		u.Path = "/"
	}
	return u.String()
}

// dedupResults drops URLs that normalize to an already-seen page, keeping the
// first occurrence (SearXNG order). Duplicate hits inflate single-engine
// rankings and fake engine consensus in the reranker.
func dedupResults(in []Result) []Result {
	if len(in) == 0 {
		return in
	}
	seen := make(map[string]struct{}, len(in))
	out := in[:0:0]
	for _, r := range in {
		key := normalizeResultURL(r.URL)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, r)
	}
	return out
}

// firstSnippet returns a compact preview of the first result for logging.
func firstSnippet(results []Result) string {
	if len(results) == 0 {
		return ""
	}
	r := results[0]
	return fmt.Sprintf("%s | %s | %.100s", r.Title, r.URL, r.Snippet)
}
