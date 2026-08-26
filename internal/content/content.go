package content

import (
	"strings"

	"github.com/JohannesKaufmann/html-to-markdown"
	"github.com/microcosm-cc/bluemonday"
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

// ToText strips markup, returns plain text.
func ToText(raw []byte) string {
	return strings.TrimSpace(string(sanitizer.SanitizeBytes(raw)))
}

// Chunk returns a window of content starting at startIndex with maxLength chars.
// It reports total length so the model can page through long documents.
func Chunk(content string, startIndex, maxLength int) (chunk string, total int) {
	total = len(content)
	if startIndex > total {
		startIndex = total
	}
	end := startIndex + maxLength
	if end > total {
		end = total
	}
	return content[startIndex:end], total
}
