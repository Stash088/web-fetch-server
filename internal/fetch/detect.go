package fetch

import (
	"errors"
	"fmt"
	"strings"
)

// BlockKind classifies the type of antibot block detected on a page or in an
// HTTP response.
type BlockKind string

const (
	// BlockNone means no antibot block was detected.
	BlockNone BlockKind = ""
	// BlockChallengeCloudflare is a Cloudflare-managed challenge ("Just a
	// moment...", cf-chl challenge platform scripts).
	BlockChallengeCloudflare BlockKind = "challenge_cloudflare"
	// BlockChallengeJS is a generic JavaScript browser check (e.g. wildberries
	// "Проверяем браузер") not tied to a known vendor.
	BlockChallengeJS BlockKind = "challenge_js"
	// BlockRateLimited means the server rate-limited the client (429/498).
	BlockRateLimited BlockKind = "rate_limited"
	// BlockCaptcha means the page demands a CAPTCHA solution (reCAPTCHA,
	// Cloudflare Turnstile, hCaptcha) which cannot be solved automatically.
	BlockCaptcha BlockKind = "captcha"
)

// BlockError reports an antibot block in a structured, agent-readable way
// instead of returning challenge HTML disguised as page content.
type BlockError struct {
	Kind       BlockKind
	URL        string
	Detail     string // page title or short snippet explaining the verdict
	RetryAfter int    // seconds, when known (rate limits)
}

func (e *BlockError) Error() string {
	msg := fmt.Sprintf("fetch %s: blocked (%s)", e.URL, e.Kind)
	if e.Detail != "" {
		msg += ": " + truncate(e.Detail, 200)
	}
	if e.RetryAfter > 0 {
		msg += fmt.Sprintf(" (retry after %ds)", e.RetryAfter)
	}
	return msg
}

// BlockKindOf extracts the block kind from err when it is a BlockError,
// otherwise BlockNone.
func BlockKindOf(err error) BlockKind {
	var be *BlockError
	if errors.As(err, &be) {
		return be.Kind
	}
	return BlockNone
}

// challengeTitleMarkers are page-title fragments that indicate a bot check
// rather than real content. Matched against the lower-cased title.
var challengeTitleMarkers = []string{
	"just a moment",
	"security verification",
	"checking your browser",
	"attention required",
	"verify you are human",
	"проверяем браузер",
	"bot protection",
	"ddos protection by",
	"доступ ограничен",
	"site verification",
}

// cloudflareBodyMarkers are HTML fragments unique to Cloudflare challenge
// pages (they never appear on normally served Cloudflare sites).
var cloudflareBodyMarkers = []string{
	"cf-chl",
	"cdn-cgi/challenge-platform",
	"cf-browser-verification",
	"_cf_chl_opt",
}

// captchaBodyMarkers are HTML fragments of CAPTCHA widget embeds.
var captchaBodyMarkers = []string{
	"data-sitekey",
	"g-recaptcha",
	"hcaptcha.com/1/api.js",
	"challenges.cloudflare.com/turnstile",
	"cf-turnstile",
}

// maxChallengeBodySize bounds the body size below which a CAPTCHA marker is
// treated as a full-page challenge rather than a widget embedded inside
// normal content (login forms on real pages are usually much larger).
const maxChallengeBodySize = 16 << 10

// classifyPage inspects a fetched page (title + HTML body) for antibot
// challenge markers and returns the detected block kind. When title is empty
// it is extracted from the body. Cloudflare markers win over generic
// challenge titles; CAPTCHA markers only classify bare/small pages so a
// footer widget on a content page is not a false positive.
func classifyPage(title string, body []byte) BlockKind {
	if title == "" {
		title = extractTitle(body)
	}
	lowerTitle := strings.ToLower(title)
	lowerBody := strings.ToLower(string(body))
	hasCF := containsAny(lowerBody, cloudflareBodyMarkers)
	if challengeTitle(lowerTitle) {
		if hasCF {
			return BlockChallengeCloudflare
		}
		return BlockChallengeJS
	}
	if hasCF {
		return BlockChallengeCloudflare
	}
	if containsAny(lowerBody, captchaBodyMarkers) && len(body) <= maxChallengeBodySize {
		return BlockCaptcha
	}
	return BlockNone
}

// classifyStatus maps an HTTP status code plus response body to a block kind.
// The body always wins: a 429 that carries a CAPTCHA page is reported as
// captcha (more specific), a plain 429/498 as rate_limited, and a plain 403
// stays unclassified (not every 403 is an antibot block).
func classifyStatus(status int, body string) BlockKind {
	if kind := classifyPage("", []byte(body)); kind != BlockNone {
		return kind
	}
	if status == 429 || status == 498 {
		return BlockRateLimited
	}
	return BlockNone
}

// challengeTitle reports whether a lower-cased page title looks like a bot
// challenge interstitial.
func challengeTitle(lowerTitle string) bool {
	return lowerTitle != "" && containsAny(lowerTitle, challengeTitleMarkers)
}

func containsAny(s string, markers []string) bool {
	for _, m := range markers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

// extractTitle pulls the <title> text out of an HTML body, "" when absent.
func extractTitle(body []byte) string {
	lower := strings.ToLower(string(body))
	i := strings.Index(lower, "<title>")
	if i < 0 {
		return ""
	}
	rest := body[i+len("<title>"):]
	if j := strings.Index(strings.ToLower(string(rest)), "</title>"); j >= 0 {
		return strings.TrimSpace(string(rest[:j]))
	}
	return ""
}
