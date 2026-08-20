// Package server exposes the HTTP API and serves the embedded frontend.
package server

import (
	"encoding/json"
	"errors"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

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
	mux.HandleFunc("POST /api/refresh", s.handleRefresh)
	mux.HandleFunc("GET /api/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not found")
	})
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
	if r.URL.Query().Get("unread") == "true" {
		q.Unread = boolPtr(true)
	}

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

// --- refresh ---

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	ok, err := s.refresher.RefreshAll(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"refreshed": ok})
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
