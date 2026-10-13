package config

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Host                   string
	Port                   string
	MCPPath                string
	DBPath                 string
	RefreshEnabled         bool
	RefreshInterval        time.Duration
	FetchTimeout           time.Duration
	MaxFeedBytes           int64
	MaxContentBytes        int
	MaxConcurrentFetches   int
	MaxFeeds               int
	RetentionDays          int
	LogLevel               slog.Level
	UserAgent              string
	AuthToken              string
	URLAllowlist           []string
	AllowedHosts           []string
	MaxArticlesPerResponse int
	MaxRequestBytes        int64
	HTTPTimeout            time.Duration
	RetryAttempts          int
}

func Load() (Config, error) {
	c := Config{
		Host:                   env("RSS_MCP_HOST", "127.0.0.1"),
		Port:                   env("RSS_MCP_PORT", "8080"),
		MCPPath:                env("RSS_MCP_PATH", "/mcp"),
		DBPath:                 env("RSS_DB_PATH", "/data/rss.db"),
		RefreshEnabled:         envBool("RSS_REFRESH_ENABLED", true),
		RefreshInterval:        envDuration("RSS_REFRESH_INTERVAL", 30*time.Minute),
		FetchTimeout:           envDuration("RSS_FETCH_TIMEOUT", 10*time.Second),
		MaxFeedBytes:           envInt64("RSS_MAX_FEED_BYTES", 2<<20),
		MaxContentBytes:        envInt("RSS_MAX_CONTENT_BYTES", 65536),
		MaxConcurrentFetches:   envInt("RSS_MAX_CONCURRENT_FETCHES", 3),
		MaxFeeds:               envInt("RSS_MAX_FEEDS", 200),
		RetentionDays:          envInt("RSS_ARTICLE_RETENTION_DAYS", 90),
		UserAgent:              env("RSS_HTTP_USER_AGENT", "RSSReaderMCP/1.0"),
		AuthToken:              strings.TrimSpace(os.Getenv("RSS_MCP_AUTH_TOKEN")),
		URLAllowlist:           splitList(os.Getenv("RSS_URL_ALLOWLIST")),
		AllowedHosts:           splitList(os.Getenv("RSS_MCP_ALLOWED_HOSTS")),
		MaxArticlesPerResponse: envInt("RSS_MAX_ARTICLES_PER_RESPONSE", 100),
		MaxRequestBytes:        envInt64("RSS_MCP_MAX_REQUEST_BYTES", 1<<20),
		HTTPTimeout:            envDuration("RSS_MCP_HTTP_TIMEOUT", 2*time.Minute),
		RetryAttempts:          envInt("RSS_FETCH_RETRY_ATTEMPTS", 3),
	}
	if _, err := net.LookupPort("tcp", c.Port); err != nil {
		return Config{}, fmt.Errorf("invalid RSS_MCP_PORT: %w", err)
	}
	if c.MCPPath == "" || c.MCPPath[0] != '/' {
		return Config{}, fmt.Errorf("RSS_MCP_PATH must start with /")
	}
	if c.DBPath == "" || c.Host == "" || c.UserAgent == "" {
		return Config{}, fmt.Errorf("host, database path, and user agent are required")
	}
	if c.RefreshInterval <= 0 || c.FetchTimeout <= 0 || c.HTTPTimeout <= 0 {
		return Config{}, fmt.Errorf("refresh and timeout durations must be positive")
	}
	if c.MaxFeedBytes < 1024 || c.MaxContentBytes < 0 || c.MaxConcurrentFetches < 1 ||
		c.MaxConcurrentFetches > 32 || c.MaxFeeds < 1 || c.RetentionDays < 1 ||
		c.MaxArticlesPerResponse < 1 || c.MaxArticlesPerResponse > 1000 ||
		c.MaxRequestBytes < 1024 || c.RetryAttempts < 1 || c.RetryAttempts > 5 {
		return Config{}, fmt.Errorf("one or more RSS limits are outside supported bounds")
	}
	switch strings.ToLower(env("RSS_LOG_LEVEL", "info")) {
	case "debug":
		c.LogLevel = slog.LevelDebug
	case "info", "":
		c.LogLevel = slog.LevelInfo
	case "warn", "warning":
		c.LogLevel = slog.LevelWarn
	case "error":
		c.LogLevel = slog.LevelError
	default:
		return Config{}, fmt.Errorf("invalid RSS_LOG_LEVEL")
	}
	return c, nil
}

func env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return strings.TrimSpace(v)
	}
	return fallback
}
func envBool(key string, fallback bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return fallback
	}
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	if err != nil {
		return fallback
	}
	return b
}
func envInt(key string, fallback int) int {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return fallback
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return fallback
	}
	return n
}
func envInt64(key string, fallback int64) int64 {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return fallback
	}
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil {
		return fallback
	}
	return n
}
func envDuration(key string, fallback time.Duration) time.Duration {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return fallback
	}
	d, err := time.ParseDuration(strings.TrimSpace(v))
	if err != nil {
		return fallback
	}
	return d
}
func splitList(v string) []string {
	var out []string
	for _, item := range strings.Split(v, ",") {
		if s := strings.ToLower(strings.TrimSpace(item)); s != "" {
			out = append(out, s)
		}
	}
	return out
}
