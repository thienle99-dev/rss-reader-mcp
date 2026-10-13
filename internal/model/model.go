package model

import "time"

type Feed struct {
	ID                     int64      `json:"id"`
	Name                   string     `json:"name"`
	URL                    string     `json:"url"`
	SiteURL                string     `json:"site_url,omitempty"`
	Category               string     `json:"category,omitempty"`
	Description            string     `json:"description,omitempty"`
	Enabled                bool       `json:"enabled"`
	RefreshIntervalMinutes int        `json:"refresh_interval_minutes"`
	ETag                   string     `json:"-"`
	LastModified           string     `json:"-"`
	LastFetchedAt          *time.Time `json:"last_fetched_at,omitempty"`
	LastSuccessAt          *time.Time `json:"last_success_at,omitempty"`
	LastError              string     `json:"last_error,omitempty"`
	CreatedAt              time.Time  `json:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
	EntryCount             int        `json:"entry_count"`
}

type Article struct {
	ID          int64      `json:"id"`
	FeedID      *int64     `json:"feed_id,omitempty"`
	Source      string     `json:"source"`
	SourceURL   string     `json:"source_url,omitempty"`
	GUID        string     `json:"guid,omitempty"`
	URL         string     `json:"url,omitempty"`
	Title       string     `json:"title"`
	Author      string     `json:"author,omitempty"`
	Description string     `json:"description,omitempty"`
	Content     string     `json:"content,omitempty"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
	UpdatedAt   *time.Time `json:"updated_at,omitempty"`
	Categories  []string   `json:"categories,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

type ParsedFeed struct {
	Title       string
	Description string
	SiteURL     string
	Items       []ParsedArticle
}

type ParsedArticle struct {
	GUID          string
	URL           string
	Title         string
	Author        string
	Description   string
	Content       string
	PublishedAt   *time.Time
	UpdatedAt     *time.Time
	Categories    []string
	NormalizedURL string
	ContentHash   string
}
