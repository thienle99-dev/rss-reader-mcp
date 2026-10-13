FROM golang:1.26-alpine3.23 AS build
WORKDIR /src
RUN apk add --no-cache ca-certificates
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w -X main.version=1.0.0" -o /out/rss-mcp ./cmd/rss-mcp

FROM alpine:3.23
RUN apk add --no-cache ca-certificates tzdata && addgroup -S -g 10001 rss && adduser -S -D -H -u 10001 -G rss rss
WORKDIR /app
COPY --from=build --chown=10001:10001 /out/rss-mcp /usr/local/bin/rss-mcp
COPY --chown=10001:10001 migrations /app/migrations
USER 10001:10001
ENV RSS_MCP_HOST=0.0.0.0 RSS_MCP_PORT=8080 RSS_DB_PATH=/data/rss.db
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/rss-mcp"]
