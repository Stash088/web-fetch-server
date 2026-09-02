package fetch

import (
	"strings"
	"testing"
)

func TestClassifyPage(t *testing.T) {
	tests := []struct {
		name  string
		title string
		body  string
		want  BlockKind
	}{
		{
			name:  "cloudflare title and body",
			title: "Just a moment...",
			body:  `<html><script src="/cdn-cgi/challenge-platform/h/b/orchestrate/jsch/v1"></script></html>`,
			want:  BlockChallengeCloudflare,
		},
		{
			name: "cloudflare body only, no title",
			body: `<html><script>window._cf_chl_opt={"chl":1}</script></html>`,
			want: BlockChallengeCloudflare,
		},
		{
			name:  "generic js challenge title",
			title: "Проверяем браузер",
			body:  `<html><body>Подождите, идёт проверка</body></html>`,
			want:  BlockChallengeJS,
		},
		{
			name:  "wildberries style challenge",
			title: "WB | Проверяем браузер",
			body:  `<html><body><div class="page-block">Проверяем браузер</div></body></html>`,
			want:  BlockChallengeJS,
		},
		{
			name: "title extracted from body when missing",
			body: `<html><head><title>Attention Required! | Cloudflare</title></head><body>cf-chl-error</body></html>`,
			want: BlockChallengeCloudflare,
		},
		{
			name:  "bare captcha page",
			title: "",
			body:  `<html><body><form action="/captcha"><div class="g-recaptcha" data-sitekey="6Lc"></div></form></body></html>`,
			want:  BlockCaptcha,
		},
		{
			name:  "captcha widget on large content page is not a block",
			title: "Login — Example Shop",
			body:  `<html><body>` + strings.Repeat(`<p>content</p>`, 4096) + `<div class="g-recaptcha" data-sitekey="6Lc"></div></body></html>`,
			want:  BlockNone,
		},
		{
			name:  "normal page",
			title: "Example Domain",
			body:  `<html><head><title>Example Domain</title></head><body><h1>Example Domain</h1></body></html>`,
			want:  BlockNone,
		},
		{
			name:  "cdn-cgi script without challenge path is not a block",
			title: "Contact",
			body:  `<html><body><a href="/cdn-cgi/l/email-protection">mail</a></body></html>`,
			want:  BlockNone,
		},
		{
			name:  "turnstile interstitial classifies as captcha",
			title: "Checking…",
			body:  `<html><body><div class="cf-turnstile" data-sitekey="0x1"></div><script src="https://challenges.cloudflare.com/turnstile/v0/api.js"></script></body></html>`,
			want:  BlockCaptcha,
		},
		{
			name: "reddit-style block wall with 200 status",
			body: `<html><body><div>You've been blocked by network security. <a href="/">Back to home</a></div></body></html>`,
			want: BlockWall,
		},
		{
			name: "pardon our interruption wall",
			body: `<html><body><h1>Pardon Our Interruption</h1><p>As you were browsing, something about your browser made us think you were a bot.</p></body></html>`,
			want: BlockWall,
		},
		{
			name:  "block message quoted inside a large article is content",
			title: "How to appeal a ban",
			body:  `<html><body>` + strings.Repeat(`<p>If you've been blocked on Discord, you can appeal the decision.</p>`, 2000) + `</body></html>`,
			want:  BlockNone,
		},
		{
			name: "reddit-style wall with 190KB of CSS and tiny visible text",
			body: `<html><head><style>` + strings.Repeat(`.theme-light{--rem360:22.5rem;}`, 5000) + `</style></head><body><div>You've been blocked by network security.</div></body></html>`,
			want: BlockWall,
		},
		{
			name: "cloudflare challenge wins over block wall text",
			body: `<html><body>You've been blocked.<script>window._cf_chl_opt={"chl":1}</script></body></html>`,
			want: BlockChallengeCloudflare,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyPage(tt.title, []byte(tt.body)); got != tt.want {
				t.Errorf("classifyPage(%q, ...) = %q, want %q", tt.title, got, tt.want)
			}
		})
	}
}

func TestClassifyStatus(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   BlockKind
	}{
		{"plain 429", 429, "too many requests", BlockRateLimited},
		{"plain 498", 498, "token invalid", BlockRateLimited},
		{"plain 403 is not classified", 403, "forbidden", BlockNone},
		{"403 with cloudflare body", 403, `<script>window.__cf_chl_opt={}</script>`, BlockChallengeCloudflare},
		{
			"403 reddit-style wall, marker deep in a large body",
			403,
			`<html><head><style>` + strings.Repeat(`.theme-light{--rem360:22.5rem;}`, 4000) + `</style></head><body>You've been blocked by network security.</body></html>`,
			BlockWall,
		},
		{"429 carrying captcha page", 429, `<div class="g-recaptcha" data-sitekey="x"></div>`, BlockCaptcha},
		{"500 plain", 500, "internal error", BlockNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyStatus(tt.status, tt.body); got != tt.want {
				t.Errorf("classifyStatus(%d, ...) = %q, want %q", tt.status, got, tt.want)
			}
		})
	}
}

func TestBlockErrorFormatting(t *testing.T) {
	be := &BlockError{Kind: BlockRateLimited, URL: "https://x.example", Detail: "too many", RetryAfter: 30}
	msg := be.Error()
	if !strings.Contains(msg, "rate_limited") || !strings.Contains(msg, "retry after 30s") {
		t.Errorf("unexpected message: %s", msg)
	}
	if got := BlockKindOf(be); got != BlockRateLimited {
		t.Errorf("BlockKindOf = %q, want rate_limited", got)
	}
	if got := BlockKindOf(nil); got != BlockNone {
		t.Errorf("BlockKindOf(nil) = %q, want empty", got)
	}
	if got := BlockKindOf(&StatusError{URL: "https://x", Status: 403}); got != BlockNone {
		t.Errorf("BlockKindOf(StatusError) = %q, want empty", got)
	}
}

func TestExtractTitle(t *testing.T) {
	if got := extractTitle([]byte("<html><head><title>  Hello World </title></head></html>")); got != "Hello World" {
		t.Errorf("extractTitle = %q, want %q", got, "Hello World")
	}
	if got := extractTitle([]byte("<html><body>no title</body></html>")); got != "" {
		t.Errorf("extractTitle without title = %q, want empty", got)
	}
}
