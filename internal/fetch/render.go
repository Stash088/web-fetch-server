package fetch

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
)

// stealthJS is the go-rod/stealth script (MIT, JSVersion v2.7.3) that patches
// the automation leaks headless Chrome exposes: navigator.webdriver, plugins,
// languages, hardwareConcurrency, WebGL vendor/renderer, window.chrome and
// more. It is injected before page scripts run on every new document.
//
//go:embed stealth/stealth.js
var stealthJS string

// renderProfile is a coherent browser persona: a mismatched combination
// (e.g. en-US locale + Moscow timezone) is itself a bot signal.
type renderProfile struct {
	lang string
	tz   string
	w, h int
}

// renderProfiles is the small pool of plausible personas randomized per render.
var renderProfiles = []renderProfile{
	{lang: "en-US,en;q=0.9", tz: "America/New_York", w: 1366, h: 768},
	{lang: "en-US,en;q=0.9,ru;q=0.8", tz: "America/Chicago", w: 1536, h: 864},
	{lang: "en-GB,en;q=0.9", tz: "Europe/London", w: 1440, h: 900},
	{lang: "ru-RU,ru;q=0.9,en-US;q=0.8", tz: "Europe/Moscow", w: 1920, h: 1080},
}

// Renderer fetches a URL with a real browser so that JS-rendered content and
// WAF browser challenges are handled.
type Renderer interface {
	Render(ctx context.Context, u *url.URL, ua string) (*Page, error)
}

type RendererOptions struct {
	ChromeBin string
	MaxBody   int64
	// ProfileDir is the base directory for persistent browser profiles
	// (RENDER_PROFILE_DIR); empty uses a default under the system tmpdir.
	ProfileDir string
	// PoolSize is the number of pooled browser processes (RENDER_POOL_SIZE).
	PoolSize int
	Logger   *slog.Logger
}

// chromeRenderer serves Render calls through a lazily created
// RenderSessionManager: the browser processes and their cookie profiles stay
// alive between renders instead of being spawned fresh every time.
type chromeRenderer struct {
	opts   RendererOptions
	logger *slog.Logger

	mu      sync.Mutex
	manager *RenderSessionManager
}

// NewChromeRenderer returns a Renderer backed by pooled headless
// Chrome/Chromium sessions.
func NewChromeRenderer(opts RendererOptions) Renderer {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &chromeRenderer{opts: opts, logger: opts.Logger}
}

// Render navigates a pooled browser session to u and returns the rendered HTML.
func (r *chromeRenderer) Render(ctx context.Context, u *url.URL, ua string) (*Page, error) {
	if err := r.ensureChrome(); err != nil {
		return nil, err
	}
	m, err := r.sessionManager()
	if err != nil {
		return nil, err
	}
	return m.Render(ctx, u, ua)
}

// Close shuts down all pooled browser sessions.
func (r *chromeRenderer) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.manager != nil {
		r.manager.Close()
		r.manager = nil
	}
}

// sessionManager lazily starts the session pool on first use; if no browser
// is available the later Render call produces the clear ensureChrome error.
func (r *chromeRenderer) sessionManager() (*RenderSessionManager, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.manager == nil {
		r.manager = NewRenderSessionManager(RenderSessionOptions{
			ChromeBin:  r.opts.ChromeBin,
			ProfileDir: r.opts.ProfileDir,
			PoolSize:   r.opts.PoolSize,
			MaxBody:    r.opts.MaxBody,
			Logger:     r.logger,
		})
	}
	return r.manager, nil
}

// waitChallenge waits until the page title no longer looks like a bot
// challenge (Cloudflare "Just a moment...", "Performing security verification",
// etc.) or until timeout. Many challenges auto-resolve after a few seconds in
// a real browser.
func waitChallenge(ctx context.Context, timeout time.Duration) chromedp.Action {
	deadline := time.Now().Add(timeout)
	var title string
	return chromedp.ActionFunc(func(ctx context.Context) error {
		for time.Now().Before(deadline) {
			if err := chromedp.Title(&title).Do(ctx); err != nil {
				return err
			}
			if !challengeTitle(strings.ToLower(title)) {
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(500 * time.Millisecond):
			}
		}
		return nil
	})
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
