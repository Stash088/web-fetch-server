package config

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Port     string
	MCPPath  string
	APIKeys  []string // Bearer tokens accepted for MCP access; empty = open
	SearxngURL    string
	SearxngKey    string // optional SearXNG API key
	MaxFetchBytes int64
	FetchTimeout  time.Duration
	UserAgent     string
	DefaultMaxLen int
	MaxResults    int
	LogLevel      slog.Level
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
		Port:          getEnv("PORT", "8080"),
		MCPPath:       getEnv("MCP_PATH", "/mcp"),
		APIKeys:       loadAPIKeys(),
		SearxngURL:    getEnv("SEARXNG_URL", "http://localhost:8888"),
		SearxngKey:    os.Getenv("SEARXNG_KEY"),
		MaxFetchBytes: getEnvInt64("MAX_FETCH_BYTES", 2<<20),
		FetchTimeout:  getEnvDur("FETCH_TIMEOUT", 20*time.Second),
		UserAgent:     getEnv("USER_AGENT", "web-fetch-server/0.1 (+MCP; search&fetch provider)"),
		DefaultMaxLen: getEnvInt("DEFAULT_MAX_LEN", 8000),
		MaxResults:    getEnvInt("MAX_RESULTS", 10),
		LogLevel:      logLevel,
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
