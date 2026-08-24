package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"elyfeed/internal/store"
)

// --- in-memory stub store ---

type stubStore struct {
	feeds       []store.Feed
	collections []store.Collection
	collFeeds   map[int64][]int64 // collection ID -> feed IDs
	items       []store.Item
}

var _ store.Store = (*stubStore)(nil)

func (m *stubStore) CreateFeed(context.Context, string, string, string) (*store.Feed, error) {
	panic("not implemented")
}

func (m *stubStore) ListFeeds(context.Context) ([]store.Feed, error) {
	return m.feeds, nil
}

func (m *stubStore) DeleteFeed(context.Context, int64) error {
	panic("not implemented")
}

func (m *stubStore) DeleteFeeds(context.Context, []int64) (int, error) {
	panic("not implemented")
}

func (m *stubStore) TouchFeed(context.Context, int64) error {
	panic("not implemented")
}

func (m *stubStore) SetFeedCollections(context.Context, int64, []int64) error {
	panic("not implemented")
}

func (m *stubStore) CreateCollection(context.Context, string) (*store.Collection, error) {
	panic("not implemented")
}

func (m *stubStore) GetCollection(_ context.Context, id int64) (*store.Collection, error) {
	for i := range m.collections {
		if m.collections[i].ID == id {
			c := m.collections[i]
			c.FeedCount = len(m.collFeeds[id])
			return &c, nil
		}
	}
	return nil, store.ErrNotFound
}

func (m *stubStore) ListCollections(context.Context) ([]store.Collection, error) {
	return m.collections, nil
}

func (m *stubStore) RenameCollection(context.Context, int64, string) (*store.Collection, error) {
	panic("not implemented")
}

func (m *stubStore) DeleteCollection(context.Context, int64) error {
	panic("not implemented")
}

func (m *stubStore) UpsertItems(context.Context, int64, []store.IncomingItem) (int, error) {
	panic("not implemented")
}

func (m *stubStore) feedInCollection(feedID, collID int64) bool {
	for _, fid := range m.collFeeds[collID] {
		if fid == feedID {
			return true
		}
	}
	return false
}

// itemTime returns the effective timestamp for an item: its publication date,
// falling back to the fetch time when the feed provides none.
func stubItemTime(it store.Item) time.Time {
	if it.PublishedAt != nil {
		return *it.PublishedAt
	}
	return it.FetchedAt
}

// ListItems mirrors the Postgres implementation's filter and ordering rules.
func (m *stubStore) ListItems(_ context.Context, q store.ItemQuery) ([]store.Item, int, error) {
	matched := make([]store.Item, 0)
	for _, it := range m.items {
		if q.FeedID != nil && it.FeedID != *q.FeedID {
			continue
		}
		if q.CollectionID != nil && !m.feedInCollection(it.FeedID, *q.CollectionID) {
			continue
		}
		if q.Unread != nil && it.Read != !*q.Unread {
			continue
		}
		t := stubItemTime(it)
		if q.Since != nil && t.Before(*q.Since) {
			continue
		}
		if q.Until != nil && t.After(*q.Until) {
			continue
		}
		matched = append(matched, it)
	}
	sort.Slice(matched, func(i, j int) bool {
		ni, nj := matched[i].PublishedAt == nil, matched[j].PublishedAt == nil
		if ni != nj {
			return !ni // dated items first
		}
		if !ni && !nj && !matched[i].PublishedAt.Equal(*matched[j].PublishedAt) {
			return matched[i].PublishedAt.After(*matched[j].PublishedAt)
		}
		return matched[i].FetchedAt.After(matched[j].FetchedAt)
	})
	total := len(matched)
	if q.Offset > 0 {
		if q.Offset >= total {
			return []store.Item{}, total, nil
		}
		matched = matched[q.Offset:]
	}
	if q.Limit > 0 && len(matched) > q.Limit {
		matched = matched[:q.Limit]
	}
	return matched, total, nil
}

func (m *stubStore) SetItemRead(context.Context, int64, bool) error {
	panic("not implemented")
}

func (m *stubStore) SetItemsRead(context.Context, []int64, bool) (int, error) {
	panic("not implemented")
}

func (m *stubStore) UnreadCount(context.Context) (int, error) {
	panic("not implemented")
}

// --- test fixture ---

var (
	ts2026_08_20 = time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	ts2026_08_21 = time.Date(2026, 8, 21, 10, 0, 0, 0, time.UTC)
	ts2026_08_22 = time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC)
)

func ts(t *testing.T, v time.Time) *time.Time { t.Helper(); return &v }

func newTestServer(t *testing.T) (http.Handler, *stubStore) {
	t.Helper()
	m := &stubStore{
		feeds: []store.Feed{
			{ID: 1, URL: "https://a.example/rss", Title: "Feed A"},
			{ID: 2, URL: "https://b.example/rss", Title: "Feed B"},
		},
		collections: []store.Collection{{ID: 1, Name: "News"}},
		collFeeds:   map[int64][]int64{1: {1, 2}},
		items: []store.Item{
			{ID: 1, FeedID: 1, FeedTitle: "Feed A", Title: "A one", Link: "https://a.example/1",
				Content: strings.Repeat("word ", 100), Author: "alice", PublishedAt: ts(t, ts2026_08_22)},
			{ID: 2, FeedID: 1, FeedTitle: "Feed A", Title: "A two", Link: "https://a.example/2",
				Content: "old post", PublishedAt: ts(t, ts2026_08_21)},
			{ID: 3, FeedID: 2, FeedTitle: "Feed B", Title: "B one", Link: "https://b.example/1",
				Content: "undated post\nsecond line", FetchedAt: ts2026_08_22}, // no published_at
			{ID: 4, FeedID: 2, FeedTitle: "Feed B", Title: "B two", Link: "https://b.example/2",
				Content: "very old", PublishedAt: ts(t, ts2026_08_20)},
		},
	}
	return New(m, nil, fstest.MapFS{}), m
}

func do(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

// --- /api/items time filters ---

func TestListItemsSinceUntil(t *testing.T) {
	h, _ := newTestServer(t)

	rec := do(t, h, "GET", "/api/items?since=2026-08-22T00:00:00Z&until=2026-08-22T23:59:59Z")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []store.Item `json:"items"`
		Total int          `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// A one (published 08-22) and B one (undated, fetched 08-22) qualify.
	if body.Total != 2 || len(body.Items) != 2 {
		t.Fatalf("total = %d, items = %d, want 2/2", body.Total, len(body.Items))
	}
	got := map[string]bool{}
	for _, it := range body.Items {
		got[it.Title] = true
	}
	if !got["A one"] || !got["B one"] {
		t.Fatalf("items = %v, want A one and B one", got)
	}
}

func TestListItemsInvalidSince(t *testing.T) {
	h, _ := newTestServer(t)

	for _, target := range []string{
		"/api/items?since=yesterday",
		"/api/items?until=2026-13-99T00:00:00Z",
	} {
		rec := do(t, h, "GET", target)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400", target, rec.Code)
		}
	}
}

// --- /api/digest ---

func TestDigestMarkdown(t *testing.T) {
	h, _ := newTestServer(t)

	rec := do(t, h, "GET", "/api/digest?collection_id=1&since=2026-08-22T00:00:00Z&until=2026-08-22T23:59:59Z")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/markdown; charset=utf-8" {
		t.Fatalf("content-type = %q", ct)
	}
	out := rec.Body.String()
	for _, want := range []string{
		"# Digest — News (",
		"## Feed A",
		"## Feed B",
		"- [A one](https://a.example/1) — alice, 2026-08-22 10:00 UTC",
		"- [B one](https://b.example/1), 2026-08-22 10:00 UTC",
		"> undated post second line", // newlines in content are collapsed
		"…",                          // long content excerpt is truncated
	} {
		if !strings.Contains(out, want) {
			t.Errorf("digest missing %q:\n%s", want, out)
		}
	}
	for _, absent := range []string{"A two", "B two", "_No items", "undated post\nsecond line"} {
		if strings.Contains(out, absent) {
			t.Errorf("digest unexpectedly contains %q:\n%s", absent, out)
		}
	}
}

func TestDigestEmptyWindow(t *testing.T) {
	h, _ := newTestServer(t)

	rec := do(t, h, "GET", "/api/digest?collection_id=1&since=2026-08-23T00:00:00Z&until=2026-08-24T00:00:00Z")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "_No items in this window._") {
		t.Fatalf("digest = %q, want empty-window marker", rec.Body.String())
	}
}

func TestDigestJSON(t *testing.T) {
	h, _ := newTestServer(t)

	rec := do(t, h, "GET", "/api/digest?collection_id=1&since=2026-08-22T00:00:00Z&until=2026-08-22T23:59:59Z&format=json")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Collection string       `json:"collection"`
		Since      string       `json:"since"`
		Until      string       `json:"until"`
		Count      int          `json:"count"`
		Items      []store.Item `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Collection != "News" || body.Count != 2 || len(body.Items) != 2 {
		t.Fatalf("body = %+v, want collection News with 2 items", body)
	}
}

func TestDigestValidation(t *testing.T) {
	h, _ := newTestServer(t)

	cases := []struct {
		target string
		want   int
	}{
		{"/api/digest", http.StatusBadRequest},                            // missing collection_id
		{"/api/digest?collection_id=abc", http.StatusBadRequest},          // not an integer
		{"/api/digest?collection_id=999", http.StatusNotFound},            // unknown collection
		{"/api/digest?collection_id=1&format=xml", http.StatusBadRequest}, // bad format
		{"/api/digest?collection_id=1&since=nope", http.StatusBadRequest}, // bad timestamp
	}
	for _, tc := range cases {
		rec := do(t, h, "GET", tc.target)
		if rec.Code != tc.want {
			t.Fatalf("%s: status = %d, want %d (%s)", tc.target, rec.Code, tc.want, rec.Body.String())
		}
	}
}

// --- /api index ---

func TestAPIIndex(t *testing.T) {
	h, _ := newTestServer(t)

	rec := do(t, h, "GET", "/api/")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Name      string `json:"name"`
		Endpoints []struct {
			Method string `json:"method"`
			Path   string `json:"path"`
		} `json:"endpoints"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Name != "elyfeed" {
		t.Fatalf("name = %q", body.Name)
	}
	found := map[string]bool{}
	for _, e := range body.Endpoints {
		found[e.Method+" "+e.Path] = true
	}
	for _, want := range []string{
		"GET /api/digest",
		"GET /api/items",
		"GET /api/collections",
	} {
		if !found[want] {
			t.Errorf("index missing %q", want)
		}
	}
}

func TestAPIIndexBarePathRedirects(t *testing.T) {
	h, _ := newTestServer(t)

	rec := do(t, h, "GET", "/api")
	if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/api/" {
		t.Fatalf("status = %d, location = %q, want 301 -> /api/", rec.Code, rec.Header().Get("Location"))
	}
}

func TestAPIIndexUnknownPath404(t *testing.T) {
	h, _ := newTestServer(t)

	rec := do(t, h, "GET", "/api/nope")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
