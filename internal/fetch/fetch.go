package fetch

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/ledongthuc/pdf"

	"github.com/amir/web-fetch-server/internal/security"
)

// Options configures the fetch client.
type Options struct {
	Timeout      time.Duration
	MaxBody      int64
	UserAgent    string
	MaxRedirects int
	BlockPrivate bool
	// LookupIP overrides DNS resolution used by the SSRF guard (for tests).
	LookupIP func(ctx context.Context, network, host string) ([]net.IP, error)
	Logger   *slog.Logger
}

type Client struct {
	http    *http.Client
	maxBody int64
	ua      string
	guard   security.NetworkGuard
	logger  *slog.Logger
}

func NewClient(timeout time.Duration, maxBody int64, ua string) *Client {
	return NewClientWithOptions(Options{Timeout: timeout, MaxBody: maxBody, UserAgent: ua, BlockPrivate: true})
}

func NewClientWithLogger(timeout time.Duration, maxBody int64, ua string, logger *slog.Logger) *Client {
	return NewClientWithOptions(Options{Timeout: timeout, MaxBody: maxBody, UserAgent: ua, BlockPrivate: true, Logger: logger})
}

// NewClientWithOptions builds a client with full control over redirects, SSRF
// protection and logging.
func NewClientWithOptions(opts Options) *Client {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	maxRedirects := opts.MaxRedirects
	if maxRedirects <= 0 {
		maxRedirects = 4
	}
	if opts.MaxBody <= 0 {
		opts.MaxBody = 2 << 20
	}
	if opts.UserAgent == "" {
		opts.UserAgent = "web-fetch-server"
	}
	hc := &http.Client{
		Timeout: opts.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("stopped after %d redirects", maxRedirects)
			}
			return nil
		},
	}
	guard := security.NetworkGuard{BlockPrivateNetworks: opts.BlockPrivate, LookupIP: opts.LookupIP}
	return &Client{
		http:    hc,
		maxBody: opts.MaxBody,
		ua:      opts.UserAgent,
		guard:   guard,
		logger:  opts.Logger,
	}
}

// newRequestID returns a short random hex id used to correlate [request]/[response] log pairs.
func newRequestID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

var (
	osCreateTemp = os.CreateTemp
	osRemove     = os.Remove
)

type Page struct {
	URL       string
	Title     string
	Body      []byte
	MediaType string
	IsMedia   bool
}

func (c *Client) Fetch(ctx context.Context, rawURL string) (*Page, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("unsupported URL scheme (only http/https allowed): %s", rawURL)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("missing host in url: %s", rawURL)
	}

	// SSRF protection: validate the host before making the request.
	if err := c.guard.ResolveAndValidateHost(ctx, u.Hostname()); err != nil {
		return nil, fmt.Errorf("blocked by network policy: %w", err)
	}

	reqID := newRequestID()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/pdf,*/*;q=0.8")

	c.logger.Info("[request] fetch",
		"request_id", reqID,
		"tool", "web_fetch",
		"method", http.MethodGet,
		"url", u.String(),
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

	mediaType := mediaType(resp.Header.Get("Content-Type"))
	body, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBody))
	if err != nil {
		c.logger.Error("[response] fetch read failed", "request_id", reqID, "error", err.Error())
		return nil, fmt.Errorf("read body: %w", err)
	}

	// Reject binary media payloads early so the model never receives junk.
	if isMediaType(mediaType) {
		c.logger.Warn("[response] fetch media content rejected",
			"request_id", reqID,
			"url", resp.Request.URL.String(),
			"content_type", mediaType,
			"bytes", len(body),
		)
		return nil, fmt.Errorf("fetch %s: content-type %q is binary media, not supported", rawURL, mediaType)
	}

	// Parse PDF bodies into plain text when requested.
	if isPDF(mediaType, body) {
		text, perr := parsePDF(resp.Request.URL.String(), body)
		if perr == nil {
			body = text
		} else {
			c.logger.Warn("[response] fetch pdf parse failed", "request_id", reqID, "error", perr.Error())
		}
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
		"content_type", mediaType,
		"bytes", len(body),
		"title", title,
	)

	return &Page{URL: resp.Request.URL.String(), Title: title, Body: body, MediaType: mediaType}, nil
}

// mediaType returns a normalized lower-case media type from a Content-Type
// header (drops parameters like charset).
func mediaType(ct string) string {
	ct = strings.ToLower(ct)
	if i := strings.Index(ct, ";"); i >= 0 {
		ct = ct[:i]
	}
	return strings.TrimSpace(ct)
}

// isMediaType reports whether the given media type is binary media we should
// reject rather than return to the model.
func isMediaType(ct string) bool {
	for _, p := range []string{"image/", "video/", "audio/", "font/"} {
		if strings.HasPrefix(ct, p) {
			return true
		}
	}
	switch ct {
	case "application/octet-stream", "application/pdf", "application/zip", "application/gzip",
		"application/x-gzip", "application/x-tar", "application/x-7z-compressed",
		"application/x-rar-compressed", "application/vnd.rar", "application/x-msdownload":
		return true
	}
	return false
}

// isPDF reports whether the content looks like a PDF based on content-type or
// magic bytes (%PDF-).
func isPDF(ct string, body []byte) bool {
	if ct == "application/pdf" {
		return true
	}
	if len(body) >= 5 && string(body[:5]) == "%PDF-" {
		return true
	}
	return false
}

// parsePDF extracts text from an in-memory PDF and returns it as bytes. The
// ledongthuc/pdf reader is file-based, so we write to a temp file.
func parsePDF(name string, data []byte) ([]byte, error) {
	tmp, err := osCreateTemp("", "fetch-*.pdf")
	if err != nil {
		return nil, err
	}
	tmpName := tmp.Name()
	defer osRemove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}

	f, r, err := pdf.Open(tmpName)
	if err != nil {
		return nil, fmt.Errorf("open pdf: %w", err)
	}
	defer f.Close()
	var sb strings.Builder
	totalPage := r.NumPage()
	for i := 1; i <= totalPage; i++ {
		p := r.Page(i)
		if p.V.IsNull() {
			continue
		}
		text, err := p.GetPlainText(nil)
		if err != nil {
			continue
		}
		sb.WriteString(text)
		sb.WriteString("\n")
	}
	if sb.Len() == 0 {
		return nil, fmt.Errorf("pdf %s produced no text", name)
	}
	return []byte(sb.String()), nil
}
