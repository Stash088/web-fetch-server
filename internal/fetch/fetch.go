package fetch

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	http    *http.Client
	maxBody int64
	ua      string
	logger  *slog.Logger
}

func NewClient(timeout time.Duration, maxBody int64, ua string) *Client {
	return NewClientWithLogger(timeout, maxBody, ua, nil)
}

// NewClientWithLogger creates a client with a custom logger. If logger is nil,
// the package default logger is used.
func NewClientWithLogger(timeout time.Duration, maxBody int64, ua string, logger *slog.Logger) *Client {
	if logger == nil {
		logger = slog.Default()
	}
	return &Client{
		http:    &http.Client{Timeout: timeout},
		maxBody: maxBody,
		ua:      ua,
		logger:  logger,
	}
}

// newRequestID returns a short random hex id used to correlate [request]/[response] log pairs.
func newRequestID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

type Page struct {
	URL   string
	Title string
	Body  []byte
}

func (c *Client) Fetch(ctx context.Context, rawURL string) (*Page, error) {
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		return nil, fmt.Errorf("unsupported URL scheme (only http/https allowed): %s", rawURL)
	}

	reqID := newRequestID()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,*/*;q=0.8")

	c.logger.Info("[request] fetch",
		"request_id", reqID,
		"tool", "web_fetch",
		"method", http.MethodGet,
		"url", rawURL,
		"user_agent", c.ua,
	)

	start := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		c.logger.Error("[request] fetch failed", "request_id", reqID, "error", err.Error())
		return nil, fmt.Errorf("fetch %s: %w", rawURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		c.logger.Warn("[response] fetch non-200",
			"request_id", reqID,
			"status", resp.StatusCode,
			"body", string(body),
		)
		return nil, fmt.Errorf("fetch %s: status %d", rawURL, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBody))
	if err != nil {
		c.logger.Error("[response] fetch read failed", "request_id", reqID, "error", err.Error())
		return nil, fmt.Errorf("read body: %w", err)
	}

	title := ""
	// crude <title> extraction before conversion
	lower := strings.ToLower(string(body))
	if i := strings.Index(lower, "<title>"); i >= 0 {
		rest := body[i+len("<title>"):]
		if j := strings.Index(strings.ToLower(string(rest)), "</title>"); j >= 0 {
			title = strings.TrimSpace(string(rest[:j]))
		}
	}

	c.logger.Info("[response] fetch",
		"request_id", reqID,
		"tool", "web_fetch",
		"status", resp.StatusCode,
		"latency_ms", time.Since(start).Milliseconds(),
		"final_url", resp.Request.URL.String(),
		"content_type", resp.Header.Get("Content-Type"),
		"bytes", len(body),
		"title", title,
	)

	return &Page{URL: resp.Request.URL.String(), Title: title, Body: body}, nil
}
