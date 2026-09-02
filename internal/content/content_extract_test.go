package content

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// articleFixture is a typical article page: real content in <article>,
// navigation and footer noise around it.
var articleFixture = `<!doctype html><html><head>
<title>Test Article</title>
<meta name="description" content="A fixture article about chunking.">
<meta name="author" content="Jane Doe">
</head><body>
<nav><a href="/">Home</a> <a href="/about">About</a> <a href="/login">Login</a></nav>
<div class="cookie-banner">We use cookies to improve your experience. Accept all cookies now.</div>
<article>
<h1>Test Article</h1>
<p>` + strings.Repeat("Real paragraph text about chunking and readability extraction. ", 8) + `</p>
<p>Second paragraph with more meaningful content that helps the scorer. ` + strings.Repeat("Extra context words. ", 6) + `</p>
</article>
<footer>&copy; 2026 Footer Corp. All rights reserved. Privacy policy and terms.</footer>
</body></html>`

// cp1251 encodes a string where ASCII passes through and cyrillic А-я maps to
// 0xC0-0xFF (Windows-1251 layout, no Ё/ё needed for the fixture).
func cp1251(s string) []byte {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		switch {
		case r < 0x80:
			out = append(out, byte(r))
		case r >= 0x410 && r <= 0x44F:
			out = append(out, byte(0xC0+int(r)-0x410))
		default:
			out = append(out, '?')
		}
	}
	return out
}

func TestExtractHTMLArticle(t *testing.T) {
	clean, meta, ok := ExtractHTML([]byte(articleFixture), "https://example.com/blog/post")
	if !ok {
		t.Fatal("expected extraction to succeed on an article page")
	}
	cleanStr := string(clean)
	if strings.Contains(cleanStr, "Footer Corp") {
		t.Error("footer leaked into extracted content")
	}
	if strings.Contains(cleanStr, "cookie-banner") || strings.Contains(cleanStr, "Accept all cookies") {
		t.Error("cookie banner leaked into extracted content")
	}
	if strings.Contains(cleanStr, "href=\"/login\"") {
		t.Error("navigation leaked into extracted content")
	}
	if !strings.Contains(cleanStr, "Real paragraph text") {
		t.Error("article body lost during extraction")
	}
	if meta["title"] != "Test Article" {
		t.Errorf("meta title = %q", meta["title"])
	}
	if meta["description"] != "A fixture article about chunking." {
		t.Errorf("meta description = %q", meta["description"])
	}
}

func TestExtractHTMLShortPageFallsBack(t *testing.T) {
	raw := []byte(`<!doctype html><html><head><title>Landing</title></head><body><h1>Buy now</h1><p>Short text.</p></body></html>`)
	_, _, ok := ExtractHTML(raw, "https://example.com/")
	if ok {
		t.Error("expected ok=false for a short non-article page (fallback to full conversion)")
	}
}

func TestExtractHTMLWindows1251(t *testing.T) {
	para := strings.Repeat("Привет мир, это тестовая страница для проверки кодировки сайта. ", 6)
	raw := cp1251(`<!doctype html><html><head>
<meta http-equiv="Content-Type" content="text/html; charset=windows-1251">
<title>Тест кодировки</title></head><body>
<article><h1>Тест кодировки</h1><p>` + para + `</p></article>
</body></html>`)

	clean, _, ok := ExtractHTML(raw, "https://example.ru/page")
	if !ok {
		t.Fatal("expected extraction to succeed for windows-1251 page")
	}
	if !utf8.Valid(clean) {
		t.Fatal("extracted content is not valid UTF-8")
	}
	if !strings.Contains(string(clean), "Привет мир") {
		t.Fatalf("cyrillic text mangled by charset handling: %q", string(clean)[:min(200, len(clean))])
	}
	if strings.Contains(string(clean), "����") || strings.Contains(string(clean), "????") {
		t.Error("replacement chars in extracted content — charset mis-detected")
	}
}
