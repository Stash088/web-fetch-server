package content

import "testing"

func TestChunkBasic(t *testing.T) {
	s := "0123456789"
	chunk, total := Chunk(s, 0, 5)
	if chunk != "01234" || total != 10 {
		t.Fatalf("got %q total=%d", chunk, total)
	}
}

func TestChunkMid(t *testing.T) {
	s := "0123456789"
	chunk, total := Chunk(s, 5, 3)
	if chunk != "567" || total != 10 {
		t.Fatalf("got %q total=%d", chunk, total)
	}
}

func TestChunkOverrun(t *testing.T) {
	s := "abc"
	chunk, _ := Chunk(s, 2, 100)
	if chunk != "c" {
		t.Fatalf("got %q", chunk)
	}
}

func TestChunkStartBeyondEnd(t *testing.T) {
	s := "abc"
	chunk, total := Chunk(s, 10, 5)
	if chunk != "" || total != 3 {
		t.Fatalf("got %q total=%d", chunk, total)
	}
}

func TestToMarkdown(t *testing.T) {
	raw := []byte("<html><head><title>T</title></head><body><h1>Hello</h1><p>World <b>bold</b></p></body></html>")
	out := ToMarkdown(raw)
	if out == "" {
		t.Fatal("expected non-empty markdown")
	}
}
