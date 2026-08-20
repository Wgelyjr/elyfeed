package rss

import (
	"strings"
	"testing"
)

func TestParseRSS20(t *testing.T) {
	data := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:content="http://purl.org/rss/1.0/modules/content/">
  <channel>
    <title>Example Blog</title>
    <link>https://example.com</link>
    <item>
      <title>First Post</title>
      <link>https://example.com/posts/1</link>
      <guid isPermaLink="false">tag:example.com,2024:1</guid>
      <dc:creator>Alice</dc:creator>
      <description>&lt;p&gt;A &amp; B &lt;b&gt;bold&lt;/b&gt; &amp;nbsp; text&lt;/p&gt;</description>
      <content:encoded>&lt;script&gt;evil()&lt;/script&gt;&lt;p&gt;Full &lt;em&gt;body&lt;/em&gt; here&lt;/p&gt;</content:encoded>
      <pubDate>Mon, 02 Jan 2024 15:04:05 -0700</pubDate>
    </item>
    <item>
      <title>No GUID Post</title>
      <link>https://example.com/posts/2</link>
      <author>Alice &lt;alice@example.com&gt;</author>
      <description>plain description</description>
    </item>
  </channel>
</rss>`)

	feed, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if feed.Title != "Example Blog" {
		t.Errorf("title = %q, want %q", feed.Title, "Example Blog")
	}
	if feed.SiteURL != "https://example.com" {
		t.Errorf("site url = %q", feed.SiteURL)
	}
	if len(feed.Items) != 2 {
		t.Fatalf("got %d items, want 2", len(feed.Items))
	}

	first := feed.Items[0]
	if first.GUID != "tag:example.com,2024:1" {
		t.Errorf("guid = %q", first.GUID)
	}
	if first.Author != "Alice" {
		t.Errorf("author = %q, want Alice", first.Author)
	}
	if first.Title != "First Post" {
		t.Errorf("title = %q", first.Title)
	}
	if first.PublishedAt == nil {
		t.Error("expected a parsed publish date")
	}
	// content:encoded takes precedence; script stripped; entities unescaped.
	want := "Full body here"
	if first.Content != want {
		t.Errorf("content = %q, want %q", first.Content, want)
	}
	if strings.Contains(first.Content, "evil") {
		t.Error("script content was not stripped")
	}

	second := feed.Items[1]
	if second.GUID == "" {
		t.Error("expected a generated GUID for item without guid/link")
	}
	if second.Author != "Alice" {
		t.Errorf("author = %q, want Alice (parsed from email form)", second.Author)
	}
}

func TestParseAtom(t *testing.T) {
	data := []byte(`<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Atom Example</title>
  <link rel="self" href="https://example.com/atom.xml"/>
  <link rel="alternate" href="https://example.com"/>
  <id>tag:example.com,2024:feed</id>
  <entry>
    <title type="html">&lt;em&gt;Entry One&lt;/em&gt;</title>
    <link rel="alternate" href="https://example.com/entry/1"/>
    <id>tag:example.com,2024:entry-1</id>
    <author><name>Bob</name></author>
    <summary>Short summary</summary>
    <content type="html">&lt;p&gt;Longer &lt;strong&gt;content&lt;/strong&gt; body&lt;/p&gt;</content>
    <published>2024-01-02T15:04:05Z</published>
  </entry>
</feed>`)

	feed, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if feed.Title != "Atom Example" {
		t.Errorf("title = %q", feed.Title)
	}
	if feed.SiteURL != "https://example.com" {
		t.Errorf("site url = %q, want alternate link", feed.SiteURL)
	}
	if len(feed.Items) != 1 {
		t.Fatalf("got %d items, want 1", len(feed.Items))
	}
	it := feed.Items[0]
	if it.GUID != "tag:example.com,2024:entry-1" {
		t.Errorf("guid = %q", it.GUID)
	}
	if it.Author != "Bob" {
		t.Errorf("author = %q", it.Author)
	}
	if it.Link != "https://example.com/entry/1" {
		t.Errorf("link = %q", it.Link)
	}
	if it.PublishedAt == nil {
		t.Error("expected published date")
	}
	// content preferred over summary, tags stripped.
	if it.Content != "Longer content body" {
		t.Errorf("content = %q", it.Content)
	}
}

func TestToPlainText(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{`<p>Hello&nbsp;world</p>`, "Hello world"},
		{`<script>x()</script><div>Keep</div>`, "Keep"},
		{`Line1<br/>Line2`, "Line1 Line2"},
		{`a &amp; b &#8212; c`, "a & b — c"},
	}
	for _, c := range cases {
		if got := toPlainText(c.in); got != c.want {
			t.Errorf("toPlainText(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSanitizeXMLRefs(t *testing.T) {
	in := `<description>AT&amp;T &nbsp; &unknown; ok</description>`
	got := sanitizeXMLRefs(in)
	// &nbsp; -> numeric, &unknown; -> escaped ampersand, &amp; preserved.
	if got != `<description>AT&amp;T &#160; &amp;unknown; ok</description>` {
		t.Errorf("sanitize = %q", got)
	}
}

