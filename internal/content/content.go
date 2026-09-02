package content

import (
	"bytes"
	"strings"

	"github.com/JohannesKaufmann/html-to-markdown"
	"github.com/microcosm-cc/bluemonday"
	"golang.org/x/net/html"
)

var mdConverter = md.NewConverter("", true, nil)
var sanitizer = bluemonday.UGCPolicy()

// ToMarkdown converts raw HTML into clean markdown text.
func ToMarkdown(raw []byte) string {
	clean := sanitizer.SanitizeBytes(raw)
	md, err := mdConverter.ConvertString(string(clean))
	if err != nil {
		return strings.TrimSpace(string(clean))
	}
	return strings.TrimSpace(md)
}

// ToText strips markup and returns plain text: scripts, styles and noscript
// content are dropped, block-level tags become line breaks and runs of
// whitespace are collapsed. No tags ever appear in the output.
func ToText(raw []byte) string {
	z := html.NewTokenizer(bytes.NewReader(raw))
	var sb strings.Builder
	skip := 0 // depth of skipped subtrees (script/style/noscript)
	brk := 0  // pending break: 0 none, 1 space, 2 newline
	const brkNone, brkSpace, brkNewline = 0, 1, 2

	ensureBreak := func() {
		if sb.Len() == 0 {
			brk = brkNone
			return
		}
		switch brk {
		case brkNewline:
			sb.WriteString("\n")
		case brkSpace:
			sb.WriteString(" ")
		}
		brk = brkNone
	}

	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		switch tt {
		case html.StartTagToken:
			name, _ := z.TagName()
			nameStr := string(name)
			if skip > 0 {
				if isSkippedTextTag(nameStr) {
					skip++
				}
				continue
			}
			if isSkippedTextTag(nameStr) {
				skip++
				continue
			}
			if isBlockTag(nameStr) {
				brk = brkNewline
			}
		case html.SelfClosingTagToken:
			name, _ := z.TagName()
			if skip > 0 {
				continue
			}
			if string(name) == "br" {
				brk = brkNewline
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			nameStr := string(name)
			if skip > 0 {
				if isSkippedTextTag(nameStr) {
					skip--
				}
				continue
			}
			if isBlockTag(nameStr) {
				brk = brkNewline
			}
		case html.TextToken:
			if skip > 0 {
				continue
			}
			txt := strings.Join(strings.Fields(string(z.Text())), " ")
			if txt == "" {
				continue
			}
			ensureBreak()
			sb.WriteString(txt)
			brk = brkSpace
		}
	}
	out := sb.String()
	for strings.Contains(out, "\n\n\n") {
		out = strings.ReplaceAll(out, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(out)
}

// isSkippedTextTag reports whether the element's text content must be dropped
// entirely (machine-readable or layout-only markup).
func isSkippedTextTag(name string) bool {
	switch name {
	case "script", "style", "noscript", "template":
		return true
	}
	return false
}

// isBlockTag reports whether the tag starts or ends a text block, which
// becomes a line break in plain-text output.
func isBlockTag(name string) bool {
	switch name {
	case "p", "div", "li", "ul", "ol", "br", "h1", "h2", "h3", "h4", "h5", "h6",
		"tr", "table", "section", "article", "header", "footer", "blockquote",
		"pre", "figcaption", "hr", "dl", "dt", "dd":
		return true
	}
	return false
}

// Chunk returns a window of content starting at startIndex with maxLength
// runes (not bytes, so multi-byte scripts stay intact). Windows are snapped
// back to the last paragraph boundary when that does not shrink them below
// half of maxLength. It reports total rune length so the model can page
// through long documents with start_index=end of the previous chunk.
func Chunk(content string, startIndex, maxLength int) (chunk string, total int) {
	runes := []rune(content)
	total = len(runes)
	if startIndex > total {
		startIndex = total
	}
	if startIndex < 0 {
		startIndex = 0
	}
	end := startIndex + maxLength
	if end > total {
		end = total
	}
	end = snapBoundary(runes, startIndex, end, maxLength)
	return string(runes[startIndex:end]), total
}

// snapBoundary moves the window end back to the last paragraph boundary
// (blank line first, single newline second) as long as at least half of the
// requested window length is preserved. The final chunk is never snapped.
func snapBoundary(runes []rune, start, end, maxLength int) int {
	if end >= len(runes) || maxLength <= 0 {
		return end
	}
	minLen := maxLength / 2
	// Blank line "\n\n": consume it, so the next chunk starts on paragraph text.
	for i := end - 2; i >= start; i-- {
		if runes[i] == '\n' && runes[i+1] == '\n' {
			if i+2-start >= minLen {
				return i + 2
			}
			return end
		}
	}
	// Single newline: end right after it.
	for i := end - 1; i >= start; i-- {
		if runes[i] == '\n' {
			if i+1-start >= minLen {
				return i + 1
			}
			return end
		}
	}
	return end
}
