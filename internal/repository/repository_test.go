package repository

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"codekit.io.vn/rss-reader-mcp/internal/model"
)

func TestFeedLifecycleAndMigration(t *testing.T) {
	ctx := context.Background()
	r, err := Open(ctx, filepath.Join(t.TempDir(), "rss.db"), "../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	f, created, err := r.CreateFeed(ctx, model.Feed{Name: "Example", URL: "https://example.org/rss", Enabled: true, RefreshIntervalMinutes: 30}, 1)
	if err != nil || !created {
		t.Fatalf("CreateFeed() = %v, %v", created, err)
	}
	if _, created, err = r.CreateFeed(ctx, model.Feed{Name: "duplicate", URL: f.URL}, 1); err != nil || created {
		t.Fatalf("duplicate CreateFeed() = %v, %v", created, err)
	}
	article := model.ParsedArticle{GUID: "g1", URL: "https://example.org/item", NormalizedURL: "https://example.org/item", Title: "Xin chào", Description: "Tin tức", ContentHash: "hash1", PublishedAt: timePtr(time.Now().UTC())}
	if _, err = r.SaveFetch(ctx, f, model.ParsedFeed{Items: []model.ParsedArticle{article}}, "", "", "success", 200, time.Now()); err != nil {
		t.Fatal(err)
	}
	items, err := r.Search(ctx, "chào", 10, "", nil, nil)
	if err != nil || len(items) != 1 {
		t.Fatalf("Search() found %d, err=%v", len(items), err)
	}
	if err = r.RemoveFeed(ctx, f.ID); err != nil {
		t.Fatal(err)
	}
	a, err := r.GetArticle(ctx, items[0].ID)
	if err != nil {
		t.Fatalf("article history should be retained: %v", err)
	}
	if a.FeedID != nil {
		t.Fatal("removed feed reference should be cleared")
	}
}
func timePtr(t time.Time) *time.Time { return &t }
