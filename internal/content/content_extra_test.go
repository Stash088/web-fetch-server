package content

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestToTextDropsTagsAndScripts(t *testing.T) {
	raw := []byte(`<html><head><style>body{color:red}</style><script>var x = 1;</script></head>
<body><h1>Заголовок</h1><p>Первый  абзац   с пробелами.</p>
<script>alert("hidden")</script><div>Второй блок<br>с переносом</div>
<noscript>no js here</noscript><ul><li>пункт 1</li><li>пункт 2</li></ul></body></html>`)
	out := ToText(raw)

	if strings.Contains(out, "<") || strings.Contains(out, ">") {
		t.Errorf("tags in text output: %q", out)
	}
	for _, junk := range []string{"var x", "alert", "no js here", "color:red"} {
		if strings.Contains(out, junk) {
			t.Errorf("script/style/noscript content leaked: %q", out)
		}
	}
	for _, want := range []string{"Заголовок", "Первый абзац с пробелами.", "Второй блок\nс переносом", "пункт 1\nпункт 2"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output: %q", want, out)
		}
	}
}

func TestChunkRuneAware(t *testing.T) {
	text := strings.Repeat("абвгд ", 100) // 600 runes, 1200 bytes
	chunk, total := Chunk(text, 0, 50)
	if total != 600 {
		t.Fatalf("total = %d, want 600 runes (not bytes)", total)
	}
	if got := utf8.RuneCountInString(chunk); got > 50 {
		t.Fatalf("chunk = %d runes, want <= 50", got)
	}
	if !utf8.ValidString(chunk) {
		t.Fatal("chunk is not valid UTF-8")
	}
}

func TestChunkSnapsToParagraphBoundary(t *testing.T) {
	para := strings.Repeat("Один два три четыре пять шесть семь восемь девять десять. ", 2) // ~116 runes
	text := para + "\n\n" + strings.Repeat("x", 200)
	chunk, _ := Chunk(text, 0, 120)
	// The blank line itself is consumed by the chunk that ends at the boundary.
	if strings.TrimSuffix(chunk, "\n\n") != para {
		t.Errorf("chunk should end at the paragraph boundary, got %q", chunk)
	}
}

// TestChunkPagingLossless walks the whole text via start_index=end semantics
// (the way mcp.go pages): concatenated chunks must reproduce the original.
func TestChunkPagingLossless(t *testing.T) {
	text := strings.Repeat("Первый абзац про проверку чанков.\n\nВторой абзац чуть длиннее предыдущего.\n\n", 25)
	start := 0
	var collected strings.Builder
	chunks := 0
	for start < utf8.RuneCountInString(text) {
		chunk, _ := Chunk(text, start, 80)
		if chunk == "" {
			t.Fatal("empty chunk, paging stuck")
		}
		if !utf8.ValidString(chunk) {
			t.Fatal("chunk is not valid UTF-8")
		}
		collected.WriteString(chunk)
		start += utf8.RuneCountInString(chunk)
		chunks++
		if chunks > 1000 {
			t.Fatal("too many chunks, paging loop broken")
		}
	}
	if collected.String() != text {
		t.Fatal("paged chunks lost or duplicated content")
	}
	if total := utf8.RuneCountInString(text); total == 0 {
		t.Fatal("fixture is empty")
	}
}

func TestChunkShortWindowKeepsHalf(t *testing.T) {
	// A single long paragraph without boundaries must not be squeezed below
	// half of maxLength by snapping attempts.
	text := strings.Repeat("слово ", 500) // no newlines at all
	chunk, _ := Chunk(text, 0, 100)
	if got := utf8.RuneCountInString(chunk); got != 100 {
		t.Errorf("chunk = %d runes, want 100 (no snapping without boundaries)", got)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
