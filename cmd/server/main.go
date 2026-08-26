package main

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/amir/web-fetch-server/internal/config"
	webmcp "github.com/amir/web-fetch-server/internal/mcp"
)

func main() {
	cfg := config.Load()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(logger)

	server := webmcp.BuildWithLogger(cfg, logger)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return server
	}, nil)

	mux := http.NewServeMux()
	mux.Handle(cfg.MCPPath, requestLogger(logger, authMiddleware(cfg.APIKey, handler)))

	addr := ":" + cfg.Port
	logger.Info("web-fetch-server listening",
		"addr", addr+cfg.MCPPath,
		"searxng_url", cfg.SearxngURL,
	)
	if cfg.APIKey == "" {
		logger.Warn("API_KEY is not set — MCP endpoint is unprotected")
	}
	if err := http.ListenAndServe(addr, mux); err != nil {
		logger.Error("server failed", "error", err.Error())
		os.Exit(1)
	}
}

// authMiddleware enforces a Bearer token on the MCP endpoint when API_KEY is set.
func authMiddleware(apiKey string, next http.Handler) http.Handler {
	if apiKey == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer "+apiKey {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
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
