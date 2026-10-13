package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/url"
	"strings"
	"sync"
	"time"

	"codekit.io.vn/rss-reader-mcp/internal/fetcher"
	"codekit.io.vn/rss-reader-mcp/internal/model"
	"codekit.io.vn/rss-reader-mcp/internal/repository"
)

type Service struct {
	Repo                                                             *repository.Repository
	Fetcher                                                          *fetcher.Fetcher
	MaxFeeds, MaxArticles, Concurrency, RetentionDays, RetryAttempts int
	Version                                                          string
	refreshMu                                                        sync.Mutex
	started                                                          time.Time
	schedulerMu                                                      sync.RWMutex
	lastScheduler                                                    *time.Time
}
type FetchResult struct {
	FeedID           int64  `json:"feed_id"`
	NewArticles      int    `json:"new_articles"`
	UpdatedArticles  int    `json:"updated_articles"`
	DuplicateEntries int    `json:"duplicate_entries"`
	DurationMS       int64  `json:"duration_ms"`
	Status           string `json:"status"`
	Error            string `json:"error,omitempty"`
}

func New(repo *repository.Repository, f *fetcher.Fetcher, maxFeeds, maxArticles, concurrency, retention, retries int) *Service {
	return &Service{Repo: repo, Fetcher: f, MaxFeeds: maxFeeds, MaxArticles: maxArticles, Concurrency: concurrency, RetentionDays: retention, RetryAttempts: retries, Version: "1.0.0", started: time.Now()}
}
func (s *Service) AddFeed(ctx context.Context, raw, name, category string, enabled *bool) (model.Feed, bool, error) {
	u, e := url.Parse(strings.TrimSpace(raw))
	if e != nil || u.Host == "" {
		return model.Feed{}, false, fmt.Errorf("invalid feed URL")
	}
	res, e := s.Fetcher.Fetch(ctx, model.Feed{URL: u.String()})
	if e != nil {
		return model.Feed{}, false, e
	}
	title := strings.TrimSpace(name)
	if title == "" {
		title = res.Feed.Title
	}
	if title == "" {
		title = u.Host
	}
	en := true
	if enabled != nil {
		en = *enabled
	}
	f, created, e := s.Repo.CreateFeed(ctx, model.Feed{Name: title, URL: u.String(), SiteURL: res.Feed.SiteURL, Category: strings.TrimSpace(category), Description: res.Feed.Description, Enabled: en, RefreshIntervalMinutes: 30}, s.MaxFeeds)
	if e != nil {
		return f, created, e
	}
	if created {
		_, e = s.Repo.SaveFetch(ctx, f, res.Feed, res.ETag, res.LastModified, "success", res.Status, time.Now().UTC())
	}
	return f, created, e
}
func (s *Service) FetchFeed(ctx context.Context, id int64, force bool) (FetchResult, error) {
	start := time.Now()
	result := FetchResult{FeedID: id}
	f, e := s.Repo.GetFeed(ctx, id)
	if e != nil {
		return result, notFound(e)
	}
	if !f.Enabled && !force {
		return result, fmt.Errorf("feed is disabled; set force=true to refresh")
	}
	for attempt := 0; attempt < s.RetryAttempts; attempt++ {
		r, err := s.Fetcher.Fetch(ctx, f)
		if err == nil {
			if r.NotModified {
				err = s.Repo.RecordNotModified(ctx, id, r.ETag, r.LastModified, r.Status, start)
				result.Status = "not_modified"
			} else {
				var c repository.FetchCounts
				c, err = s.Repo.SaveFetch(ctx, f, r.Feed, r.ETag, r.LastModified, "success", r.Status, start)
				result.NewArticles = c.New
				result.UpdatedArticles = c.Updated
				result.DuplicateEntries = c.Duplicates
				result.Status = "success"
			}
			result.DurationMS = time.Since(start).Milliseconds()
			return result, err
		}
		if attempt+1 < s.RetryAttempts {
			delay := time.Duration(1<<attempt)*250*time.Millisecond + time.Duration(rand.IntN(250))*time.Millisecond
			select {
			case <-ctx.Done():
				return result, ctx.Err()
			case <-time.After(delay):
			}
		}
		_ = s.Repo.RecordFailure(ctx, id, 0, start, err)
		result.Status = "failed"
		result.Error = err.Error()
		result.DurationMS = time.Since(start).Milliseconds()
		return result, err
	}
	return result, fmt.Errorf("fetch failed")
}
func (s *Service) FetchAll(ctx context.Context, force bool, category string) ([]FetchResult, error) {
	if !s.refreshMu.TryLock() {
		return nil, fmt.Errorf("a feed refresh is already running")
	}
	defer s.refreshMu.Unlock()
	feeds, e := s.Repo.ListFeeds(ctx, category, boolPtr(true), s.MaxFeeds)
	if e != nil {
		return nil, e
	}
	results := make([]FetchResult, len(feeds))
	jobs := make(chan int)
	var wg sync.WaitGroup
	workers := s.Concurrency
	if workers > len(feeds) {
		workers = len(feeds)
	}
	for n := 0; n < workers; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				r, _ := s.FetchFeed(ctx, feeds[i].ID, force)
				results[i] = r
			}
		}()
	}
	for i := range feeds {
		select {
		case jobs <- i:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return results, ctx.Err()
		}
	}
	close(jobs)
	wg.Wait()
	now := time.Now().UTC()
	s.schedulerMu.Lock()
	s.lastScheduler = &now
	s.schedulerMu.Unlock()
	return results, nil
}
func (s *Service) Add(ctx context.Context, raw, name, cat string, enabled *bool) (model.Feed, bool, error) {
	return s.AddFeed(ctx, raw, name, cat, enabled)
}
func (s *Service) List(ctx context.Context, cat string, enabled *bool, limit int) ([]model.Feed, error) {
	if limit <= 0 || limit > s.MaxFeeds {
		limit = s.MaxFeeds
	}
	return s.Repo.ListFeeds(ctx, cat, enabled, limit)
}
func (s *Service) Update(ctx context.Context, id int64, n, c *string, e *bool, r *int) (model.Feed, error) {
	if r != nil && (*r < 1 || *r > 1440) {
		return model.Feed{}, fmt.Errorf("refresh interval must be 1–1440 minutes")
	}
	f, err := s.Repo.UpdateFeed(ctx, id, n, c, e, r)
	return f, notFound(err)
}
func (s *Service) Remove(ctx context.Context, id int64) error {
	return notFound(s.Repo.RemoveFeed(ctx, id))
}
func (s *Service) Latest(ctx context.Context, limit int, cat string, fid *int64, since, until *time.Time, content bool) ([]model.Article, error) {
	return s.Repo.Latest(ctx, clamp(limit, s.MaxArticles), cat, fid, since, until, content)
}
func (s *Service) Search(ctx context.Context, q string, limit int, cat string, fid *int64, since *time.Time) ([]model.Article, error) {
	if strings.TrimSpace(q) == "" {
		return nil, fmt.Errorf("query is required")
	}
	return s.Repo.Search(ctx, q, clamp(limit, s.MaxArticles), cat, fid, since)
}
func (s *Service) Article(ctx context.Context, id int64) (model.Article, error) {
	a, e := s.Repo.GetArticle(ctx, id)
	return a, notFound(e)
}
func (s *Service) Stats(ctx context.Context) (repository.Stats, error) {
	x, e := s.Repo.Stats(ctx)
	s.schedulerMu.RLock()
	x.LastSchedulerExecution = s.lastScheduler
	s.schedulerMu.RUnlock()
	return x, e
}
func (s *Service) Health(ctx context.Context) map[string]any {
	e := s.Repo.Ready(ctx)
	status := "ok"
	if e != nil {
		status = "degraded"
	}
	return map[string]any{"status": status, "database": map[bool]string{true: "ok", false: "unavailable"}[e == nil], "scheduler": "running", "uptime_seconds": int64(time.Since(s.started).Seconds()), "version": s.Version}
}
func (s *Service) Digest(ctx context.Context, hours, limit int, category string, keywords []string, group string) (map[string]any, error) {
	if hours <= 0 {
		hours = 24
	}
	if hours > 24*365 {
		hours = 24 * 365
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > s.MaxArticles {
		limit = s.MaxArticles
	}
	since := time.Now().UTC().Add(-time.Duration(hours) * time.Hour)
	articles, e := s.Repo.Latest(ctx, limit*3, category, nil, &since, nil, false)
	if e != nil {
		return nil, e
	}
	groups := map[string][]model.Article{}
	selected := []model.Article{}
	seen := map[[32]byte]bool{}
	for _, a := range articles {
		blob := strings.ToLower(a.Title + " " + a.Description)
		ok := len(keywords) == 0
		for _, k := range keywords {
			if strings.Contains(blob, strings.ToLower(strings.TrimSpace(k))) {
				ok = true
				break
			}
		}
		if !ok {
			continue
		}
		h := sha256.Sum256([]byte(strings.Join(strings.Fields(strings.ToLower(a.Title)), " ")))
		if seen[h] {
			continue
		}
		seen[h] = true
		selected = append(selected, a)
		if len(selected) >= limit {
			break
		}
		key := ""
		switch group {
		case "source":
			key = a.Source
		case "category":
			if len(a.Categories) > 0 {
				key = a.Categories[0]
			} else {
				key = "uncategorized"
			}
		case "date":
			key = a.CreatedAt.UTC().Format("2006-01-02")
		}
		if group != "none" && key != "" {
			groups[key] = append(groups[key], a)
		}
	}
	if group == "" {
		group = "none"
	}
	return map[string]any{"hours": hours, "group_by": group, "articles": selected, "groups": groups}, nil
}
func (s *Service) Prune(ctx context.Context) (int64, error) {
	return s.Repo.Prune(ctx, s.RetentionDays)
}
func (s *Service) RefreshLoop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = s.RefreshDue(ctx)
			_, _ = s.Prune(ctx)
		}
	}
}

func (s *Service) RefreshDue(ctx context.Context) ([]FetchResult, error) {
	if !s.refreshMu.TryLock() {
		return nil, fmt.Errorf("a feed refresh is already running")
	}
	defer s.refreshMu.Unlock()
	feeds, err := s.Repo.DueFeeds(ctx)
	if err != nil {
		return nil, err
	}
	results := make([]FetchResult, len(feeds))
	jobs := make(chan int)
	var wg sync.WaitGroup
	workers := s.Concurrency
	if workers > len(feeds) {
		workers = len(feeds)
	}
	for n := 0; n < workers; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				results[i], _ = s.FetchFeed(ctx, feeds[i].ID, false)
			}
		}()
	}
	for i := range feeds {
		select {
		case jobs <- i:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return results, ctx.Err()
		}
	}
	close(jobs)
	wg.Wait()
	now := time.Now().UTC()
	s.schedulerMu.Lock()
	s.lastScheduler = &now
	s.schedulerMu.Unlock()
	return results, nil
}
func boolPtr(v bool) *bool { return &v }
func clamp(n, max int) int {
	if n <= 0 {
		return 20
	}
	if n > max {
		return max
	}
	return n
}
func notFound(e error) error {
	if errors.Is(e, sql.ErrNoRows) {
		return fmt.Errorf("requested record was not found")
	}
	return e
}
