package mcpserver

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"codekit.io.vn/rss-reader-mcp/internal/config"
	"codekit.io.vn/rss-reader-mcp/internal/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Input struct {
	URL                    string   `json:"url,omitempty"`
	Name                   string   `json:"name,omitempty"`
	Category               string   `json:"category,omitempty"`
	Enabled                *bool    `json:"enabled,omitempty"`
	FeedID                 *int64   `json:"feed_id,omitempty"`
	RefreshIntervalMinutes *int     `json:"refresh_interval_minutes,omitempty"`
	Force                  *bool    `json:"force,omitempty"`
	Limit                  *int     `json:"limit,omitempty"`
	Since                  string   `json:"since,omitempty"`
	Until                  string   `json:"until,omitempty"`
	IncludeContent         *bool    `json:"include_content,omitempty"`
	Query                  string   `json:"query,omitempty"`
	ArticleID              *int64   `json:"article_id,omitempty"`
	Hours                  *int     `json:"hours,omitempty"`
	Keywords               []string `json:"keywords,omitempty"`
	GroupBy                string   `json:"group_by,omitempty"`
}

func NewHandler(s *service.Service, c config.Config) http.Handler {
	server := mcp.NewServer(&mcp.Implementation{Name: "rss-reader", Version: "1.0.0"}, nil)
	add := func(name, description string, fn func(context.Context, Input) (any, error)) {
		mcp.AddTool[Input, map[string]any](server, &mcp.Tool{Name: name, Description: description, InputSchema: toolSchema(name)}, func(ctx context.Context, _ *mcp.CallToolRequest, in Input) (*mcp.CallToolResult, map[string]any, error) {
			v, e := fn(ctx, in)
			if e != nil {
				return nil, nil, publicError(e)
			}
			out, ok := v.(map[string]any)
			if !ok {
				out = map[string]any{"result": v}
			}
			return nil, out, nil
		})
	}
	required := func(v *int64, label string) (int64, error) {
		if v == nil || *v <= 0 {
			return 0, fmt.Errorf("%s is required and must be positive", label)
		}
		return *v, nil
	}
	add("rss_add_feed", "Validate, subscribe to, and store an RSS or Atom feed.", func(ctx context.Context, i Input) (any, error) {
		if strings.TrimSpace(i.URL) == "" {
			return nil, fmt.Errorf("url is required")
		}
		f, created, e := s.Add(ctx, i.URL, i.Name, i.Category, i.Enabled)
		return map[string]any{"feed": f, "created": created}, e
	})
	add("rss_list_feeds", "List registered sources, optionally filtered by category or enabled state.", func(ctx context.Context, i Input) (any, error) {
		limit := 100
		if i.Limit != nil {
			limit = *i.Limit
		}
		x, e := s.List(ctx, i.Category, i.Enabled, limit)
		return map[string]any{"feeds": x}, e
	})
	add("rss_remove_feed", "Remove one feed while preserving its stored article history.", func(ctx context.Context, i Input) (any, error) {
		id, e := required(i.FeedID, "feed_id")
		if e == nil {
			e = s.Remove(ctx, id)
		}
		return map[string]any{"removed": e == nil, "feed_id": id}, e
	})
	add("rss_update_feed", "Update explicitly supplied feed name, category, enabled state, or refresh interval.", func(ctx context.Context, i Input) (any, error) {
		id, e := required(i.FeedID, "feed_id")
		if e != nil {
			return nil, e
		}
		f, e := s.Update(ctx, id, optionalString(i.Name), optionalString(i.Category), i.Enabled, i.RefreshIntervalMinutes)
		return map[string]any{"feed": f}, e
	})
	add("rss_fetch_feed", "Fetch one feed now and report inserted, updated, and duplicate article counts.", func(ctx context.Context, i Input) (any, error) {
		id, e := required(i.FeedID, "feed_id")
		if e != nil {
			return nil, e
		}
		r, e := s.FetchFeed(ctx, id, i.Force != nil && *i.Force)
		return r, e
	})
	add("rss_fetch_all", "Refresh all enabled feeds with bounded concurrency; individual feed failures are included in results.", func(ctx context.Context, i Input) (any, error) {
		r, e := s.FetchAll(ctx, i.Force != nil && *i.Force, i.Category)
		sum := map[string]int{"new_articles": 0, "updated_articles": 0, "duplicate_entries": 0, "failed_feeds": 0}
		for _, x := range r {
			sum["new_articles"] += x.NewArticles
			sum["updated_articles"] += x.UpdatedArticles
			sum["duplicate_entries"] += x.DuplicateEntries
			if x.Status == "failed" {
				sum["failed_feeds"]++
			}
		}
		return map[string]any{"results": r, "totals": sum}, e
	})
	add("rss_latest", "Get newest stored articles with source links and optional feed/category/time filters.", func(ctx context.Context, i Input) (any, error) {
		limit := 20
		if i.Limit != nil {
			limit = *i.Limit
		}
		since, e := parseOptionalTime(i.Since)
		if e != nil {
			return nil, e
		}
		until, e := parseOptionalTime(i.Until)
		if e != nil {
			return nil, e
		}
		a, e := s.Latest(ctx, limit, i.Category, i.FeedID, since, until, i.IncludeContent != nil && *i.IncludeContent)
		return map[string]any{"articles": a}, e
	})
	add("rss_search", "Search titles, feed descriptions, and content using SQLite full-text search.", func(ctx context.Context, i Input) (any, error) {
		limit := 20
		if i.Limit != nil {
			limit = *i.Limit
		}
		since, e := parseOptionalTime(i.Since)
		if e != nil {
			return nil, e
		}
		a, e := s.Search(ctx, i.Query, limit, i.Category, i.FeedID, since)
		return map[string]any{"articles": a}, e
	})
	add("rss_get_article", "Retrieve an article and only the content supplied by its feed; never scrapes its website.", func(ctx context.Context, i Input) (any, error) {
		id, e := required(i.ArticleID, "article_id")
		if e != nil {
			return nil, e
		}
		a, e := s.Article(ctx, id)
		return map[string]any{"article": a}, e
	})
	add("rss_digest", "Prepare compact source-linked news data for an agent to summarize; this tool does not generate summaries.", func(ctx context.Context, i Input) (any, error) {
		hours := 24
		if i.Hours != nil {
			hours = *i.Hours
		}
		group := i.GroupBy
		if group == "" {
			group = "none"
		}
		if group != "none" && group != "source" && group != "category" && group != "date" {
			return nil, fmt.Errorf("group_by must be source, category, date, or none")
		}
		limit := 20
		if i.Limit != nil {
			limit = *i.Limit
		}
		return s.Digest(ctx, hours, limit, i.Category, i.Keywords, group)
	})
	add("rss_stats", "Return feed, article, refresh, and SQLite database statistics.", func(ctx context.Context, _ Input) (any, error) {
		x, e := s.Stats(ctx)
		return map[string]any{"stats": x}, e
	})
	add("rss_health", "Report application health, database readiness, uptime, and version.", func(ctx context.Context, _ Input) (any, error) { return s.Health(ctx), nil })
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c.AuthToken != "" {
			got := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
			if len(got) == len(c.AuthToken) && subtle.ConstantTimeCompare([]byte(got), []byte(c.AuthToken)) == 1 {
				goto origin
			}
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	origin:
		if origin := r.Header.Get("Origin"); origin != "" {
			u, e := url.Parse(origin)
			if e != nil || !allowedOrigin(u.Hostname(), c.AllowedHosts) {
				http.Error(w, "origin not allowed", http.StatusForbidden)
				return
			}
		}
		r.Body = http.MaxBytesReader(w, r.Body, c.MaxRequestBytes)
		handler.ServeHTTP(w, r)
	})
}

func publicError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("requested record was not found")
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{"sql logic error", "sqlite", "database is locked", "constraint failed", "foreign key constraint"} {
		if strings.Contains(message, marker) {
			return errors.New("database operation failed; check server logs")
		}
	}
	return err
}

func toolSchema(name string) map[string]any {
	props := map[string]any{
		"url": map[string]any{"type": "string"}, "name": map[string]any{"type": "string"}, "category": map[string]any{"type": "string"},
		"enabled": map[string]any{"type": "boolean"}, "feed_id": map[string]any{"type": "integer", "minimum": 1},
		"refresh_interval_minutes": map[string]any{"type": "integer", "minimum": 1, "maximum": 1440}, "force": map[string]any{"type": "boolean"},
		"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 100}, "since": map[string]any{"type": "string", "format": "date-time"},
		"until": map[string]any{"type": "string", "format": "date-time"}, "include_content": map[string]any{"type": "boolean"},
		"query": map[string]any{"type": "string"}, "article_id": map[string]any{"type": "integer", "minimum": 1},
		"hours": map[string]any{"type": "integer", "minimum": 1, "maximum": 8760}, "keywords": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"group_by": map[string]any{"type": "string", "enum": []string{"source", "category", "date", "none"}},
	}
	required := map[string][]string{"rss_add_feed": {"url"}, "rss_remove_feed": {"feed_id"}, "rss_update_feed": {"feed_id"}, "rss_fetch_feed": {"feed_id"}, "rss_search": {"query"}, "rss_get_article": {"article_id"}}
	fields := map[string][]string{
		"rss_add_feed": {"url", "name", "category", "enabled"}, "rss_list_feeds": {"category", "enabled", "limit"},
		"rss_remove_feed": {"feed_id"}, "rss_update_feed": {"feed_id", "name", "category", "enabled", "refresh_interval_minutes"},
		"rss_fetch_feed": {"feed_id", "force"}, "rss_fetch_all": {"force", "category"},
		"rss_latest": {"limit", "category", "feed_id", "since", "until", "include_content"},
		"rss_search": {"query", "limit", "category", "feed_id", "since"}, "rss_get_article": {"article_id"},
		"rss_digest": {"hours", "limit", "category", "keywords", "group_by"},
	}
	toolProps := make(map[string]any)
	for _, field := range fields[name] {
		toolProps[field] = props[field]
	}
	schema := map[string]any{"type": "object", "properties": toolProps, "additionalProperties": false}
	if len(required[name]) > 0 {
		schema["required"] = required[name]
	}
	return schema
}
func optionalString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
func parseOptionalTime(s string) (*time.Time, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	t, e := time.Parse(time.RFC3339, s)
	if e != nil {
		return nil, fmt.Errorf("timestamp must be RFC3339")
	}
	return &t, nil
}
func allowedOrigin(host string, allowed []string) bool {
	if len(allowed) == 0 {
		return false
	}
	host = strings.ToLower(host)
	for _, a := range allowed {
		if host == a {
			return true
		}
	}
	return false
}
