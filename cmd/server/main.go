package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/amir/web-fetch-server/internal/config"
	webmcp "github.com/amir/web-fetch-server/internal/mcp"
)

type ctxKey string

const userKey ctxKey = "mcp.key_id"

func main() {
	cfg := config.Load()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(logger)

	server := webmcp.BuildWithLogger(cfg, logger)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return server
	}, nil)

	mux := http.NewServeMux()
	mux.Handle(cfg.MCPPath, authMiddleware(cfg.APIKeys, requestLogger(logger, handler)))

	addr := ":" + cfg.Port
	logger.Info("web-fetch-server listening",
		"addr", addr+cfg.MCPPath,
		"searxng_url", cfg.SearxngURL,
		"api_keys", len(cfg.APIKeys),
	)
	if len(cfg.APIKeys) == 0 {
		logger.Warn("no API keys configured (API_KEYS / API_KEY) — MCP endpoint is unprotected")
	}
	if err := http.ListenAndServe(addr, mux); err != nil {
		logger.Error("server failed", "error", err.Error())
		os.Exit(1)
	}
}

// authMiddleware enforces a Bearer token from the accepted API keys on the MCP
// endpoint. When no keys are configured the endpoint is left open. The matched
// key's fingerprint is stored in the request context for logging.
func authMiddleware(keys []string, next http.Handler) http.Handler {
	if len(keys) == 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if token, ok := strings.CutPrefix(auth, "Bearer "); ok && matchesKey(keys, token) {
			r = r.WithContext(context.WithValue(r.Context(), userKey, keyFingerprint(token)))
			next.ServeHTTP(w, r)
			return
		}
		slog.Warn("[reject] http",
			"method", r.Method,
			"path", r.URL.Path,
			"remote", r.RemoteAddr,
			"key_id", keyFingerprint(auth),
		)
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
}

// matchesKey reports whether token equals any accepted key, using constant-time
// comparison per candidate to avoid timing side channels.
func matchesKey(keys []string, token string) bool {
	for _, k := range keys {
		if subtle.ConstantTimeCompare([]byte(k), []byte(token)) == 1 {
			return true
		}
	}
	return false
}

// keyFingerprint returns a short, non-reversible identifier for a token so that
// full API keys never appear in logs.
func keyFingerprint(token string) string {
	if token == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:6])
}

// requestLogger logs incoming MCP HTTP requests ([request]) and their outcome
// ([response]), including the JSON-RPC method and params body.
func requestLogger(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body []byte
		if r.Body != nil {
			body, _ = io.ReadAll(io.LimitReader(r.Body, 8192))
			r.Body = io.NopCloser(bytes.NewReader(body))
		}

		start := time.Now()
		rw := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(rw, r)

		logger.Info("[request] http",
			"method", r.Method,
			"path", r.URL.Path,
			"remote", r.RemoteAddr,
			"key_id", r.Context().Value(userKey),
			"status", rw.status,
			"latency_ms", time.Since(start).Milliseconds(),
			"body", string(body),
		)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}
