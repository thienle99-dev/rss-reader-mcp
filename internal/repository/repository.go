package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"codekit.io.vn/rss-reader-mcp/internal/model"
	_ "modernc.org/sqlite"
)

type Repository struct {
	db   *sql.DB
	path string
}
type Stats struct {
	RegisteredFeeds        int        `json:"registered_feeds"`
	EnabledFeeds           int        `json:"enabled_feeds"`
	TotalArticles          int        `json:"total_stored_articles"`
	RecentArticles         int        `json:"articles_collected_last_24_hours"`
	LastSchedulerExecution *time.Time `json:"last_scheduler_execution,omitempty"`
	FailedFeeds            int        `json:"failed_feeds"`
	DatabaseBytes          int64      `json:"database_size_bytes"`
	Refreshes              int64      `json:"refreshes"`
	NotModified            int64      `json:"not_modified_checks"`
}
type FetchCounts struct{ New, Updated, Duplicates int }

const feedSelect = "SELECT f.id,f.name,f.url,f.site_url,f.category,f.description,f.enabled,f.refresh_interval_minutes,f.etag,f.last_modified,f.last_fetched_at,f.last_success_at,f.last_error,f.created_at,f.updated_at,(SELECT count(*) FROM articles a WHERE a.feed_id=f.id) FROM feeds f"

func Open(ctx context.Context, path, migrationsDir string) (*Repository, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	r := &Repository{db: db, path: path}
	for _, q := range []string{"PRAGMA journal_mode=WAL", "PRAGMA foreign_keys=ON", "PRAGMA busy_timeout=5000", "PRAGMA synchronous=NORMAL"} {
		if _, err = db.ExecContext(ctx, q); err != nil {
			db.Close()
			return nil, fmt.Errorf("configure sqlite: %w", err)
		}
	}
	schema, err := os.ReadFile(filepath.Join(migrationsDir, "001_init.sql"))
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("read migration: %w", err)
	}
	if _, err = db.ExecContext(ctx, string(schema)); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply migration: %w", err)
	}
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return r, nil
}
func (r *Repository) Close() error                    { return r.db.Close() }
func (r *Repository) Ready(ctx context.Context) error { return r.db.PingContext(ctx) }

type rowScanner interface{ Scan(...any) error }

func scanFeed(row rowScanner) (model.Feed, error) {
	var f model.Feed
	var enabled int
	var fetched, success sql.NullString
	var created, updated string
	err := row.Scan(&f.ID, &f.Name, &f.URL, &f.SiteURL, &f.Category, &f.Description, &enabled, &f.RefreshIntervalMinutes, &f.ETag, &f.LastModified, &fetched, &success, &f.LastError, &created, &updated, &f.EntryCount)
	if err != nil {
		return f, err
	}
	f.Enabled = enabled != 0
	f.LastFetchedAt = parseTime(fetched)
	f.LastSuccessAt = parseTime(success)
	f.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	f.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return f, nil
}
func (r *Repository) GetFeed(ctx context.Context, id int64) (model.Feed, error) {
	return scanFeed(r.db.QueryRowContext(ctx, feedSelect+" WHERE f.id=?", id))
}
func (r *Repository) GetFeedByURL(ctx context.Context, url string) (model.Feed, error) {
	return scanFeed(r.db.QueryRowContext(ctx, feedSelect+" WHERE f.url=?", url))
}
func (r *Repository) CreateFeed(ctx context.Context, f model.Feed, max int) (model.Feed, bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return f, false, err
	}
	defer tx.Rollback()
	var existingID int64
	err = tx.QueryRowContext(ctx, "SELECT id FROM feeds WHERE url=?", f.URL).Scan(&existingID)
	if err == nil {
		if err = tx.Commit(); err != nil {
			return f, false, err
		}
		existing, e := r.GetFeed(ctx, existingID)
		return existing, false, e
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return f, false, err
	}
	var count int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM feeds").Scan(&count); err != nil {
		return f, false, err
	}
	if count >= max {
		return f, false, fmt.Errorf("feed limit of %d reached", max)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO feeds(name,url,site_url,category,description,enabled,refresh_interval_minutes,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)", f.Name, f.URL, f.SiteURL, f.Category, f.Description, boolInt(f.Enabled), f.RefreshIntervalMinutes, now, now)
	if err != nil {
		return f, false, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		var id int64
		if err = tx.QueryRowContext(ctx, "SELECT id FROM feeds WHERE url=?", f.URL).Scan(&id); err != nil {
			return f, false, err
		}
		if err = tx.Commit(); err != nil {
			return f, false, err
		}
		existing, e := r.GetFeed(ctx, id)
		return existing, false, e
	}
	id, err := res.LastInsertId()
	if err != nil {
		return f, false, err
	}
	if err = tx.Commit(); err != nil {
		return f, false, err
	}
	created, err := r.GetFeed(ctx, id)
	return created, true, err
}
func (r *Repository) ListFeeds(ctx context.Context, category string, enabled *bool, limit int) ([]model.Feed, error) {
	q := feedSelect + " WHERE (?='' OR lower(f.category)=lower(?)) AND (? IS NULL OR f.enabled=?) ORDER BY f.id LIMIT ?"
	var e any
	if enabled != nil {
		e = boolInt(*enabled)
	}
	rows, err := r.db.QueryContext(ctx, q, category, category, e, e, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Feed{}
	for rows.Next() {
		f, x := scanFeed(rows)
		if x != nil {
			return nil, x
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
func (r *Repository) UpdateFeed(ctx context.Context, id int64, name, category *string, enabled *bool, refresh *int) (model.Feed, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Feed{}, err
	}
	defer tx.Rollback()
	f, err := scanFeed(tx.QueryRowContext(ctx, feedSelect+" WHERE f.id=?", id))
	if err != nil {
		return f, err
	}
	if name != nil {
		f.Name = strings.TrimSpace(*name)
	}
	if category != nil {
		f.Category = strings.TrimSpace(*category)
	}
	if enabled != nil {
		f.Enabled = *enabled
	}
	if refresh != nil {
		f.RefreshIntervalMinutes = *refresh
	}
	_, err = tx.ExecContext(ctx, "UPDATE feeds SET name=?,category=?,enabled=?,refresh_interval_minutes=?,updated_at=? WHERE id=?", f.Name, f.Category, boolInt(f.Enabled), f.RefreshIntervalMinutes, time.Now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return f, err
	}
	if err = tx.Commit(); err != nil {
		return f, err
	}
	return r.GetFeed(ctx, id)
}
func (r *Repository) RemoveFeed(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx, "DELETE FROM feeds WHERE id=?", id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return sql.ErrNoRows
	}
	return err
}

func (r *Repository) SaveFetch(ctx context.Context, f model.Feed, pf model.ParsedFeed, etag, lastModified string, status string, httpStatus int, started time.Time) (FetchCounts, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return FetchCounts{}, err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	counts := FetchCounts{}
	seen := map[string]bool{}
	for _, it := range pf.Items {
		key := it.GUID
		if key == "" {
			key = it.NormalizedURL
		}
		if key == "" {
			key = it.ContentHash
		}
		if key != "" && seen[key] {
			counts.Duplicates++
			continue
		}
		if key != "" {
			seen[key] = true
		}
		var id int64
		var oldHash string
		err = tx.QueryRowContext(ctx, "SELECT id,content_hash FROM articles WHERE feed_id=? AND ((?<>'' AND guid_key=?) OR (?<>'' AND normalized_url=?) OR (?<>'' AND content_hash=?)) ORDER BY CASE WHEN ?<>'' AND guid_key=? THEN 0 WHEN ?<>'' AND normalized_url=? THEN 1 ELSE 2 END LIMIT 1",
			f.ID, it.GUID, it.GUID, it.NormalizedURL, it.NormalizedURL, it.ContentHash, it.ContentHash, it.GUID, it.GUID, it.NormalizedURL, it.NormalizedURL).Scan(&id, &oldHash)
		categories, _ := json.Marshal(it.Categories)
		if errors.Is(err, sql.ErrNoRows) {
			_, err = tx.ExecContext(ctx, "INSERT INTO articles(feed_id,source_name,source_url,guid,guid_key,url,normalized_url,title,author,description,content,published_at,article_updated_at,categories_json,content_hash,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
				f.ID, f.Name, f.URL, it.GUID, it.GUID, it.URL, it.NormalizedURL, it.Title, it.Author, it.Description, it.Content, timeString(it.PublishedAt), timeString(it.UpdatedAt), string(categories), it.ContentHash, now.Format(time.RFC3339Nano))
			if err != nil {
				return FetchCounts{}, err
			}
			counts.New++
		} else if err != nil {
			return FetchCounts{}, err
		} else if oldHash == it.ContentHash {
			counts.Duplicates++
		} else {
			_, err = tx.ExecContext(ctx, "UPDATE articles SET source_name=?,source_url=?,guid=?,guid_key=?,url=?,normalized_url=?,title=?,author=?,description=?,content=?,published_at=?,article_updated_at=?,categories_json=?,content_hash=? WHERE id=?",
				f.Name, f.URL, it.GUID, it.GUID, it.URL, it.NormalizedURL, it.Title, it.Author, it.Description, it.Content, timeString(it.PublishedAt), timeString(it.UpdatedAt), string(categories), it.ContentHash, id)
			if err != nil {
				return FetchCounts{}, err
			}
			counts.Updated++
		}
	}
	nowStr := now.Format(time.RFC3339Nano)
	_, err = tx.ExecContext(ctx, "UPDATE feeds SET site_url=CASE WHEN ?<>'' THEN ? ELSE site_url END,description=CASE WHEN ?<>'' THEN ? ELSE description END,etag=?,last_modified=?,last_fetched_at=?,last_success_at=?,last_error='',updated_at=? WHERE id=?",
		pf.SiteURL, pf.SiteURL, pf.Description, pf.Description, etag, lastModified, nowStr, nowStr, nowStr, f.ID)
	if err != nil {
		return FetchCounts{}, err
	}
	if err = recordHistory(ctx, tx, f.ID, started, now, "success", httpStatus, counts, ""); err != nil {
		return FetchCounts{}, err
	}
	if err = tx.Commit(); err != nil {
		return FetchCounts{}, err
	}
	return counts, nil
}
func (r *Repository) RecordNotModified(ctx context.Context, feedID int64, etag, lastModified string, status int, started time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	n := now.Format(time.RFC3339Nano)
	_, err = tx.ExecContext(ctx, "UPDATE feeds SET etag=CASE WHEN ?<>'' THEN ? ELSE etag END,last_modified=CASE WHEN ?<>'' THEN ? ELSE last_modified END,last_fetched_at=?,last_success_at=?,last_error='',updated_at=? WHERE id=?", etag, etag, lastModified, lastModified, n, n, n, feedID)
	if err != nil {
		return err
	}
	if err = recordHistory(ctx, tx, feedID, started, now, "not_modified", status, FetchCounts{}, ""); err != nil {
		return err
	}
	return tx.Commit()
}
func (r *Repository) RecordFailure(ctx context.Context, feedID int64, status int, started time.Time, failure error) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	msg := failure.Error()
	if len(msg) > 500 {
		msg = msg[:500]
	}
	n := now.Format(time.RFC3339Nano)
	_, err = tx.ExecContext(ctx, "UPDATE feeds SET last_fetched_at=?,last_error=?,updated_at=? WHERE id=?", n, msg, n, feedID)
	if err != nil {
		return err
	}
	if err = recordHistory(ctx, tx, feedID, started, now, "failed", status, FetchCounts{}, msg); err != nil {
		return err
	}
	return tx.Commit()
}
func recordHistory(ctx context.Context, tx *sql.Tx, feedID int64, start, end time.Time, status string, httpStatus int, c FetchCounts, msg string) error {
	_, err := tx.ExecContext(ctx, "INSERT INTO fetch_history(feed_id,started_at,completed_at,status,http_status,new_articles,updated_articles,duplicate_entries,error_message) VALUES(?,?,?,?,?,?,?,?,?)", feedID, start.UTC().Format(time.RFC3339Nano), end.UTC().Format(time.RFC3339Nano), status, httpStatus, c.New, c.Updated, c.Duplicates, msg)
	return err
}
func (r *Repository) DueFeeds(ctx context.Context) ([]model.Feed, error) {
	q := feedSelect + " WHERE f.enabled=1 AND (f.last_fetched_at IS NULL OR julianday('now')-julianday(f.last_fetched_at)>=f.refresh_interval_minutes/1440.0) ORDER BY f.last_fetched_at IS NOT NULL,f.last_fetched_at LIMIT 200"
	rows, err := r.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Feed{}
	for rows.Next() {
		f, e := scanFeed(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
func (r *Repository) Latest(ctx context.Context, limit int, category string, feedID *int64, since, until *time.Time, include bool) ([]model.Article, error) {
	content := "''"
	if include {
		content = "a.content"
	}
	q := articleSelect(content) + " WHERE (?='' OR lower(f.category)=lower(?)) AND (? IS NULL OR a.feed_id=?) AND (? IS NULL OR COALESCE(a.published_at,a.created_at)>=?) AND (? IS NULL OR COALESCE(a.published_at,a.created_at)<=?) ORDER BY COALESCE(a.published_at,a.created_at) DESC,a.id DESC LIMIT ?"
	var fid any
	if feedID != nil {
		fid = *feedID
	}
	rows, err := r.db.QueryContext(ctx, q, category, category, fid, fid, timeString(since), timeString(since), timeString(until), timeString(until), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collect(rows)
}
func articleSelect(content string) string {
	return "SELECT a.id,a.feed_id,a.source_name,a.source_url,a.guid,a.url,a.title,a.author,a.description," + content + ",a.published_at,a.article_updated_at,a.categories_json,a.created_at FROM articles a LEFT JOIN feeds f ON f.id=a.feed_id"
}
func collect(rows *sql.Rows) ([]model.Article, error) {
	out := []model.Article{}
	for rows.Next() {
		var a model.Article
		var fid sql.NullInt64
		var pub, upd sql.NullString
		var cats, created string
		if e := rows.Scan(&a.ID, &fid, &a.Source, &a.SourceURL, &a.GUID, &a.URL, &a.Title, &a.Author, &a.Description, &a.Content, &pub, &upd, &cats, &created); e != nil {
			return nil, e
		}
		if fid.Valid {
			v := fid.Int64
			a.FeedID = &v
		}
		a.PublishedAt = parseTime(pub)
		a.UpdatedAt = parseTime(upd)
		_ = json.Unmarshal([]byte(cats), &a.Categories)
		a.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, a)
	}
	return out, rows.Err()
}
func (r *Repository) Search(ctx context.Context, query string, limit int, category string, feedID *int64, since *time.Time) ([]model.Article, error) {
	terms := strings.Fields(query)
	for i, t := range terms {
		terms[i] = "\"" + strings.ReplaceAll(t, "\"", "") + "\""
	}
	match := strings.Join(terms, " AND ")
	var fid any
	if feedID != nil {
		fid = *feedID
	}
	q := articleSelect("a.content") + " JOIN articles_fts ON articles_fts.rowid=a.id WHERE articles_fts MATCH ? AND (?='' OR lower(f.category)=lower(?)) AND (? IS NULL OR a.feed_id=?) AND (? IS NULL OR COALESCE(a.published_at,a.created_at)>=?) ORDER BY bm25(articles_fts),COALESCE(a.published_at,a.created_at) DESC,a.id DESC LIMIT ?"
	rows, err := r.db.QueryContext(ctx, q, match, category, category, fid, fid, timeString(since), timeString(since), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collect(rows)
}
func (r *Repository) GetArticle(ctx context.Context, id int64) (model.Article, error) {
	rows, err := r.db.QueryContext(ctx, articleSelect("a.content")+" WHERE a.id=?", id)
	if err != nil {
		return model.Article{}, err
	}
	defer rows.Close()
	items, err := collect(rows)
	if err != nil {
		return model.Article{}, err
	}
	if len(items) == 0 {
		return model.Article{}, sql.ErrNoRows
	}
	return items[0], nil
}
func (r *Repository) Stats(ctx context.Context) (Stats, error) {
	var s Stats
	err := r.db.QueryRowContext(ctx, "SELECT (SELECT count(*) FROM feeds),(SELECT count(*) FROM feeds WHERE enabled=1),(SELECT count(*) FROM articles),(SELECT count(*) FROM articles WHERE julianday(created_at)>=julianday('now','-1 day')),(SELECT count(*) FROM feeds WHERE last_error<>''),(SELECT count(*) FROM fetch_history),(SELECT count(*) FROM fetch_history WHERE status='not_modified')").Scan(&s.RegisteredFeeds, &s.EnabledFeeds, &s.TotalArticles, &s.RecentArticles, &s.FailedFeeds, &s.Refreshes, &s.NotModified)
	if err != nil {
		return s, err
	}
	if fi, e := os.Stat(r.path); e == nil {
		s.DatabaseBytes = fi.Size()
	}
	return s, nil
}
func (r *Repository) Prune(ctx context.Context, days int) (int64, error) {
	cut := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour).Format(time.RFC3339Nano)
	res, err := r.db.ExecContext(ctx, "DELETE FROM articles WHERE COALESCE(published_at,created_at) < ?", cut)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
func timeString(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}
func parseTime(s sql.NullString) *time.Time {
	if !s.Valid {
		return nil
	}
	t, e := time.Parse(time.RFC3339Nano, s.String)
	if e != nil {
		return nil
	}
	return &t
}
