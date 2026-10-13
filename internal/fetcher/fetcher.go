package fetcher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"codekit.io.vn/rss-reader-mcp/internal/model"
	"github.com/mmcdole/gofeed"
)

type Result struct {
	Feed               model.ParsedFeed
	ETag, LastModified string
	Status             int
	NotModified        bool
}
type Fetcher struct {
	client     *http.Client
	timeout    time.Duration
	maxBytes   int64
	maxContent int
	userAgent  string
	allowlist  []string
}

func New(timeout time.Duration, maxBytes int64, maxContent int, userAgent string, allowlist []string) *Fetcher {
	transport := &http.Transport{Proxy: nil, DialContext: safeDial, MaxIdleConns: 16, MaxIdleConnsPerHost: 2, IdleConnTimeout: 30 * time.Second, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: timeout}
	return &Fetcher{client: &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("too many redirects")
		}
		return validateURL(req.Context(), req.URL, allowlist)
	}}, timeout: timeout, maxBytes: maxBytes, maxContent: maxContent, userAgent: userAgent, allowlist: allowlist}
}

func validateURL(ctx context.Context, u *url.URL, allow []string) error {
	if u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return fmt.Errorf("feed URL must be HTTP(S), include a host, and contain no credentials")
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if len(allow) > 0 {
		ok := false
		for _, d := range allow {
			if host == d || strings.HasSuffix(host, "."+d) {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Errorf("feed host is not in RSS_URL_ALLOWLIST")
		}
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("resolve feed host: %w", err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("feed host has no addresses")
	}
	for _, ip := range ips {
		if !publicIP(ip.IP) {
			return fmt.Errorf("feed host resolves to a restricted address")
		}
	}
	return nil
}
func publicIP(ip net.IP) bool {
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return false
	}
	for _, block := range reservedNetworks {
		if block.Contains(ip) {
			return false
		}
	}
	return true
}

var reservedNetworks = func() []*net.IPNet {
	var out []*net.IPNet
	for _, cidr := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "100::/64", "2001::/23", "2001:db8::/32", "2002::/16"} {
		_, block, err := net.ParseCIDR(cidr)
		if err == nil {
			out = append(out, block)
		}
	}
	return out
}()

func safeDial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	d := net.Dialer{Timeout: 5 * time.Second}
	for _, v := range ips {
		if !publicIP(v.IP) {
			continue
		}
		c, e := d.DialContext(ctx, network, net.JoinHostPort(v.IP.String(), port))
		if e == nil {
			return c, nil
		}
	}
	return nil, fmt.Errorf("no public address available for feed host")
}

func (f *Fetcher) Fetch(ctx context.Context, feed model.Feed) (Result, error) {
	u, err := url.Parse(feed.URL)
	if err != nil {
		return Result{}, fmt.Errorf("invalid feed URL")
	}
	if err = validateURL(ctx, u, f.allowlist); err != nil {
		return Result{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feed.URL, nil)
	if err != nil {
		return Result{}, fmt.Errorf("create feed request: %w", err)
	}
	req.Header.Set("User-Agent", f.userAgent)
	req.Header.Set("Accept", "application/atom+xml, application/rss+xml, application/xml, text/xml, */*;q=0.5")
	if feed.ETag != "" {
		req.Header.Set("If-None-Match", feed.ETag)
	}
	if feed.LastModified != "" {
		req.Header.Set("If-Modified-Since", feed.LastModified)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("fetch feed: %w", err)
	}
	defer resp.Body.Close()
	out := Result{Status: resp.StatusCode, ETag: resp.Header.Get("ETag"), LastModified: resp.Header.Get("Last-Modified")}
	if resp.StatusCode == http.StatusNotModified {
		out.NotModified = true
		return out, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return out, fmt.Errorf("feed returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, f.maxBytes+1))
	if err != nil {
		return out, fmt.Errorf("read feed: %w", err)
	}
	if int64(len(body)) > f.maxBytes {
		return out, fmt.Errorf("feed response exceeds %d bytes", f.maxBytes)
	}
	out.Feed, err = Parse(body, f.userAgent, f.maxContent)
	if err != nil {
		return out, err
	}
	return out, nil
}

func Parse(body []byte, userAgent string, maxContent int) (model.ParsedFeed, error) {
	parser := gofeed.NewParser()
	parser.UserAgent = userAgent
	parsed, err := parser.ParseString(string(body))
	if err != nil {
		return model.ParsedFeed{}, fmt.Errorf("parse RSS/Atom feed: %w", err)
	}
	out := model.ParsedFeed{Title: parsed.Title, Description: parsed.Description, SiteURL: parsed.Link}
	for _, item := range parsed.Items {
		a := model.ParsedArticle{GUID: strings.TrimSpace(item.GUID), URL: strings.TrimSpace(item.Link), Title: strings.TrimSpace(item.Title), Description: item.Description, Content: item.Content, Categories: item.Categories}
		if item.Author != nil {
			a.Author = strings.TrimSpace(item.Author.Name)
		} else if len(item.Authors) > 0 {
			a.Author = strings.TrimSpace(item.Authors[0].Name)
		}
		if item.PublishedParsed != nil {
			t := item.PublishedParsed.UTC()
			a.PublishedAt = &t
		}
		if item.UpdatedParsed != nil {
			t := item.UpdatedParsed.UTC()
			a.UpdatedAt = &t
		}
		if len(a.Content) > maxContent {
			a.Content = a.Content[:maxContent]
		}
		if len(a.Description) > maxContent {
			a.Description = a.Description[:maxContent]
		}
		a.NormalizedURL = NormalizeURL(a.URL)
		sum := sha256.Sum256([]byte(strings.ToLower(strings.Join([]string{strings.TrimSpace(a.Title), strings.TrimSpace(a.Description), strings.TrimSpace(a.Content)}, "\n"))))
		a.ContentHash = hex.EncodeToString(sum[:])
		out.Items = append(out.Items, a)
	}
	return out, nil
}
func NormalizeURL(raw string) string {
	u, e := url.Parse(strings.TrimSpace(raw))
	if e != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	u.Fragment = ""
	u.Host = strings.ToLower(u.Host)
	q := u.Query()
	for k := range q {
		lk := strings.ToLower(k)
		if strings.HasPrefix(lk, "utm_") || lk == "fbclid" || lk == "gclid" || lk == "ref" {
			q.Del(k)
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}
