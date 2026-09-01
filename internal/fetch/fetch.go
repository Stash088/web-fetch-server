package fetch

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	mathrand "math/rand/v2"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ledongthuc/pdf"

	"github.com/amir/web-fetch-server/internal/security"
)

// maxAttempts is how many times a direct fetch is tried before giving up.
const maxAttempts = 2

// defaultUserAgent mimics a current desktop Chrome so antibot heuristics see a
// plausible browser instead of an obvious scraper.
const defaultUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"

const (
	renderNever  = "never"
	renderAuto   = "auto"
	renderAlways = "always"
)

// Options configures the fetch client.
type Options struct {
	Timeout      time.Duration
	MaxBody      int64
	UserAgent    string
	MaxRedirects int
	BlockPrivate bool
	// TLSFingerprint selects the TLS ClientHello fingerprint: "chrome" (uTLS)
	// or "off" (stdlib TLS). Empty defaults to "chrome".
	TLSFingerprint string
	// JSRenderMode controls JS rendering: "never", "auto" or "always".
	JSRenderMode string
	// JSRenderTimeout bounds a single browser render.
	JSRenderTimeout time.Duration
	// ChromeBin overrides the Chrome/Chromium binary path (empty = auto-detect).
	ChromeBin string
	// RenderProfileDir is the base dir for persistent render browser profiles
	// (RENDER_PROFILE_DIR); empty uses a default under the system tmpdir.
	RenderProfileDir string
	// RenderPoolSize is the number of pooled render browsers
	// (RENDER_POOL_SIZE); 0 or below defaults to 1.
	RenderPoolSize int
	// LookupIP overrides DNS resolution used by the SSRF guard (for tests).
	LookupIP func(ctx context.Context, network, host string) ([]net.IP, error)
	Logger   *slog.Logger
}

type Client struct {
	http          *http.Client
	maxBody       int64
	ua            string
	timeout       time.Duration
	guard         security.NetworkGuard
	renderMode    string
	renderer      Renderer
	renderTimeout time.Duration
	logger        *slog.Logger
}

func NewClient(timeout time.Duration, maxBody int64, ua string) *Client {
	return NewClientWithOptions(Options{Timeout: timeout, MaxBody: maxBody, UserAgent: ua, BlockPrivate: true})
}

func NewClientWithLogger(timeout time.Duration, maxBody int64, ua string, logger *slog.Logger) *Client {
	return NewClientWithOptions(Options{Timeout: timeout, MaxBody: maxBody, UserAgent: ua, BlockPrivate: true, Logger: logger})
}

// NewClientWithOptions builds a client with full control over redirects, SSRF
// protection, TLS fingerprinting, JS rendering and logging.
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
	ua := opts.UserAgent
	if ua == "" {
		ua = defaultUserAgent
	}
	renderMode := opts.JSRenderMode
	if renderMode == "" {
		renderMode = renderNever
	}
	renderTimeout := opts.JSRenderTimeout
	if renderTimeout <= 0 {
		renderTimeout = 30 * time.Second
	}
	dialTimeout := opts.Timeout
	if dialTimeout <= 0 || dialTimeout > 10*time.Second {
		dialTimeout = 10 * time.Second
	}

	jar, _ := cookiejar.New(nil)

	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   dialTimeout,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		MaxIdleConnsPerHost:   8,
		ExpectContinueTimeout: time.Second,
	}
	if opts.TLSFingerprint != "off" {
		// DialTLSContext takes over HTTPS dialing, so the TLSClientConfig above
		// is not used for TLS; the uTLS dial sets its own config.
		tr.DialTLSContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return tlsDialChrome(ctx, network, addr, dialTimeout)
		}
	}

	hc := &http.Client{
		Transport: tr,
		Timeout:   opts.Timeout,
		Jar:       jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("stopped after %d redirects", maxRedirects)
			}
			return nil
		},
	}
	guard := security.NetworkGuard{BlockPrivateNetworks: opts.BlockPrivate, LookupIP: opts.LookupIP}

	// The renderer is always created so that a per-call render=true works even
	// when JS_RENDER=never; ensureChrome() gives a clear error if no browser
	// binary is present. renderMode only controls the auto-fallback.
	renderer := NewChromeRenderer(RendererOptions{
		ChromeBin:  opts.ChromeBin,
		MaxBody:    opts.MaxBody,
		ProfileDir: opts.RenderProfileDir,
		PoolSize:   opts.RenderPoolSize,
		Logger:     opts.Logger,
	})

	return &Client{
		http:          hc,
		maxBody:       opts.MaxBody,
		ua:            ua,
		timeout:       opts.Timeout,
		guard:         guard,
		renderMode:    renderMode,
		renderer:      renderer,
		renderTimeout: renderTimeout,
		logger:        opts.Logger,
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

// FetchOptions are per-call options for web_fetch.
type FetchOptions struct {
	// Render forces the request through the JS-rendering browser.
	Render bool
}

// StatusError reports a non-200 HTTP response during a direct fetch.
type StatusError struct {
	URL        string
	Status     int
	Body       string
	RetryAfter int // seconds, 0 if absent
}

func (e *StatusError) Error() string {
	if e.Body != "" {
		return fmt.Sprintf("fetch %s: status %d: %s", e.URL, e.Status, truncate(e.Body, 300))
	}
	return fmt.Sprintf("fetch %s: status %d", e.URL, e.Status)
}

func (c *Client) Fetch(ctx context.Context, rawURL string) (*Page, error) {
	return c.FetchWithOptions(ctx, rawURL, FetchOptions{})
}

// Close releases renderer resources (pooled browser processes). It is safe to
// call multiple times and on a client without a browser renderer.
func (c *Client) Close() {
	if cr, ok := c.renderer.(*chromeRenderer); ok {
		cr.Close()
	}
}

// FetchWithOptions fetches rawURL, optionally rendering it with a browser and
// falling back to the renderer when auto-mode detects a bot block.
func (c *Client) FetchWithOptions(ctx context.Context, rawURL string, fo FetchOptions) (*Page, error) {
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

	if fo.Render || c.renderMode == renderAlways {
		return c.render(ctx, u)
	}

	direct := ctx
	if c.timeout > 0 {
		var cancel context.CancelFunc
		direct, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}
	page, err := c.fetchWithRetry(direct, u)
	if err == nil {
		return page, nil
	}
	var se *StatusError
	if errors.As(err, &se) {
		// Classify the block so logs and agent-facing errors carry the block
		// type instead of raw challenge HTML.
		kind := classifyStatus(se.Status, se.Body)
		if kind != BlockNone {
			c.logger.Warn("[request] fetch block classified",
				"url", u.String(),
				"status", se.Status,
				"block_kind", string(kind),
			)
		}
		if c.renderMode == renderAuto && c.renderer != nil && isBlockable(err) {
			c.logger.Warn("[request] fetch falling back to JS render",
				"url", u.String(),
				"error", err.Error(),
			)
			return c.render(ctx, u)
		}
		if kind != BlockNone {
			detail := extractTitle([]byte(se.Body))
			if detail == "" {
				detail = strings.TrimSpace(se.Body)
			}
			return nil, &BlockError{Kind: kind, URL: se.URL, Detail: detail, RetryAfter: se.RetryAfter}
		}
	}
	return nil, err
}

func (c *Client) render(ctx context.Context, u *url.URL) (*Page, error) {
	if c.renderer == nil {
		return nil, fmt.Errorf("JS rendering requested but browser renderer is unavailable (install a headless browser or set CHROME_BIN)")
	}
	rctx, cancel := context.WithTimeout(ctx, c.renderTimeout)
	defer cancel()
	return c.renderer.Render(rctx, u, c.ua)
}

func (c *Client) fetchWithRetry(ctx context.Context, u *url.URL) (*Page, error) {
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		page, err := c.doFetch(ctx, u)
		if err == nil {
			return page, nil
		}
		lastErr = err
		if attempt+1 >= maxAttempts || !shouldRetry(err) {
			break
		}
		delay := retryDelay(attempt, retryAfterSeconds(err))
		c.logger.Warn("[request] fetch retry",
			"url", u.String(),
			"attempt", attempt+1,
			"error", err.Error(),
			"delay_ms", delay.Milliseconds(),
		)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
	}
	return nil, lastErr
}

func (c *Client) doFetch(ctx context.Context, u *url.URL) (*Page, error) {
	reqID := newRequestID()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	applyBrowserHeaders(req, c.ua)

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
		return nil, fmt.Errorf("fetch %s: %w", u.String(), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		c.logger.Warn("[response] fetch non-200",
			"request_id", reqID,
			"status", resp.StatusCode,
			"body", string(body),
		)
		return nil, &StatusError{
			URL:        u.String(),
			Status:     resp.StatusCode,
			Body:       string(body),
			RetryAfter: parseRetryAfter(resp),
		}
	}

	return c.processResponse(resp, reqID, start)
}

// processResponse validates and converts a successful HTTP response into a Page.
func (c *Client) processResponse(resp *http.Response, reqID string, start time.Time) (*Page, error) {
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
		return nil, fmt.Errorf("fetch %s: content-type %q is binary media, not supported", resp.Request.URL.String(), mediaType)
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

	title := extractTitle(body)

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

// shouldRetry reports whether a failed attempt is worth retrying.
func shouldRetry(err error) bool {
	var se *StatusError
	if errors.As(err, &se) {
		switch se.Status {
		case 408, 429, 498:
			return true
		}
		return se.Status >= 500
	}
	return true // transport / TLS errors
}

// isBlockable reports whether an error looks like an antibot block, in which
// case auto-mode falls back to JS rendering.
func isBlockable(err error) bool {
	var se *StatusError
	if errors.As(err, &se) {
		switch se.Status {
		case 403, 429, 498:
			return true
		}
		return se.Status >= 500
	}
	return true
}

func retryAfterSeconds(err error) int {
	var se *StatusError
	if errors.As(err, &se) {
		return se.RetryAfter
	}
	return 0
}

func parseRetryAfter(resp *http.Response) int {
	ra := resp.Header.Get("Retry-After")
	if ra == "" {
		return 0
	}
	if secs, err := strconv.Atoi(ra); err == nil {
		return secs
	}
	if t, err := http.ParseTime(ra); err == nil {
		if d := time.Until(t); d > 0 {
			return int(d.Seconds()) + 1 // round up to the next second
		}
	}
	return 0
}

// retryDelay returns the backoff before the given attempt, honoring Retry-After
// (capped) and adding jitter for the exponential case.
func retryDelay(attempt, retryAfter int) time.Duration {
	if retryAfter > 0 {
		if d := time.Duration(retryAfter) * time.Second; d <= 5*time.Second {
			return d
		}
		return 5 * time.Second
	}
	base := time.Duration(500*(1<<attempt)) * time.Millisecond // 500ms, 1s
	jitter := time.Duration(mathrand.IntN(250)) * time.Millisecond
	return base + jitter
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

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
