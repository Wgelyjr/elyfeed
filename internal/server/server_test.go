package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"elyfeed/internal/auth"
	"elyfeed/internal/store"
)

// --- in-memory stub store ---

type stubStore struct {
	feeds       []store.Feed
	collections []store.Collection
	collFeeds   map[int64][]int64 // collection ID -> feed IDs
	items       []store.Item

	users         []store.User
	sessions      map[string]*store.Session
	verifications map[string]stubVerification
}

type stubVerification struct {
	email     string
	purpose   string
	expiresAt time.Time
}

var _ store.Store = (*stubStore)(nil)

func (m *stubStore) CreateFeed(context.Context, int64, string, string, string) (*store.Feed, error) {
	panic("not implemented")
}

func (m *stubStore) ListFeeds(context.Context, int64) ([]store.Feed, error) {
	return m.feeds, nil
}

func (m *stubStore) ListAllFeeds(context.Context) ([]store.Feed, error) {
	return m.feeds, nil
}

func (m *stubStore) DeleteFeed(context.Context, int64, int64) error {
	panic("not implemented")
}

func (m *stubStore) DeleteFeeds(context.Context, int64, []int64) (int, error) {
	panic("not implemented")
}

func (m *stubStore) TouchFeed(context.Context, int64, int64) error {
	panic("not implemented")
}

func (m *stubStore) SetFeedCollections(context.Context, int64, int64, []int64) error {
	panic("not implemented")
}

func (m *stubStore) CreateCollection(context.Context, int64, string) (*store.Collection, error) {
	panic("not implemented")
}

func (m *stubStore) GetCollection(_ context.Context, _, id int64) (*store.Collection, error) {
	for i := range m.collections {
		if m.collections[i].ID == id {
			c := m.collections[i]
			c.FeedCount = len(m.collFeeds[id])
			return &c, nil
		}
	}
	return nil, store.ErrNotFound
}

func (m *stubStore) ListCollections(context.Context, int64) ([]store.Collection, error) {
	return m.collections, nil
}

func (m *stubStore) RenameCollection(context.Context, int64, int64, string) (*store.Collection, error) {
	panic("not implemented")
}

func (m *stubStore) DeleteCollection(context.Context, int64, int64) error {
	panic("not implemented")
}

func (m *stubStore) AddFeedsToCollection(context.Context, int64, int64, []int64) (int, error) {
	return 0, nil
}

func (m *stubStore) CreateCollectionShare(context.Context, int64, int64, string) error {
	return nil
}

func (m *stubStore) GetCollectionShare(context.Context, string) (*store.CollectionShare, error) {
	return nil, store.ErrNotFound
}

func (m *stubStore) SetCollectionVisibilityRequest(context.Context, int64, int64, string) (*store.Collection, error) {
	return nil, store.ErrNotFound
}

func (m *stubStore) ListPendingCollectionShares(context.Context) ([]store.CollectionShareRequest, error) {
	return []store.CollectionShareRequest{}, nil
}

func (m *stubStore) ResolveCollectionShare(context.Context, int64, bool) (*store.Collection, error) {
	return nil, store.ErrNotFound
}

func (m *stubStore) CancelCollectionVisibilityRequest(context.Context, int64, int64) (*store.Collection, error) {
	return nil, store.ErrNotFound
}

func (m *stubStore) ListPublicCollections(context.Context) ([]store.PublicCollection, error) {
	return []store.PublicCollection{}, nil
}

func (m *stubStore) GetPublicCollectionForImport(context.Context, int64) (*store.CollectionShare, error) {
	return nil, store.ErrNotFound
}

func (m *stubStore) ListRecommendedFeeds(context.Context) ([]store.RecommendedFeed, error) {
	return []store.RecommendedFeed{}, nil
}

func (m *stubStore) CreateRecommendedFeed(context.Context, string, string, string) (*store.RecommendedFeed, error) {
	return &store.RecommendedFeed{ID: 1}, nil
}

func (m *stubStore) DeleteRecommendedFeed(context.Context, int64) error {
	return nil
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
func (m *stubStore) ListItems(_ context.Context, _ int64, q store.ItemQuery) ([]store.Item, int, error) {
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

func (m *stubStore) SetItemRead(context.Context, int64, int64, bool) error {
	panic("not implemented")
}

func (m *stubStore) SetItemsRead(context.Context, int64, []int64, bool) (int, error) {
	panic("not implemented")
}

func (m *stubStore) UnreadCount(context.Context, int64) (int, error) {
	panic("not implemented")
}

func (m *stubStore) AssignLegacyData(context.Context, int64) (int, int, error) {
	return 0, 0, nil
}

// --- auth stubs ---

func (m *stubStore) CreateUser(_ context.Context, email, displayName, passwordHash string) (*store.User, error) {
	for _, u := range m.users {
		if strings.EqualFold(u.Email, email) {
			return nil, store.ErrConflict
		}
	}
	u := store.User{ID: int64(len(m.users) + 1), Email: email, DisplayName: displayName,
		PasswordHash: passwordHash, Role: "user", CreatedAt: time.Now().UTC()}
	m.users = append(m.users, u)
	return &u, nil
}

func (m *stubStore) GetUserByEmail(_ context.Context, email string) (*store.User, error) {
	for i := range m.users {
		if strings.EqualFold(m.users[i].Email, email) {
			u := m.users[i]
			return &u, nil
		}
	}
	return nil, store.ErrNotFound
}

func (m *stubStore) GetUser(_ context.Context, id int64) (*store.User, error) {
	for i := range m.users {
		if m.users[i].ID == id {
			u := m.users[i]
			return &u, nil
		}
	}
	return nil, store.ErrNotFound
}

func (m *stubStore) SetUserPasswordHash(_ context.Context, id int64, passwordHash string) error {
	for i := range m.users {
		if m.users[i].ID == id {
			m.users[i].PasswordHash = passwordHash
			return nil
		}
	}
	return store.ErrNotFound
}

func (m *stubStore) ActivateUser(_ context.Context, id int64) (bool, error) {
	verified := 0
	for _, u := range m.users {
		if u.EmailVerified {
			verified++
		}
	}
	for i := range m.users {
		if m.users[i].ID == id {
			first := verified == 0
			m.users[i].EmailVerified = true
			if first {
				m.users[i].Role = "admin"
			}
			return first, nil
		}
	}
	return false, store.ErrNotFound
}

func (m *stubStore) CreateSession(_ context.Context, tokenHash string, userID int64, expiresAt time.Time) error {
	m.sessions[tokenHash] = &store.Session{TokenHash: tokenHash, UserID: userID,
		CreatedAt: time.Now().UTC(), ExpiresAt: expiresAt}
	return nil
}

func (m *stubStore) GetSession(_ context.Context, tokenHash string) (*store.Session, error) {
	if s, ok := m.sessions[tokenHash]; ok {
		return s, nil
	}
	return nil, store.ErrNotFound
}

func (m *stubStore) DeleteSession(_ context.Context, tokenHash string) error {
	delete(m.sessions, tokenHash)
	return nil
}

func (m *stubStore) CreateEmailVerification(_ context.Context, email, tokenHash, purpose string, expiresAt time.Time) error {
	m.verifications[tokenHash] = stubVerification{email: email, purpose: purpose, expiresAt: expiresAt}
	return nil
}

func (m *stubStore) ConsumeEmailVerification(_ context.Context, tokenHash, purpose string) (string, bool, error) {
	v, ok := m.verifications[tokenHash]
	if !ok || v.purpose != purpose || time.Now().After(v.expiresAt) {
		return "", false, nil
	}
	delete(m.verifications, tokenHash)
	return v.email, true, nil
}

// --- recording mailer ---

type mailMessage struct {
	to, subject, body string
}

type recordingMailer struct {
	messages []mailMessage
}

func (m *recordingMailer) Send(to, subject, body string) error {
	m.messages = append(m.messages, mailMessage{to: to, subject: subject, body: body})
	return nil
}

// tokenFromEmail extracts the token query parameter from the first link in an
// email body.
func tokenFromEmail(t *testing.T, body string) string {
	t.Helper()
	const marker = "?token="
	idx := strings.Index(body, marker)
	if idx < 0 {
		t.Fatalf("email body contains no token link: %q", body)
	}
	end := idx + len(marker)
	for end < len(body) {
		switch body[end] {
		case ' ', '\t', '\r', '\n', '"', '\'', '<':
			return body[idx+len(marker) : end]
		}
		end++
	}
	return body[idx+len(marker):]
}

// --- test fixture ---

var (
	ts2026_08_20 = time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	ts2026_08_21 = time.Date(2026, 8, 21, 10, 0, 0, 0, time.UTC)
	ts2026_08_22 = time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC)
)

func ts(t *testing.T, v time.Time) *time.Time { t.Helper(); return &v }

// testSessionToken is the raw session token for the fixture user's session.
const testSessionToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// newTestServer builds a handler with a logged-in fixture user (ID 1, admin,
// verified) and a pre-seeded session, so data endpoints are reachable via
// doAuth.
func newTestServer(t *testing.T) (http.Handler, *stubStore, *recordingMailer) {
	t.Helper()
	mail := &recordingMailer{}
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
		users: []store.User{{
			ID: 1, Email: "admin@example.com", DisplayName: "Admin", Role: "admin",
			EmailVerified: true, CreatedAt: ts2026_08_20,
		}},
		sessions:      map[string]*store.Session{},
		verifications: map[string]stubVerification{},
	}
	hash := auth.HashToken(testSessionToken)
	m.sessions[hash] = &store.Session{TokenHash: hash, UserID: 1,
		CreatedAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(24 * time.Hour)}
	a := auth.NewService(m, mail, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), "", time.Hour, false)
	return New(m, nil, fstest.MapFS{}, a), m, mail
}

// do issues a request without a session cookie.
func do(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

// doAuth issues a request carrying the fixture user's session cookie.
func doAuth(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: testSessionToken})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// doJSON issues a JSON-body request without a session cookie.
func doJSON(t *testing.T, h http.Handler, method, target string, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(method, target, strings.NewReader(string(data)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// doAuthJSON issues a JSON-body request carrying the fixture user's session
// cookie.
func doAuthJSON(t *testing.T, h http.Handler, method, target string, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(method, target, strings.NewReader(string(data)))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: testSessionToken})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// --- /api/items time filters ---

func TestListItemsSinceUntil(t *testing.T) {
	h, _, _ := newTestServer(t)

	rec := doAuth(t, h, "GET", "/api/items?since=2026-08-22T00:00:00Z&until=2026-08-22T23:59:59Z")
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
	h, _, _ := newTestServer(t)

	for _, target := range []string{
		"/api/items?since=yesterday",
		"/api/items?until=2026-13-99T00:00:00Z",
	} {
		rec := doAuth(t, h, "GET", target)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400", target, rec.Code)
		}
	}
}

// --- /api/digest ---

func TestDigestMarkdown(t *testing.T) {
	h, _, _ := newTestServer(t)

	rec := doAuth(t, h, "GET", "/api/digest?collection_id=1&since=2026-08-22T00:00:00Z&until=2026-08-22T23:59:59Z")
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
	h, _, _ := newTestServer(t)

	rec := doAuth(t, h, "GET", "/api/digest?collection_id=1&since=2026-08-23T00:00:00Z&until=2026-08-24T00:00:00Z")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "_No items in this window._") {
		t.Fatalf("digest = %q, want empty-window marker", rec.Body.String())
	}
}

func TestDigestJSON(t *testing.T) {
	h, _, _ := newTestServer(t)

	rec := doAuth(t, h, "GET", "/api/digest?collection_id=1&since=2026-08-22T00:00:00Z&until=2026-08-22T23:59:59Z&format=json")
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
	h, _, _ := newTestServer(t)

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
		rec := doAuth(t, h, "GET", tc.target)
		if rec.Code != tc.want {
			t.Fatalf("%s: status = %d, want %d (%s)", tc.target, rec.Code, tc.want, rec.Body.String())
		}
	}
}

// --- /api index ---

func TestAPIIndex(t *testing.T) {
	h, _, _ := newTestServer(t)

	rec := do(t, h, "GET", "/api/")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Name      string `json:"name"`
		User      any    `json:"user"`
		Endpoints []struct {
			Method string `json:"method"`
			Path   string `json:"path"`
			Auth   bool   `json:"auth"`
		} `json:"endpoints"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Name != "elyfeed" {
		t.Fatalf("name = %q", body.Name)
	}
	if body.User != nil {
		t.Errorf("user = %v, want null when logged out", body.User)
	}
	found := map[string]bool{}
	authed := map[string]bool{}
	for _, e := range body.Endpoints {
		found[e.Method+" "+e.Path] = true
		authed[e.Method+" "+e.Path] = e.Auth
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
	for ep, wantAuth := range map[string]bool{
		"GET /api/feeds":          true,
		"POST /api/refresh":       true,
		"GET /api/auth/me":        true,
		"POST /api/auth/login":    false,
		"POST /api/auth/register": false,
		"GET /api/auth/oidc":      false,
		"GET /api/auth/verify":    false,
		"POST /api/auth/logout":   false,
	} {
		if got := authed[ep]; got != wantAuth {
			t.Errorf("auth flag for %q = %v, want %v", ep, got, wantAuth)
		}
	}
}

func TestAPIIndexUserWhenAuthenticated(t *testing.T) {
	h, _, _ := newTestServer(t)

	rec := doAuth(t, h, "GET", "/api/")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		User *store.User `json:"user"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.User == nil {
		t.Fatalf("user is null, want the authenticated user")
	}
	if body.User.Email != "admin@example.com" || body.User.DisplayName != "Admin" {
		t.Errorf("user = %+v, want admin@example.com / Admin", body.User)
	}
}

func TestAPIIndexBarePathRedirects(t *testing.T) {
	h, _, _ := newTestServer(t)

	rec := do(t, h, "GET", "/api")
	if rec.Code != http.StatusTemporaryRedirect || rec.Header().Get("Location") != "/api/" {
		t.Fatalf("status = %d, location = %q, want 307 -> /api/", rec.Code, rec.Header().Get("Location"))
	}
}

func TestAPIIndexUnknownPath404(t *testing.T) {
	h, _, _ := newTestServer(t)

	rec := do(t, h, "GET", "/api/nope")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// --- security hardening ---

func TestSecurityHeaders(t *testing.T) {
	h, _, _ := newTestServer(t)

	// API (JSON), authenticated API, and SPA (404: the test FS has no assets)
	// responses all carry the headers.
	recs := []*httptest.ResponseRecorder{
		do(t, h, "GET", "/api/"),
		doAuth(t, h, "GET", "/api/feeds"),
		do(t, h, "GET", "/"),
	}
	for _, rec := range recs {
		if got := rec.Header().Get("Content-Security-Policy"); got != contentSecurityPolicy {
			t.Errorf("Content-Security-Policy = %q, want %q", got, contentSecurityPolicy)
		}
		if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
		}
		if got := rec.Header().Get("Referrer-Policy"); got != "no-referrer" {
			t.Errorf("Referrer-Policy = %q, want no-referrer", got)
		}
	}
}

func TestNoCORSHeaders(t *testing.T) {
	h, _, _ := newTestServer(t)

	for _, rec := range []*httptest.ResponseRecorder{
		do(t, h, "GET", "/api/"),
		do(t, h, "GET", "/"),
	} {
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("Access-Control-Allow-Origin = %q, want absent", got)
		}
	}
}

func TestRequestBodyTooLarge(t *testing.T) {
	h, _, _ := newTestServer(t)

	body := map[string]string{
		"email":   "u@example.com",
		"padding": strings.Repeat("x", int(maxRequestBodyBytes)+1),
	}
	rec := doJSON(t, h, "POST", "/api/auth/register", body)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413: %s", rec.Code, rec.Body.String())
	}
}

func TestMutatingRequiresJSONContentType(t *testing.T) {
	h, _, _ := newTestServer(t)

	// No content type at all (the SPA always sends one).
	if rec := do(t, h, "POST", "/api/refresh"); rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("no content type: status = %d, want 415: %s", rec.Code, rec.Body.String())
	}
	// A non-JSON content type.
	req := httptest.NewRequest("POST", "/api/collections", strings.NewReader(`{"name":"x"}`))
	req.Header.Set("Content-Type", "text/plain")
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: testSessionToken})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("text/plain: status = %d, want 415: %s", rec.Code, rec.Body.String())
	}
	// A no-body POST with the JSON content type is accepted (logout, logged
	// out: still 200).
	req = httptest.NewRequest("POST", "/api/auth/logout", nil)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("no-body JSON POST: status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

func TestGetWithoutContentTypeStillWorks(t *testing.T) {
	h, _, _ := newTestServer(t)

	if rec := do(t, h, "GET", "/api/feeds"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated GET: status = %d, want 401", rec.Code)
	}
	if rec := doAuth(t, h, "GET", "/api/feeds"); rec.Code != http.StatusOK {
		t.Fatalf("authenticated GET: status = %d, want 200", rec.Code)
	}
	if rec := do(t, h, "GET", "/api/"); rec.Code != http.StatusOK {
		t.Fatalf("api index: status = %d, want 200", rec.Code)
	}
}
