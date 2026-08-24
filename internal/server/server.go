// Package server exposes the HTTP API and serves the embedded frontend.
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"elyfeed/internal/refresh"
	"elyfeed/internal/store"
)

func init() {
	// Serve deterministic content types for the embedded frontend regardless of
	// the host's mime.types table (minimal container images often lack entries).
	mime.AddExtensionType(".webmanifest", "application/manifest+json")
	mime.AddExtensionType(".ico", "image/x-icon")
}

// Server wires the store and refresher behind HTTP handlers.
type Server struct {
	store     store.Store
	refresher *refresh.Refresher
	assets    fs.FS
}

// New builds the HTTP handler: the JSON API under /api and the embedded
// single-page app for every other route.
func New(st store.Store, ref *refresh.Refresher, assets fs.FS) http.Handler {
	s := &Server{store: st, refresher: ref, assets: assets}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/feeds", s.handleListFeeds)
	mux.HandleFunc("POST /api/feeds", s.handleAddFeed)
	mux.HandleFunc("POST /api/feeds/bulk", s.handleAddFeeds)
	mux.HandleFunc("POST /api/feeds/bulk-delete", s.handleBulkDeleteFeeds)
	mux.HandleFunc("PUT /api/feeds/{id}/collections", s.handleSetFeedCollections)
	mux.HandleFunc("DELETE /api/feeds/{id}", s.handleDeleteFeed)
	mux.HandleFunc("GET /api/collections", s.handleListCollections)
	mux.HandleFunc("POST /api/collections", s.handleCreateCollection)
	mux.HandleFunc("PATCH /api/collections/{id}", s.handleRenameCollection)
	mux.HandleFunc("DELETE /api/collections/{id}", s.handleDeleteCollection)
	mux.HandleFunc("GET /api/items", s.handleListItems)
	mux.HandleFunc("GET /api/items/unread-count", s.handleUnreadCount)
	mux.HandleFunc("POST /api/items/{id}/read", s.handleSetItemRead)
	mux.HandleFunc("POST /api/items/bulk-read", s.handleBulkSetItemRead)
	mux.HandleFunc("GET /api/digest", s.handleDigest)
	mux.HandleFunc("POST /api/refresh", s.handleRefresh)
	mux.HandleFunc("GET /api/", s.handleAPIIndex)
	mux.Handle("GET /", spaHandler{assets: assets})

	return withCORS(mux)
}

// --- feeds ---

func (s *Server) handleListFeeds(w http.ResponseWriter, r *http.Request) {
	feeds, err := s.store.ListFeeds(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if feeds == nil {
		feeds = []store.Feed{}
	}
	writeJSON(w, http.StatusOK, feeds)
}

func (s *Server) handleAddFeed(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	u := strings.TrimSpace(req.URL)
	if u == "" {
		writeError(w, http.StatusBadRequest, "url is required")
		return
	}
	parsed, err := url.Parse(u)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		writeError(w, http.StatusBadRequest, "url must be a valid http(s) URL")
		return
	}

	feed, err := s.refresher.AddFeed(r.Context(), u)
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not add feed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, feed)
}

func (s *Server) handleDeleteFeed(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r.PathValue("id"))
	if !ok {
		return
	}
	if err := s.store.DeleteFeed(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleAddFeeds adds several feeds at once (one per URL). Each URL is
// validated and seeded independently; failures are reported per URL.
func (s *Server) handleAddFeeds(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URLs []string `json:"urls"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if len(req.URLs) == 0 {
		writeError(w, http.StatusBadRequest, "urls is required")
		return
	}
	results := s.refresher.AddFeeds(r.Context(), req.URLs)
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

// handleBulkDeleteFeeds removes several feeds by ID.
func (s *Server) handleBulkDeleteFeeds(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs []int64 `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if len(req.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "ids is required")
		return
	}
	n, err := s.store.DeleteFeeds(r.Context(), req.IDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": n})
}

// handleSetFeedCollections replaces the collections a feed belongs to.
func (s *Server) handleSetFeedCollections(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r.PathValue("id"))
	if !ok {
		return
	}
	var req struct {
		CollectionIDs []int64 `json:"collection_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := s.store.SetFeedCollections(r.Context(), id, req.CollectionIDs); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// --- collections ---

func (s *Server) handleListCollections(w http.ResponseWriter, r *http.Request) {
	collections, err := s.store.ListCollections(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if collections == nil {
		collections = []store.Collection{}
	}
	writeJSON(w, http.StatusOK, collections)
}

func (s *Server) handleCreateCollection(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	collection, err := s.store.CreateCollection(r.Context(), name)
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, collection)
}

func (s *Server) handleRenameCollection(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r.PathValue("id"))
	if !ok {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	collection, err := s.store.RenameCollection(r.Context(), id, name)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrConflict):
			writeError(w, http.StatusConflict, err.Error())
		case errors.Is(err, store.ErrNotFound):
			writeError(w, http.StatusNotFound, "collection not found")
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, collection)
}

func (s *Server) handleDeleteCollection(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r.PathValue("id"))
	if !ok {
		return
	}
	if err := s.store.DeleteCollection(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "collection not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// --- items ---

func (s *Server) handleListItems(w http.ResponseWriter, r *http.Request) {
	q := store.ItemQuery{}

	if fid := r.URL.Query().Get("feed_id"); fid != "" {
		id, err := strconv.ParseInt(fid, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "feed_id must be an integer")
			return
		}
		q.FeedID = &id
	}
	if cid := r.URL.Query().Get("collection_id"); cid != "" {
		id, err := strconv.ParseInt(cid, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "collection_id must be an integer")
			return
		}
		q.CollectionID = &id
	}
	switch r.URL.Query().Get("unread") {
	case "true":
		q.Unread = boolPtr(true)
	case "false":
		q.Unread = boolPtr(false)
	}
	since, err := parseTimeParam("since", r.URL.Query().Get("since"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	q.Since = since
	until, err := parseTimeParam("until", r.URL.Query().Get("until"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	q.Until = until

	q.Limit = queryInt(r, "limit", 50, 1, 200)
	q.Offset = queryInt(r, "offset", 0, 0, 1<<30)

	items, total, err := s.store.ListItems(r.Context(), q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if items == nil {
		items = []store.Item{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total})
}

func (s *Server) handleUnreadCount(w http.ResponseWriter, r *http.Request) {
	n, err := s.store.UnreadCount(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": n})
}

func (s *Server) handleSetItemRead(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r.PathValue("id"))
	if !ok {
		return
	}
	var req struct {
		Read bool `json:"read"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := s.store.SetItemRead(r.Context(), id, req.Read); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleBulkSetItemRead(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs  []int64 `json:"ids"`
		Read bool    `json:"read"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if len(req.IDs) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "updated": 0})
		return
	}
	n, err := s.store.SetItemsRead(r.Context(), req.IDs, req.Read)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "updated": n})
}

// handleDigest returns the recent items of one collection as a single
// LLM-ready payload: a markdown digest (default) or the raw item list as JSON.
// Query params: collection_id (required), since, until (RFC3339; defaults to
// the last 24h), format (markdown|json), limit.
func (s *Server) handleDigest(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	cid, err := strconv.ParseInt(q.Get("collection_id"), 10, 64)
	if err != nil || cid <= 0 {
		writeError(w, http.StatusBadRequest, "collection_id must be a positive integer")
		return
	}
	collection, err := s.store.GetCollection(r.Context(), cid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "collection not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	now := time.Now().UTC()
	since := now.Add(-24 * time.Hour)
	if raw := q.Get("since"); raw != "" {
		t, err := parseTimeParam("since", raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		since = *t
	}
	until := now
	if raw := q.Get("until"); raw != "" {
		t, err := parseTimeParam("until", raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		until = *t
	}

	items, _, err := s.store.ListItems(r.Context(), store.ItemQuery{
		CollectionID: &cid,
		Since:        &since,
		Until:        &until,
		Limit:        queryInt(r, "limit", 50, 1, 200),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if items == nil {
		items = []store.Item{}
	}

	format := q.Get("format")
	if format == "" {
		format = "markdown"
	}
	switch format {
	case "markdown":
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, renderDigestMarkdown(collection.Name, since, until, items))
	case "json":
		writeJSON(w, http.StatusOK, map[string]any{
			"collection": collection.Name,
			"since":      since.UTC().Format(time.RFC3339),
			"until":      until.UTC().Format(time.RFC3339),
			"count":      len(items),
			"items":      items,
		})
	default:
		writeError(w, http.StatusBadRequest, "format must be markdown or json")
	}
}

// --- api index ---

// apiEndpoints is the discovery document served at GET /api (and listed in
// the README). Keep it in sync with the routes registered in New.
var apiEndpoints = []struct {
	Method      string
	Path        string
	Description string
}{
	{"GET", "/api", "This index: all available endpoints"},
	{"GET", "/api/feeds", "List feeds (with collection IDs)"},
	{"POST", "/api/feeds", "Add a feed (fetches + seeds it), body {\"url\": \"...\"}"},
	{"POST", "/api/feeds/bulk", "Add many feeds, body {\"urls\": [\"...\", ...]}"},
	{"POST", "/api/feeds/bulk-delete", "Delete feeds by ID, body {\"ids\": [1, 2]}"},
	{"PUT", "/api/feeds/{id}/collections", "Set a feed's collections, body {\"collection_ids\": [1]}"},
	{"DELETE", "/api/feeds/{id}", "Delete a feed and its items"},
	{"GET", "/api/collections", "List collections (with feed counts)"},
	{"POST", "/api/collections", "Create a collection, body {\"name\": \"...\"}"},
	{"PATCH", "/api/collections/{id}", "Rename a collection, body {\"name\": \"...\"}"},
	{"DELETE", "/api/collections/{id}", "Delete a collection"},
	{"GET", "/api/items", "List items. Query: feed_id, collection_id, unread (true|false), since, until (RFC3339), limit (1-200), offset"},
	{"GET", "/api/items/unread-count", "Total unread item count"},
	{"POST", "/api/items/{id}/read", "Set an item's read state, body {\"read\": true}"},
	{"POST", "/api/items/bulk-read", "Set read state for many items, body {\"ids\": [1, 2], \"read\": true}"},
	{"GET", "/api/digest", "LLM-ready digest of a collection's recent items. Query: collection_id (required), since, until (RFC3339; default: last 24h), format (markdown|json), limit"},
	{"POST", "/api/refresh", "Refresh all feeds now"},
}

// handleAPIIndex is the catch-all for GET /api/*. It serves the endpoint
// index at the API base (GET /api, GET /api/) so the surface is discoverable
// by humans, LLMs, and other automation, and 404s for anything else.
func (s *Server) handleAPIIndex(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimSuffix(path.Clean(r.URL.Path), "/")
	if p != "/api" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	endpoints := make([]map[string]string, 0, len(apiEndpoints))
	for _, e := range apiEndpoints {
		endpoints = append(endpoints, map[string]string{
			"method":      e.Method,
			"path":        e.Path,
			"description": e.Description,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name":        "elyfeed",
		"description": "Single-user RSS reader. Pull recent items by time window (GET /api/items?since=...&until=...) or get a ready-to-use digest per collection (GET /api/digest).",
		"endpoints":   endpoints,
	})
}

// --- refresh ---

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	ok, err := s.refresher.RefreshAll(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"refreshed": ok})
}

// renderDigestMarkdown renders items as a markdown digest grouped by feed
// (alphabetical), newest first within each feed.
func renderDigestMarkdown(collection string, since, until time.Time, items []store.Item) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Digest — %s (%s → %s)\n\n",
		collection, since.UTC().Format(time.RFC3339), until.UTC().Format(time.RFC3339))
	if len(items) == 0 {
		b.WriteString("_No items in this window._\n")
		return b.String()
	}

	sorted := make([]store.Item, len(items))
	copy(sorted, items)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].FeedTitle != sorted[j].FeedTitle {
			return sorted[i].FeedTitle < sorted[j].FeedTitle
		}
		return itemTime(sorted[i]).After(itemTime(sorted[j]))
	})

	lastFeed := ""
	for _, it := range sorted {
		if it.FeedTitle != lastFeed {
			lastFeed = it.FeedTitle
			fmt.Fprintf(&b, "## %s\n\n", it.FeedTitle)
		}
		line := fmt.Sprintf("- [%s](%s)", it.Title, it.Link)
		if it.Author != "" {
			line += " — " + it.Author
		}
		line += ", " + itemTime(it).UTC().Format("2006-01-02 15:04 UTC")
		b.WriteString(line + "\n")
		if excerpt := digestExcerpt(it.Content); excerpt != "" {
			b.WriteString("  > " + excerpt + "\n")
		}
	}
	return b.String()
}

// itemTime is the timestamp shown for an item: its publication date, falling
// back to the fetch time when the feed provides none.
func itemTime(it store.Item) time.Time {
	if it.PublishedAt != nil {
		return *it.PublishedAt
	}
	return it.FetchedAt
}

// digestExcerpt trims item content to a short single-line excerpt so digests
// stay small enough for LLM context windows. Whitespace (including newlines)
// is collapsed so the excerpt never breaks the markdown line it lives on.
func digestExcerpt(content string) string {
	c := strings.Join(strings.Fields(content), " ")
	const maxRunes = 300
	if len([]rune(c)) <= maxRunes {
		return c
	}
	r := []rune(c)[:maxRunes]
	if i := strings.LastIndex(string(r), " "); i > 0 {
		r = r[:i]
	}
	return string(r) + "…"
}

// --- SPA fallback ---

type spaHandler struct {
	assets fs.FS
}

func (h spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if p == "" || p == "." {
		p = "index.html"
	}
	if f, err := h.assets.Open(p); err == nil {
		f.Close()
		http.ServeFileFS(w, r, h.assets, p)
		return
	}
	http.ServeFileFS(w, r, h.assets, "index.html")
}

// --- helpers ---

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func parseID(w http.ResponseWriter, raw string) (int64, bool) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid id")
		return 0, false
	}
	return id, true
}

// parseTimeParam parses an optional RFC3339 timestamp query parameter. It
// returns nil when the parameter is absent and an error when it is present
// but malformed.
func parseTimeParam(key, raw string) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, fmt.Errorf("%s must be an RFC3339 timestamp (e.g. 2026-08-22T00:00:00Z)", key)
	}
	return &t, nil
}

func boolPtr(b bool) *bool { return &b }

func queryInt(r *http.Request, key string, def, min, max int) int {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	if n < min {
		return min
	}
	if n > max {
		return max
	}
	return n
}
