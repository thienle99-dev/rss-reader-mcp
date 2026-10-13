# RSS Reader MCP

A lightweight RSS/Atom collector and MCP Streamable HTTP server for GoClaw. It stores source data in SQLite and exposes structured tools; it does not call an LLM or scrape publisher pages.

## Features

- RSS 2.0, Atom and RDF parsing through gofeed.
- SQLite with WAL, FTS5 search, article metadata and retention pruning.
- Twelve MCP tools for feed management, refresh, search, article retrieval, digest, stats and health.
- Conditional ETag/Last-Modified requests, response size limits, timeouts, bounded workers and retry delays.
- Feed URL validation rejects credentials and private/reserved IP destinations. Redirects are validated and the dialer only connects to globally routable IPs.
- Optional bearer authentication and Origin host allowlist.
- Multi-stage no-CGO Docker image for Linux AMD64 and ARM64.

## Requirements

- Go 1.26 or Docker BuildKit.
- Docker Compose v2.
- Existing Docker network `goclaw-net` shared with GoClaw.

## Local development

```sh
cp .env.example .env
mkdir -p data
go mod download
go fmt ./...
go vet ./...
go test ./...
go test -race ./...
set -a; . ./.env; set +a
RSS_MCP_HOST=127.0.0.1 RSS_DB_PATH=./data/rss.db go run ./cmd/rss-mcp
```

MCP endpoint: `http://127.0.0.1:8080/mcp`; health endpoints are `/healthz` and `/readyz`. MCP calls use the token from `.env`.

## Configuration

Copy `.env.example` to `.env` and set `RSS_MCP_AUTH_TOKEN` to a long random secret. Compose binds the service to the Docker network.

| Variable | Default | Purpose |
|---|---:|---|
| `RSS_MCP_HOST` | `127.0.0.1` | Bind interface; Compose sets `0.0.0.0` internally |
| `RSS_MCP_PORT` | `8080` | HTTP port |
| `RSS_MCP_PATH` | `/mcp` | MCP endpoint |
| `RSS_DB_PATH` | `/data/rss.db` | SQLite path |
| `RSS_REFRESH_ENABLED` | `true` | Background refresh |
| `RSS_REFRESH_INTERVAL` | `30m` | Scheduler tick |
| `RSS_FETCH_TIMEOUT` | `10s` | Per-fetch timeout |
| `RSS_MAX_FEED_BYTES` | `2097152` | Maximum response size |
| `RSS_MAX_CONTENT_BYTES` | `65536` | Maximum stored article text |
| `RSS_MAX_CONCURRENT_FETCHES` | `3` | Concurrent worker count |
| `RSS_MAX_FEEDS` | `200` | Feed cap |
| `RSS_ARTICLE_RETENTION_DAYS` | `90` | Retention window |
| `RSS_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, or `error` |
| `RSS_HTTP_USER_AGENT` | `RSSReaderMCP/1.0` | Feed request User-Agent |
| `RSS_MCP_AUTH_TOKEN` | empty | Bearer token; set for deployment |
| `RSS_URL_ALLOWLIST` | empty | Optional comma-separated feed host suffix allowlist |
| `RSS_MCP_ALLOWED_HOSTS` | empty | Allowed Origin hosts; empty rejects browser origins |
| `RSS_MAX_ARTICLES_PER_RESPONSE` | `100` | Tool response bound |
| `RSS_MCP_MAX_REQUEST_BYTES` | `1048576` | MCP request size |
| `RSS_MCP_HTTP_TIMEOUT` | `2m` | HTTP write timeout |
| `RSS_FETCH_RETRY_ATTEMPTS` | `3` | Fetch attempts |

## Docker deployment

```sh
cp .env.example .env
TOKEN="$(openssl rand -hex 32)"
sed -i "s/REPLACE_WITH_LONG_RANDOM_SECRET/${TOKEN}/" .env
chmod 600 .env
mkdir -p data
sudo chown 10001:10001 data
docker network inspect goclaw-net >/dev/null
docker compose up -d --build
docker compose ps
docker compose logs --tail=100 rss-mcp
```

Compose publishes no host port. GoClaw reaches `http://rss-mcp:8080/mcp` on `goclaw-net`. SQLite persists in `./data`. For a backup, stop the service, archive `data`, then start it again. Restore while stopped.

Compose builds for the VPS host architecture. For a published multi-architecture image:
```sh
docker buildx build --platform linux/amd64,linux/arm64 --push -t <registry>/rss-reader-mcp:1.0.0 .
```

## GoClaw connection

In GoClaw MCP server settings add:

```json
{
  "name": "rss-reader",
  "transport": "streamable-http",
  "url": "http://rss-mcp:8080/mcp",
  "headers": { "Authorization": "Bearer <RSS_MCP_AUTH_TOKEN>" }
}
```

Use the exact token from `/root/rss-reader/.env` on the VPS. Save the server, verify discovery, then grant needed RSS tools to the Summary Agent. GoClaw's MCP client initializes the Streamable HTTP session and lists tools. This project does not alter GoClaw settings or grants.

## MCP tools

| Tool | Main inputs | Behavior |
|---|---|---|
| `rss_add_feed` | `url`, optional `name`, `category`, `enabled` | Validates and subscribes; stores initial fetch |
| `rss_list_feeds` | `category`, `enabled`, `limit` | Lists feeds and entry counts |
| `rss_remove_feed` | `feed_id` | Removes feed while keeping article history |
| `rss_update_feed` | `feed_id`, optional settings | Changes supplied settings |
| `rss_fetch_feed` | `feed_id`, `force` | Refreshes one feed |
| `rss_fetch_all` | `force`, `category` | Bounded refresh with per-feed results |
| `rss_latest` | `limit`, `category`, `feed_id`, `since`, `until`, `include_content` | Newest articles |
| `rss_search` | `query`, `limit`, filters | FTS5 title, description and content search |
| `rss_get_article` | `article_id` | Returns stored feed content only |
| `rss_digest` | `hours`, `limit`, `category`, `keywords`, `group_by` | Source-linked data for agent summarization |
| `rss_stats` | none | Feed, article, refresh and DB counts |
| `rss_health` | none | Service and database status |

Example inputs:
```json
{"url":"https://example.org/feed.xml","category":"technology"}
{"limit":10,"since":"2026-10-12T00:00:00Z"}
{"hours":24,"limit":20,"group_by":"source","keywords":["AI","technology"]}
```

For a summary, GoClaw should call `rss_digest` and compose an answer from returned evidence while preserving source URLs. This server does not generate summaries.

## Operations and troubleshooting

- `docker compose ps` should show `healthy`; `/readyz` checks SQLite and `/healthz` is a liveness endpoint.
- If GoClaw cannot resolve `rss-mcp`, verify both containers are on `goclaw-net` with `docker network inspect goclaw-net`.
- For 401, configure `Authorization: Bearer …` with the token in `.env`.
- Feed registration only accepts public HTTP(S) feeds with valid RSS/Atom XML.
- Keep concurrency at 3 and the container limit at 256 MB for a 2 GB VPS. Monitor with `docker stats`. Memory targets are budgets, not measured guarantees; benchmark using your actual feed set.
- Do not expose port 8080 publicly. Put remote access behind authenticated TLS and set the Origin allowlist.

## License

MIT. See [LICENSE](LICENSE).
