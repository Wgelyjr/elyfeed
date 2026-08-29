// Package rss fetches and parses RSS 2.0 and Atom feeds into normalized items.
//
// It uses a small namespace-agnostic XML tree walker rather than a typed
// decoder so that feeds with varying namespaces (dc:creator, content:encoded,
// atom:*) and minor malformations still parse. HTML in item bodies is reduced
// to a plain-text excerpt; the original article is reached via the item link.
package rss

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	neturl "net/url"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html"
	xcharset "golang.org/x/net/html/charset"
)

// maxContentRunes bounds the length of a stored plain-text excerpt.
const maxContentRunes = 2000

// maxBodyBytes bounds how much of a feed body we will read.
const maxBodyBytes = 10 << 20

// Feed is a parsed feed with normalized entries.
type Feed struct {
	Title   string
	SiteURL string
	Items   []Item
}

// Item is a single normalized feed entry.
type Item struct {
	GUID        string
	Title       string
	Link        string
	Content     string
	Author      string
	PublishedAt *time.Time
}

// ErrBlockedAddress is returned when a feed fetch is rejected by the SSRF
// guard: the URL uses a scheme other than http/https, or the host resolves
// to a blocked address (loopback, private, link-local, ULA, or multicast).
var ErrBlockedAddress = errors.New("blocked address")

// NewClient returns an *http.Client for fetching feeds. The transport
// validates the resolved address of every connection at dial time and
// refuses to connect to blockedAddr ranges (ErrBlockedAddress), so DNS
// rebinding and multi-record hosts cannot bypass the check. When
// allowPrivate is true (FEED_ALLOW_PRIVATE) the address check is skipped.
func NewClient(timeout time.Duration, allowPrivate bool) *http.Client {
	dialer := &net.Dialer{
		Timeout: 30 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			if allowPrivate {
				return nil
			}
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return fmt.Errorf("dial address %q has no IP", address)
			}
			if blockedAddr(ip) {
				return fmt.Errorf("%s: %w", ip, ErrBlockedAddress)
			}
			return nil
		},
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           dialer.DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}
}

// blockedAddr reports whether ip is a target the SSRF guard rejects:
// loopback (127.0.0.0/8, ::1), RFC1918 private (10.0.0.0/8, 172.16.0.0/12,
// 192.168.0.0/16), ULA (fc00::/7), link-local (169.254.0.0/16, including
// the cloud metadata address 169.254.169.254, and fe80::/10), or multicast
// (224.0.0.0/4, ff00::/8).
func blockedAddr(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast()
}

// Fetch downloads and parses the feed at url.
func Fetch(ctx context.Context, client *http.Client, url, userAgent string) (*Feed, error) {
	u, err := neturl.Parse(url)
	if err != nil {
		return nil, fmt.Errorf("parse feed url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("feed url %q: unsupported scheme %q: %w", url, u.Scheme, ErrBlockedAddress)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept",
		"application/rss+xml, application/atom+xml, application/rdf+xml, application/xml, text/xml, */*")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch feed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch feed: unexpected status %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("read feed body: %w", err)
	}
	body = gunzipIfGzipped(body, resp.Header.Get("Content-Encoding"))

	feed, err := Parse(body)
	if err != nil {
		return nil, fmt.Errorf("parse feed: %w", err)
	}
	return feed, nil
}

// Parse turns raw feed bytes into a normalized Feed.
func Parse(data []byte) (*Feed, error) {
	text := sanitizeXMLRefs(string(data))

	root, err := parseTree(bytes.NewReader([]byte(text)))
	if err != nil {
		return nil, err
	}

	var doc *node
	for _, c := range root.kids {
		if c.name == "rss" || c.name == "feed" {
			doc = c
			break
		}
	}
	if doc == nil {
		return nil, fmt.Errorf("no <rss> or <feed> element found")
	}
	return parseDoc(doc), nil
}

func parseDoc(doc *node) *Feed {
	feed := &Feed{}
	if doc.name == "rss" {
		channel := doc.first("channel")
		if channel == nil {
			return feed
		}
		feed.Title = channel.textOf("title")
		feed.SiteURL = channel.textOf("link")
		for _, it := range channel.all("item") {
			feed.Items = append(feed.Items, parseRSSItem(it))
		}
		return feed
	}

	// Atom
	feed.Title = doc.textOf("title")
	feed.SiteURL = atomLink(doc)
	for _, e := range doc.all("entry") {
		feed.Items = append(feed.Items, parseAtomEntry(e))
	}
	return feed
}

func parseRSSItem(it *node) Item {
	var item Item
	item.Title = it.textOf("title")
	item.Link = it.textOf("link")
	item.GUID = it.textOf("guid")
	if item.GUID == "" {
		item.GUID = item.Link
	}

	item.Author = it.textOf("creator") // dc:creator
	if item.Author == "" {
		item.Author = cleanRSSAuthor(it.textOf("author"))
	}

	content := it.textOf("encoded") // content:encoded
	if content == "" {
		content = it.textOf("description")
	}
	item.Content = toPlainText(content)
	item.PublishedAt = parseDate(it.textOf("pubDate"))

	if item.GUID == "" {
		item.GUID = fallbackGUID(item.Link, item.Title, item.PublishedAt)
	}
	return item
}

func parseAtomEntry(e *node) Item {
	var item Item
	item.Title = e.textOf("title")
	item.Link = atomLink(e)
	item.GUID = e.textOf("id")
	if item.GUID == "" {
		item.GUID = item.Link
	}

	for _, a := range e.all("author") {
		if n := a.first("name"); n != nil {
			item.Author = strings.TrimSpace(n.fullText())
			break
		}
	}

	content := e.textOf("content")
	if content == "" {
		content = e.textOf("summary")
	}
	item.Content = toPlainText(content)
	item.PublishedAt = parseDate(e.textOf("published"))
	if item.PublishedAt == nil {
		item.PublishedAt = parseDate(e.textOf("updated"))
	}

	if item.GUID == "" {
		item.GUID = fallbackGUID(item.Link, item.Title, item.PublishedAt)
	}
	return item
}

// atomLink returns the href of the entry/feed's alternate link, falling back
// to the first link present.
func atomLink(n *node) string {
	var fallback string
	for _, l := range n.all("link") {
		href := l.attrs["href"]
		if href == "" {
			continue
		}
		if rel := l.attrs["rel"]; rel == "" || rel == "alternate" {
			return href
		}
		if fallback == "" {
			fallback = href
		}
	}
	return fallback
}

// cleanRSSAuthor handles RSS <author> values of the form "Name (email)".
func cleanRSSAuthor(s string) string {
	if s == "" {
		return ""
	}
	// "Name (email)"
	if i := strings.Index(s, " ("); i > 0 {
		if name := strings.TrimSpace(s[:i]); !strings.Contains(name, "@") {
			return name
		}
	}
	// "Name <email>"
	if i := strings.Index(s, " <"); i > 0 {
		if name := strings.TrimSpace(s[:i]); !strings.Contains(name, "@") {
			return name
		}
	}
	return s
}

func fallbackGUID(link, title string, pub *time.Time) string {
	var b strings.Builder
	b.WriteString(link)
	b.WriteByte(0)
	b.WriteString(title)
	b.WriteByte(0)
	if pub != nil {
		b.WriteString(pub.UTC().Format(time.RFC3339))
	}
	sum := sha1.Sum([]byte(b.String()))
	return "gen-" + hex.EncodeToString(sum[:])
}

var dateLayouts = []string{
	time.RFC3339,
	time.RFC3339Nano,
	"Mon, 02 Jan 2006 15:04:05 MST",
	"Mon, 02 Jan 2006 15:04:05 -0700",
	"Mon, 02 Jan 2006 15:04:05",
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05Z07:00",
	"2006-01-02 15:04:05",
	"2006-01-02",
}

func parseDate(s string) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return &t
		}
	}
	return nil
}

// toPlainText strips HTML tags and entities from a feed body down to a
// whitespace-normalized plain-text excerpt.
func toPlainText(src string) string {
	s := reBlock.ReplaceAllString(src, " ")
	s = reTag.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = reWhitespace.ReplaceAllString(strings.TrimSpace(s), " ")
	if utf8.RuneCountInString(s) > maxContentRunes {
		runes := []rune(s)[:maxContentRunes]
		s = string(runes) + "…"
	}
	return s
}

var (
	reBlock      = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script\s*>|<style\b[^>]*>.*?</style\s*>`)
	reTag        = regexp.MustCompile(`(?s)<[^>]+>`)
	reWhitespace = regexp.MustCompile(`[\s\p{Zs}]+`)
)

// htmlEntityPairs maps common HTML named entities to numeric references so
// they survive XML decoding and render readably in plain text.
var htmlEntityPairs = [][2]string{
	{"&nbsp;", "&#160;"},
	{"&ndash;", "&#8211;"},
	{"&mdash;", "&#8212;"},
	{"&hellip;", "&#8230;"},
	{"&lsquo;", "&#8216;"},
	{"&rsquo;", "&#8217;"},
	{"&ldquo;", "&#8220;"},
	{"&rdquo;", "&#8221;"},
	{"&copy;", "&#169;"},
	{"&reg;", "&#174;"},
	{"&trade;", "&#8482;"},
	{"&deg;", "&#176;"},
	{"&plusmn;", "&#177;"},
	{"&frac12;", "&#189;"},
	{"&cent;", "&#162;"},
	{"&pound;", "&#163;"},
	{"&euro;", "&#8364;"},
	{"&yen;", "&#165;"},
}

// reEntityCandidate matches an ampersand followed by a run of entity
// characters (letters, digits, #) and an optional terminating semicolon. Each
// candidate is then classified as a valid XML reference or escaped.
var reEntityCandidate = regexp.MustCompile(`&[#0-9a-zA-Z]*;?`)

// xmlNamedRefs are the predefined XML character references.
var xmlNamedRefs = map[string]bool{
	"&amp;": true, "&lt;": true, "&gt;": true, "&quot;": true, "&apos;": true,
}

// sanitizeXMLRefs makes a raw feed body parseable by the XML decoder: common
// HTML entities are turned into numeric references, and any ampersand that does
// not begin a valid XML reference is escaped into a literal ampersand.
func sanitizeXMLRefs(s string) string {
	for _, p := range htmlEntityPairs {
		s = strings.ReplaceAll(s, p[0], p[1])
	}
	return reEntityCandidate.ReplaceAllStringFunc(s, func(m string) string {
		if isValidXMLRef(m) {
			return m
		}
		return "&amp;" + m[1:]
	})
}

// isValidXMLRef reports whether m is a well-formed XML character reference.
func isValidXMLRef(m string) bool {
	if xmlNamedRefs[m] {
		return true
	}
	if !strings.HasPrefix(m, "&#") {
		return false
	}
	rest := strings.TrimSuffix(m[2:], ";")
	if rest == "" {
		return false
	}
	if rest[0] == 'x' || rest[0] == 'X' {
		if len(rest) < 2 {
			return false
		}
		for i := 1; i < len(rest); i++ {
			if !isHexDigit(rest[i]) {
				return false
			}
		}
		return true
	}
	for i := 0; i < len(rest); i++ {
		if rest[i] < '0' || rest[i] > '9' {
			return false
		}
	}
	return true
}

func isHexDigit(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// node is a lightweight, namespace-agnostic XML element.
type node struct {
	name  string
	attrs map[string]string
	text  string
	kids  []*node
}

// first returns the first direct child with the given local name.
func (n *node) first(name string) *node {
	for _, c := range n.kids {
		if c.name == name {
			return c
		}
	}
	return nil
}

// all returns every direct child with the given local name.
func (n *node) all(name string) []*node {
	var out []*node
	for _, c := range n.kids {
		if c.name == name {
			out = append(out, c)
		}
	}
	return out
}

// textOf returns the trimmed full text of the first child with the given name.
func (n *node) textOf(name string) string {
	c := n.first(name)
	if c == nil {
		return ""
	}
	return strings.TrimSpace(c.fullText())
}

// fullText returns all character data in the element's subtree.
func (n *node) fullText() string {
	var b strings.Builder
	var walk func(nd *node)
	walk = func(nd *node) {
		b.WriteString(nd.text)
		for _, c := range nd.kids {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

// parseTree builds a namespace-agnostic element tree from an XML reader.
func parseTree(r io.Reader) (*node, error) {
	dec := xml.NewDecoder(r)
	dec.Strict = false
	dec.CharsetReader = func(charset string, input io.Reader) (io.Reader, error) {
		return xcharset.NewReader(input, charset)
	}

	root := &node{name: "#root", attrs: map[string]string{}}
	stack := []*node{root}

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			n := &node{name: t.Name.Local, attrs: map[string]string{}}
			for _, a := range t.Attr {
				n.attrs[a.Name.Local] = a.Value
			}
			top := stack[len(stack)-1]
			top.kids = append(top.kids, n)
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
		case xml.CharData:
			stack[len(stack)-1].text += string(t)
		}
	}
	return root, nil
}

func gunzipIfGzipped(body []byte, contentEncoding string) []byte {
	isGz := strings.EqualFold(contentEncoding, "gzip") ||
		(len(body) > 2 && body[0] == 0x1f && body[1] == 0x8b)
	if !isGz {
		return body
	}
	gr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return body
	}
	defer gr.Close()
	out, err := io.ReadAll(io.LimitReader(gr, maxBodyBytes))
	if err != nil {
		return body
	}
	return out
}
