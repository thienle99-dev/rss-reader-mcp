CREATE TABLE IF NOT EXISTS schema_migrations(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS feeds (
 id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, url TEXT NOT NULL UNIQUE, site_url TEXT NOT NULL DEFAULT '',
 category TEXT NOT NULL DEFAULT '', description TEXT NOT NULL DEFAULT '', enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0,1)),
 refresh_interval_minutes INTEGER NOT NULL DEFAULT 30 CHECK(refresh_interval_minutes > 0), etag TEXT NOT NULL DEFAULT '',
 last_modified TEXT NOT NULL DEFAULT '', last_fetched_at TEXT, last_success_at TEXT, last_error TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS articles (
 id INTEGER PRIMARY KEY AUTOINCREMENT, feed_id INTEGER REFERENCES feeds(id) ON DELETE SET NULL,
 source_name TEXT NOT NULL, source_url TEXT NOT NULL DEFAULT '', guid TEXT NOT NULL DEFAULT '', guid_key TEXT NOT NULL DEFAULT '',
 url TEXT NOT NULL DEFAULT '', normalized_url TEXT NOT NULL DEFAULT '', title TEXT NOT NULL, author TEXT NOT NULL DEFAULT '',
 description TEXT NOT NULL DEFAULT '', content TEXT NOT NULL DEFAULT '', published_at TEXT, article_updated_at TEXT,
 categories_json TEXT NOT NULL DEFAULT '[]', content_hash TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS fetch_history (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 feed_id INTEGER REFERENCES feeds(id) ON DELETE SET NULL,
 started_at TEXT NOT NULL,
 completed_at TEXT NOT NULL,
 status TEXT NOT NULL CHECK(status IN ('success','not_modified','failed')),
 http_status INTEGER NOT NULL DEFAULT 0,
 new_articles INTEGER NOT NULL DEFAULT 0,
 updated_articles INTEGER NOT NULL DEFAULT 0,
 duplicate_entries INTEGER NOT NULL DEFAULT 0,
 error_message TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_articles_feed ON articles(feed_id);
CREATE INDEX IF NOT EXISTS idx_articles_published ON articles(published_at DESC,id DESC);
CREATE INDEX IF NOT EXISTS idx_articles_created ON articles(created_at DESC,id DESC);
CREATE INDEX IF NOT EXISTS idx_articles_category ON articles(feed_id,published_at DESC);
CREATE INDEX IF NOT EXISTS idx_fetch_history_feed ON fetch_history(feed_id,completed_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_articles_feed_guid ON articles(feed_id,guid_key) WHERE feed_id IS NOT NULL AND guid_key<>'';
CREATE UNIQUE INDEX IF NOT EXISTS idx_articles_feed_url ON articles(feed_id,normalized_url) WHERE feed_id IS NOT NULL AND normalized_url<>'';
CREATE UNIQUE INDEX IF NOT EXISTS idx_articles_feed_hash ON articles(feed_id,content_hash) WHERE feed_id IS NOT NULL AND content_hash<>'';
CREATE VIRTUAL TABLE IF NOT EXISTS articles_fts USING fts5(title,description,content,tokenize='unicode61 remove_diacritics 2');
CREATE TRIGGER IF NOT EXISTS articles_ai AFTER INSERT ON articles BEGIN INSERT INTO articles_fts(rowid,title,description,content) VALUES(new.id,new.title,new.description,new.content); END;
CREATE TRIGGER IF NOT EXISTS articles_ad AFTER DELETE ON articles BEGIN DELETE FROM articles_fts WHERE rowid=old.id; END;
CREATE TRIGGER IF NOT EXISTS articles_au AFTER UPDATE ON articles BEGIN DELETE FROM articles_fts WHERE rowid=old.id; INSERT INTO articles_fts(rowid,title,description,content) VALUES(new.id,new.title,new.description,new.content); END;
INSERT OR IGNORE INTO schema_migrations(version,applied_at) VALUES(1,strftime('%Y-%m-%dT%H:%M:%fZ','now'));
