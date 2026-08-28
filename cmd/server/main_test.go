package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestAuthMiddlewareMultipleKeys(t *testing.T) {
	keys := []string{"key-one", "key-two", "key-three"}
	var gotKeyID string
	h := authMiddleware(keys, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKeyID, _ = r.Context().Value(userKey).(string)
		w.WriteHeader(http.StatusOK)
	}))

	for _, k := range keys {
		gotKeyID = ""
		req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
		req.Header.Set("Authorization", "Bearer "+k)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("Bearer %q: got status %d, want %d", k, rec.Code, http.StatusOK)
		}
		if gotKeyID == "" {
			t.Errorf("Bearer %q: expected key_id in context, got %q", k, gotKeyID)
		}
		if want := keyFingerprint(k); gotKeyID != want {
			t.Errorf("Bearer %q: key_id = %q, want %q", k, gotKeyID, want)
		}
	}
}

func TestAuthMiddlewareRejects(t *testing.T) {
	keys := []string{"valid-key"}
	h := authMiddleware(keys, testHandler())

	tests := []struct {
		name       string
		authHeader string
	}{
		{name: "wrong key", authHeader: "Bearer wrong-key"},
		{name: "missing header", authHeader: ""},
		{name: "missing Bearer prefix", authHeader: "valid-key"},
		{name: "lowercase scheme", authHeader: "bearer valid-key"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("got status %d, want %d", rec.Code, http.StatusUnauthorized)
			}
			if www := rec.Header().Get("WWW-Authenticate"); www != "Bearer" {
				t.Errorf("expected WWW-Authenticate: Bearer, got %q", www)
			}
		})
	}
}

func TestAuthMiddlewareDisabled(t *testing.T) {
	h := authMiddleware(nil, testHandler())

	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("no keys configured: got status %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestKeyFingerprint(t *testing.T) {
	a := keyFingerprint("super-secret")
	b := keyFingerprint("super-secret")
	c := keyFingerprint("other-secret")

	if a != b {
		t.Error("fingerprint must be deterministic for the same key")
	}
	if a == c {
		t.Error("different keys must produce different fingerprints")
	}
	if len(a) != 12 {
		t.Errorf("expected 12-hex fingerprint, got %q (%d)", a, len(a))
	}
	if strings.Contains(a, "secret") {
		t.Error("fingerprint must not leak the key material")
	}
	if keyFingerprint("") != "" {
		t.Error("empty token should produce empty fingerprint")
	}
}

func TestMatchesKey(t *testing.T) {
	keys := []string{"alpha", "beta"}
	if !matchesKey(keys, "alpha") || !matchesKey(keys, "beta") {
		t.Error("expected configured keys to match")
	}
	if matchesKey(keys, "gamma") || matchesKey(keys, "alph") {
		t.Error("expected non-configured keys not to match")
	}
}
