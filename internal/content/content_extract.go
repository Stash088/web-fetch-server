package content

import (
	"bytes"
	"io"
	"net/url"
	"regexp"
	"time"
	"unicode/utf8"

	"github.com/PuerkitoBio/goquery"
	"github.com/go-shiori/go-readability"
	"golang.org/x/net/html/charset"
)

// minExtractRunes is the minimum amount of extracted text for readability
// extraction to be considered successful. Below the threshold (docs, landing
// pages, changelogs — anything that is not an article) callers fall back to
// converting the full page.
const minExtractRunes = 200

// noiseClassRe matches class/id names of elements that are never article
// content (cookie banners, consent walls, promos). Applied before readability
// because its scoring sometimes keeps such blocks on small pages.
var noiseClassRe = regexp.MustCompile(`(?i)(cookie|consent|banner|newsletter|subscribe|popup|modal|overlay|advert|sponsor|breadcrumb|social-share|share-buttons)`)

// ExtractHTML extracts the main article content from raw HTML with
// go-readability, dropping navigation, footers and cookie banners. The input
// is re-decoded via x/net/html/charset first, so windows-1251 RU pages are
// handled correctly. pageURL resolves relative links inside the article.
// ok is false (and cleanHTML nil) when extraction fails or the extracted text
// is too short — the caller should fall back to full-page conversion.
func ExtractHTML(raw []byte, pageURL string) (cleanHTML []byte, meta map[string]string, ok bool) {
	meta = map[string]string{}

	var articleURL *url.URL
	if u, err := url.Parse(pageURL); err == nil {
		articleURL = u
	}

	// readability expects UTF-8 input; charset.NewReader performs HTML5
	// encoding sniffing (meta charset, BOM, chardet fallback).
	var reader io.Reader = bytes.NewReader(raw)
	if decoded, err := charset.NewReader(bytes.NewReader(raw), pageURL); err == nil {
		reader = decoded
	}

	reader = bytes.NewReader(stripNoise(reader))

	article, err := readability.FromReader(reader, articleURL)
	if err != nil {
		return nil, meta, false
	}
	if utf8.RuneCountInString(article.TextContent) < minExtractRunes {
		return nil, meta, false
	}

	if article.Title != "" {
		meta["title"] = article.Title
	}
	if article.Byline != "" {
		meta["byline"] = article.Byline
	}
	if article.Excerpt != "" {
		meta["description"] = article.Excerpt
	}
	if article.SiteName != "" {
		meta["site_name"] = article.SiteName
	}
	if article.Language != "" {
		meta["language"] = article.Language
	}
	if article.PublishedTime != nil {
		meta["published_time"] = article.PublishedTime.UTC().Format(time.RFC3339)
	}
	return []byte(article.Content), meta, true
}

// stripNoise removes structural and class-based noise (nav/footer/aside and
// cookie/consent/promo blocks) from the HTML before readability scoring, and
// returns the cleaned serialized document.
func stripNoise(r io.Reader) []byte {
	doc, err := goquery.NewDocumentFromReader(r)
	if err != nil {
		// Return the original bytes on parse failure; readability gets its chance next.
		b, rerr := io.ReadAll(r)
		if rerr != nil {
			return nil
		}
		return b
	}
	doc.Find("nav, footer, aside").Remove()
	doc.Find("[class],[id]").Each(func(_ int, s *goquery.Selection) {
		marks, _ := s.Attr("class")
		id, hasID := s.Attr("id")
		if hasID {
			marks += " " + id
		}
		if noiseClassRe.MatchString(marks) {
			s.Remove()
		}
	})
	out, err := doc.Html()
	if err != nil {
		return nil
	}
	return []byte(out)
}
