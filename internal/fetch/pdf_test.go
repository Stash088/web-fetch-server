package fetch

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// buildTestPDF assembles a minimal valid one-page PDF with correct xref
// offsets, containing the given text run.
func buildTestPDF(text string) []byte {
	stream := fmt.Sprintf("BT /F1 24 Tf 72 720 Td (%s) Tj ET", text)
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}

	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objs)+1)
	for i, o := range objs {
		offsets[i+1] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xrefStart := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n", len(objs)+1)
	buf.WriteString("0000000000 65535 f \n")
	for i := 1; i <= len(objs); i++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xrefStart)
	return buf.Bytes()
}

func TestLooksLikePDF(t *testing.T) {
	tests := []struct {
		raw  string
		want bool
	}{
		{"https://arxiv.org/pdf/1706.03762", true},
		{"https://example.com/paper.pdf", true},
		{"https://example.com/reports/Q3.PDF", true},
		{"https://example.com/pdfs/list", false},
		{"https://example.com/article?file=a.pdf", false}, // query, not path
		{"https://example.com/article", false},
	}
	for _, tt := range tests {
		u, err := url.Parse(tt.raw)
		if err != nil {
			t.Fatalf("parse %s: %v", tt.raw, err)
		}
		if got := looksLikePDF(u); got != tt.want {
			t.Errorf("looksLikePDF(%s) = %v, want %v", tt.raw, got, tt.want)
		}
	}
	if looksLikePDF(nil) {
		t.Error("looksLikePDF(nil) = true, want false")
	}
}

func TestFetchPDFContentParsed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.Write(buildTestPDF("Hello PDF world"))
	}))
	defer srv.Close()

	c := NewClientWithOptions(Options{Timeout: 5 * time.Second, MaxBody: 1 << 20, UserAgent: "test-agent/1.0", TLSFingerprint: "off"})
	page, err := c.Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("fetch pdf: %v", err)
	}
	if !strings.Contains(string(page.Body), "Hello PDF world") {
		t.Errorf("body does not contain extracted text: %q", string(page.Body))
	}
}

func TestPDFRetryAfterBodyDeadline(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.Header().Set("Content-Type", "application/pdf")
		flusher := w.(http.Flusher)
		// Drip a ~1.5MB PDF slowly: way past the plain timeout, well inside
		// the PDF budget.
		chunk := bytes.Repeat([]byte("A"), 64<<10)
		w.Write([]byte("%PDF-1.4\n"))
		flusher.Flush()
		for i := 0; i < 24; i++ {
			time.Sleep(60 * time.Millisecond)
			w.Write(chunk)
		}
	}))
	defer srv.Close()

	c := NewClientWithOptions(Options{
		Timeout: 150 * time.Millisecond, PDFTimeout: 4 * time.Second,
		MaxBody: 4 << 20, UserAgent: "test-agent/1.0", TLSFingerprint: "off",
	})
	page, err := c.Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("expected pdf retry to succeed: %v", err)
	}
	if attempts < 2 {
		t.Errorf("attempts = %d, want retry after the plain timeout fired", attempts)
	}
	if len(page.Body) == 0 {
		t.Error("expected non-empty body")
	}
}

func TestPDFURLHintAvoidsFirstTimeout(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.Header().Set("Content-Type", "application/pdf")
		flusher := w.(http.Flusher)
		chunk := bytes.Repeat([]byte("A"), 64<<10)
		w.Write([]byte("%PDF-1.4\n"))
		flusher.Flush()
		for i := 0; i < 20; i++ {
			time.Sleep(60 * time.Millisecond)
			w.Write(chunk)
		}
	}))
	defer srv.Close()

	c := NewClientWithOptions(Options{
		Timeout: 200 * time.Millisecond, PDFTimeout: 4 * time.Second,
		MaxBody: 4 << 20, UserAgent: "test-agent/1.0", TLSFingerprint: "off",
	})
	if _, err := c.Fetch(context.Background(), srv.URL+"/doc/paper.pdf"); err != nil {
		t.Fatalf("expected /pdf/ URL hint to use the pdf timeout: %v", err)
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1 (no wasted attempt on the plain timeout)", attempts)
	}
}

func TestPlainTimeoutStillAppliesToNonPDF(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		flusher := w.(http.Flusher)
		w.Write([]byte("<html><body>"))
		flusher.Flush()
		for i := 0; i < 20; i++ {
			time.Sleep(60 * time.Millisecond)
			w.Write([]byte("<p>slow page</p>"))
		}
	}))
	defer srv.Close()

	c := NewClientWithOptions(Options{
		Timeout: 200 * time.Millisecond, PDFTimeout: 4 * time.Second,
		MaxBody: 1 << 20, UserAgent: "test-agent/1.0", TLSFingerprint: "off",
	})
	if _, err := c.Fetch(context.Background(), srv.URL); err == nil {
		t.Fatal("expected plain-timeout fetch of a slow HTML page to fail")
	}
}
