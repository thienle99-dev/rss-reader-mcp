package fetcher

import (
	"context"
	"net/url"
	"strings"
	"testing"
)

func TestNormalizeURLRemovesFragmentAndTracking(t *testing.T) {
	got := NormalizeURL("HTTPS://Example.COM/story?id=42&utm_source=mail#section")
	if got != "https://example.com/story?id=42" {
		t.Fatalf("NormalizeURL() = %q", got)
	}
}

func TestNormalizeURLRejectsRelativeURL(t *testing.T) {
	if got := NormalizeURL("/story/42"); got != "" {
		t.Fatalf("NormalizeURL() = %q, want empty", got)
	}
}

func TestValidateURLRejectsLoopback(t *testing.T) {
	u, _ := url.Parse("http://127.0.0.1/feed.xml")
	if err := validateURL(context.Background(), u, nil); err == nil {
		t.Fatal("loopback destination should be rejected")
	}
}

func TestParseRSSAndAtom(t *testing.T) {
	tests := []struct{ name, xml, title, content string }{
		{"RSS 2.0", `<rss version="2.0"><channel><title>Vietnam News</title><link>https://example.org</link><description>Tin tức</description><item><title>Xin chào</title><guid>entry-1</guid><link>https://example.org/a</link><description><![CDATA[<p>Nội dung tiếng Việt</p>]]></description><author>editor@example.org</author></item></channel></rss>`, "Vietnam News", "Nội dung tiếng Việt"},
		{"Atom", `<feed xmlns="http://www.w3.org/2005/Atom"><title>Atom News</title><link href="https://example.org"/><entry><title>你好</title><id>tag:example.org,1</id><link href="https://example.org/1"/><updated>2026-10-13T00:00:00Z</updated><summary>世界</summary></entry></feed>`, "Atom News", "世界"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			feed, err := Parse([]byte(tc.xml), "test", 1024)
			if err != nil {
				t.Fatal(err)
			}
			if feed.Title != tc.title || len(feed.Items) != 1 {
				t.Fatalf("unexpected feed: %#v", feed)
			}
			item := feed.Items[0]
			if item.Title == "" || !strings.Contains(item.Description, tc.content) {
				t.Fatalf("unexpected item: %#v", item)
			}
			if item.ContentHash == "" || item.NormalizedURL == "" {
				t.Fatalf("missing normalized data: %#v", item)
			}
		})
	}
}

func TestParseRejectsInvalidXML(t *testing.T) {
	if _, err := Parse([]byte("<rss><broken>"), "test", 1024); err == nil {
		t.Fatal("malformed feed should fail")
	}
}
