package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"codekit.io.vn/rss-reader-mcp/internal/config"
	"codekit.io.vn/rss-reader-mcp/internal/fetcher"
	"codekit.io.vn/rss-reader-mcp/internal/mcpserver"
	"codekit.io.vn/rss-reader-mcp/internal/repository"
	"codekit.io.vn/rss-reader-mcp/internal/service"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	c, err := config.Load()
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: c.LogLevel}))
	slog.SetDefault(logger)
	root, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	repo, err := repository.Open(root, c.DBPath, "/app/migrations")
	if err != nil && os.IsNotExist(err) {
		repo, err = repository.Open(root, c.DBPath, "migrations")
	}
	if err != nil {
		return err
	}
	defer repo.Close()
	f := fetcher.New(c.FetchTimeout, c.MaxFeedBytes, c.MaxContentBytes, c.UserAgent, c.URLAllowlist)
	svc := service.New(repo, f, c.MaxFeeds, c.MaxArticlesPerResponse, c.MaxConcurrentFetches, c.RetentionDays, c.RetryAttempts)
	mux := http.NewServeMux()
	mux.Handle(c.MCPPath, mcpserver.NewHandler(svc, c))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		if err := repo.Ready(ctx); err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready\n"))
	})
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		stats, err := svc.Stats(r.Context())
		if err != nil {
			http.Error(w, "metrics unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = fmt.Fprintf(w, "rss_feeds_registered %d\nrss_feeds_enabled %d\nrss_articles_stored %d\nrss_feeds_failed %d\nrss_database_bytes %d\nrss_fetches_total %d\nrss_not_modified_total %d\n", stats.RegisteredFeeds, stats.EnabledFeeds, stats.TotalArticles, stats.FailedFeeds, stats.DatabaseBytes, stats.Refreshes, stats.NotModified)
	})
	address := net.JoinHostPort(c.Host, c.Port)
	server := &http.Server{Addr: address, Handler: limitRequests(mux, 32), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: c.HTTPTimeout, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	errCh := make(chan error, 1)
	go func() {
		slog.Info("RSS MCP server listening", "address", address, "mcp_path", c.MCPPath, "version", version)
		errCh <- server.ListenAndServe()
	}()
	if c.RefreshEnabled {
		go svc.RefreshLoop(root, c.RefreshInterval)
	}
	select {
	case <-root.Done():
		ctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		return server.Shutdown(ctx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("HTTP server: %w", err)
	}
}
func limitRequests(next http.Handler, max int) http.Handler {
	sem := make(chan struct{}, max)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case sem <- struct{}{}:
			defer func() { <-sem }()
			next.ServeHTTP(w, r)
		case <-r.Context().Done():
			http.Error(w, "request canceled", 499)
		}
	})
}
