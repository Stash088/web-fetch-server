package config

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Port          string
	MCPPath       string
	APIKeys       []string // Bearer tokens accepted for MCP access; empty = open
	SearxngURL    string
	SearxngKey    string // optional SearXNG API key
	// SearchCategories is the comma-separated SearXNG categories list sent
	// with every web_search (SEARCH_CATEGORIES, default "general,it").
	SearchCategories string
	MaxFetchBytes    int64
	// PDFMaxFetchBytes bounds body size for PDF responses (PDF_MAX_FETCH_BYTES);
	// PDFs are parsed to text client-side so they get a higher cap.
	PDFMaxFetchBytes int64
	FetchTimeout     time.Duration
	// PDFFetchTimeout bounds fetches of PDF documents (PDF_FETCH_TIMEOUT);
	// only used when larger than FetchTimeout.
	PDFFetchTimeout time.Duration
	UserAgent       string
	DefaultMaxLen   int
	MaxResults      int
	LogLevel        slog.Level
	// TLSFingerprint controls the TLS ClientHello fingerprint sent by the
	// fetch client: "chrome" (uTLS, mimics Chrome) or "off" (stdlib TLS).
	TLSFingerprint string
	// JSRenderMode controls JS rendering via a headless browser:
	// "never" (default), "auto" (fallback on block) or "always".
	JSRenderMode string
	// JSRenderTimeout bounds a single browser render.
	JSRenderTimeout time.Duration
	// ChromeBin overrides the Chrome/Chromium binary path (empty = auto-detect).
	ChromeBin string
	// RenderProfileDir is the base directory for persistent render browser
	// profiles (cookie/session reuse between renders). Empty = tmpdir default.
	RenderProfileDir string
	// RenderPoolSize is the number of pooled render browser processes.
	RenderPoolSize int
	// Cache TTLs for the MCP tools. 0 disables the corresponding cache.
	FetchCacheTTL  time.Duration // direct HTTP fetches
	RenderCacheTTL time.Duration // headless-browser renders (shorter: cookies drift)
	SearchCacheTTL time.Duration // SearXNG search results
	// RerankMode controls web_search result reranking: "rrf" (BM25 + engine
	// consensus + RRF fusion, default), "semantic" (adds a cross-encoder
	// rerank-API vote, requires RerankAPIKey) or "none" (SearXNG order
	// passthrough).
	RerankMode string
	// External rerank API (Cohere-compatible, default RouterAI). Used when
	// RerankMode == "semantic".
	RerankAPIURL  string
	RerankAPIKey  string
	RerankModel   string
	RerankTimeout time.Duration
	// BlockPrivateNetworks enables SSRF protection: rejects private, loopback
	// and other unsafe address ranges in web_fetch targets.
	BlockPrivateNetworks bool
}

func Load() Config {
	logLevel := slog.LevelInfo
	switch os.Getenv("LOG_LEVEL") {
	case "debug", "DEBUG":
		logLevel = slog.LevelDebug
	case "warn", "WARN":
		logLevel = slog.LevelWarn
	case "error", "ERROR":
		logLevel = slog.LevelError
	}
	return Config{
		Port:                 getEnv("PORT", "8080"),
		MCPPath:              getEnv("MCP_PATH", "/mcp"),
		APIKeys:              loadAPIKeys(),
		SearxngURL:           getEnv("SEARXNG_URL", "http://localhost:8888"),
		SearxngKey:           os.Getenv("SEARXNG_KEY"),
		SearchCategories:     getEnv("SEARCH_CATEGORIES", "general,it"),
		MaxFetchBytes:        getEnvInt64("MAX_FETCH_BYTES", 2<<20),
		PDFMaxFetchBytes:     getEnvInt64("PDF_MAX_FETCH_BYTES", 8<<20),
		FetchTimeout:         getEnvDur("FETCH_TIMEOUT", 20*time.Second),
		PDFFetchTimeout:      getEnvDur("PDF_FETCH_TIMEOUT", 60*time.Second),
		UserAgent:            getEnv("USER_AGENT", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"),
		DefaultMaxLen:        getEnvInt("DEFAULT_MAX_LEN", 8000),
		MaxResults:           getEnvInt("MAX_RESULTS", 10),
		LogLevel:             logLevel,
		TLSFingerprint:       getEnv("TLS_FINGERPRINT", "chrome"),
		JSRenderMode:         getEnv("JS_RENDER", "never"),
		JSRenderTimeout:      getEnvDur("JS_RENDER_TIMEOUT", 30*time.Second),
		ChromeBin:            os.Getenv("CHROME_BIN"),
		RenderProfileDir:     os.Getenv("RENDER_PROFILE_DIR"),
		RenderPoolSize:       getEnvInt("RENDER_POOL_SIZE", 1),
		FetchCacheTTL:        getEnvDur("FETCH_CACHE_TTL", 20*time.Minute),
		RenderCacheTTL:       getEnvDur("RENDER_CACHE_TTL", 5*time.Minute),
		SearchCacheTTL:       getEnvDur("SEARCH_CACHE_TTL", 10*time.Minute),
		RerankMode:           getRerankMode(),
		RerankAPIURL:         getEnv("RERANK_API_URL", "https://routerai.ru/api/v1"),
		RerankAPIKey:         os.Getenv("RERANK_API_KEY"),
		RerankModel:          getEnv("RERANK_MODEL", "voyageai/rerank-2.5-lite"),
		RerankTimeout:        getEnvDur("RERANK_TIMEOUT", 3*time.Second),
		BlockPrivateNetworks: getEnvBool("BLOCK_PRIVATE_NETWORKS", true),
	}
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// loadAPIKeys reads the accepted Bearer tokens for MCP access. API_KEYS takes
// precedence (comma-separated list); API_KEY is kept as a legacy alias for a
// single key when API_KEYS is unset.
func loadAPIKeys() []string {
	if raw := os.Getenv("API_KEYS"); raw != "" {
		if keys := parseKeys(raw); len(keys) > 0 {
			return keys
		}
	}
	if k := os.Getenv("API_KEY"); k != "" {
		return []string{k}
	}
	return nil
}

func parseKeys(raw string) []string {
	seen := map[string]struct{}{}
	var keys []string
	for _, part := range strings.Split(raw, ",") {
		k := strings.TrimSpace(part)
		if k == "" {
			continue
		}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		keys = append(keys, k)
	}
	return keys
}

// getRerankMode reads RERANK: "none" disables reranking, "semantic" enables
// the external rerank-API vote, anything else (including unset or invalid
// values) falls back to the default "rrf".
func getRerankMode() string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("RERANK"))) {
	case "none":
		return "none"
	case "semantic":
		return "semantic"
	default:
		return "rrf"
	}
}

func getEnvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func getEnvInt64(key string, def int64) int64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}

func getEnvDur(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func getEnvBool(key string, def bool) bool {
	switch strings.ToLower(os.Getenv(key)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}
