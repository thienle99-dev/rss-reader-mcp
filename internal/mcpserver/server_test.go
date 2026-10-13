package mcpserver

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"codekit.io.vn/rss-reader-mcp/internal/config"
	"codekit.io.vn/rss-reader-mcp/internal/fetcher"
	"codekit.io.vn/rss-reader-mcp/internal/repository"
	"codekit.io.vn/rss-reader-mcp/internal/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestStreamableHTTPDiscoveryAndInvocation(t *testing.T) {
	ctx := context.Background()
	repo, err := repository.Open(ctx, filepath.Join(t.TempDir(), "rss.db"), "../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	f := fetcher.New(time.Second, 1<<20, 1024, "test", nil)
	svc := service.New(repo, f, 200, 100, 3, 90, 1)
	httpServer := httptest.NewServer(NewHandler(svc, config.Config{MaxRequestBytes: 1 << 20}))
	defer httpServer.Close()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: httpServer.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 12 {
		t.Fatalf("discovered %d tools, want 12", len(tools.Tools))
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "rss_stats", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("rss_stats returned an MCP tool error")
	}
	if result.StructuredContent == nil {
		t.Fatal("expected structured tool result")
	}
	invalid, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "rss_remove_feed", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if !invalid.IsError {
		t.Fatal("missing feed_id should be rejected")
	}
}
