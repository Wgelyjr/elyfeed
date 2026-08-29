package rss

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBlockedAddr(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "127.8.8.8", // loopback
		"::1",                    // loopback
		"::ffff:127.0.0.1",       // IPv4-mapped loopback
		"10.0.0.1",               // RFC1918
		"172.16.0.1", "172.31.255.255", // RFC1918
		"192.168.1.1",            // RFC1918
		"fc00::1", "fd12:3456::1", // ULA
		"169.254.1.1",            // link-local
		"169.254.169.254",        // cloud metadata
		"fe80::1",                // link-local
		"224.0.0.1", "239.255.255.255", // multicast
		"ff02::1",                // multicast
	}
	allowed := []string{
		"93.184.216.34",        // public (example.com)
		"8.8.8.8",
		"11.0.0.1",             // just outside 10.0.0.0/8
		"172.32.0.1",           // just outside 172.16.0.0/12
		"2001:4860:4860::8888", // public IPv6
	}
	for _, s := range blocked {
		if !blockedAddr(net.ParseIP(s)) {
			t.Errorf("blockedAddr(%s) = false, want true", s)
		}
	}
	for _, s := range allowed {
		if blockedAddr(net.ParseIP(s)) {
			t.Errorf("blockedAddr(%s) = true, want false", s)
		}
	}
}

func TestFetchBlockedLoopback(t *testing.T) {
	client := NewClient(0, false)
	_, err := Fetch(context.Background(), client, "http://127.0.0.1:1/", "test")
	if !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("Fetch = %v, want ErrBlockedAddress", err)
	}
}

func TestFetchBlockedScheme(t *testing.T) {
	// Scheme is rejected even with the escape hatch on.
	for _, allow := range []bool{false, true} {
		client := NewClient(0, allow)
		for _, u := range []string{"ftp://example.com/feed", "file:///etc/passwd"} {
			if _, err := Fetch(context.Background(), client, u, "test"); !errors.Is(err, ErrBlockedAddress) {
				t.Errorf("Fetch(%q, allowPrivate=%v) = %v, want ErrBlockedAddress", u, allow, err)
			}
		}
	}
}

func TestFetchAllowPrivate(t *testing.T) {
	const body = `<?xml version="1.0"?>
<rss version="2.0">
  <channel>
    <title>Local Feed</title>
    <link>http://127.0.0.1</link>
    <item>
      <title>Item One</title>
      <link>http://127.0.0.1/1</link>
      <description>body</description>
    </item>
  </channel>
</rss>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	// Without the escape hatch the same server is refused.
	strict := NewClient(0, false)
	if _, err := Fetch(context.Background(), strict, srv.URL, "test"); !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("Fetch without escape hatch = %v, want ErrBlockedAddress", err)
	}

	client := NewClient(0, true)
	feed, err := Fetch(context.Background(), client, srv.URL, "test")
	if err != nil {
		t.Fatalf("Fetch with escape hatch: %v", err)
	}
	if feed.Title != "Local Feed" {
		t.Errorf("title = %q, want %q", feed.Title, "Local Feed")
	}
	if len(feed.Items) != 1 {
		t.Errorf("got %d items, want 1", len(feed.Items))
	}
}
