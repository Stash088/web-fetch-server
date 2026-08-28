package fetch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

// Renderer fetches a URL with a real browser so that JS-rendered content and
// WAF browser challenges are handled.
type Renderer interface {
	Render(ctx context.Context, u *url.URL, ua string) (*Page, error)
}

type RendererOptions struct {
	ChromeBin string
	MaxBody   int64
	Logger    *slog.Logger
}

type chromeRenderer struct {
	opts   RendererOptions
	logger *slog.Logger
}

// NewChromeRenderer returns a Renderer backed by a headless Chrome/Chromium.
func NewChromeRenderer(opts RendererOptions) Renderer {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &chromeRenderer{opts: opts, logger: opts.Logger}
}

// Render navigates a headless browser to u and returns the rendered HTML.
func (r *chromeRenderer) Render(ctx context.Context, u *url.URL, ua string) (*Page, error) {
	if err := r.ensureChrome(); err != nil {
		return nil, err
	}

	execOpts := []chromedp.ExecAllocatorOption{
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
		chromedp.Headless,
		chromedp.DisableGPU,
		chromedp.NoSandbox,
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("disable-background-networking", true),
		chromedp.Flag("blink-settings", "imagesEnabled=false"),
		chromedp.UserAgent(ua),
		chromedp.Flag("accept-language", "en-US,en;q=0.9,ru;q=0.8"),
	}
	if r.opts.ChromeBin != "" {
		execOpts = append(execOpts, chromedp.ExecPath(r.opts.ChromeBin))
	}

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, execOpts...)
	defer cancelAlloc()
	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	defer cancelBrowser()

	var html, title string
	err := chromedp.Run(browserCtx,
		chromedp.Navigate(u.String()),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Sleep(500*time.Millisecond),
		chromedp.Title(&title),
		chromedp.OuterHTML("html", &html, chromedp.ByQuery),
	)
	if err != nil {
		return nil, fmt.Errorf("render %s: %w", u.String(), err)
	}

	body := []byte(html)
	if r.opts.MaxBody > 0 && int64(len(body)) > r.opts.MaxBody {
		body = body[:r.opts.MaxBody]
	}

	r.logger.Info("[response] fetch (js render)",
		"url", u.String(),
		"bytes", len(body),
		"title", strings.TrimSpace(title),
	)

	return &Page{URL: u.String(), Title: strings.TrimSpace(title), Body: body, MediaType: "text/html"}, nil
}

// ensureChrome verifies a usable Chrome/Chromium binary exists, producing a
// clear error message when JS rendering is requested but no browser is present.
func (r *chromeRenderer) ensureChrome() error {
	if r.opts.ChromeBin != "" {
		if _, err := os.Stat(r.opts.ChromeBin); err != nil {
			return fmt.Errorf("chrome binary not found at CHROME_BIN=%s: %w", r.opts.ChromeBin, err)
		}
		return nil
	}
	for _, name := range []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable", "chrome", "chrome-headless-shell"} {
		if _, err := exec.LookPath(name); err == nil {
			return nil
		}
	}
	return errors.New("headless browser not found (install chromium or set CHROME_BIN)")
}
