package fetch

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

var uaMajorRe = regexp.MustCompile(`Chrome/(\d+)`)

// applyBrowserHeaders decorates a request with headers a real browser sends,
// so antibot heuristics see a plausible navigator instead of an HTTP client.
// Client hints (sec-ch-ua*) are derived from the User-Agent so the declared
// fingerprint stays consistent — a Chrome UA without matching client hints is
// itself a bot signal. Accept-Encoding is intentionally left for net/http (it
// adds gzip and transparently decompresses it).
func applyBrowserHeaders(req *http.Request, ua string) {
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9,ru;q=0.8")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Sec-Fetch-User", "?1")
	req.Header.Set("Sec-CH-UA", chromeBrand(ua))
	req.Header.Set("Sec-CH-UA-Mobile", "?0")
	req.Header.Set("Sec-CH-UA-Platform", chromePlatform(ua))
	req.Header.Set("Cache-Control", "max-age=0")
	req.Header.Set("Pragma", "no-cache")
}

// chromeBrand builds the Sec-CH-UA brand list for the Chrome major version
// declared in the UA string (defaults to a recent Chrome when unparseable).
func chromeBrand(ua string) string {
	major := "126"
	if m := uaMajorRe.FindStringSubmatch(ua); m != nil {
		major = m[1]
	}
	return fmt.Sprintf(`"Chromium";v="%s", "Google Chrome";v="%s", "Not-A.Brand";v="99"`, major, major)
}

// chromePlatform derives the Sec-CH-UA-Platform value from the UA's OS token.
func chromePlatform(ua string) string {
	switch {
	case strings.Contains(ua, "Windows"):
		return `"Windows"`
	case strings.Contains(ua, "Macintosh"), strings.Contains(ua, "Mac OS X"):
		return `"macOS"`
	case strings.Contains(ua, "Android"):
		return `"Android"`
	case strings.Contains(ua, "iPhone"), strings.Contains(ua, "iPad"):
		return `"iOS"`
	case strings.Contains(ua, "X11"), strings.Contains(ua, "Linux"):
		return `"Linux"`
	default:
		return `"macOS"`
	}
}
