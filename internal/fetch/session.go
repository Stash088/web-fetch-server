package fetch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	mathrand "math/rand/v2"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/page"
)

// RenderSessionOptions configures the render session manager.
type RenderSessionOptions struct {
	// ChromeBin overrides the Chrome/Chromium binary path (empty = auto-detect).
	ChromeBin string
	// ProfileDir is the base directory holding persistent browser profiles
	// (cookies, cf_clearance). Empty defaults to a directory under os.TempDir().
	ProfileDir string
	// PoolSize is the number of parallel browser processes. Each gets its own
	// user-data-dir so profiles never lock. Values below 1 mean 1.
	PoolSize int
	// MaxBody truncates the rendered HTML (0 = no truncation).
	MaxBody int64
	Logger *slog.Logger
}

// RenderSessionManager keeps a pool of long-lived Chrome processes with
// persistent profiles. Cookies (cf_clearance, shop sessions) survive between
// renders, so a challenge solved once is not solved again on every request.
type RenderSessionManager struct {
	opts      RenderSessionOptions
	logger    *slog.Logger
	workers   []*renderWorker
	next      atomic.Uint64
	closeOnce sync.Once
	closed    chan struct{}
}

// renderWorker is one browser process with an exclusive profile directory.
// A single-slot channel serializes renders per worker so parallel requests
// spread over the pool instead of fighting over one profile lock.
type renderWorker struct {
	mgr         *RenderSessionManager
	index       int
	slots       chan struct{}
	mu          sync.Mutex
	allocCancel context.CancelFunc
	browserCtx  context.Context
}

// NewRenderSessionManager builds the pool and wipes profile directories:
// cookies collected during the previous process lifetime (potentially from
// domains the SSRF guard now blocks) must never leak into the new run.
func NewRenderSessionManager(opts RenderSessionOptions) *RenderSessionManager {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.PoolSize <= 0 {
		opts.PoolSize = 1
	}
	if opts.ProfileDir == "" {
		opts.ProfileDir = filepath.Join(os.TempDir(), "web-fetch-render-profiles")
	}
	_ = os.RemoveAll(opts.ProfileDir)
	m := &RenderSessionManager{opts: opts, logger: opts.Logger, closed: make(chan struct{})}
	for i := 0; i < opts.PoolSize; i++ {
		m.workers = append(m.workers, &renderWorker{mgr: m, index: i, slots: make(chan struct{}, 1)})
	}
	m.logger.Info("[render] session manager started",
		"pool_size", opts.PoolSize,
		"profile_dir", opts.ProfileDir,
	)
	return m
}

// Render navigates to u in a tab of one of the pooled browsers and returns
// the rendered HTML. The browser process and its profile outlive the call;
// only the tab is discarded afterwards.
func (m *RenderSessionManager) Render(ctx context.Context, u *url.URL, ua string) (*Page, error) {
	select {
	case <-m.closed:
		return nil, errors.New("render session manager is closed")
	default:
	}

	w := m.workers[m.next.Add(1)%uint64(len(m.workers))]
	select {
	case w.slots <- struct{}{}:
		defer func() { <-w.slots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-m.closed:
		return nil, errors.New("render session manager is closed")
	}

	bctx, err := w.ensure()
	if err != nil {
		return nil, err
	}

	// A new tab inside the existing browser shares its cookie jar; canceling
	// the tab context at the end closes only the tab, not the browser.
	tabCtx, cancelTab := chromedp.NewContext(bctx)
	defer cancelTab()

	return renderInTab(tabCtx, u, ua, m.opts.MaxBody, m.logger)
}

// renderInTab runs the stealth injection, navigation and challenge wait in the
// given tab context, then classifies the result.
func renderInTab(tabCtx context.Context, u *url.URL, ua string, maxBody int64, logger *slog.Logger) (*Page, error) {
	prof := renderProfiles[mathrand.IntN(len(renderProfiles))]
	var html, title string
	err := chromedp.Run(tabCtx,
		// Inject the stealth patches and persona before any page script runs;
		// after navigation it would be too late for first-paint checks.
		chromedp.ActionFunc(func(ctx context.Context) error {
			if _, err := page.AddScriptToEvaluateOnNewDocument(stealthJS).Do(ctx); err != nil {
				return fmt.Errorf("inject stealth: %w", err)
			}
			if err := emulation.SetTimezoneOverride(prof.tz).Do(ctx); err != nil {
				return fmt.Errorf("set timezone: %w", err)
			}
			// The UA is per-tab because the browser process is shared and the
			// configured UA could change between renders.
			if err := emulation.SetUserAgentOverride(ua).WithAcceptLanguage(prof.lang).Do(ctx); err != nil {
				return fmt.Errorf("set user agent: %w", err)
			}
			return nil
		}),
		chromedp.Navigate(u.String()),
		chromedp.WaitReady("body", chromedp.ByQuery),
		waitChallenge(tabCtx, 20*time.Second),
		chromedp.Title(&title),
		chromedp.OuterHTML("html", &html, chromedp.ByQuery),
	)
	if err != nil {
		return nil, fmt.Errorf("render %s: %w", u.String(), err)
	}

	body := []byte(html)
	if maxBody > 0 && int64(len(body)) > maxBody {
		body = body[:maxBody]
	}

	// Transparency: a challenge page that survived rendering is reported as a
	// structured block, never passed to the agent as if it were content.
	if kind := classifyPage(title, body); kind != BlockNone {
		logger.Warn("[response] fetch (js render) blocked by challenge",
			"url", u.String(),
			"block_kind", string(kind),
			"title", strings.TrimSpace(title),
			"bytes", len(body),
		)
		return nil, &BlockError{Kind: kind, URL: u.String(), Detail: strings.TrimSpace(title)}
	}

	logger.Info("[response] fetch (js render)",
		"url", u.String(),
		"bytes", len(body),
		"title", strings.TrimSpace(title),
	)

	return &Page{URL: u.String(), Title: strings.TrimSpace(title), Body: body, MediaType: "text/html"}, nil
}

// ensure starts the worker's browser process lazily and recreates it when it
// has crashed or been closed.
func (w *renderWorker) ensure() (context.Context, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.browserCtx != nil && w.browserCtx.Err() == nil {
		return w.browserCtx, nil
	}
	if w.allocCancel != nil {
		w.allocCancel()
		w.allocCancel = nil
		w.browserCtx = nil
	}

	m := w.mgr
	prof := renderProfiles[mathrand.IntN(len(renderProfiles))]
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
		chromedp.Flag("accept-language", prof.lang),
		chromedp.Flag("lang", strings.SplitN(prof.lang, ",", 2)[0]),
		chromedp.WindowSize(prof.w, prof.h),
		chromedp.UserDataDir(w.profileDir()),
		// Anti-detection: chromedp defaults to enable-automation=true, which
		// makes window.navigator.webdriver=true and is a dead giveaway to
		// Cloudflare-style bot challenges. Flip it off and use the modern
		// headless mode, which looks like a normal browser.
		chromedp.Flag("enable-automation", false),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("headless", "new"),
	}
	if m.opts.ChromeBin != "" {
		execOpts = append(execOpts, chromedp.ExecPath(m.opts.ChromeBin))
	}

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), execOpts...)
	bctx, cancelBrowser := chromedp.NewContext(allocCtx)
	// Start the browser process right away so profile problems surface here.
	if err := chromedp.Run(bctx); err != nil {
		cancelBrowser()
		cancelAlloc()
		return nil, fmt.Errorf("start render session browser %d: %w", w.index, err)
	}
	w.allocCancel = func() {
		cancelBrowser()
		cancelAlloc()
	}
	w.browserCtx = bctx
	return bctx, nil
}

// profileDir is the persistent user-data-dir backing this worker's browser.
func (w *renderWorker) profileDir() string {
	return filepath.Join(w.mgr.opts.ProfileDir, fmt.Sprintf("worker-%d", w.index))
}

// close shuts the worker's browser process down.
func (w *renderWorker) close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.allocCancel != nil {
		w.allocCancel()
		w.allocCancel = nil
		w.browserCtx = nil
	}
}

// Close terminates all pooled browsers and releases their profiles.
func (m *RenderSessionManager) Close() {
	m.closeOnce.Do(func() {
		close(m.closed)
		for _, w := range m.workers {
			w.close()
		}
		m.logger.Info("[render] session manager stopped")
	})
}
